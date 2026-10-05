package cli_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/cli"
	internalspec "github.com/rest-sh/restish/v2/internal/spec"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

var testPluginManifestCachePath string

// newTestCLI returns a CLI wired to in-memory buffers for use in tests.
// RetryBaseDelay is set to 1 ms so retry backoffs don't slow down the suite.
func newTestCLI(t *testing.T) (*cli.CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	c := cli.New()
	c.Stdin = strings.NewReader("")
	c.Stdout = &stdout
	c.Stderr = &stderr
	c.Hooks().PassReader = strings.NewReader("")
	c.Hooks().RetryBaseDelay = time.Millisecond
	stateDir := t.TempDir()
	if configDir := os.Getenv("RSH_CONFIG_DIR"); configDir != "" {
		stateDir = configDir
	}
	c.Hooks().ConfigPath = filepath.Join(stateDir, "restish.json")
	c.Hooks().TokenCachePath = filepath.Join(stateDir, "tokens.cbor")
	c.Hooks().CachePath = filepath.Join(stateDir, "http-cache")
	c.Hooks().SpecCachePath = filepath.Join(stateDir, "spec-cache")
	if testPluginManifestCachePath != "" {
		c.Hooks().PluginManifestCachePath = testPluginManifestCachePath
	} else {
		c.Hooks().PluginManifestCachePath = filepath.Join(stateDir, "plugin-manifest.cbor")
	}
	return c, &stdout, &stderr
}

func TestVersion(t *testing.T) {
	c, out, _ := newTestCLI(t)
	if err := c.Run([]string{"restish", "--version"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "2.0.0") {
		t.Errorf("expected version output to contain '2.0.0', got: %q", out.String())
	}
}

func TestVersionCommand(t *testing.T) {
	c, out, _ := newTestCLI(t)
	if err := c.Run([]string{"restish", "version"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "2.0.0") {
		t.Errorf("expected version output to contain '2.0.0', got: %q", out.String())
	}
}

func TestSpecBackedShortNameSyncsGeneratedCommandBeforeGenericFallback(t *testing.T) {
	c, _, _ := newTestCLI(t)
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	specBody := `openapi: "3.1.0"
info:
  title: Test
  version: "1.0.0"
paths:
  /ping:
    get:
      operationId: getPing
      responses:
        "200":
          description: OK
`
	if err := os.WriteFile(specPath, []byte(specBody), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	configBody := `{"apis":{"svc":{"base_url":"https://api.example.com","spec_files":[` + strconv.Quote(specPath) + `]}}}`
	if err := os.WriteFile(c.Hooks().ConfigPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var rr requestRecorder
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{"ok":true}`), nil
	})
	if err := c.Run([]string{"restish", "svc", "get-ping"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := rr.Last()
	if req == nil {
		t.Fatal("expected request")
	}
	if got := req.Method; got != "GET" {
		t.Fatalf("method = %q, want GET", got)
	}
	if got := req.URL.Path; got != "/ping" {
		t.Fatalf("path = %q, want /ping", got)
	}
}

func TestCommandSurfacePromotesGeneratedOperationsToRoot(t *testing.T) {
	c, _, _ := newTestCLI(t)
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	specBody := `openapi: "3.1.0"
info:
  title: Test
  version: "1.0.0"
paths:
  /ping:
    get:
      operationId: getPing
      responses:
        "200":
          description: OK
`
	if err := os.WriteFile(specPath, []byte(specBody), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	configBody := `{"apis":{"svc":{"base_url":"https://api.example.com","spec_files":[` + strconv.Quote(specPath) + `]}}}`
	if err := os.WriteFile(c.Hooks().ConfigPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "svc"})

	var rr requestRecorder
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{"ok":true}`), nil
	})

	if err := c.Run([]string{"restish", "get-ping"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := rr.Last()
	if req == nil {
		t.Fatal("expected request")
	}
	if got := req.Method; got != "GET" {
		t.Fatalf("method = %q, want GET", got)
	}
	if got := req.URL.Path; got != "/ping" {
		t.Fatalf("path = %q, want /ping", got)
	}
}

func TestCommandSurfacePromotedOperationHelpDoesNotPanic(t *testing.T) {
	c, out, _ := newTestCLI(t)
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	specBody := `openapi: "3.1.0"
info:
  title: Test
  version: "1.0.0"
paths:
  /ping:
    get:
      operationId: getPing
      responses:
        "200":
          description: OK
`
	if err := os.WriteFile(specPath, []byte(specBody), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	configBody := `{"apis":{"svc":{"base_url":"https://api.example.com","spec_files":[` + strconv.Quote(specPath) + `]}}}`
	if err := os.WriteFile(c.Hooks().ConfigPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "svc"})

	if err := c.Run([]string{"restish", "get-ping", "-h"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "get-ping") {
		t.Fatalf("expected help output to mention command, got:\n%s", got)
	}
}

func TestCommandSurfacePromotedAPIHelpFetchesSpecURLOnFirstRun(t *testing.T) {
	var specRequests int
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.yaml":
			specRequests++
			w.Header().Set("Content-Type", "application/yaml")
			fmt.Fprintf(w, `openapi: "3.1.0"
info:
  title: Test
  version: "1.0.0"
servers:
  - url: %s
paths:
  /ping:
    get:
      operationId: getPing
      responses:
        "200":
          description: OK
`, baseURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	c, out, _ := newTestCLI(t)
	c.SetDefaultConfig(&config.Config{APIs: map[string]*config.APIConfig{
		"api": {
			BaseURL: baseURL,
			SpecURL: baseURL + "/openapi.yaml",
		},
	}})
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})

	if err := c.Run([]string{"example", "--help"}); err != nil {
		t.Fatalf("help: %v", err)
	}
	if specRequests != 1 {
		t.Fatalf("spec requests = %d, want 1", specRequests)
	}
	if got := out.String(); !strings.Contains(got, "get-ping") {
		t.Fatalf("help missing promoted operation:\n%s", got)
	}
}

func TestCommandSurfaceSyncedFallbackPreservesOriginalArgs(t *testing.T) {
	specIncludesNew := false
	var lastPath, lastHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.yaml":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, commandSurfaceRefreshSpec(specIncludesNew))
		case "/staging/new", "/default/new":
			lastPath = r.URL.Path
			lastHeader = r.Header.Get("X-Keep")
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/staging/new" {
				fmt.Fprint(w, `{"profile":"staging"}`)
			} else {
				fmt.Fprint(w, `{"profile":"default"}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfgFile := writeTestConfig(t, &config.Config{APIs: map[string]*config.APIConfig{
		"api": {
			BaseURL: srv.URL + "/default",
			SpecURL: srv.URL + "/openapi.yaml",
			Profiles: map[string]*config.ProfileConfig{
				"staging": {BaseURL: srv.URL + "/staging"},
			},
		},
	}})
	cacheDir := t.TempDir()

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	if err := c.Run([]string{"example", "--rsh-profile", "staging", "--help"}); err != nil {
		t.Fatalf("prime help: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "get-old") || strings.Contains(got, "get-new") {
		t.Fatalf("expected initial help to use old cached operations, got:\n%s", got)
	}

	specIncludesNew = true
	c, out, _ = newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	if err := c.Run([]string{"example", "--rsh-profile", "staging", "get-new", "--rsh-header", "X-Keep: yes", "-f", "body.profile", "-o", "lines"}); err != nil {
		t.Fatalf("refreshed generated command: %v", err)
	}
	if lastPath != "/staging/new" {
		t.Fatalf("request path = %q, want /staging/new", lastPath)
	}
	if lastHeader != "yes" {
		t.Fatalf("X-Keep header = %q, want yes", lastHeader)
	}
	if got := strings.TrimSpace(out.String()); got != "staging" {
		t.Fatalf("filtered output = %q, want staging", got)
	}
}

func TestCommandSurfaceBaseURLFallbackRefreshesUnknownOperation(t *testing.T) {
	specIncludesNew := false
	var specRequests int
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.json":
			specRequests++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, commandSurfaceRefreshSpec(specIncludesNew))
		case "/new":
			lastPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfgFile := writeTestConfig(t, &config.Config{APIs: map[string]*config.APIConfig{
		"api": {BaseURL: srv.URL},
	}})
	cacheDir := t.TempDir()

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	if err := c.Run([]string{"example", "--help"}); err != nil {
		t.Fatalf("prime help: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "get-old") || strings.Contains(got, "get-new") {
		t.Fatalf("expected initial help to use old cached operations, got:\n%s", got)
	}

	specIncludesNew = true
	c, _, _ = newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	if err := c.Run([]string{"example", "get-new"}); err != nil {
		t.Fatalf("refreshed generated command: %v", err)
	}
	if lastPath != "/new" {
		t.Fatalf("request path = %q, want /new", lastPath)
	}
	if specRequests < 2 {
		t.Fatalf("spec requests = %d, want initial discovery and refresh", specRequests)
	}
}

func commandSurfaceRefreshSpec(includeNew bool) string {
	paths := `"/old":{"get":{"operationId":"getOld","responses":{"200":{"description":"OK"}}}}`
	if includeNew {
		paths += `,"/new":{"get":{"operationId":"getNew","responses":{"200":{"description":"OK"}}}}`
	}
	return `{"openapi":"3.1.0","info":{"title":"Test","version":"1.0.0"},"paths":{` + paths + `}}`
}

func TestCommandSurfaceSupportCommandsUnderNamespace(t *testing.T) {
	c, out, _ := newTestCLI(t)
	writeCommandSurfaceSpecConfig(t, c, "api", "getPing")
	c.SetCommandSurface(cli.CommandSurface{
		PromotedAPI:             "api",
		SupportCommandNamespace: "cli",
	})

	if err := c.Run([]string{"example", "cli", "cache", "--help"}); err != nil {
		t.Fatalf("support command help: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "clear") {
		t.Fatalf("namespaced cache help missing subcommand:\n%s", got)
	}

	c, out, _ = newTestCLI(t)
	writeCommandSurfaceSpecConfig(t, c, "api", "getPing")
	c.SetCommandName("example")
	c.SetCommandSurface(cli.CommandSurface{
		PromotedAPI:             "api",
		SupportCommandNamespace: "cli",
	})
	if err := c.Run([]string{"example", "cli", "plugin", "--help"}); err != nil {
		t.Fatalf("namespaced plugin help: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Manage example plugins") {
		t.Fatalf("namespaced plugin help missing branded description:\n%s", got)
	}
}

func TestCommandSurfaceHideSupportCommands(t *testing.T) {
	c, out, _ := newTestCLI(t)
	writeCommandSurfaceSpecConfig(t, c, "api", "getPing")
	c.SetCommandSurface(cli.CommandSurface{
		PromotedAPI:         "api",
		HideSupportCommands: true,
	})

	err := c.Run([]string{"example", "version"})
	if err == nil || !strings.Contains(err.Error(), `unknown command "version"`) {
		t.Fatalf("version command error = %v, want unknown command", err)
	}

	c, out, _ = newTestCLI(t)
	writeCommandSurfaceSpecConfig(t, c, "api", "getPing")
	c.SetCommandSurface(cli.CommandSurface{
		PromotedAPI:         "api",
		HideSupportCommands: true,
	})
	if err := c.Run([]string{"example", "--version"}); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "2.0.0") {
		t.Fatalf("--version output = %q", got)
	}
}

func TestCommandSurfaceSupportCommandCollisionErrors(t *testing.T) {
	c, _, _ := newTestCLI(t)
	writeCommandSurfaceSpecConfig(t, c, "api", "cache")
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})

	err := c.Run([]string{"example", "--help"})
	if err == nil || !strings.Contains(err.Error(), "SupportCommandNamespace") {
		t.Fatalf("collision error = %v, want SupportCommandNamespace hint", err)
	}
}

func TestCommandSurfaceRootAuthHeader(t *testing.T) {
	app := newTestApp(t)
	app.SetConfigPath(writeAPIConfigObject(t, "api", testAPIConfig("https://api.example.com", profileAuth(apiKeyAuth("header", "X-Partner-Key", "secret")))))
	app.CLI.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})

	app.Run("auth", "header")
	if got := strings.TrimSpace(app.Stdout.String()); got != "X-Partner-Key: secret" {
		t.Fatalf("auth header = %q, want X-Partner-Key: secret", got)
	}

	app = newTestApp(t)
	app.SetConfigPath(writeAPIConfigObject(t, "api", testAPIConfig("https://api.example.com", profileAuth(apiKeyAuth("header", "X-Partner-Key", "secret")))))
	app.CLI.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	app.Run("auth", "get", "--print-header")
	if got := strings.TrimSpace(app.Stdout.String()); got != "X-Partner-Key: secret" {
		t.Fatalf("auth get --print-header = %q, want X-Partner-Key: secret", got)
	}

	app = newTestApp(t)
	app.SetConfigPath(writeAPIConfigObject(t, "api", testAPIConfig("https://api.example.com", profileAuth(apiKeyAuth("query", "api_key", "secret")))))
	app.CLI.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	err := app.RunErr("auth", "header")
	if err == nil || !strings.Contains(err.Error(), "query parameter") {
		t.Fatalf("query auth header error = %v, want query parameter error", err)
	}

	app = newTestApp(t)
	app.SetConfigPath(writeAPIConfigObject(t, "api", testAPIConfig("https://api.example.com", profileAuth(apiKeyAuth("cookie", "session", "secret")))))
	app.CLI.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	err = app.RunErr("auth", "header")
	if err == nil || !strings.Contains(err.Error(), "cookie") {
		t.Fatalf("cookie auth header error = %v, want cookie error", err)
	}
}

func TestCommandSurfaceRootAuthOperationFetchesMetadataOnFirstRun(t *testing.T) {
	var specRequests int
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.json":
			specRequests++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, commandSurfaceAuthSpec(baseURL, true))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	cfgFile := writeTestConfig(t, &config.Config{APIs: map[string]*config.APIConfig{
		"api": {
			BaseURL: srv.URL,
			Profiles: map[string]*config.ProfileConfig{
				"default": profileCredentials(map[string]*config.CredentialConfig{
					"PartnerKey": testCredential(apiKeyAuth("header", "X-Partner-Key", "secret")),
				}),
			},
		},
	}})

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	if err := c.Run([]string{"example", "auth", "header", "--operation", "partner-report"}); err != nil {
		t.Fatalf("auth header operation: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "X-Partner-Key: secret" {
		t.Fatalf("auth header operation = %q, want X-Partner-Key: secret", got)
	}
	if specRequests != 1 {
		t.Fatalf("spec requests = %d, want 1", specRequests)
	}
}

func TestCommandSurfaceRootAuthOperationRefreshesMissingFreshOperation(t *testing.T) {
	specIncludesPartner := false
	var specRequests int
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.json":
			specRequests++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, commandSurfaceAuthSpec(baseURL, specIncludesPartner))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	cfgFile := writeTestConfig(t, &config.Config{APIs: map[string]*config.APIConfig{
		"api": {
			BaseURL: srv.URL,
			Profiles: map[string]*config.ProfileConfig{
				"default": profileCredentials(map[string]*config.CredentialConfig{
					"PartnerKey": testCredential(apiKeyAuth("header", "X-Partner-Key", "secret")),
				}),
			},
		},
	}})
	cacheDir := t.TempDir()

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	if err := c.Run([]string{"example", "--help"}); err != nil {
		t.Fatalf("prime help: %v", err)
	}
	if got := out.String(); strings.Contains(got, "partner-report") {
		t.Fatalf("expected initial help to omit partner-report, got:\n%s", got)
	}

	specIncludesPartner = true
	c, out, _ = newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	if err := c.Run([]string{"example", "auth", "header", "--operation", "partner-report"}); err != nil {
		t.Fatalf("auth header operation: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "X-Partner-Key: secret" {
		t.Fatalf("auth header operation = %q, want X-Partner-Key: secret", got)
	}
	if specRequests < 2 {
		t.Fatalf("spec requests = %d, want initial discovery and refresh", specRequests)
	}
}

func TestCommandSurfaceRootAuthOperationUsesRawSpecCacheBeforeRefresh(t *testing.T) {
	var originRequests int
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests++
		http.NotFound(w, r)
	}))
	defer origin.Close()
	var profileRequests int
	profile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		profileRequests++
		http.NotFound(w, r)
	}))
	defer profile.Close()

	rawSpec := openAPISpec(origin.URL, "Auth API",
		openAPISecuritySchemes(`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`),
		openAPIPaths(openAPIGet("/v2/private", "", `"security":[{"PartnerKey":[]}]`)))
	apiSpec, err := internalspec.OpenAPILoader{}.Load([]byte(rawSpec))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	cacheDir := t.TempDir()
	if err := internalspec.StoreSpecInCache(cacheDir, "api", cli.Version, apiSpec, nil, internalspec.OperationOptions{
		BaseURL:       origin.URL,
		OperationBase: "/v1",
	}, time.Hour); err != nil {
		t.Fatalf("store spec cache: %v", err)
	}

	cfgFile := writeTestConfig(t, &config.Config{APIs: map[string]*config.APIConfig{
		"api": {
			BaseURL:       origin.URL,
			OperationBase: "/v1",
			Profiles: map[string]*config.ProfileConfig{
				"staging": {
					BaseURL:       profile.URL,
					OperationBase: "/v2",
					Credentials: map[string]*config.CredentialConfig{
						"PartnerKey": testCredential(apiKeyAuth("header", "X-Partner-Key", "secret")),
					},
				},
			},
		},
	}})

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})
	if err := c.Run([]string{"example", "--rsh-profile", "staging", "auth", "header", "--operation", "get-private"}); err != nil {
		t.Fatalf("auth header operation: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "X-Partner-Key: secret" {
		t.Fatalf("auth header operation = %q, want X-Partner-Key: secret", got)
	}
	if originRequests != 0 || profileRequests != 0 {
		t.Fatalf("discovery requests: origin=%d profile=%d, want 0", originRequests, profileRequests)
	}
}

func TestCommandSurfaceRootAuthOperationConflictsDoNotRefreshMetadata(t *testing.T) {
	var specRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openapi.json" {
			specRequests++
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cfgFile := writeTestConfig(t, &config.Config{APIs: map[string]*config.APIConfig{
		"api": {
			BaseURL: srv.URL,
			Profiles: map[string]*config.ProfileConfig{
				"default": profileCredentials(map[string]*config.CredentialConfig{
					"PartnerKey": testCredential(apiKeyAuth("header", "X-Partner-Key", "secret")),
				}),
			},
		},
	}})

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "auth get credential and operation",
			args: []string{"example", "auth", "get", "PartnerKey", "--operation", "partner-report"},
			want: "--operation and credential ID are mutually exclusive",
		},
		{
			name: "auth inspect credential and operation",
			args: []string{"example", "auth", "inspect", "--credential", "PartnerKey", "--operation", "partner-report"},
			want: "--operation and --credential are mutually exclusive",
		},
		{
			name: "auth inspect operation and output format",
			args: []string{"example", "auth", "inspect", "--operation", "partner-report", "-o", "json"},
			want: "does not support -o/--rsh-output-format",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specRequests = 0
			c, _, _ := newTestCLI(t)
			c.Hooks().ConfigPath = cfgFile
			c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})

			err := c.Run(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if specRequests != 0 {
				t.Fatalf("spec requests = %d, want 0", specRequests)
			}
		})
	}
}

func commandSurfaceAuthSpec(baseURL string, includePartner bool) string {
	paths := []string{openAPIGet("/old", "oldReport", `"security":[{"PartnerKey":[]}]`)}
	if includePartner {
		paths = append(paths, openAPIGet("/partner", "partnerReport", `"security":[{"PartnerKey":[]}]`))
	}
	return openAPISpec(baseURL, "Auth API",
		openAPISecuritySchemes(`"PartnerKey":{"type":"apiKey","in":"header","name":"X-Partner-Key"}`),
		openAPIPaths(paths...))
}

func writeCommandSurfaceSpecConfig(t *testing.T, c *cli.CLI, apiName, operationID string) {
	t.Helper()
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	specBody := fmt.Sprintf(`openapi: "3.1.0"
info:
  title: Test
  version: "1.0.0"
paths:
  /ping:
    get:
      operationId: %s
      responses:
        "200":
          description: OK
`, operationID)
	if err := os.WriteFile(specPath, []byte(specBody), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	configBody := `{"apis":{` + strconv.Quote(apiName) + `:{"base_url":"https://api.example.com","spec_files":[` + strconv.Quote(specPath) + `]}}}`
	if err := os.WriteFile(c.Hooks().ConfigPath, []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestHelp(t *testing.T) {
	c, out, _ := newTestCLI(t)
	if err := c.Run([]string{"restish", "--help"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"restish", "HTTP"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected help output to contain %q:\n%s", want, got)
		}
	}
}

func TestHelpColorizesWhenColorEnabled(t *testing.T) {
	t.Setenv("COLOR", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("NOCOLOR", "")

	c, out, _ := newTestCLI(t)
	if err := c.Run([]string{"restish", "--help"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected colored help output, got:\n%s", got)
	}
	plain := stripANSI(got)
	for _, want := range []string{"Usage:", "Generic HTTP Commands", "get"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("colored help should preserve %q in plain text:\n%s", want, plain)
		}
	}
}

func TestHelpHidesRequestFlagsForNonRequestCommands(t *testing.T) {
	for _, args := range [][]string{
		{"restish", "shell", "setup", "--help"},
		{"restish", "plugin", "--help"},
		{"restish", "cache", "--help"},
		{"restish", "config", "--help"},
		{"restish", "api", "list", "--help"},
		{"restish", "api", "remove", "--help"},
		{"restish", "config", "theme", "--help"},
	} {
		c, out, _ := newTestCLI(t)
		if err := c.Run(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		got := out.String()
		for _, hidden := range []string{"--rsh-header", "--rsh-output-format", "--rsh-no-paginate", "--rsh-insecure"} {
			if strings.Contains(got, hidden) {
				t.Fatalf("%v should omit request global %s by default:\n%s", args, hidden, got)
			}
		}
		for _, visible := range []string{"--help-all", "--rsh-config", "--rsh-verbose"} {
			if !strings.Contains(got, visible) {
				t.Fatalf("%v should keep core global %s visible:\n%s", args, visible, got)
			}
		}
	}
}

func TestRequestHelpShowsRequestFlagsAndHelpAllExpandsNonRequestHelp(t *testing.T) {
	c, out, _ := newTestCLI(t)
	if err := c.Run([]string{"restish", "get", "--help"}); err != nil {
		t.Fatalf("get --help: %v", err)
	}
	got := out.String()
	for _, want := range []string{"--rsh-header", "--rsh-output-format", "--rsh-no-paginate"} {
		if !strings.Contains(got, want) {
			t.Fatalf("request help should show %s:\n%s", want, got)
		}
	}

	c, out, _ = newTestCLI(t)
	if err := c.Run([]string{"restish", "shell", "setup", "--help-all", "--help"}); err != nil {
		t.Fatalf("setup --help-all --help: %v", err)
	}
	got = out.String()
	for _, want := range []string{"--rsh-header", "--rsh-output-format", "--rsh-no-paginate"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help-all should show %s:\n%s", want, got)
		}
	}
}

func TestHelpAllBypassesValidationAndExecution(t *testing.T) {
	for _, args := range [][]string{
		{"restish", "get", "--help-all"},
		{"restish", "api", "connect", "--help-all"},
		{"restish", "links", "--help-all"},
		{"restish", "cert", "--help-all"},
		{"restish", "shell", "setup", "--help-all"},
		{"restish", "config", "theme", "reset", "--help-all"},
		{"restish", "config", "path", "--help-all"},
		{"restish", "config", "show", "--help-all"},
	} {
		c, out, _ := newTestCLI(t)
		if err := c.Run(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		got := out.String()
		if !strings.Contains(got, "Usage:") || !strings.Contains(got, "--rsh-header") {
			t.Fatalf("%v should show expanded help, got:\n%s", args, got)
		}
	}

	c, out, _ := newTestCLI(t)
	called := false
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		called = true
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "https://api.example.com/items", "--help-all"}); err != nil {
		t.Fatalf("get URL --help-all: %v", err)
	}
	if called {
		t.Fatal("help-all after URL should not execute the HTTP request")
	}
	if got := out.String(); !strings.Contains(got, "Perform an HTTP `GET` request") || !strings.Contains(got, "--rsh-header") {
		t.Fatalf("expected GET help-all output, got:\n%s", got)
	}
}

func TestBootstrapCommandsIgnoreInvalidConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "help flag", args: []string{"restish", "--help"}},
		{name: "help command", args: []string{"restish", "help"}},
		{name: "version flag", args: []string{"restish", "--version"}},
		{name: "version command", args: []string{"restish", "version"}},
		{name: "completion", args: []string{"restish", "completion", "bash"}},
		{name: "setup help", args: []string{"restish", "shell", "setup", "--help"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newTestCLI(t)
			if err := os.WriteFile(c.Hooks().ConfigPath, []byte(`{"apis":`), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if err := c.Run(tc.args); err != nil {
				t.Fatalf("%v returned error with invalid config: %v", tc.args, err)
			}
		})
	}
}

func TestRunRejectsInsecureConfigPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not authoritative on Windows")
	}
	c, _, _ := newTestCLI(t)
	if err := os.WriteFile(c.Hooks().ConfigPath, []byte(`{"apis":{}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	err := c.Run([]string{"restish", "help"})
	if err == nil {
		t.Fatal("expected insecure config permissions error")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoctorReportsInvalidConfigWithoutFailing(t *testing.T) {
	c, out, errOut := newTestCLI(t)
	if err := os.WriteFile(c.Hooks().ConfigPath, []byte("{\n  \"apiss\": {}\n}"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := c.Run([]string{"restish", "doctor"}); err != nil {
		t.Fatalf("doctor returned error with invalid config: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Config parse: invalid") ||
		!strings.Contains(got, c.Hooks().ConfigPath) ||
		!strings.Contains(got, "did you mean \"apis\"") {
		t.Fatalf("unexpected doctor output:\n%s", got)
	}
	if !strings.Contains(errOut.String(), "Tip: use -o json for machine-readable output.") {
		t.Fatalf("expected redirected-output JSON hint on stderr, got:\n%s", errOut.String())
	}
}

func TestBuiltInAPINameFailsAtConfigLoad(t *testing.T) {
	c, _, _ := newTestCLI(t)
	if err := os.WriteFile(c.Hooks().ConfigPath, []byte(`{"apis":{"get":{"base_url":"https://api.example.com"}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	err := c.Run([]string{"restish", "get", "https://api.example.com"})
	if err == nil {
		t.Fatal("expected built-in API name to fail config load")
	}
	if !strings.Contains(err.Error(), `API name "get" conflicts`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHelpGroupsTopLevelCommands(t *testing.T) {
	c, out, _ := newTestCLI(t)
	if err := os.WriteFile(c.Hooks().ConfigPath, []byte(`{
  "apis": {
    "zapi": {
      "base_url": "https://api.example.com"
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := c.Run([]string{"restish", "--help"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"Generic HTTP Commands",
		"Configuration and Setup",
		"Plugin Commands",
		"Registered APIs",
		"Utilities",
		"Help",
		"General Options",
		"--help-all",
		"--rsh-config",
		"--rsh-verbose",
		"zapi",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected grouped help to contain %q:\n%s", want, got)
		}
	}
	for _, hidden := range []string{
		"Request Options",
		"Output Options",
		"TLS Options",
		"Pagination and Streaming Options",
		"Cache and Retry Options",
		"--rsh-header",
		"--rsh-output-format",
		"--rsh-insecure",
		"--rsh-no-paginate",
	} {
		if strings.Contains(got, hidden) {
			t.Errorf("expected default root help to omit %q:\n%s", hidden, got)
		}
	}
	if strings.Contains(got, "Additional Commands:") {
		t.Fatalf("expected all top-level commands to be grouped:\n%s", got)
	}

	c, out, _ = newTestCLI(t)
	if err := c.Run([]string{"restish", "--help-all", "--help"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got = out.String()
	for _, want := range []string{
		"Request Options",
		"Output Options",
		"Auth and Profile Options",
		"TLS Options",
		"Pagination and Streaming Options",
		"Cache and Retry Options",
		"General Options",
		"--rsh-auth",
		"--rsh-config",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected root --help-all to contain %q:\n%s", want, got)
		}
	}
	outputGroupIdx := strings.Index(got, "Output Options")
	printFlagIdx := strings.Index(got, "--rsh-print")
	authGroupIdx := strings.Index(got, "Auth and Profile Options")
	if outputGroupIdx < 0 || printFlagIdx < outputGroupIdx || authGroupIdx < 0 || printFlagIdx > authGroupIdx {
		t.Fatalf("--rsh-print should be grouped under Output Options in --help-all:\n%s", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	c, _, _ := newTestCLI(t)
	err := c.Run([]string{"restish", "apis"})
	if err == nil {
		t.Error("expected error for unknown command, got nil")
	} else if !strings.Contains(err.Error(), `did you mean "api"?`) {
		t.Fatalf("expected suggestion for api command, got %v", err)
	}
}

func TestUnknownRootCommandWithLaterURLHintsQuoting(t *testing.T) {
	c, _, _ := newTestCLI(t)
	err := c.Run([]string{"restish", "Bearer", "docs-token", "https://api.example.com/auth/bearer"})
	if err == nil {
		t.Fatal("expected unknown command error")
	}
	if !strings.Contains(err.Error(), `unknown command "Bearer"`) ||
		!strings.Contains(err.Error(), "a URL appears later in the command") ||
		!strings.Contains(err.Error(), "flag value with spaces needs quotes") {
		t.Fatalf("unexpected unknown command error: %v", err)
	}
}

func TestUnknownRestishFlagSuggestsNearestFlag(t *testing.T) {
	c, _, _ := newTestCLI(t)
	err := c.Run([]string{"restish", "get", "https://api.example.com/items", "--rsh-prnt", "b"})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag: --rsh-prnt") ||
		!strings.Contains(err.Error(), "did you mean --rsh-print?") {
		t.Fatalf("unexpected flag error: %v", err)
	}
}

func TestUnknownFlagSuggestsRestishPrefixedFlag(t *testing.T) {
	c, _, _ := newTestCLI(t)
	err := c.Run([]string{"restish", "get", "https://api.example.com/items", "--print", "hb"})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag: --print") ||
		!strings.Contains(err.Error(), "did you mean --rsh-print?") {
		t.Fatalf("unexpected flag error: %v", err)
	}
}

func TestUnknownFlagSuggestsRestishPrefixedTypo(t *testing.T) {
	c, _, _ := newTestCLI(t)
	err := c.Run([]string{"restish", "get", "https://api.example.com/items", "--prnt", "hb"})
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	if !strings.Contains(err.Error(), "unknown flag: --prnt") ||
		!strings.Contains(err.Error(), "did you mean --rsh-print?") {
		t.Fatalf("unexpected flag error: %v", err)
	}
}

func TestExplicitConfigFlagWritesSelectedFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "project-restish.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	c := cli.New()
	c.Stdin = strings.NewReader("")
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run([]string{"restish", "--rsh-config", cfgPath, "api", "connect", "myapi", "https://api.example.com", "--no-discover"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load explicit config: %v", err)
	}
	if got := cfg.APIs["myapi"].BaseURL; got != "https://api.example.com" {
		t.Fatalf("base_url = %q", got)
	}
}

func TestExplicitConfigFlagConnectCreatesSelectedFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nested", "project-restish.json")

	var stdout, stderr bytes.Buffer
	c := cli.New()
	c.Stdin = strings.NewReader("")
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run([]string{"restish", "--rsh-config", cfgPath, "api", "connect", "myapi", "https://api.example.com", "--no-discover"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat explicit config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load explicit config: %v", err)
	}
	if got := cfg.APIs["myapi"].BaseURL; got != "https://api.example.com" {
		t.Fatalf("base_url = %q, want https://api.example.com", got)
	}
}

func TestRSHConfigReadsSelectedFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "project-restish.json")
	if err := os.WriteFile(cfgPath, []byte(`{
  "apis": {
    "project": {"base_url": "https://project.example.com"}
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RSH_CONFIG", cfgPath)

	var stdout, stderr bytes.Buffer
	c := cli.New()
	c.Stdin = strings.NewReader("")
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run([]string{"restish", "api", "list"}); err != nil {
		t.Fatalf("api list: %v", err)
	}
	if !strings.Contains(stdout.String(), "project.example.com") {
		t.Fatalf("expected RSH_CONFIG API in output, got %q", stdout.String())
	}
}

func TestRSHConfigMissingDoesNotRunLegacyMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RSH_CONFIG", "")
	t.Setenv("RSH_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	}

	defaultConfigPath := config.DefaultPath()
	defaultConfigDir := filepath.Dir(defaultConfigPath)
	if err := os.MkdirAll(defaultConfigDir, 0o700); err != nil {
		t.Fatalf("mkdir default config dir: %v", err)
	}
	legacyPath := filepath.Join(defaultConfigDir, "apis.json")
	if err := os.WriteFile(legacyPath, []byte(`{
  "legacy": {
    "base": "https://legacy.example.com"
  }
}`), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	selectedPath := filepath.Join(t.TempDir(), "project-restish.json")
	t.Setenv("RSH_CONFIG", selectedPath)

	var stdout, stderr bytes.Buffer
	c := cli.New()
	c.Stdin = strings.NewReader("")
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run([]string{"restish", "api", "list"})
	if err == nil {
		t.Fatal("expected missing RSH_CONFIG file to error")
	}
	if !strings.Contains(err.Error(), selectedPath) || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(selectedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("selected config was created or stat failed: %v", statErr)
	}
	if _, statErr := os.Stat(defaultConfigPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("default config was migrated or stat failed: %v", statErr)
	}
	if _, statErr := os.Stat(legacyPath); statErr != nil {
		t.Fatalf("legacy config should remain in place: %v", statErr)
	}
	if _, statErr := os.Stat(defaultConfigDir + ".bak.v1"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("legacy config backup was created or stat failed: %v", statErr)
	}
}

func TestExplicitConfigMissingErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	c := cli.New()
	c.Stdin = strings.NewReader("")
	c.Stdout = &stdout
	c.Stderr = &stderr
	missing := filepath.Join(t.TempDir(), "missing.json")
	err := c.Run([]string{"restish", "--rsh-config", missing, "api", "list"})
	if err == nil {
		t.Fatal("expected missing explicit config to error")
	}
	if !strings.Contains(err.Error(), "--rsh-config") ||
		!strings.Contains(err.Error(), "v2 does not fall back to the default config") ||
		!strings.Contains(err.Error(), "create the file or remove the flag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_UsesInjectedHTTPTransport(t *testing.T) {
	c, out, _ := newTestCLI(t)
	c.Hooks().HTTPTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if got, want := r.URL.String(), "https://api.example.com/items"; got != want {
			t.Fatalf("URL = %q, want %q", got, want)
		}
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    r,
		}, nil
	})

	if err := c.Run([]string{"restish", "get", "api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), `"ok"`) {
		t.Fatalf("expected response body in stdout, got %q", out.String())
	}
}

func TestHelpDoesNotExposeRetrySentinelValue(t *testing.T) {
	c, out, _ := newTestCLI(t)
	if err := c.Run([]string{"restish", "get", "--help"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "default -1") || strings.Contains(out.String(), "(default -1)") {
		t.Fatalf("help leaked sentinel retry value:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Maximum retry attempts for network errors and transient HTTP responses (0 = disable) (default 2)") {
		t.Fatalf("expected user-facing retry default in help, got:\n%s", out.String())
	}
}

func TestInvalidRSHRetryFailsFast(t *testing.T) {
	t.Setenv("RSH_RETRY", "abc")
	c, _, _ := newTestCLI(t)
	c.Hooks().HTTPTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("request should not be sent with invalid RSH_RETRY")
		return nil, nil
	})
	err := c.Run([]string{"restish", "get", "https://api.example.com/items"})
	if err == nil {
		t.Fatal("expected invalid RSH_RETRY error")
	}
	if !strings.Contains(err.Error(), "invalid RSH_RETRY") {
		t.Fatalf("expected invalid RSH_RETRY error, got %v", err)
	}
}

func TestNegativeNumericGlobalFlagsFailFast(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "retry",
			args: []string{"restish", "get", "--rsh-retry", "-1", "https://api.example.com/items"},
			want: "invalid --rsh-retry -1",
		},
		{
			name: "max pages",
			args: []string{"restish", "get", "--rsh-max-pages", "-1", "https://api.example.com/items"},
			want: "invalid --rsh-max-pages -1",
		},
		{
			name: "max items",
			args: []string{"restish", "get", "--rsh-max-items", "-1", "https://api.example.com/items"},
			want: "invalid --rsh-max-items -1",
		},
		{
			name: "max body size",
			args: []string{"restish", "get", "--rsh-max-body-size", "-1", "https://api.example.com/items"},
			want: "invalid --rsh-max-body-size -1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newTestCLI(t)
			c.Hooks().HTTPTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				t.Fatal("request should not be sent with invalid numeric global flag")
				return nil, nil
			})
			err := c.Run(tt.args)
			if err == nil {
				t.Fatalf("%v: expected invalid numeric flag error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("%v: expected error containing %q, got %v", tt.args, tt.want, err)
			}
		})
	}
}

func TestNegativeTimeoutGlobalFlagsFailFast(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
		want string
	}{
		{
			name: "flag",
			args: []string{"restish", "get", "--rsh-timeout", "-1s", "https://api.example.com/items"},
			want: `invalid --rsh-timeout "-1s"`,
		},
		{
			name: "env",
			args: []string{"restish", "get", "https://api.example.com/items"},
			env:  "-1s",
			want: `invalid RSH_TIMEOUT "-1s"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("RSH_TIMEOUT", tt.env)
			}
			c, _, _ := newTestCLI(t)
			c.Hooks().HTTPTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				t.Fatal("request should not be sent with invalid timeout")
				return nil, nil
			})
			err := c.Run(tt.args)
			if err == nil {
				t.Fatalf("%v: expected invalid timeout error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("%v: expected error containing %q, got %v", tt.args, tt.want, err)
			}
		})
	}
}

func TestInvalidFilterLangFailsFast(t *testing.T) {
	c, _, _ := newTestCLI(t)
	c.Hooks().HTTPTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("request should not be sent with invalid filter language")
		return nil, nil
	})
	err := c.Run([]string{"restish", "get", "--rsh-filter-lang", "nope", "-f", "body.url", "https://api.example.com/items"})
	if err == nil {
		t.Fatal("expected invalid filter language error")
	}
	if !strings.Contains(err.Error(), `invalid --rsh-filter-lang "nope"`) {
		t.Fatalf("expected invalid --rsh-filter-lang error, got %v", err)
	}
}

func TestInvalidTLSMinVersionFailsFast(t *testing.T) {
	c, _, _ := newTestCLI(t)
	c.Hooks().HTTPTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("request should not be sent with invalid TLS minimum version")
		return nil, nil
	})
	err := c.Run([]string{"restish", "get", "--rsh-tls-min-version", "TLS1.1", "https://api.example.com/items"})
	if err == nil {
		t.Fatal("expected invalid TLS minimum version error")
	}
	if !strings.Contains(err.Error(), `invalid --rsh-tls-min-version "TLS1.1"`) {
		t.Fatalf("expected invalid --rsh-tls-min-version error, got %v", err)
	}
	if !strings.Contains(err.Error(), "TLS1.2") || !strings.Contains(err.Error(), "TLS1.3") {
		t.Fatalf("expected error to list supported TLS versions, got %v", err)
	}
}

func TestNegativeRSHRetryFailsFast(t *testing.T) {
	t.Setenv("RSH_RETRY", "-1")
	c, _, _ := newTestCLI(t)
	c.Hooks().HTTPTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("request should not be sent with invalid RSH_RETRY")
		return nil, nil
	})
	err := c.Run([]string{"restish", "get", "https://api.example.com/items"})
	if err == nil {
		t.Fatal("expected invalid RSH_RETRY error")
	}
	if !strings.Contains(err.Error(), `invalid RSH_RETRY "-1"`) {
		t.Fatalf("expected invalid RSH_RETRY error, got %v", err)
	}
}

func TestRun_PrintsLegacyMigrationNotice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	}

	legacyConfigPath := config.DefaultPath()
	legacyDir := filepath.Dir(legacyConfigPath)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "apis.json"), []byte(`{
  "example": {
    "base": "https://api.example.com"
  }
}`), 0o600); err != nil {
		t.Fatalf("write apis.json: %v", err)
	}

	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = legacyConfigPath
	if err := c.Run([]string{"restish", "--help"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := errOut.String()
	want := "Migrated config from v1 at " + legacyDir + "; kept backup at " + legacyDir + ".bak.v1"
	if !strings.Contains(got, want) {
		t.Fatalf("expected migration notice %q, got:\n%s", want, got)
	}
}
