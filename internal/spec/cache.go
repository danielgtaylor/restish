package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/rest-sh/restish/v2/internal/fileutil"
)

// cacheEntry is the on-disk format for a cached spec.
type cacheEntry struct {
	Version    string           `cbor:"version"`
	FetchedAt  time.Time        `cbor:"fetched_at"`
	ExpiresAt  time.Time        `cbor:"expires_at"`
	Schema     int              `cbor:"schema,omitempty"`
	Spec       cachedRaw        `cbor:"spec,omitempty"`
	SpecFiles  []cachedSpecFile `cbor:"spec_files,omitempty"`
	Operations []opsBlob        `cbor:"operations,omitempty"`

	// Legacy v2-dev fields. Keep them readable so older raw-only cache entries
	// can be upgraded opportunistically after a successful parse.
	ContentType string `cbor:"content_type,omitempty"`
	Raw         []byte `cbor:"raw,omitempty"`
}

type cachedSpecFile struct {
	Source          string    `cbor:"source"`
	Local           bool      `cbor:"local,omitempty"`
	Path            string    `cbor:"path,omitempty"`
	ModTime         time.Time `cbor:"mod_time,omitempty"`
	ModTimeUnixNano int64     `cbor:"mod_time_unix_nano,omitempty"`
	Size            int64     `cbor:"size,omitempty"`
	SHA256          string    `cbor:"sha256,omitempty"`
}

type cachedRaw struct {
	ContentType      string `cbor:"content_type,omitempty"`
	Raw              []byte `cbor:"raw,omitempty"`
	DiscoveryBaseURL string `cbor:"discovery_base_url,omitempty"`
	RequestedURL     string `cbor:"requested_url,omitempty"`
	SourceURL        string `cbor:"source_url,omitempty"`
	LocalPath        string `cbor:"local_path,omitempty"`
	AllowCrossOrigin bool   `cbor:"allow_cross_origin,omitempty"`
}

type opsBlob struct {
	Schema             int                 `cbor:"schema"`
	BaseURL            string              `cbor:"base_url"`
	OperationBase      string              `cbor:"operation_base,omitempty"`
	ServerVariablesKey string              `cbor:"server_variables,omitempty"`
	RawSHA256          string              `cbor:"raw_sha256"`
	Info               APIInfo             `cbor:"info,omitempty"`
	Operations         []Operation         `cbor:"operations"`
	XCLIExtensions     XCLIExtensionReport `cbor:"x_cli_extensions,omitempty"`
}

const currentCacheSchema = 2
const currentOperationCacheSchema = 12

var cacheDecMode = func() cbor.DecMode {
	mode, err := (cbor.DecOptions{MaxNestedLevels: 512}).DecMode()
	if err != nil {
		panic(fmt.Sprintf("spec cache: initialize CBOR decoder: %v", err))
	}
	return mode
}()

// OperationCacheStatus describes the freshness of cached operation metadata.
type OperationCacheStatus struct {
	FetchedAt time.Time
	ExpiresAt time.Time
	Stale     bool
}

// cacheFile returns the path of the CBOR cache file for the given API.
func cacheFile(cacheDir, apiName string) string {
	return filepath.Join(cacheDir, apiName+".cbor")
}

func validCacheAPIName(apiName string) bool {
	if apiName == "" || apiName == "." || apiName == ".." {
		return false
	}
	if strings.ContainsAny(apiName, `/\`) {
		return false
	}
	return filepath.Base(apiName) == apiName
}

// readCache loads and validates a cached spec entry.
// Returns the entry and true if the cache is valid (not expired, schema compatible).
func readCache(cacheDir, apiName, version string) (*cacheEntry, bool) {
	return readCacheEntry(cacheDir, apiName, version, false)
}

func readCacheEntry(cacheDir, apiName, version string, allowExpired bool) (*cacheEntry, bool) {
	if !validCacheAPIName(apiName) {
		return nil, false
	}
	path := cacheFile(cacheDir, apiName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var e cacheEntry
	if err := cacheDecMode.Unmarshal(data, &e); err != nil {
		return nil, false
	}
	if e.Schema > currentCacheSchema {
		return nil, false // cache was written by a newer incompatible schema
	}
	if !allowExpired && !e.ExpiresAt.IsZero() && time.Now().After(e.ExpiresAt) {
		return nil, false // TTL expired
	}
	e.normalize()
	return &e, true
}

// writeCache serialises entry to the CBOR cache file.
func writeCache(cacheDir, apiName string, entry *cacheEntry) error {
	if !validCacheAPIName(apiName) {
		return fmt.Errorf("spec cache: invalid API name %q", apiName)
	}
	entry.normalize()
	sanitizeCacheEntryForWrite(entry)
	if entry.Schema == 0 {
		entry.Schema = currentCacheSchema
	}
	data, err := cbor.Marshal(entry)
	if err != nil {
		return fmt.Errorf("spec cache: marshal: %w", err)
	}
	if err := atomicWriteCacheFile(cacheFile(cacheDir, apiName), data); err != nil {
		return fmt.Errorf("spec cache: write: %w", err)
	}
	return nil
}

func atomicWriteCacheFile(path string, data []byte) error {
	return fileutil.AtomicWriteFile(path, data, fileutil.AtomicWriteOptions{
		FileMode:    0o600,
		DirMode:     0o700,
		TempPattern: "spec-*.tmp",
	})
}

func (e *cacheEntry) normalize() {
	if e == nil {
		return
	}
	if len(e.Spec.Raw) == 0 && len(e.Raw) > 0 {
		e.Spec.Raw = e.Raw
	}
	if e.Spec.ContentType == "" && e.ContentType != "" {
		e.Spec.ContentType = e.ContentType
	}
}

func (e *cacheEntry) contentType() string {
	if e == nil {
		return ""
	}
	e.normalize()
	return e.Spec.ContentType
}

func (e *cacheEntry) raw() []byte {
	if e == nil {
		return nil
	}
	e.normalize()
	return e.Spec.Raw
}

func (e *cacheEntry) loadOptions() LoadOptions {
	if e == nil {
		return LoadOptions{}
	}
	e.normalize()
	return LoadOptions{
		SourceURL:        e.Spec.SourceURL,
		RequestedURL:     e.Spec.RequestedURL,
		LocalPath:        e.Spec.LocalPath,
		AllowCrossOrigin: e.Spec.AllowCrossOrigin,
	}
}

// LoadFromCache reads the cached spec for apiName, re-parses it using loaders,
// and returns the result. Returns nil, nil when the cache is empty or expired.
func LoadFromCache(cacheDir, apiName, version string, specFiles []string, loaders []Loader) (*APISpec, error) {
	return loadFromCache(cacheDir, apiName, version, specFiles, loaders, false)
}

// LoadStaleFromCache reads the cached spec for apiName even when the remote
// cache entry has expired. Local spec files still invalidate stale cache data
// when they changed after the cache was written.
func LoadStaleFromCache(cacheDir, apiName, version string, specFiles []string, loaders []Loader) (*APISpec, error) {
	return loadFromCache(cacheDir, apiName, version, specFiles, loaders, true)
}

func loadFromCache(cacheDir, apiName, version string, specFiles []string, loaders []Loader, allowExpired bool) (*APISpec, error) {
	entry, ok := readCacheEntry(cacheDir, apiName, version, allowExpired)
	if !ok {
		return nil, nil
	}
	if !cacheSpecFilesMatch(specFiles, entry) {
		return nil, nil
	}
	if specFilesChangedSince(specFiles, entry.FetchedAt) {
		return nil, nil
	}
	return loadWithOptions(entry.contentType(), entry.raw(), loaders, entry.loadOptions())
}

// LoadOperationSetFromCache reads extracted operations and API metadata for a
// cached spec without reparsing the raw OpenAPI document. The bool return is
// false when the cache entry is missing, expired, stale against local files,
// or lacks operations for the requested base URL, operation base, and server
// variable set.
func LoadOperationSetFromCache(cacheDir, apiName, version string, specFiles []string, opts OperationOptions) (OperationSet, bool) {
	set, status, ok := LoadOperationSetFromCacheStatus(cacheDir, apiName, version, specFiles, opts, false)
	if !ok || status.Stale {
		return OperationSet{}, false
	}
	return set, true
}

// LoadOperationSetFromCacheStatus reads extracted operations and reports
// whether the matching metadata is stale. When allowStale is true, expired
// remote spec metadata remains usable as "last known good" operations. Local
// spec file changes still invalidate the metadata, because the local source is
// immediately available and should be treated as authoritative.
func LoadOperationSetFromCacheStatus(cacheDir, apiName, version string, specFiles []string, opts OperationOptions, allowStale bool) (OperationSet, OperationCacheStatus, bool) {
	entry, ok := readCacheEntry(cacheDir, apiName, version, allowStale)
	if !ok {
		return OperationSet{}, OperationCacheStatus{}, false
	}
	if !cacheSpecFilesMatch(specFiles, entry) {
		return OperationSet{}, OperationCacheStatus{}, false
	}
	if specFilesChangedSince(specFiles, entry.FetchedAt) {
		return OperationSet{}, OperationCacheStatus{}, false
	}
	status := OperationCacheStatus{
		FetchedAt: entry.FetchedAt,
		ExpiresAt: entry.ExpiresAt,
		Stale:     !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt),
	}
	if status.Stale && !allowStale {
		return OperationSet{}, status, false
	}
	rawHash := cacheRawHash(entry.raw())
	cacheOpts := cacheOperationOptions(opts)
	for _, blob := range entry.Operations {
		if blob.Schema != currentOperationCacheSchema {
			continue
		}
		if blob.BaseURL == cacheOpts.BaseURL &&
			blob.OperationBase == cacheOpts.OperationBase &&
			blob.ServerVariablesKey == ServerVariablesCacheKey(cacheOpts.ServerVariables) &&
			blob.RawSHA256 == rawHash {
			set := OperationSet{
				Info:           blob.Info,
				Operations:     append([]Operation(nil), blob.Operations...),
				XCLIExtensions: blob.XCLIExtensions,
			}
			if blob.Operations == nil {
				set.Operations = []Operation{}
			}
			return set, status, true
		}
	}
	return OperationSet{}, status, false
}

// RawSpecCacheStatus reports the freshness of the cached raw spec. Expired
// remote specs can still report status; local spec file changes invalidate the
// cache because the local source is authoritative.
func RawSpecCacheStatus(cacheDir, apiName, version string, specFiles []string) (OperationCacheStatus, bool) {
	entry, ok := readCacheEntry(cacheDir, apiName, version, true)
	if !ok {
		return OperationCacheStatus{}, false
	}
	if !cacheSpecFilesMatch(specFiles, entry) {
		return OperationCacheStatus{}, false
	}
	if specFilesChangedSince(specFiles, entry.FetchedAt) {
		return OperationCacheStatus{}, false
	}
	return OperationCacheStatus{
		FetchedAt: entry.FetchedAt,
		ExpiresAt: entry.ExpiresAt,
		Stale:     !entry.ExpiresAt.IsZero() && time.Now().After(entry.ExpiresAt),
	}, true
}

// StoreOperationSetInCache updates an existing raw cache entry with extracted
// operations and API metadata. It is best-effort for callers; failed upgrades
// should not make startup fail when the raw cache can still be parsed. Expired
// entries can be upgraded so rebuilt operation metadata is durable, but the
// original cache TTL is preserved. The cache entry is keyed by base URL,
// operation base, and server variable values (via opts).
func StoreOperationSetInCache(cacheDir, apiName, version string, opts OperationOptions, set OperationSet) error {
	entry, ok := readCacheEntry(cacheDir, apiName, version, true)
	if !ok {
		return nil
	}
	entry.upsertOperationSet(cacheOperationOptions(opts), set)
	return writeCache(cacheDir, apiName, entry)
}

// StoreSpecInCache writes a parsed API spec and, when possible, extracted
// operation metadata to the on-disk spec cache. A non-positive ttl uses the
// default spec-cache lifetime.
func StoreSpecInCache(cacheDir, apiName, version string, apiSpec *APISpec, specFiles []string, opts OperationOptions, ttl time.Duration) error {
	if cacheDir == "" || apiSpec == nil {
		return nil
	}
	expiresAt := time.Now().Add(24 * time.Hour)
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	set, _ := apiSpec.OperationSet(opts)
	entry := &cacheEntry{
		Version:   version,
		FetchedAt: time.Now(),
		ExpiresAt: expiresAt,
		Spec: cachedRaw{
			ContentType:      apiSpec.ContentType,
			Raw:              apiSpec.Raw,
			DiscoveryBaseURL: cleanSourceURL(opts.BaseURL),
			RequestedURL:     cleanSourceURL(apiSpec.RequestedURL),
			SourceURL:        cleanSourceURL(apiSpec.SourceURL),
			LocalPath:        apiSpec.LocalPath,
			AllowCrossOrigin: apiSpec.AllowCrossOrigin,
		},
	}
	entry.SpecFiles = cacheSpecFileMetadata(specFiles)
	if set.Operations != nil {
		entry.upsertOperationSet(cacheOperationOptions(opts), set)
	}
	return writeCache(cacheDir, apiName, entry)
}

func cacheOperationOptions(opts OperationOptions) OperationOptions {
	opts.BaseURL = cleanSourceURL(opts.BaseURL)
	opts.OperationBase = cleanSourceURL(opts.OperationBase)
	return opts
}

func sanitizeCacheEntryForWrite(entry *cacheEntry) {
	if entry == nil {
		return
	}
	entry.Spec.DiscoveryBaseURL = cleanSourceURL(entry.Spec.DiscoveryBaseURL)
	entry.Spec.RequestedURL = cleanSourceURL(entry.Spec.RequestedURL)
	entry.Spec.SourceURL = cleanSourceURL(entry.Spec.SourceURL)
	for i := range entry.SpecFiles {
		if !entry.SpecFiles[i].Local {
			entry.SpecFiles[i].Source = cleanSourceURL(entry.SpecFiles[i].Source)
		}
	}
	entry.Operations = sanitizeOpsBlobs(entry.Operations)
}

func sanitizeOpsBlobs(blobs []opsBlob) []opsBlob {
	if len(blobs) == 0 {
		return blobs
	}
	out := make([]opsBlob, 0, len(blobs))
	index := map[string]int{}
	for _, blob := range blobs {
		blob.BaseURL = cleanSourceURL(blob.BaseURL)
		blob.OperationBase = cleanSourceURL(blob.OperationBase)
		key := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", blob.Schema, blob.BaseURL, blob.OperationBase, blob.ServerVariablesKey, blob.RawSHA256)
		if i, ok := index[key]; ok {
			out[i] = blob
			continue
		}
		index[key] = len(out)
		out = append(out, blob)
	}
	return out
}

func (e *cacheEntry) upsertOperationSet(opts OperationOptions, set OperationSet) {
	rawHash := cacheRawHash(e.raw())
	blob := opsBlob{
		Schema:             currentOperationCacheSchema,
		BaseURL:            opts.BaseURL,
		OperationBase:      opts.OperationBase,
		ServerVariablesKey: ServerVariablesCacheKey(opts.ServerVariables),
		RawSHA256:          rawHash,
		Info:               set.Info,
		Operations:         append([]Operation(nil), set.Operations...),
		XCLIExtensions:     set.XCLIExtensions,
	}
	for i := range e.Operations {
		if e.Operations[i].BaseURL == opts.BaseURL &&
			e.Operations[i].OperationBase == opts.OperationBase &&
			e.Operations[i].ServerVariablesKey == blob.ServerVariablesKey {
			e.Operations[i] = blob
			return
		}
	}
	e.Operations = append(e.Operations, blob)
}

func cacheRawHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func HasLocalSpecFiles(specFiles []string) bool {
	for _, src := range specFiles {
		if isLocalPath(src) {
			return true
		}
	}
	return false
}

// InvalidateCache removes the cached spec file for apiName.
// Returns nil if the file did not exist.
func InvalidateCache(cacheDir, apiName string) error {
	if !validCacheAPIName(apiName) {
		return fmt.Errorf("spec cache: invalid API name %q", apiName)
	}
	err := os.Remove(cacheFile(cacheDir, apiName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
