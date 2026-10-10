package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rest-sh/restish/v2/internal/fileutil"
	"github.com/tidwall/jsonc"
)

// Config is the top-level configuration for Restish, loaded from restish.json.
type Config struct {
	// APIs is a map of short API name to per-API configuration.
	APIs map[string]*APIConfig `json:"apis,omitempty"`

	// AuthProfiles holds named auth configurations that API profiles can
	// reference with auth_ref.
	AuthProfiles map[string]*AuthConfig `json:"auth_profiles,omitempty"`

	// Cache holds global cache settings.
	Cache CacheConfig `json:"cache,omitempty"`

	// Theme customizes syntax highlighting for readable terminal output.
	// Keys are Chroma token names or Restish theme aliases; values are Chroma
	// style descriptors such as "#afd787" or "bold #ff5f87".
	Theme map[string]string `json:"theme,omitempty"`

	// ThemeSource records the source URL last used by `config theme set`.
	ThemeSource string `json:"theme_source,omitempty"`

	// Plugins holds per-plugin configuration keyed by plugin name (without the
	// "restish-" prefix). Each value is stored as raw JSON so that restish
	// itself does not need to know the shape of each plugin's config.
	// Plugins can read their config via the "config-read" message.
	//
	// Example restish.json entry:
	//   "plugins": {
	//     "bulk": { "concurrency": 4, "retry": true }
	//   }
	Plugins map[string]json.RawMessage `json:"plugins,omitempty"`

	// Migration describes a one-time v1 -> v2 config migration that happened
	// while loading this config. It is not persisted back into restish.json.
	// Embedders using config.Load will not see this populated; only the restish
	// CLI's explicit migration step sets it.
	Migration *MigrationInfo `json:"-"`
}

// MigrationInfo describes a one-time v1 -> v2 config migration performed
// by the restish CLI when an old apis.json is detected.
type MigrationInfo struct {
	SourcePath string
	BackupPath string
	Warnings   []string
}

// APIConfig holds per-API configuration.
type APIConfig struct {
	// BaseURL is the base URL for all requests to this API.
	BaseURL string `json:"base_url,omitempty"`
	// SpecURL is the URL of the OpenAPI spec for this API (optional).
	// Mutually exclusive with SpecFiles; SpecFiles takes precedence when both are set.
	SpecURL string `json:"spec_url,omitempty"`
	// UnauthenticatedSpec fetches OpenAPI discovery metadata without applying
	// the API profile credential. Use it only for publicly published contracts.
	UnauthenticatedSpec bool `json:"unauthenticated_spec,omitempty"`
	// AllowCrossOriginSpec permits discovery from Link-header spec URLs on
	// hosts other than base_url. Private, loopback, link-local, and
	// unspecified IP literal targets are still rejected.
	AllowCrossOriginSpec bool `json:"allow_cross_origin_spec,omitempty"`
	// SpecFiles is an ordered list of local file paths or URLs to load the API
	// spec from. Multiple files are deep-merged in order (later entries win on
	// conflict). When set, network spec discovery is skipped entirely.
	SpecFiles []string `json:"spec_files,omitempty"`
	// OperationBase, when set, is an absolute path resolved against base_url for
	// paths generated from OpenAPI operations. Useful when operation paths should
	// escape or replace a sub-path in base_url.
	OperationBase string `json:"operation_base,omitempty"`
	// CommandLayout controls how generated operations are arranged under the
	// API command. Empty or "flat" keeps one flat command namespace; "tags"
	// groups operations under first-tag subcommands.
	CommandLayout string `json:"command_layout,omitempty"`
	// ExcludedOperationIDs hides selected OpenAPI operations from generated
	// commands for an embedded product surface without changing the upstream
	// API description.
	ExcludedOperationIDs []string `json:"excluded_operation_ids,omitempty"`
	// ShowHiddenOperations exposes operations marked x-cli-hidden when an
	// embedded product intentionally owns a different curated command surface.
	ShowHiddenOperations bool `json:"show_hidden_operations,omitempty"`
	// ServerVariables supplies explicit values for OpenAPI server URL variables.
	// Values are used for generated operation path resolution; enum values from
	// remote specs are never expanded eagerly.
	ServerVariables map[string]string `json:"server_variables,omitempty"`
	// URLOverrides rewrites resolved request URL prefixes before execution.
	// It is useful when an OpenAPI document names canonical servers but this
	// profile should route requests to staging, local, or test endpoints.
	URLOverrides map[string]string `json:"url_overrides,omitempty"`
	// AllowedOperationOrigins permits generated commands to use operation- or
	// path-level OpenAPI servers on origins outside base_url.
	AllowedOperationOrigins []string `json:"allowed_operation_origins,omitempty"`
	// Profiles is a map of profile name to profile configuration.
	Profiles map[string]*ProfileConfig `json:"profiles,omitempty"`
	// Pagination holds optional per-API pagination configuration.
	Pagination *PaginationConfig `json:"pagination,omitempty"`
	// RetryMaxWait caps Retry-After/X-Retry-In delays for this API when no
	// command-line or environment override is supplied.
	RetryMaxWait string `json:"retry_max_wait,omitempty"`
	// PreserveHeaderCase sends user/API-supplied header names with their
	// configured casing for broken HTTP/1.x servers that treat names as
	// case-sensitive. It cannot affect HTTP/2, where header names are lowercase
	// by protocol.
	PreserveHeaderCase bool `json:"preserve_header_case,omitempty"`
}

// PaginationConfig holds per-API pagination settings.
type PaginationConfig struct {
	// ItemsPath is a filter expression that extracts the items array from the
	// response body (e.g. "data" for JSON:API, "results" for some REST APIs).
	// When empty, the body itself is used (if it is an array).
	ItemsPath string `json:"items_path,omitempty"`
	// NextPath is a filter expression that extracts the next-page URL from the
	// response body (alternative to Link header rel="next").
	NextPath string `json:"next_path,omitempty"`
	// PageParam is the URL query parameter that increments between pages when
	// an API has no next links or next_path metadata.
	PageParam string `json:"page_param,omitempty"`
}

// ProfileConfig holds per-profile overrides for an API.
type ProfileConfig struct {
	// BaseURL overrides the API-level base_url when this profile is active.
	BaseURL string `json:"base_url,omitempty"`
	// OperationBase overrides API-level operation_base when this profile is active.
	OperationBase string `json:"operation_base,omitempty"`
	// Headers is a list of persistent "Name: Value" headers sent with every request.
	Headers []string `json:"headers,omitempty"`
	// Query is a list of persistent "key=value" query params sent with every request.
	Query []string `json:"query,omitempty"`
	// CACertPath is an optional PEM CA bundle for this profile.
	CACertPath string `json:"ca_cert,omitempty"`
	// ClientCertPath is the PEM client certificate path for this profile.
	ClientCertPath string `json:"client_cert,omitempty"`
	// ClientKeyPath is the PEM client private key path for this profile.
	ClientKeyPath string `json:"client_key,omitempty"`
	// TLSSigner selects a tls-signer plugin for mTLS client certificate signing.
	TLSSigner string `json:"tls_signer,omitempty"`
	// TLSSignerParams passes plugin-specific configuration to the tls-signer.
	TLSSignerParams map[string]string `json:"tls_signer_params,omitempty"`
	// ServerVariables overrides API-level OpenAPI server URL variables for this
	// profile when generating operation paths.
	ServerVariables map[string]string `json:"server_variables,omitempty"`
	// URLOverrides overrides or extends API-level URL prefix rewrites for this
	// profile.
	URLOverrides map[string]string `json:"url_overrides,omitempty"`
	// Auth holds authentication configuration for this profile.
	Auth *AuthConfig `json:"auth,omitempty"`
	// AuthRef names a top-level auth_profiles entry to use for this profile.
	AuthRef string `json:"auth_ref,omitempty"`
	// Credentials maps operation credential requirement IDs to auth
	// configurations that satisfy them.
	Credentials map[string]*CredentialConfig `json:"credentials,omitempty"`
}

// CredentialConfig binds a local auth configuration to a generated operation
// credential requirement.
type CredentialConfig struct {
	// Auth holds inline authentication configuration for this credential.
	Auth *AuthConfig `json:"auth,omitempty"`
	// AuthRef names a top-level auth_profiles entry to use for this credential.
	AuthRef string `json:"auth_ref,omitempty"`
	// Satisfies lists requirement values, such as OAuth scopes or non-OAuth
	// roles, that this credential is intended to satisfy.
	Satisfies []string `json:"satisfies,omitempty"`
}

// AuthConfig holds authentication configuration for a profile.
type AuthConfig struct {
	// Type identifies the auth mechanism (e.g. "http-basic", "oauth-client-credentials").
	Type string `json:"type,omitempty"`
	// Params holds handler-specific configuration, e.g. {"username": "alice"}.
	Params map[string]string `json:"params,omitempty"`
}

// CacheConfig holds cache settings.
type CacheConfig struct {
	// MaxSize is the maximum cache size (e.g. "100MB"). Default: "100MB".
	MaxSize string `json:"max_size,omitempty"`
}

// DefaultPath returns the path to the default config file, honoring
// the RSH_CONFIG_DIR and XDG environment variable overrides.
func DefaultPath() string {
	return NewPaths().ConfigFile()
}

// ErrPermissionCheckUnsupported reports that Restish cannot verify file
// permissions on the current platform.
var ErrPermissionCheckUnsupported = errors.New("permission check unsupported on this platform")

// ConfigFileHasInsecurePermissions reports whether path is readable by group or others.
// On Windows, existing files return ErrPermissionCheckUnsupported because Unix
// permission bits are not authoritative and ACL inspection is not implemented.
func ConfigFileHasInsecurePermissions(path string) (bool, error) {
	return configFileHasInsecurePermissions(path, runtime.GOOS)
}

func configFileHasInsecurePermissions(path, goos string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if goos == "windows" {
		return false, fmt.Errorf("%w: Windows ACL inspection is not implemented", ErrPermissionCheckUnsupported)
	}
	return info.Mode().Perm()&0o077 != 0, nil
}

// Save serialises cfg as indented JSON and writes it to path, creating the
// parent directory if necessary.  Existing JSONC comments are not preserved.
func Save(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	lock, err := fileutil.LockSiblingFile(path)
	if err != nil {
		return err
	}
	defer lock.Close()
	return fileutil.AtomicWriteFile(path, append(data, '\n'), fileutil.AtomicWriteOptions{
		FileMode: 0o600, DirMode: 0o700,
	})
}

// Load reads and parses the JSONC config file at path.
// If the file does not exist, an empty default Config is returned without error.
// A missing config file is normal for first-time users; Load does not
// perform a v1->v2 migration. The restish CLI runs its own legacy migration
// step before calling Load. Use LoadExplicit when a missing file should be an
// error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %w", path, err)
	}

	return ParseConfigBytes(path, data)
}

// LoadExplicit reads a user-selected config file. Unlike Load, a missing file
// is an error because explicit config selection must not silently fall back to
// an empty or platform-default config.
func LoadExplicit(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("config: --rsh-config %s does not exist; v2 does not fall back to the default config; create the file or remove the flag", path)
	}
	if err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %w", path, err)
	}
	return ParseConfigBytes(path, data)
}

// LoadExplicitOrEmpty reads a user-selected config file, returning an empty
// config when the selected file does not exist. Use this only for commands that
// are about to write the selected config path.
func LoadExplicitOrEmpty(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %w", path, err)
	}
	return ParseConfigBytes(path, data)
}

// ParseConfigBytes parses a config payload from in-memory bytes using the same
// JSONC stripping, strict-field checks, and validation as Load.
func ParseConfigBytes(path string, data []byte) (*Config, error) {

	// Strip JSONC comments before parsing so users can annotate their config.
	stripped := jsonc.ToJSON(data)

	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(stripped))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		err = withUnknownFieldSuggestion(err, stripped)
		line, col := extractJSONErrorPosition(err, stripped)
		return nil, &ParseError{Path: path, Err: err, Line: line, Column: col}
	}
	if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("unexpected trailing content")
		}
		line, col := extractJSONErrorPosition(err, stripped)
		return nil, &ParseError{Path: path, Err: err, Line: line, Column: col}
	}
	if err := Validate(&cfg); err != nil {
		return nil, &ParseError{Path: path, Err: err}
	}

	return &cfg, nil
}

// ConfigDiagnostics describes non-executing config diagnostics. It is intended
// for recovery commands such as doctor; normal execution still uses strict
// parsing and validation.
type ConfigDiagnostics struct {
	UnknownFields []UnknownFieldDiagnostic
}

// UnknownFieldDiagnostic reports a config field that is not part of the v2
// schema. Path is a dotted config path such as "apis.example.base".
type UnknownFieldDiagnostic struct {
	Path       string
	Field      string
	Line       int
	Column     int
	Suggestion string
	Hint       string
}

// DiagnoseConfig inspects a config file without accepting unknown fields for
// execution. It returns syntax errors, but otherwise reports schema diagnostics
// that recovery commands can display alongside strict parser failures.
func DiagnoseConfig(path string) (*ConfigDiagnostics, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &ConfigDiagnostics{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %w", path, err)
	}
	return diagnoseConfigBytes(data)
}

func diagnoseConfigBytes(data []byte) (*ConfigDiagnostics, error) {
	stripped := jsonc.ToJSON(data)
	var root any
	dec := json.NewDecoder(bytes.NewReader(stripped))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	diags := &ConfigDiagnostics{}
	collectUnknownFields(diags, stripped, root, reflect.TypeOf(Config{}), "")
	return diags, nil
}

// Validate checks cross-field config invariants that JSON decoding alone cannot
// enforce.
func Validate(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	for name, api := range cfg.APIs {
		if err := ValidateAPIName(name); err != nil {
			return fmt.Errorf("apis.%s: invalid API name: %w", name, err)
		}
		if api == nil {
			continue
		}
		if err := ValidateOperationBase(api.OperationBase); err != nil {
			return fmt.Errorf("apis.%s.operation_base: %w", name, err)
		}
		if err := ValidateCommandLayout(api.CommandLayout); err != nil {
			return fmt.Errorf("apis.%s.command_layout: %w", name, err)
		}
		if err := ValidateRetryMaxWait(api.RetryMaxWait); err != nil {
			return fmt.Errorf("apis.%s.retry_max_wait: %w", name, err)
		}
		if err := ValidateURLOverrides(api.URLOverrides); err != nil {
			return fmt.Errorf("apis.%s.url_overrides: %w", name, err)
		}
		for i, origin := range api.AllowedOperationOrigins {
			if err := ValidateOperationOriginPattern(origin); err != nil {
				return fmt.Errorf("apis.%s.allowed_operation_origins[%d]: %w", name, i, err)
			}
		}
		if api.OperationBase != "" {
			if err := ValidateBaseURLForOperationBase(api.BaseURL); err != nil {
				return fmt.Errorf("apis.%s.base_url: %w", name, err)
			}
		}
		for profileName, prof := range api.Profiles {
			if prof == nil {
				continue
			}
			if err := ValidateOperationBase(prof.OperationBase); err != nil {
				return fmt.Errorf("apis.%s.profiles.%s.operation_base: %w", name, profileName, err)
			}
			if prof.OperationBase != "" {
				baseURL := api.BaseURL
				if prof.BaseURL != "" {
					baseURL = prof.BaseURL
				}
				if err := ValidateBaseURLForOperationBase(baseURL); err != nil {
					return fmt.Errorf("apis.%s.profiles.%s.base_url: %w", name, profileName, err)
				}
			}
			if err := ValidateURLOverrides(prof.URLOverrides); err != nil {
				return fmt.Errorf("apis.%s.profiles.%s.url_overrides: %w", name, profileName, err)
			}
			if prof.Auth != nil && prof.AuthRef != "" {
				return fmt.Errorf("apis.%s.profiles.%s: auth and auth_ref are mutually exclusive", name, profileName)
			}
			if prof.AuthRef != "" {
				if cfg.AuthProfiles == nil {
					return fmt.Errorf("apis.%s.profiles.%s.auth_ref: auth profile %q is referenced, but auth_profiles is not defined; define auth_profiles.%s first", name, profileName, prof.AuthRef, prof.AuthRef)
				}
				if cfg.AuthProfiles[prof.AuthRef] == nil {
					return fmt.Errorf("apis.%s.profiles.%s.auth_ref: unknown auth profile %q", name, profileName, prof.AuthRef)
				}
			}
			for credentialID, credential := range prof.Credentials {
				if credentialID == "" {
					return fmt.Errorf("apis.%s.profiles.%s.credentials: credential id must not be empty", name, profileName)
				}
				if credential == nil {
					continue
				}
				if credential.Auth != nil && credential.AuthRef != "" {
					return fmt.Errorf("apis.%s.profiles.%s.credentials.%s: auth and auth_ref are mutually exclusive", name, profileName, credentialID)
				}
				if credential.AuthRef != "" {
					if cfg.AuthProfiles == nil {
						return fmt.Errorf("apis.%s.profiles.%s.credentials.%s.auth_ref: auth profile %q is referenced, but auth_profiles is not defined; define auth_profiles.%s first", name, profileName, credentialID, credential.AuthRef, credential.AuthRef)
					}
					if cfg.AuthProfiles[credential.AuthRef] == nil {
						return fmt.Errorf("apis.%s.profiles.%s.credentials.%s.auth_ref: unknown auth profile %q", name, profileName, credentialID, credential.AuthRef)
					}
				}
				for _, need := range credential.Satisfies {
					if strings.TrimSpace(need) == "" {
						return fmt.Errorf("apis.%s.profiles.%s.credentials.%s.satisfies: values must not be empty", name, profileName, credentialID)
					}
				}
			}
		}
	}
	return nil
}

// ValidateAPIName enforces a shell- and URL-friendly API short name while
// allowing non-English letters and numbers.
func ValidateAPIName(name string) error {
	if name == "" {
		return fmt.Errorf("must not be empty")
	}
	first, _ := utf8.DecodeRuneInString(name)
	if first == utf8.RuneError || !(unicode.IsLetter(first) || unicode.IsNumber(first)) {
		return fmt.Errorf("must start with a Unicode letter or number")
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '-' || r == '_' {
			continue
		}
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("must not contain whitespace or control characters")
		}
		return fmt.Errorf("contains unsupported character %q; use Unicode letters, numbers, marks, '-' or '_'", r)
	}
	return nil
}

// ValidateCommandLayout enforces supported generated command arrangements.
func ValidateCommandLayout(raw string) error {
	switch raw {
	case "", "flat", "tags":
		return nil
	default:
		return fmt.Errorf("must be \"flat\" or \"tags\"")
	}
}

// ValidateRetryMaxWait enforces the retry_max_wait duration contract.
func ValidateRetryMaxWait(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return err
	}
	if d <= 0 {
		return fmt.Errorf("must be greater than 0")
	}
	return nil
}

// ValidateURLOverrides enforces the URL prefix rewrite contract.
func ValidateURLOverrides(overrides map[string]string) error {
	for source, destination := range overrides {
		if err := validateURLOverrideEndpoint(source); err != nil {
			return fmt.Errorf("%q: %w", source, err)
		}
		if err := validateURLOverrideEndpoint(destination); err != nil {
			return fmt.Errorf("%q destination: %w", source, err)
		}
	}
	return nil
}

func validateURLOverrideEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("must be an absolute http/https URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must not include userinfo, query, or fragment")
	}
	return nil
}

// ApplyURLOverrides rewrites rawURL when it matches a configured URL prefix.
// The longest matching source prefix wins. Query strings and fragments from the
// request URL are preserved.
func ApplyURLOverrides(rawURL string, overrides map[string]string) (string, bool, error) {
	if len(overrides) == 0 {
		return rawURL, false, nil
	}
	target, err := url.Parse(rawURL)
	if err != nil || !target.IsAbs() || target.Host == "" {
		return rawURL, false, nil
	}
	if err := ValidateURLOverrides(overrides); err != nil {
		return rawURL, false, err
	}
	sources := make([]string, 0, len(overrides))
	for source := range overrides {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		left := normalizedOverrideSource(sources[i])
		right := normalizedOverrideSource(sources[j])
		if len(left) != len(right) {
			return len(left) > len(right)
		}
		return left < right
	})
	for _, source := range sources {
		src, _ := url.Parse(source)
		if !urlOverrideMatches(target, src) {
			continue
		}
		dst, _ := url.Parse(overrides[source])
		rewritten := *dst
		rewritten.RawQuery = target.RawQuery
		rewritten.Fragment = target.Fragment
		if err := setURLOverridePath(&rewritten, joinURLOverridePath(dst.EscapedPath(), overrideSuffixPath(target.EscapedPath(), src.EscapedPath()))); err != nil {
			return rawURL, false, err
		}
		return rewritten.String(), true, nil
	}
	return rawURL, false, nil
}

func normalizedOverrideSource(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimRight(p, "/")
}

func urlOverrideMatches(target, source *url.URL) bool {
	if !strings.EqualFold(target.Scheme, source.Scheme) || !strings.EqualFold(target.Host, source.Host) {
		return false
	}
	sourcePath := source.EscapedPath()
	if sourcePath == "" {
		sourcePath = "/"
	}
	targetPath := target.EscapedPath()
	if targetPath == "" {
		targetPath = "/"
	}
	sourcePath = strings.TrimRight(sourcePath, "/")
	if sourcePath == "" {
		return true
	}
	return targetPath == sourcePath || strings.HasPrefix(targetPath, sourcePath+"/")
}

func overrideSuffixPath(targetPath, sourcePath string) string {
	if targetPath == "" {
		targetPath = "/"
	}
	if sourcePath == "" {
		sourcePath = "/"
	}
	sourcePath = strings.TrimRight(sourcePath, "/")
	if sourcePath == "" {
		if targetPath == "/" {
			return ""
		}
		return targetPath
	}
	return strings.TrimPrefix(targetPath, sourcePath)
}

func joinURLOverridePath(basePath, suffix string) string {
	if suffix == "" {
		if basePath == "" {
			return ""
		}
		return basePath
	}
	if basePath == "" || basePath == "/" {
		return suffix
	}
	return strings.TrimRight(basePath, "/") + suffix
}

func setURLOverridePath(u *url.URL, escapedPath string) error {
	path, err := url.PathUnescape(escapedPath)
	if err != nil {
		return err
	}
	u.Path = path
	u.RawPath = escapedPath
	return nil
}

// ValidateOperationBase enforces the v2 contract that operation_base is an
// absolute URL path prefix.
func ValidateOperationBase(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("must be an absolute path")
	}
	if u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return fmt.Errorf("must be an absolute path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must not include query or fragment")
	}
	return nil
}

// ValidateBaseURLForOperationBase ensures operation_base can be resolved at
// load time instead of failing later when generated commands run.
func ValidateBaseURLForOperationBase(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return fmt.Errorf("must be an absolute http/https URL when operation_base is set")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("must use http or https when operation_base is set")
	}
	if u.Host == "" {
		return fmt.Errorf("must be an absolute http/https URL when operation_base is set")
	}
	return nil
}

// ResolveOperationBaseURL resolves an absolute operation_base path against the
// API base URL using the same URL-reference semantics as Restish v1.
func ResolveOperationBaseURL(baseURL, operationBase string) (string, error) {
	if operationBase == "" {
		return baseURL, nil
	}
	if err := ValidateOperationBase(operationBase); err != nil {
		return "", err
	}
	base, err := url.Parse(baseURL)
	if err != nil || !base.IsAbs() || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		if validateErr := ValidateBaseURLForOperationBase(baseURL); validateErr != nil {
			return "", validateErr
		}
	}
	return base.ResolveReference(&url.URL{Path: operationBase}).String(), nil
}

// ValidateOperationOriginPattern validates an allowed_operation_origins entry.
// Patterns are origins such as https://api.example.com or conservative
// single-label wildcards such as https://*.example.com.
func ValidateOperationOriginPattern(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("must be an absolute http/https origin")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must not include userinfo, path, query, or fragment")
	}
	host := u.Hostname()
	if strings.Count(host, "*") == 0 {
		return nil
	}
	if !strings.HasPrefix(host, "*.") || strings.Count(host, "*") != 1 {
		return fmt.Errorf("wildcard host must use the form *.example.com")
	}
	suffix := strings.TrimPrefix(host, "*.")
	if suffix == "" || strings.Contains(suffix, "*") || !strings.Contains(suffix, ".") {
		return fmt.Errorf("wildcard host must include a concrete parent domain")
	}
	return nil
}

// OperationOriginAllowed reports whether serverURL's origin is permitted by
// allowed_operation_origins.
func OperationOriginAllowed(serverURL string, patterns []string) bool {
	u, err := url.Parse(serverURL)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return false
	}
	for _, pattern := range patterns {
		if operationOriginPatternMatches(u, pattern) {
			return true
		}
	}
	return false
}

func operationOriginPatternMatches(origin *url.URL, rawPattern string) bool {
	pattern, err := url.Parse(rawPattern)
	if err != nil || !pattern.IsAbs() || pattern.Host == "" {
		return false
	}
	if pattern.Scheme != origin.Scheme {
		return false
	}
	patternPort := pattern.Port()
	if patternPort != "" && patternPort != origin.Port() {
		return false
	}
	patternHost := pattern.Hostname()
	originHost := origin.Hostname()
	if strings.HasPrefix(patternHost, "*.") {
		suffix := strings.TrimPrefix(patternHost, "*.")
		return strings.HasSuffix(originHost, "."+suffix) && originHost != suffix
	}
	return strings.EqualFold(pattern.Host, origin.Host)
}

// extractJSONErrorPosition attempts to extract line:column from a JSON decode error.
// Returns (0, 0) if the position cannot be determined.
func extractJSONErrorPosition(err error, data []byte) (int, int) {
	// json.SyntaxError has Offset field in Go 1.11+
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		line, col := byteOffsetToLineColumn(data, syntaxErr.Offset)
		return line, col
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		line, col := byteOffsetToLineColumn(data, typeErr.Offset)
		return line, col
	}
	return 0, 0
}

func withUnknownFieldSuggestion(err error, stripped []byte) error {
	const prefix = "json: unknown field "
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return err
	}
	field := strings.Trim(msg[len(prefix):], `"`)
	if diags, diagErr := diagnoseConfigBytes(stripped); diagErr == nil {
		for _, diag := range diags.UnknownFields {
			if diag.Field != field {
				continue
			}
			if diag.Suggestion != "" && diag.Hint != "" {
				return fmt.Errorf("%w at %s (did you mean %q? %s)", err, diag.Path, diag.Suggestion, diag.Hint)
			}
			if diag.Suggestion != "" {
				return fmt.Errorf("%w at %s (did you mean %q?)", err, diag.Path, diag.Suggestion)
			}
			if diag.Hint != "" {
				return fmt.Errorf("%w at %s (%s)", err, diag.Path, diag.Hint)
			}
			return fmt.Errorf("%w at %s", err, diag.Path)
		}
	}
	return err
}

func collectUnknownFields(diags *ConfigDiagnostics, data []byte, node any, t reflect.Type, path string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := node.(map[string]any)
		if !ok {
			return
		}
		fields := jsonFieldTypes(t)
		for key, child := range obj {
			ft, ok := fields[key]
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if !ok {
				line, col := jsonFieldPosition(data, key)
				suggestion := closestJSONField(key, fields)
				diags.UnknownFields = append(diags.UnknownFields, UnknownFieldDiagnostic{
					Path:       childPath,
					Field:      key,
					Line:       line,
					Column:     col,
					Suggestion: suggestion,
					Hint:       legacyFieldHint(key),
				})
				continue
			}
			collectUnknownFields(diags, data, child, ft, childPath)
		}
	case reflect.Map:
		elem := t.Elem()
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() != reflect.Struct {
			return
		}
		obj, ok := node.(map[string]any)
		if !ok {
			return
		}
		for key, child := range obj {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			collectUnknownFields(diags, data, child, elem, childPath)
		}
	case reflect.Slice:
		elem := t.Elem()
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if elem.Kind() != reflect.Struct {
			return
		}
		items, ok := node.([]any)
		if !ok {
			return
		}
		for i, child := range items {
			collectUnknownFields(diags, data, child, elem, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

func jsonFieldTypes(t reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != "" {
			fields[name] = f.Type
		}
	}
	return fields
}

func closestJSONField(input string, fields map[string]reflect.Type) string {
	best := ""
	bestDistance := max(1, utf8.RuneCountInString(input)/4) + 1
	for name := range fields {
		d := levenshteinDistance(strings.ToLower(input), strings.ToLower(name))
		if d < bestDistance {
			bestDistance = d
			best = name
		}
	}
	return best
}

func legacyFieldHint(field string) string {
	switch field {
	case "base":
		return `Restish v1 used "base"; v2 uses "base_url".`
	case "security":
		return `older v2 drafts used "security"; v2 operation auth uses profile credentials and --rsh-auth.`
	case "auth-header":
		return `the old auth-header command was replaced by api auth inspect.`
	default:
		return ""
	}
}

func jsonFieldPosition(data []byte, field string) (int, int) {
	needle, err := json.Marshal(field)
	if err != nil {
		return 0, 0
	}
	idx := bytes.Index(data, needle)
	if idx < 0 {
		return 0, 0
	}
	return byteOffsetToLineColumn(data, int64(idx+1))
}

func levenshteinDistance(a, b string) int {
	if a == b {
		return 0
	}
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr := make([]int, len(br)+1)
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			insert := curr[j-1] + 1
			delete := prev[j] + 1
			replace := prev[j-1] + cost
			curr[j] = min(insert, min(delete, replace))
		}
		prev = curr
	}
	return prev[len(br)]
}

// ParseError is returned when the config file contains invalid JSON or
// an unrecognized field. It includes line:column position when available.
type ParseError struct {
	Path   string
	Err    error
	Line   int
	Column int
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("config: invalid config %s:%d:%d: %v", e.Path, e.Line, e.Column, e.Err)
	}
	return fmt.Sprintf("config: invalid config %s\n  %v", e.Path, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// byteOffsetToLineColumn translates a byte offset in data to a 1-indexed line:column.
func byteOffsetToLineColumn(data []byte, offset int64) (line int, col int) {
	line = 1
	col = 1
	for i := 0; i < int(offset) && i < len(data); i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return
}

func atomicWriteFile(path string, data []byte, fileMode os.FileMode, dirMode os.FileMode) error {
	lock, err := fileutil.LockSiblingFile(path)
	if err != nil {
		return err
	}
	defer lock.Close()
	return atomicWriteFileLocked(path, data, fileMode, dirMode, true)
}

func atomicWriteFileLocked(path string, data []byte, fileMode os.FileMode, dirMode os.FileMode, chmodExistingDir bool) error {
	return fileutil.AtomicWriteFile(path, data, fileutil.AtomicWriteOptions{
		FileMode:         fileMode,
		DirMode:          dirMode,
		ChmodExistingDir: chmodExistingDir,
		SyncDir:          true,
	})
}

// PluginConfig unmarshals the configuration stored under plugins[name] into v.
// Returns nil without modifying v when no config exists for that plugin.
// name should be the plugin's short name (without the "restish-" prefix).
func (c *Config) PluginConfig(name string, v any) error {
	if c == nil || c.Plugins == nil {
		return nil
	}
	raw, ok := c.Plugins[name]
	if !ok {
		return nil
	}
	return json.Unmarshal(raw, v)
}
