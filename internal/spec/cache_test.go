package spec

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
)

const testSpecRaw = `{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"paths":{}}`
const testSpecRawWithOperation = `{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"OK"}}}}}}`

func TestWriteAndReadCache(t *testing.T) {
	dir := t.TempDir()
	entry := &cacheEntry{
		Version:     "v2",
		FetchedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	got, ok := readCache(dir, "testapi", "v2")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(got.Raw) != testSpecRaw {
		t.Errorf("Raw mismatch: got %q, want %q", got.Raw, testSpecRaw)
	}
}

func TestWriteAndReadCacheWithDeepOperationSchema(t *testing.T) {
	dir := t.TempDir()
	schema := map[string]any{"type": "string"}
	for range 64 {
		schema = map[string]any{"properties": schema}
	}
	entry := &cacheEntry{
		Version:   "v2",
		FetchedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Spec: cachedRaw{
			ContentType: "application/json",
			Raw:         []byte(testSpecRaw),
		},
	}
	entry.upsertOperationSet(OperationOptions{BaseURL: "https://api.example.com"}, OperationSet{
		Operations: []Operation{{
			ID:     "getItem",
			Method: "GET",
			Path:   "/items/{id}",
			Parameters: []Param{{
				Name:       "id",
				In:         "path",
				JSONSchema: schema,
			}},
		}},
	})
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	got, ok := readCache(dir, "testapi", "v2")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got.Operations) != 1 || len(got.Operations[0].Operations) != 1 {
		t.Fatalf("cached operations = %#v", got.Operations)
	}
}

func TestWriteCacheUsesAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	entry := &cacheEntry{
		Version:     "v2",
		FetchedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "spec-*.tmp"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary cache files left behind: %v", matches)
	}
	info, err := os.Stat(filepath.Join(dir, "testapi.cbor"))
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("cache mode = %v, want 0600", got)
	}
}

func credentialURLTestOperationOptions() OperationOptions {
	return OperationOptions{
		BaseURL:       "https://api.example.com?api_key=base-secret&region=us",
		OperationBase: "https://ops.example.com/root?api_key=op-secret&page=1",
	}
}

func assertCacheEntryHasCleanCredentialURLMetadata(t *testing.T, entry *cacheEntry, opCount int) {
	t.Helper()
	if got := entry.Spec.DiscoveryBaseURL; got != "https://api.example.com?region=us" {
		t.Fatalf("DiscoveryBaseURL = %q, want clean URL", got)
	}
	if got := entry.Spec.SourceURL; got != "https://api.example.com/openapi.json?version=1" {
		t.Fatalf("SourceURL = %q, want clean URL", got)
	}
	if len(entry.SpecFiles) != 1 || entry.SpecFiles[0].Source != "https://files.example.com/openapi.json?version=1" {
		t.Fatalf("SpecFiles = %#v, want clean remote source", entry.SpecFiles)
	}
	if len(entry.Operations) != opCount {
		t.Fatalf("Operations = %d, want %d cached operation blob(s)", len(entry.Operations), opCount)
	}
	if got := entry.Operations[0].BaseURL; got != "https://api.example.com?region=us" {
		t.Fatalf("operation BaseURL = %q, want clean URL", got)
	}
	if got := entry.Operations[0].OperationBase; got != "https://ops.example.com/root?page=1" {
		t.Fatalf("operation OperationBase = %q, want clean URL", got)
	}
}

func TestStoreSpecInCacheCleansCredentialURLMetadata(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(testSpecRawWithOperation)
	apiSpec, err := OpenAPILoader{}.LoadWithOptions(raw, LoadOptions{
		SourceURL: "https://api.example.com/openapi.json?api_key=spec-secret&version=1",
	})
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	apiSpec.SourceURL = "https://api.example.com/openapi.json?api_key=spec-secret&version=1"

	specFiles := []string{"https://files.example.com/openapi.json?access_token=file-secret&version=1"}
	if err := StoreSpecInCache(dir, "testapi", "v2", apiSpec, specFiles, credentialURLTestOperationOptions(), time.Hour); err != nil {
		t.Fatalf("StoreSpecInCache: %v", err)
	}

	entry, ok := readCache(dir, "testapi", "v2")
	if !ok {
		t.Fatal("expected cache entry")
	}
	assertCacheEntryHasCleanCredentialURLMetadata(t, entry, 1)
}

func TestStoreOperationSetInCacheCleansLegacyCredentialURLMetadata(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(testSpecRawWithOperation)
	legacy := &cacheEntry{
		Version:   "v2",
		FetchedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Schema:    currentCacheSchema,
		Spec: cachedRaw{
			ContentType:      "application/json",
			Raw:              raw,
			DiscoveryBaseURL: "https://api.example.com?api_key=base-secret&region=us",
			SourceURL:        "https://api.example.com/openapi.json?api_key=spec-secret&version=1",
		},
		SpecFiles: []cachedSpecFile{{
			Source: "https://files.example.com/openapi.json?access_token=file-secret&version=1",
		}},
		Operations: []opsBlob{{
			Schema:        currentOperationCacheSchema,
			BaseURL:       "https://api.example.com?api_key=base-secret&region=us",
			OperationBase: "https://ops.example.com/root?api_key=op-secret&page=1",
			RawSHA256:     cacheRawHash(raw),
			Operations:    []Operation{{ID: "old"}},
		}},
	}
	data, err := cbor.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy cache: %v", err)
	}
	if err := atomicWriteCacheFile(cacheFile(dir, "testapi"), data); err != nil {
		t.Fatalf("write legacy cache: %v", err)
	}

	if err := StoreOperationSetInCache(dir, "testapi", "v2", credentialURLTestOperationOptions(), OperationSet{Operations: []Operation{{ID: "new"}}}); err != nil {
		t.Fatalf("StoreOperationSetInCache: %v", err)
	}

	entry, ok := readCache(dir, "testapi", "v2")
	if !ok {
		t.Fatal("expected cache entry")
	}
	assertCacheEntryHasCleanCredentialURLMetadata(t, entry, 1)
	if got := entry.Operations[0].Operations[0].ID; got != "new" {
		t.Fatalf("operation blob ID = %q, want updated blob to win", got)
	}
}

func TestWriteCacheRejectsUnsafeAPIName(t *testing.T) {
	entry := &cacheEntry{
		Version:     "v2",
		ExpiresAt:   time.Now().Add(time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	for _, name := range []string{"../secret", "nested/api", ".", ".."} {
		if err := writeCache(t.TempDir(), name, entry); err == nil {
			t.Fatalf("expected unsafe cache name %q to fail", name)
		}
	}
}

func TestReadCacheRejectsUnsafeAPIName(t *testing.T) {
	if _, ok := readCache(t.TempDir(), "../secret", "v2"); ok {
		t.Fatal("expected unsafe cache name to miss")
	}
}

func TestReadCache_Miss_Missing(t *testing.T) {
	_, ok := readCache(t.TempDir(), "nonexistent", "v2")
	if ok {
		t.Error("expected cache miss for nonexistent entry")
	}
}

func TestReadCache_AllowsVersionMismatchWhenSchemaCompatible(t *testing.T) {
	dir := t.TempDir()
	entry := &cacheEntry{
		Version:     "v1",
		Schema:      currentCacheSchema,
		ExpiresAt:   time.Now().Add(time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	writeCache(dir, "testapi", entry)

	_, ok := readCache(dir, "testapi", "v2")
	if !ok {
		t.Error("expected cache hit for compatible schema despite version mismatch")
	}
}

func TestReadCache_Miss_FutureSchema(t *testing.T) {
	dir := t.TempDir()
	entry := &cacheEntry{
		Version:     "v2",
		Schema:      currentCacheSchema + 1,
		ExpiresAt:   time.Now().Add(time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	writeCache(dir, "testapi", entry)

	_, ok := readCache(dir, "testapi", "v2")
	if ok {
		t.Error("expected cache miss for future schema")
	}
}

func TestReadCache_Miss_Expired(t *testing.T) {
	dir := t.TempDir()
	entry := &cacheEntry{
		Version:     "v2",
		ExpiresAt:   time.Now().Add(-time.Hour), // already expired
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	writeCache(dir, "testapi", entry)

	_, ok := readCache(dir, "testapi", "v2")
	if ok {
		t.Error("expected cache miss for expired entry")
	}
}

func TestLoadFromCache(t *testing.T) {
	dir := t.TempDir()
	entry := &cacheEntry{
		Version:     "v2",
		ExpiresAt:   time.Now().Add(time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	writeCache(dir, "testapi", entry)

	spec, err := LoadFromCache(dir, "testapi", "v2", nil, DefaultLoaders())
	if err != nil {
		t.Fatalf("LoadFromCache: %v", err)
	}
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
}

func TestLoadStaleFromCacheAllowsExpiredRemoteSpec(t *testing.T) {
	dir := t.TempDir()
	entry := &cacheEntry{
		Version:     "v2",
		ExpiresAt:   time.Now().Add(-time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	writeCache(dir, "testapi", entry)

	fresh, err := LoadFromCache(dir, "testapi", "v2", nil, DefaultLoaders())
	if err != nil {
		t.Fatalf("LoadFromCache: %v", err)
	}
	if fresh != nil {
		t.Fatal("expected regular cache load to reject expired entry")
	}

	stale, err := LoadStaleFromCache(dir, "testapi", "v2", nil, DefaultLoaders())
	if err != nil {
		t.Fatalf("LoadStaleFromCache: %v", err)
	}
	if stale == nil {
		t.Fatal("expected stale cache load to allow expired entry")
	}
}

func TestRawSpecCacheStatusReportsExpiredRemoteSpec(t *testing.T) {
	dir := t.TempDir()
	fetchedAt := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	expiresAt := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	entry := &cacheEntry{
		Version:     "v2",
		FetchedAt:   fetchedAt,
		ExpiresAt:   expiresAt,
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	writeCache(dir, "testapi", entry)

	status, ok := RawSpecCacheStatus(dir, "testapi", "v2", nil)
	if !ok {
		t.Fatal("expected raw spec cache status")
	}
	if !status.Stale {
		t.Fatal("expected expired raw spec to report stale")
	}
	if !status.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("fetched_at = %v, want %v", status.FetchedAt, fetchedAt)
	}
	if !status.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %v, want %v", status.ExpiresAt, expiresAt)
	}
}

func TestLoadFromCache_Miss(t *testing.T) {
	spec, err := LoadFromCache(t.TempDir(), "nonexistent", "v2", nil, DefaultLoaders())
	if err != nil {
		t.Fatalf("LoadFromCache: %v", err)
	}
	if spec != nil {
		t.Error("expected nil spec for cache miss")
	}
}

func TestLoadFromCache_LocalSpecFileNewerThanCache(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(testSpecRaw), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	entry := &cacheEntry{
		Version:     "v2",
		FetchedAt:   time.Now().Add(-time.Hour),
		ExpiresAt:   time.Now().Add(time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}
	if err := os.Chtimes(specPath, time.Now(), time.Now()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	spec, err := LoadFromCache(dir, "testapi", "v2", []string{specPath}, DefaultLoaders())
	if err != nil {
		t.Fatalf("LoadFromCache: %v", err)
	}
	if spec != nil {
		t.Fatal("expected stale local spec file to invalidate cache")
	}
}

func TestLoadOperationsFromCache(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"paths":{"/items/{id}":{"get":{"operationId":"getItem","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"OK"}}}}}}`)
	loaded, err := load("application/json", raw, DefaultLoaders())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ops, err := loaded.Operations(OperationOptions{BaseURL: "https://api.example.com"})
	if err != nil {
		t.Fatalf("operations: %v", err)
	}

	entry := &cacheEntry{
		Version:   "v2",
		FetchedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Spec: cachedRaw{
			ContentType: "application/json",
			Raw:         raw,
		},
	}
	entry.upsertOperationSet(OperationOptions{BaseURL: "https://api.example.com"}, OperationSet{Operations: ops})
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	set, ok := LoadOperationSetFromCache(dir, "testapi", "v2", nil, OperationOptions{BaseURL: "https://api.example.com"})
	if !ok {
		t.Fatal("expected operations cache hit")
	}
	got := set.Operations
	if len(got) != 1 || got[0].ID != "getItem" || got[0].Path != "/items/{id}" {
		t.Fatalf("unexpected operations: %#v", got)
	}
}

func TestLoadOperationSetFromCacheAcceptsLegacyLocalSpecFileSecondPrecisionModTime(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	raw := []byte(`{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"OK"}}}}}}`)
	if err := os.WriteFile(specPath, raw, 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	mtime := time.Unix(1700000000, 987654321)
	if err := os.Chtimes(specPath, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	info, err := os.Stat(specPath)
	if err != nil {
		t.Fatalf("stat spec: %v", err)
	}
	if info.ModTime().Nanosecond() == 0 {
		t.Skip("filesystem does not preserve subsecond mtimes")
	}
	loaded, err := load("application/json", raw, DefaultLoaders())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	set, err := loaded.OperationSet(OperationOptions{})
	if err != nil {
		t.Fatalf("operation set: %v", err)
	}

	entry := &cacheEntry{
		Version:   "v2",
		FetchedAt: info.ModTime().Add(time.Minute),
		ExpiresAt: time.Now().Add(time.Hour),
		Spec: cachedRaw{
			ContentType: "application/json",
			Raw:         raw,
			LocalPath:   specPath,
		},
		SpecFiles: []cachedSpecFile{{
			Source:  specPath,
			Local:   true,
			Path:    specPath,
			ModTime: info.ModTime().Truncate(time.Second),
			Size:    info.Size(),
		}},
	}
	entry.upsertOperationSet(OperationOptions{}, set)
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	got, _, ok := LoadOperationSetFromCacheStatus(dir, "testapi", "v2", []string{specPath}, OperationOptions{}, true)
	if !ok {
		t.Fatal("expected operations cache hit")
	}
	if len(got.Operations) != 1 || got.Operations[0].ID != "listItems" {
		t.Fatalf("unexpected operations: %#v", got.Operations)
	}
}

func TestStoreSpecInCachePreservesLocalSpecFileNanosecondModTime(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	raw := []byte(`{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"OK"}}}}}}`)
	if err := os.WriteFile(specPath, raw, 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	mtime := time.Unix(1700000000, 456789123)
	if err := os.Chtimes(specPath, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	info, err := os.Stat(specPath)
	if err != nil {
		t.Fatalf("stat spec: %v", err)
	}
	if info.ModTime().Nanosecond() == 0 {
		t.Skip("filesystem does not preserve subsecond mtimes")
	}
	apiSpec, err := OpenAPILoader{}.Load(raw)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if err := StoreSpecInCache(dir, "testapi", "v2", apiSpec, []string{specPath}, OperationOptions{}, time.Hour); err != nil {
		t.Fatalf("StoreSpecInCache: %v", err)
	}

	entry, ok := readCacheEntry(dir, "testapi", "v2", true)
	if !ok {
		t.Fatal("expected cache entry")
	}
	if len(entry.SpecFiles) != 1 {
		t.Fatalf("SpecFiles len = %d, want 1", len(entry.SpecFiles))
	}
	if got, want := entry.SpecFiles[0].ModTimeUnixNano, info.ModTime().UnixNano(); got != want {
		t.Fatalf("cached ModTimeUnixNano = %d, want %d", got, want)
	}

	got, _, ok := LoadOperationSetFromCacheStatus(dir, "testapi", "v2", []string{specPath}, OperationOptions{}, true)
	if !ok {
		t.Fatal("expected operations cache hit")
	}
	if len(got.Operations) != 1 || got.Operations[0].ID != "listItems" {
		t.Fatalf("unexpected operations: %#v", got.Operations)
	}
}

func TestLoadOperationSetFromCacheStatusAllowsStaleRemoteMetadata(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"OK"}}}}}}`)
	loaded, err := load("application/json", raw, DefaultLoaders())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	set, err := loaded.OperationSet(OperationOptions{BaseURL: "https://api.example.com"})
	if err != nil {
		t.Fatalf("operation set: %v", err)
	}

	entry := &cacheEntry{
		Version:   "v2",
		FetchedAt: time.Now().Add(-48 * time.Hour),
		ExpiresAt: time.Now().Add(-24 * time.Hour),
		Spec: cachedRaw{
			ContentType: "application/json",
			Raw:         raw,
		},
	}
	entry.upsertOperationSet(OperationOptions{BaseURL: "https://api.example.com"}, set)
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	if _, ok := LoadOperationSetFromCache(dir, "testapi", "v2", nil, OperationOptions{BaseURL: "https://api.example.com"}); ok {
		t.Fatal("fresh-only operation cache should miss expired metadata")
	}
	got, status, ok := LoadOperationSetFromCacheStatus(dir, "testapi", "v2", nil, OperationOptions{BaseURL: "https://api.example.com"}, true)
	if !ok {
		t.Fatal("expected stale operation cache hit")
	}
	if !status.Stale {
		t.Fatal("expected stale status")
	}
	if len(got.Operations) != 1 || got.Operations[0].ID != "listItems" {
		t.Fatalf("unexpected operations: %#v", got.Operations)
	}
}

func TestStoreOperationSetInCacheUpgradesExpiredRemoteCache(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"OK"}}}}}}`)
	loaded, err := load("application/json", raw, DefaultLoaders())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	set, err := loaded.OperationSet(OperationOptions{BaseURL: "https://api.example.com"})
	if err != nil {
		t.Fatalf("operation set: %v", err)
	}

	expiresAt := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	entry := &cacheEntry{
		Version:   "v2",
		FetchedAt: time.Now().Add(-48 * time.Hour),
		ExpiresAt: expiresAt,
		Spec: cachedRaw{
			ContentType: "application/json",
			Raw:         raw,
		},
	}
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	opts := OperationOptions{BaseURL: "https://api.example.com"}
	if err := StoreOperationSetInCache(dir, "testapi", "v2", opts, set); err != nil {
		t.Fatalf("StoreOperationSetInCache: %v", err)
	}
	if _, ok := LoadOperationSetFromCache(dir, "testapi", "v2", nil, opts); ok {
		t.Fatal("fresh-only operation cache should still miss expired metadata")
	}
	got, status, ok := LoadOperationSetFromCacheStatus(dir, "testapi", "v2", nil, opts, true)
	if !ok {
		t.Fatal("expected stale operation cache hit after upgrade")
	}
	if !status.Stale {
		t.Fatal("expected cache to remain stale after operation upgrade")
	}
	if !status.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %v, want %v", status.ExpiresAt, expiresAt)
	}
	if len(got.Operations) != 1 || got.Operations[0].ID != "listItems" {
		t.Fatalf("unexpected operations: %#v", got.Operations)
	}
}

func TestLoadOperationsFromCachePreservesCredentialMetadata(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"security":[{"ApiKey":[]}],"components":{"securitySchemes":{"ApiKey":{"type":"apiKey","in":"header","name":"X-API-Key"}}},"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"OK"}}}}}}`)
	loaded, err := load("application/json", raw, DefaultLoaders())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ops, err := loaded.Operations(OperationOptions{BaseURL: "https://api.example.com"})
	if err != nil {
		t.Fatalf("operations: %v", err)
	}

	entry := &cacheEntry{
		Version:   "v2",
		FetchedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Spec: cachedRaw{
			ContentType: "application/json",
			Raw:         raw,
		},
	}
	entry.upsertOperationSet(OperationOptions{BaseURL: "https://api.example.com"}, OperationSet{Operations: ops})
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	set, ok := LoadOperationSetFromCache(dir, "testapi", "v2", nil, OperationOptions{BaseURL: "https://api.example.com"})
	if !ok {
		t.Fatal("expected operations cache hit")
	}
	got := set.Operations
	want := []CredentialAlternative{{
		{ID: "ApiKey", Ref: "#/components/securitySchemes/ApiKey", Kind: "api-key", In: "header", Name: "X-API-Key", Source: "openapi"},
	}}
	if !reflect.DeepEqual(got[0].CredentialAlternatives, want) {
		t.Fatalf("credential alternatives:\ngot  %#v\nwant %#v", got[0].CredentialAlternatives, want)
	}
}

func TestLoadOperationSetFromCacheIncludesInfo(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0","description":"API **docs**"},"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"OK"}}}}}}`)
	loaded, err := load("application/json", raw, DefaultLoaders())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	set, err := loaded.OperationSet(OperationOptions{BaseURL: "https://api.example.com"})
	if err != nil {
		t.Fatalf("operation set: %v", err)
	}

	entry := &cacheEntry{
		Version:   "v2",
		FetchedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Spec: cachedRaw{
			ContentType: "application/json",
			Raw:         raw,
		},
	}
	entry.upsertOperationSet(OperationOptions{BaseURL: "https://api.example.com"}, set)
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	got, ok := LoadOperationSetFromCache(dir, "testapi", "v2", nil, OperationOptions{BaseURL: "https://api.example.com"})
	if !ok {
		t.Fatal("expected operations cache hit")
	}
	if got.Info.Description != "API **docs**" {
		t.Fatalf("description = %q, want API **docs**", got.Info.Description)
	}
	if len(got.Operations) != 1 || got.Operations[0].ID != "listItems" {
		t.Fatalf("unexpected operations: %#v", got.Operations)
	}
}

func TestLoadOperationSetFromCacheKeysServerVariables(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"servers":[{"url":"https://api.example.com/{version}","variables":{"version":{"default":"v1"}}}],"paths":{"/items":{"get":{"operationId":"listItems","responses":{"200":{"description":"OK"}}}}}}`)
	loaded, err := load("application/json", raw, DefaultLoaders())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	optsV1 := OperationOptions{BaseURL: "https://api.example.com", ServerVariables: map[string]string{"version": "v1"}}
	optsV2 := OperationOptions{BaseURL: "https://api.example.com", ServerVariables: map[string]string{"version": "v2"}}
	setV1, err := loaded.OperationSet(optsV1)
	if err != nil {
		t.Fatalf("operation set v1: %v", err)
	}
	setV2, err := loaded.OperationSet(optsV2)
	if err != nil {
		t.Fatalf("operation set v2: %v", err)
	}

	entry := &cacheEntry{
		Version:   "v2",
		FetchedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Spec: cachedRaw{
			ContentType: "application/json",
			Raw:         raw,
		},
	}
	entry.upsertOperationSet(optsV1, setV1)
	entry.upsertOperationSet(optsV2, setV2)
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	gotV1, ok := LoadOperationSetFromCache(dir, "testapi", "v2", nil, optsV1)
	if !ok {
		t.Fatal("expected v1 operations cache hit")
	}
	if got := gotV1.Operations[0].Path; got != "/v1/items" {
		t.Fatalf("v1 path = %q, want /v1/items", got)
	}
	gotV2, ok := LoadOperationSetFromCache(dir, "testapi", "v2", nil, optsV2)
	if !ok {
		t.Fatal("expected v2 operations cache hit")
	}
	if got := gotV2.Operations[0].Path; got != "/v2/items" {
		t.Fatalf("v2 path = %q, want /v2/items", got)
	}
}

func TestLoadOperationsFromCache_MissRawOnlyEntry(t *testing.T) {
	dir := t.TempDir()
	entry := &cacheEntry{
		Version:     "v2",
		FetchedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	if err := writeCache(dir, "testapi", entry); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	if _, ok := LoadOperationSetFromCache(dir, "testapi", "v2", nil, OperationOptions{BaseURL: "https://api.example.com"}); ok {
		t.Fatal("expected raw-only cache to miss operations")
	}
}

func BenchmarkLoadOperationsFromCache(b *testing.B) {
	dir := b.TempDir()
	ops := make([]Operation, 0, 250)
	for i := 0; i < cap(ops); i++ {
		ops = append(ops, Operation{
			ID:     "getItem",
			Method: "GET",
			Path:   "/items/{id}",
			Parameters: []Param{{
				Name:     "id",
				In:       "path",
				Required: true,
			}},
		})
	}
	entry := &cacheEntry{
		Version:   "v2",
		FetchedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Spec: cachedRaw{
			ContentType: "application/json",
			Raw:         []byte(testSpecRaw),
		},
	}
	entry.upsertOperationSet(OperationOptions{BaseURL: "https://api.example.com"}, OperationSet{Operations: ops})
	if err := writeCache(dir, "testapi", entry); err != nil {
		b.Fatalf("writeCache: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := LoadOperationSetFromCache(dir, "testapi", "v2", nil, OperationOptions{BaseURL: "https://api.example.com"}); !ok {
			b.Fatal("operations cache miss")
		}
	}
}

func TestInvalidateCache(t *testing.T) {
	dir := t.TempDir()
	entry := &cacheEntry{
		Version:     "v2",
		ExpiresAt:   time.Now().Add(time.Hour),
		ContentType: "application/json",
		Raw:         []byte(testSpecRaw),
	}
	writeCache(dir, "testapi", entry)

	if err := InvalidateCache(dir, "testapi"); err != nil {
		t.Fatalf("InvalidateCache: %v", err)
	}

	_, ok := readCache(dir, "testapi", "v2")
	if ok {
		t.Error("expected cache miss after invalidation")
	}
}

func TestInvalidateCache_Nonexistent(t *testing.T) {
	// Should not error if the cache file doesn't exist.
	if err := InvalidateCache(t.TempDir(), "nonexistent"); err != nil {
		t.Fatalf("InvalidateCache: %v", err)
	}
}

func TestInvalidateCacheRejectsUnsafeAPIName(t *testing.T) {
	if err := InvalidateCache(t.TempDir(), "../secret"); err == nil {
		t.Fatal("expected unsafe cache name to fail")
	}
}
