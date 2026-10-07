package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/rest-sh/restish/v2/config"
)

// writeConfig writes content to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "restish.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setLegacyConfigEnv(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	} else {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	}
}

func TestLoad_Empty(t *testing.T) {
	path := writeConfig(t, `{}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restish.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("missing file should not return an error, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config for missing file")
	}
}

func TestSavePreservesExistingConfigDirMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "restish.json")
	if err := config.Save(path, &config.Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("dir mode = %#o, want 0755", got)
	}
}

func TestSaveCreatesMissingConfigDirSecurely(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	path := filepath.Join(dir, "restish.json")
	if err := config.Save(path, &config.Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %#o, want 0700", got)
	}
}

func TestLoadExplicit_MissingFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restish.json")
	_, err := config.LoadExplicit(path)
	if err == nil {
		t.Fatal("expected missing explicit config to error")
	}
	if !strings.Contains(err.Error(), "--rsh-config") ||
		!strings.Contains(err.Error(), "v2 does not fall back to the default config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_AuthRefRequiresKnownProfile(t *testing.T) {
	cfg := &config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {
				Profiles: map[string]*config.ProfileConfig{
					"default": {AuthRef: "missing"},
				},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected unknown auth_ref error")
	}
	if !strings.Contains(err.Error(), "auth_profiles is not defined") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_AuthAndAuthRefAreMutuallyExclusive(t *testing.T) {
	cfg := &config.Config{
		AuthProfiles: map[string]*config.AuthConfig{
			"shared": {Type: "http-basic"},
		},
		APIs: map[string]*config.APIConfig{
			"myapi": {
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						AuthRef: "shared",
						Auth:    &config.AuthConfig{Type: "http-basic"},
					},
				},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected mutual exclusion error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_CredentialAuthRefRequiresKnownProfile(t *testing.T) {
	cfg := &config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						Credentials: map[string]*config.CredentialConfig{
							"UserOAuth": {AuthRef: "missing"},
						},
					},
				},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected unknown credential auth_ref error")
	}
	if !strings.Contains(err.Error(), "auth_profiles is not defined") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_CredentialAuthAndAuthRefAreMutuallyExclusive(t *testing.T) {
	cfg := &config.Config{
		AuthProfiles: map[string]*config.AuthConfig{
			"shared": {Type: "http-basic"},
		},
		APIs: map[string]*config.APIConfig{
			"myapi": {
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						Credentials: map[string]*config.CredentialConfig{
							"UserOAuth": {
								AuthRef: "shared",
								Auth:    &config.AuthConfig{Type: "http-basic"},
							},
						},
					},
				},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected mutual exclusion error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_CredentialSatisfiesRejectsEmptyValues(t *testing.T) {
	cfg := &config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						Credentials: map[string]*config.CredentialConfig{
							"UserOAuth": {Satisfies: []string{"items:read", " "}},
						},
					},
				},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected empty satisfies value error")
	}
	if !strings.Contains(err.Error(), "satisfies") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOpenAPIExtensionAliases(t *testing.T) {
	tests := []struct {
		name    string
		aliases map[string]string
		wantErr string
	}{
		{name: "canonical", aliases: map[string]string{config.XCLIPositionExtension: "x-cli-position"}},
		{name: "custom", aliases: map[string]string{config.XCLIPositionExtension: "x-vendor-position"}},
		{name: "unsupported semantic", aliases: map[string]string{"x-cli-unknown": "x-vendor-unknown"}, wantErr: "unsupported Restish extension"},
		{name: "not an extension", aliases: map[string]string{config.XCLIPositionExtension: "position"}, wantErr: "starting with \"x-\""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateOpenAPIExtensionAliases(tc.aliases)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoad_JSONC_Comments(t *testing.T) {
	path := writeConfig(t, `{
		// This is a comment
		"apis": {
			/* block comment */
			"myapi": {
				"base_url": "https://api.example.com"
			}
		}
	}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("JSONC with comments should parse without error, got: %v", err)
	}
	if cfg.APIs["myapi"] == nil {
		t.Fatal("expected myapi to be loaded")
	}
	if cfg.APIs["myapi"].BaseURL != "https://api.example.com" {
		t.Errorf("unexpected base_url: %q", cfg.APIs["myapi"].BaseURL)
	}
}

func TestLoad_ValidAPIs(t *testing.T) {
	path := writeConfig(t, `{
		"apis": {
			"github": { "base_url": "https://api.github.com" },
			"stripe": { "base_url": "https://api.stripe.com" }
		}
	}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.APIs) != 2 {
		t.Fatalf("expected 2 APIs, got %d", len(cfg.APIs))
	}
	if cfg.APIs["github"].BaseURL != "https://api.github.com" {
		t.Errorf("unexpected github base_url: %q", cfg.APIs["github"].BaseURL)
	}
}

func TestValidateAPIName(t *testing.T) {
	valid := []string{"café", "привет", "東京", "mañana-api", "api_123"}
	for _, name := range valid {
		t.Run("valid_"+name, func(t *testing.T) {
			if err := config.ValidateAPIName(name); err != nil {
				t.Fatalf("ValidateAPIName(%q): %v", name, err)
			}
		})
	}

	invalid := []string{"", "foo/bar", "foo:bar", "foo bar", "foo?bar", "foo#bar", "foo=bar", "-foo", "_foo", "foo$bar"}
	for _, name := range invalid {
		t.Run("invalid_"+name, func(t *testing.T) {
			if err := config.ValidateAPIName(name); err == nil {
				t.Fatalf("ValidateAPIName(%q) succeeded, want error", name)
			}
		})
	}
}

func TestValidateRejectsInvalidConfiguredAPIName(t *testing.T) {
	cfg := &config.Config{
		APIs: map[string]*config.APIConfig{
			"foo/bar": {BaseURL: "https://api.example.com"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected invalid API name error")
	}
	if !strings.Contains(err.Error(), "API name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_ProfileTLSSignerParams(t *testing.T) {
	path := writeConfig(t, `{
		"apis": {
			"demo": {
				"profiles": {
					"default": {
						"tls_signer": "pkcs11",
						"tls_signer_params": {
							"module": "/usr/local/lib/pkcs11.so",
							"slot": "0"
						}
					}
				}
			}
		}
	}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prof := cfg.APIs["demo"].Profiles["default"]
	if prof.TLSSigner != "pkcs11" {
		t.Fatalf("unexpected tls_signer: %q", prof.TLSSigner)
	}
	if prof.TLSSignerParams["module"] != "/usr/local/lib/pkcs11.so" {
		t.Fatalf("unexpected module param: %q", prof.TLSSignerParams["module"])
	}
	if prof.TLSSignerParams["slot"] != "0" {
		t.Fatalf("unexpected slot param: %q", prof.TLSSignerParams["slot"])
	}
}

func TestLoad_CacheConfig(t *testing.T) {
	path := writeConfig(t, `{"cache": {"max_size": "500MB"}}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Cache.MaxSize != "500MB" {
		t.Errorf("unexpected max_size: %q", cfg.Cache.MaxSize)
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	path := writeConfig(t, `{ "apis": [}`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	var parseErr *config.ParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("expected *config.ParseError, got %T: %v", err, err)
	}
	if parseErr.Line == 0 || parseErr.Column == 0 {
		t.Fatalf("expected line:column in parse error, got %d:%d", parseErr.Line, parseErr.Column)
	}
}

func TestLoad_UnmarshalTypeErrorIncludesLineColumn(t *testing.T) {
	path := writeConfig(t, `{
  "apis": {
    "example": {
      "base_url": "https://api.example.com",
      "spec_files": "spec.yaml"
    }
  }
}`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for wrong field type, got nil")
	}
	var parseErr *config.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected *config.ParseError, got %T: %v", err, err)
	}
	if parseErr.Line != 5 || parseErr.Column == 0 {
		t.Fatalf("expected line 5/column for type error, got %d:%d", parseErr.Line, parseErr.Column)
	}
}

func TestLoad_UnknownField(t *testing.T) {
	path := writeConfig(t, `{"apiss": {}}`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "config:") {
		t.Errorf("expected 'config:' prefix in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected unknown-field suggestion, got: %v", err)
	}
}

func TestDiagnoseConfig_UnknownNestedLegacyField(t *testing.T) {
	path := writeConfig(t, `{
  "apis": {
    "example": {
      "base": "https://api.example.com"
    }
  }
}`)
	diags, err := config.DiagnoseConfig(path)
	if err != nil {
		t.Fatalf("DiagnoseConfig: %v", err)
	}
	if len(diags.UnknownFields) != 1 {
		t.Fatalf("UnknownFields = %#v, want one", diags.UnknownFields)
	}
	diag := diags.UnknownFields[0]
	if diag.Path != "apis.example.base" {
		t.Fatalf("Path = %q, want apis.example.base", diag.Path)
	}
	if diag.Line != 4 || diag.Column == 0 {
		t.Fatalf("expected line/column for unknown field, got %d:%d", diag.Line, diag.Column)
	}
	if !strings.Contains(diag.Hint, `v1 used "base"`) {
		t.Fatalf("expected v1 hint, got %q", diag.Hint)
	}

	_, err = config.Load(path)
	if err == nil {
		t.Fatal("expected strict Load to reject v1 field")
	}
	for _, want := range []string{"apis.example.base", `v2 uses "base_url"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected strict error to contain %q, got %v", want, err)
		}
	}
}

func TestLoad_RejectsTrailingTokens(t *testing.T) {
	path := writeConfig(t, `{"apis": {}} true`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for trailing tokens, got nil")
	}
	var parseErr *config.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected *config.ParseError, got %T: %v", err, err)
	}
}

func TestDefaultPath_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RSH_CONFIG_DIR", dir)
	got := config.DefaultPath()
	want := filepath.Join(dir, "restish.json")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPath_ContainsRestish(t *testing.T) {
	// Unset override to test the platform default.
	t.Setenv("RSH_CONFIG_DIR", "")
	got := config.DefaultPath()
	if !strings.Contains(got, "restish") {
		t.Errorf("DefaultPath() = %q, expected it to contain 'restish'", got)
	}
	if !strings.HasSuffix(got, "restish.json") {
		t.Errorf("DefaultPath() = %q, expected it to end with 'restish.json'", got)
	}
}

func TestSave_WritesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restish.json")
	if err := config.Save(path, &config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "api.example.com") {
		t.Fatalf("expected written config, got:\n%s", string(data))
	}

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "restish.json.*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected temp files to be cleaned up, got: %v", matches)
	}
}

func TestSave_CreatesConfigDirWithSecurePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(dir, "restish.json")
	if err := config.Save(path, &config.Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("expected config dir permission 0700, got %04o", perm)
	}
}

func TestSave_ConcurrentWritesLeaveValidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restish.json")
	if err := config.Save(path, &config.Config{}); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := &config.Config{
				APIs: map[string]*config.APIConfig{
					"myapi": {BaseURL: "https://api.example.com"},
				},
			}
			if i%2 == 0 {
				cfg.Cache.MaxSize = "2MB"
			}
			_ = config.Save(path, cfg)
		}(i)
	}
	wg.Wait()

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load after concurrent writes: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil config after concurrent writes")
	}
}

func TestSave_IgnoresStaleCrashTempFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restish.json")
	if err := config.Save(path, &config.Config{}); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	staleTmp := path + ".stale.tmp"
	if err := os.WriteFile(staleTmp, []byte(`{"apis": {`), 0o600); err != nil {
		t.Fatalf("write stale tmp: %v", err)
	}

	want := &config.Config{APIs: map[string]*config.APIConfig{"myapi": {BaseURL: "https://api.example.com"}}}
	if err := config.Save(path, want); err != nil {
		t.Fatalf("Save with stale tmp present: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if loaded.APIs["myapi"] == nil || loaded.APIs["myapi"].BaseURL != "https://api.example.com" {
		t.Fatalf("unexpected loaded config after stale tmp save: %#v", loaded)
	}
}

func TestConfigFileHasInsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not authoritative on Windows")
	}

	path := writeConfig(t, `{}`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	insecure, err := config.ConfigFileHasInsecurePermissions(path)
	if err != nil {
		t.Fatalf("ConfigFileHasInsecurePermissions: %v", err)
	}
	if !insecure {
		t.Fatalf("expected insecure permissions for 0644")
	}
}

func TestConfigFileHasInsecurePermissions_Private(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not authoritative on Windows")
	}

	path := writeConfig(t, `{}`)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	insecure, err := config.ConfigFileHasInsecurePermissions(path)
	if err != nil {
		t.Fatalf("ConfigFileHasInsecurePermissions: %v", err)
	}
	if insecure {
		t.Fatalf("expected secure permissions for 0600")
	}
}
