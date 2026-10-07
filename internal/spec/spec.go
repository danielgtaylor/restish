// Package spec handles API specification discovery, loading, and caching.
package spec

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/pb33f/libopenapi"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
)

// Loader detects and parses an API specification format.
// Multiple loaders may be registered; the highest-priority one that detects
// a given body is used.
type Loader interface {
	// Detect returns true if this loader recognises the content type and/or body.
	Detect(contentType string, body []byte) bool
	// LoadWithOptions parses body and returns a structured APISpec.
	LoadWithOptions(body []byte, opts LoadOptions) (*APISpec, error)
	// Priority determines loader selection order; higher priority wins.
	Priority() int
}

// HTTPFetcher fetches a remote spec resource and returns a response whose body
// the caller must close. Callers should use transport unless they intentionally
// need to replace the network stack.
type HTTPFetcher func(ctx context.Context, rawURL string, transport http.RoundTripper) (*http.Response, error)

// LoadOptions carries source metadata needed by loaders that resolve external
// references. Plain loaders may ignore it.
type LoadOptions struct {
	Context          context.Context
	ContentType      string
	SourceURL        string
	RequestedURL     string
	LocalPath        string
	AllowCrossOrigin bool
	Transport        http.RoundTripper
	Fetch            HTTPFetcher
	Trace            func(format string, args ...any)
}

// APISpec is a parsed API specification.
type APISpec struct {
	// ContentType is the MIME type the spec was fetched with.
	ContentType string
	// Raw is the original spec bytes (JSON or YAML).
	Raw []byte
	// Document is the libopenapi parsed representation.
	Document libopenapi.Document
	// SourceURL/LocalPath capture where the spec came from so external refs can
	// be resolved consistently when the raw spec is cached and reparsed.
	RequestedURL     string
	SourceURL        string
	LocalPath        string
	AllowCrossOrigin bool

	// modelOnce guards lazy construction of the V3 model.
	modelOnce   sync.Once
	modelResult *libopenapi.DocumentModel[v3.Document]
	modelErr    error

	// opsCacheMu guards the operations cache.
	opsCacheMu sync.Mutex
	opsCache   map[opsKey]opsEntry
}

// APIInfo is the top-level OpenAPI info object fields Restish surfaces in
// generated command help and cached operation metadata.
type APIInfo struct {
	Title       string
	Summary     string
	Description string
	Version     string
}

type opsKey struct {
	baseURL, operationBase string
	serverVariables        string
	extensionAliases       string
}
type opsEntry struct {
	ops      []Operation
	warnings []string
	err      error
}

// OperationOptions controls config-sensitive OpenAPI operation extraction.
type OperationOptions struct {
	BaseURL          string
	OperationBase    string
	ServerVariables  map[string]string
	ExtensionAliases map[string]string
	Warnf            func(format string, args ...any)
}

func operationOptionsKey(opts OperationOptions) opsKey {
	return opsKey{
		baseURL:          opts.BaseURL,
		operationBase:    opts.OperationBase,
		serverVariables:  ServerVariablesCacheKey(opts.ServerVariables),
		extensionAliases: ExtensionAliasesCacheKey(opts.ExtensionAliases),
	}
}

// ExtensionAliasesCacheKey returns a deterministic operation-cache identity
// for configured OpenAPI extension aliases.
func ExtensionAliasesCacheKey(values map[string]string) string {
	return stringMapCacheKey(values)
}

// ServerVariablesCacheKey returns a deterministic string for operation-cache
// identity. It is exported so on-disk cache metadata can use the same shape as
// the in-memory APISpec cache.
func ServerVariablesCacheKey(values map[string]string) string {
	return stringMapCacheKey(values)
}

func stringMapCacheKey(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(values[key])
		b.WriteByte('\n')
	}
	return b.String()
}

// V3Model returns the built V3 document model, memoizing the result so that
// Document.BuildV3Model() is called at most once per APISpec.
func (s *APISpec) V3Model() (*libopenapi.DocumentModel[v3.Document], error) {
	s.modelOnce.Do(func() {
		s.modelResult, s.modelErr = s.Document.BuildV3Model()
	})
	return s.modelResult, s.modelErr
}

// Info returns top-level OpenAPI info metadata from the V3 model.
func (s *APISpec) Info() (APIInfo, error) {
	model, err := s.V3Model()
	if err != nil || model == nil || model.Model.Info == nil {
		return APIInfo{}, err
	}
	info := model.Model.Info
	return APIInfo{
		Title:       info.Title,
		Summary:     info.Summary,
		Description: info.Description,
		Version:     info.Version,
	}, nil
}

// DefaultLoaders returns the built-in set of loaders.
func DefaultLoaders() []Loader {
	return []Loader{OpenAPILoader{}}
}

// load tries each loader (highest priority first) and returns the first match.
// Returns nil, nil if no loader recognises the content.
// Loaders that set ContentType or Raw in the returned spec retain their values;
// the caller-supplied contentType and body are only used as fallbacks.
func load(contentType string, body []byte, loaders []Loader) (*APISpec, error) {
	return loadWithOptions(contentType, body, loaders, LoadOptions{})
}

func loadWithOptions(contentType string, body []byte, loaders []Loader, opts LoadOptions) (*APISpec, error) {
	if opts.ContentType == "" {
		opts.ContentType = contentType
	}
	best := pickLoader(contentType, body, loaders)
	if best == nil {
		return nil, nil
	}
	spec, err := best.LoadWithOptions(body, opts)
	if err != nil {
		return nil, err
	}
	if spec.ContentType == "" {
		spec.ContentType = contentType
	}
	if len(spec.Raw) == 0 {
		spec.Raw = body
	}
	if spec.SourceURL == "" {
		spec.SourceURL = opts.SourceURL
	}
	if spec.RequestedURL == "" {
		spec.RequestedURL = opts.RequestedURL
	}
	if spec.LocalPath == "" {
		spec.LocalPath = opts.LocalPath
	}
	spec.AllowCrossOrigin = opts.AllowCrossOrigin
	return spec, nil
}

// pickLoader returns the highest-priority loader that detects the content, or nil.
func pickLoader(contentType string, body []byte, loaders []Loader) Loader {
	var best Loader
	for _, l := range loaders {
		if l.Detect(contentType, body) {
			if best == nil || l.Priority() > best.Priority() {
				best = l
			}
		}
	}
	return best
}
