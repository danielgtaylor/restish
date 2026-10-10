package cli_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
	cachepkg "github.com/rest-sh/restish/v2/internal/cache"
	restishcli "github.com/rest-sh/restish/v2/internal/cli"
	"github.com/rest-sh/restish/v2/internal/spec"
)

// specWithXCLIConfig returns an OpenAPI spec with x-cli-config pre-populating
// a default profile.
func specWithXCLIConfig(baseURL string) string {
	return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "servers": [{"url": %q}],
  "x-cli-config": {
    "command_layout": "tags",
    "profiles": {
      "default": {
        "headers": ["Accept: application/json"],
        "auth": {"type": "bearer", "params": {"token": ""}}
      }
    }
  },
  "paths": {}
}`, baseURL)
}

func specWithXCLIVisibility(baseURL string) string {
	return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Visible XCLI API", "version": "1.0"},
  "servers": [{"url": %q}],
  "x-cli-config": {
    "profiles": {
      "default": {"headers": ["Accept: application/json"]}
    }
  },
  "paths": {
    "/hidden": {
      "get": {
        "operationId": "hiddenOp",
        "x-cli-hidden": true,
        "responses": {"200": {"description": "OK"}}
      }
    },
    "/items/{id}": {
      "get": {
        "operationId": "getItem",
        "x-cli-name": "item",
        "parameters": [
          {
            "name": "id",
            "in": "path",
            "required": true,
            "schema": {"type": "string"},
            "x-cli-name": "item-id"
          }
        ],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, baseURL)
}

// TestAPIConnect verifies that "api connect" fetches the spec, reads
// x-cli-config, and writes a config file with the pre-populated fields.
func TestAPIConnect(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specWithXCLIConfig("https://api.example.com"))

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	if !strings.Contains(out.String(), "myapi") {
		t.Errorf("expected confirmation message, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "Wrote config: "+cfgFile) {
		t.Errorf("expected written config path, got: %q", out.String())
	}

	// Load the written config and verify the fields.
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	api, ok := written.APIs["myapi"]
	if !ok {
		t.Fatal("expected myapi in config")
	}
	if api.BaseURL != "https://api.example.com" {
		t.Errorf("base_url: got %q, want %q", api.BaseURL, "https://api.example.com")
	}
	if api.CommandLayout != "tags" {
		t.Errorf("command_layout: got %q, want tags", api.CommandLayout)
	}
	prof := api.Profiles["default"]
	if prof == nil {
		t.Fatal("expected default profile in config")
	}
	if prof.Auth == nil || prof.Auth.Type != "bearer" {
		t.Errorf("expected bearer auth in default profile, got: %+v", prof.Auth)
	}
	if len(prof.Headers) == 0 || !strings.Contains(prof.Headers[0], "application/json") {
		t.Errorf("expected Accept header in default profile, got: %v", prof.Headers)
	}
}

func TestAPIConnectPreservesExplicitCommandLayoutByDefault(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specWithXCLIConfig("https://api.example.com"))

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("first api connect: %v", err)
	}
	if err := c.Run([]string{"restish", "api", "set", "myapi", "command_layout: flat"}); err != nil {
		t.Fatalf("api set: %v", err)
	}
	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("second api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["myapi"].CommandLayout; got != "flat" {
		t.Fatalf("command_layout = %q, want explicit local value flat", got)
	}

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com", "--replace"}); err != nil {
		t.Fatalf("replacement api connect: %v", err)
	}
	written, err = config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load replaced config: %v", err)
	}
	if got := written.APIs["myapi"].CommandLayout; got != "tags" {
		t.Fatalf("command_layout = %q, want contract default tags after --replace", got)
	}
}

func TestAPIConnectIgnoresInvalidXCLICommandLayout(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	c, _, stderr := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "x-cli-config": {"command_layout": "nested"},
  "paths": {}
}`)

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	if !strings.Contains(stderr.String(), `ignoring x-cli-config.command_layout: must be "flat" or "tags"`) {
		t.Fatalf("stderr = %q, want invalid command_layout warning", stderr.String())
	}
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["myapi"].CommandLayout; got != "" {
		t.Fatalf("command_layout = %q, want unset", got)
	}
}

func TestAPIConnectAppliesXCLICommandLayoutWithoutProfiles(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "x-cli-config": {"command_layout": "tags"},
  "paths": {}
}`)

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["myapi"].CommandLayout; got != "tags" {
		t.Fatalf("command_layout = %q, want tags", got)
	}
}

func TestAPIConnectPrintsXCLIExtensionSummary(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specWithXCLIVisibility("https://api.example.com"))

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"OpenAPI x-cli extensions:",
		"x-cli-config",
		"1 hidden operation",
		"1 renamed operation",
		"1 renamed parameter",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("api connect output missing %q:\n%s", want, got)
		}
	}
}

func TestAPIConnectIgnoresUnusedDeclaredSecuritySchemes(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	specFile := filepath.Join(t.TempDir(), "openapi.json")
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Unused Auth API", "version": "1.0"},
  "servers": [{"url": "https://api.example.com"}],
  "components": {
    "securitySchemes": {
      "UnusedBearer": {"type": "http", "scheme": "bearer"}
    }
  },
  "paths": {
    "/items": {
      "get": {
        "operationId": "listItems",
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`
	if err := os.WriteFile(specFile, []byte(specBody), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com", "--spec", specFile, "--yes"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	api := written.APIs["myapi"]
	if api == nil {
		t.Fatal("expected myapi in config")
	}
	if prof := api.Profiles["default"]; prof != nil {
		if got := prof.Credentials; len(got) != 0 {
			t.Fatalf("unused declared schemes should not become credentials: %#v", got)
		}
	}
}

func TestAPIConnectYesAllowsDiscoveredOperationOrigins(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	specFile := filepath.Join(t.TempDir(), "openapi.json")
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "DigitalOcean", "version": "1.0"},
  "servers": [{"url": "https://api.digitalocean.com"}],
  "paths": {
    "/v1/models": {
      "get": {
        "operationId": "inferenceListModels",
        "servers": [{"url": "https://inference.do-ai.run"}],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`
	if err := os.WriteFile(specFile, []byte(specBody), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	if err := c.Run([]string{"restish", "api", "connect", "do", "https://api.digitalocean.com", "--spec", specFile, "--yes"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["do"].AllowedOperationOrigins; !reflect.DeepEqual(got, []string{"https://*.do-ai.run"}) {
		t.Fatalf("allowed_operation_origins = %#v, want DigitalOcean wildcard", got)
	}
}

func TestAPIConnectPrimesGeneratedHelp(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	cacheDir := t.TempDir()

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	useOpenAPISpecTransport(c, specWithOperations("https://api.example.com"))

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	helpCLI, helpOut, _ := newTestCLI(t)
	helpCLI.Hooks().ConfigPath = cfgFile
	helpCLI.Hooks().SpecCachePath = cacheDir
	useTransport(helpCLI, func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("generated help should use the primed spec cache")
	})

	if err := helpCLI.Run([]string{"restish", "myapi", "--help"}); err != nil {
		t.Fatalf("generated help after api connect: %v", err)
	}
	help := helpOut.String()
	if !strings.Contains(help, "list-items") {
		t.Fatalf("expected generated operation help after api connect, got:\n%s", help)
	}
	if strings.Contains(help, "Generic requests using") {
		t.Fatalf("expected generated API help, got generic short-name help:\n%s", help)
	}
}

func TestAPIConnectExplicitSpecServedAsTextPlain(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://raw.example.com/openapi.yml":
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(specWithOperations("https://api.example.com"))),
				Request:    r,
			}, nil
		default:
			return &http.Response{
				StatusCode: 404,
				Proto:      "HTTP/1.1",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("not found")),
				Request:    r,
			}, nil
		}
	})

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com", "--spec", "https://raw.example.com/openapi.yml"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	if strings.Contains(out.String(), "no spec found") {
		t.Fatalf("expected text/plain explicit spec to be discovered, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "operations discovered") {
		t.Fatalf("expected discovered operations, got:\n%s", out.String())
	}
}

func TestAPIConnectReportsMutualTLSAsTransportAuth(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "mTLS API", "version": "1.0"},
  "servers": [{"url": "https://api.example.com"}],
  "components": {
    "securitySchemes": {
      "ClientCert": {"type": "mutualTLS"}
    }
  },
  "security": [{"ClientCert": []}],
  "paths": {
    "/secure": {"get": {"operationId": "getSecure", "responses": {"200": {"description": "OK"}}}}
  }
}`

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{"restish", "api", "connect", "mtls", "https://api.example.com", "--yes"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "ClientCert") || !strings.Contains(got, "use TLS client certificate or signer") {
		t.Fatalf("expected mTLS transport auth guidance, got:\n%s", got)
	}
	if strings.Contains(got, "unsupported") {
		t.Fatalf("mutualTLS should not be reported as unsupported auth, got:\n%s", got)
	}
}

func TestAPIConnectExplicitSwaggerSpecReportsUnsupported(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://api.example.com/swagger.json":
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"swagger":"2.0","info":{"title":"Old","version":"1.0"},"paths":{}}`)),
				Request:    r,
			}, nil
		default:
			return &http.Response{
				StatusCode: 404,
				Proto:      "HTTP/1.1",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("not found")),
				Request:    r,
			}, nil
		}
	})

	err := c.Run([]string{"restish", "api", "connect", "oldapi", "https://api.example.com", "--spec", "https://api.example.com/swagger.json"})
	if err == nil {
		t.Fatal("expected unsupported Swagger error")
	}
	if !strings.Contains(err.Error(), "Swagger/OpenAPI 2.0 is not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIConnectPreservesEmbedderDefaultConfig(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.SetDefaultConfig(&config.Config{
		APIs: map[string]*config.APIConfig{
			"embedded": {
				BaseURL: "https://embedded.example.com",
			},
		},
	})

	if err := c.Run([]string{"restish", "api", "connect", "newapi", "https://api.example.com", "--no-discover"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	loaded := c.Config()
	if loaded == nil {
		t.Fatal("expected loaded config")
	}
	if loaded.APIs["embedded"] == nil {
		t.Fatalf("expected embedded default API to remain in c.cfg, got %#v", loaded.APIs)
	}
	if loaded.APIs["newapi"] == nil {
		t.Fatalf("expected connected API in c.cfg, got %#v", loaded.APIs)
	}
}

func TestAPIConnectConcurrentUpdatesPreserveBothAPIs(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	if err := os.WriteFile(cfgFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	run := func(name, baseURL string) {
		defer wg.Done()
		c, _, _ := newTestCLI(t)
		c.Hooks().ConfigPath = cfgFile
		errCh <- c.Run([]string{"restish", "api", "connect", name, baseURL, "--no-discover"})
	}

	wg.Add(2)
	go run("one", "https://one.example.com")
	go run("two", "https://two.example.com")
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("api connect: %v", err)
		}
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.APIs["one"] == nil || cfg.APIs["two"] == nil {
		t.Fatalf("concurrent api connect lost update: %#v", cfg.APIs)
	}
}

func TestAPIConnectConcurrentUpdatesPreserveSpecCaches(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "restish.json")
	if err := os.WriteFile(cfgFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cacheDir := t.TempDir()
	twoSpecRequested := make(chan struct{})
	releaseTwoSpec := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		c, _, _ := newTestCLI(t)
		c.Hooks().ConfigPath = cfgFile
		c.Hooks().SpecCachePath = cacheDir
		var once sync.Once
		useTransport(c, func(r *http.Request) (*http.Response, error) {
			switch r.URL.String() {
			case "https://two.example.com":
				return jsonResponse(200, ""), nil
			case "https://two.example.com/openapi.json":
				once.Do(func() { close(twoSpecRequested) })
				<-releaseTwoSpec
				return jsonResponse(200, specWithOperations("https://two.example.com")), nil
			default:
				return jsonResponse(404, "{}"), nil
			}
		})
		errCh <- c.Run([]string{"restish", "api", "connect", "two", "https://two.example.com"})
	}()

	select {
	case <-twoSpecRequested:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second api connect to reach spec discovery")
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://one.example.com":
			return jsonResponse(200, ""), nil
		case "https://one.example.com/openapi.json":
			return jsonResponse(200, specWithOperations("https://one.example.com")), nil
		default:
			return jsonResponse(404, "{}"), nil
		}
	})
	if err := c.Run([]string{"restish", "api", "connect", "one", "https://one.example.com"}); err != nil {
		t.Fatalf("first api connect: %v", err)
	}

	close(releaseTwoSpec)
	if err := <-errCh; err != nil {
		t.Fatalf("second api connect: %v", err)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.APIs["one"] == nil || cfg.APIs["two"] == nil {
		t.Fatalf("concurrent api connect lost config update: %#v", cfg.APIs)
	}
	for _, tc := range []struct {
		name    string
		baseURL string
	}{
		{"one", "https://one.example.com"},
		{"two", "https://two.example.com"},
	} {
		set, ok := spec.LoadOperationSetFromCache(cacheDir, tc.name, restishcli.Version, nil, spec.OperationOptions{BaseURL: tc.baseURL})
		if !ok || len(set.Operations) == 0 {
			t.Fatalf("expected generated operation cache for %s, ok=%v set=%#v", tc.name, ok, set)
		}
	}
}

func TestAPIConnectFindsWellKnownOfficialOpenAPISpec(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	specBody := `{"components":{"schemas":{"Thing":{"type":"object"}}},"info":{"title":"Managed API","version":"1.0"},"paths":{"/things":{"get":{"operationId":"list-things","responses":{"200":{"description":"OK"}}}}},"openapi":"3.1.0"}`

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://api.example.com/openapi.json":
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Content-Type": []string{"application/vnd.oai.openapi+json"}},
				Body:       io.NopCloser(strings.NewReader(specBody)),
				Request:    r,
			}, nil
		default:
			return &http.Response{
				StatusCode: 404,
				Proto:      "HTTP/1.1",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("not found")),
				Request:    r,
			}, nil
		}
	})

	if err := c.Run([]string{"restish", "api", "connect", "example", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	if strings.Contains(out.String(), "no spec found") {
		t.Fatalf("expected spec to be found, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "operations discovered") {
		t.Fatalf("expected discovered operations message, got: %q", out.String())
	}
}

func TestAPISyncDiscoveryUsesProfileCACert(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, specWithXCLIConfig("https://api.example.com"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	cfgFile := filepath.Join(t.TempDir(), "restish.json")
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"secure": {
				BaseURL: srv.URL,
				Profiles: map[string]*config.ProfileConfig{
					"default": {CACertPath: caPath},
				},
			},
		},
	})
	if err := os.WriteFile(cfgFile, cfgData, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	if err := c.Run([]string{"restish", "api", "sync", "secure"}); err != nil {
		t.Fatalf("api sync with profile CA: %v", err)
	}
}

func newAPISyncCLI(t *testing.T, api *config.APIConfig) (*restishcli.CLI, string, string) {
	t.Helper()
	cfgFile := writeAPIConfigObject(t, "protected", api)
	cacheDir := t.TempDir()
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	return c, cfgFile, cacheDir
}

func protectedAPI(baseURL, specURL string, authCfg *config.AuthConfig) *config.APIConfig {
	api := &config.APIConfig{BaseURL: baseURL, SpecURL: specURL}
	if authCfg != nil {
		api.Profiles = map[string]*config.ProfileConfig{
			"default": {Auth: authCfg},
		}
	}
	return api
}

func runProtectedAPISync(t *testing.T, c *restishcli.CLI, args ...string) error {
	t.Helper()
	cmdArgs := append([]string{"restish"}, args...)
	cmdArgs = append(cmdArgs, "api", "sync", "protected")
	return c.Run(cmdArgs)
}

func assertSyncedSpecURL(t *testing.T, cfgFile, cacheDir, want string) {
	t.Helper()
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["protected"].SpecURL; got != want {
		t.Fatalf("spec_url = %q, want %q", got, want)
	}

	cached, err := spec.LoadStaleFromCache(cacheDir, "protected", restishcli.Version, nil, []spec.Loader{spec.OpenAPILoader{}})
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if cached == nil {
		t.Fatal("expected cached spec")
	}
	if got := cached.SourceURL; got != want {
		t.Fatalf("cached SourceURL = %q, want %q", got, want)
	}
}

// TestAPISyncDiscoverySendsAuthCredentials verifies that spec discovery requests
// carry auth headers when the profile has auth configured. This is a regression
// test for the bug where discoveryTransport only set up TLS and never applied
// auth, causing servers that require a Bearer token to return 307/401 on sync.
func TestAPISyncDiscoverySendsAuthCredentials(t *testing.T) {
	var authHeader string
	var specHits int

	c, _, _ := newAPISyncCLI(t, protectedAPI("https://api.example.com", "", bearerAuth("test-token")))
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/openapi.json" {
			authHeader = r.Header.Get("Authorization")
			specHits++
			return jsonResponse(200, minimalOpenAPI), nil
		}
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	if err := runProtectedAPISync(t, c); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	if specHits == 0 {
		t.Fatal("spec endpoint was never hit")
	}
	if authHeader != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want %q", authHeader, "Bearer test-token")
	}
}

func TestAPISyncDiscoverySendsAuthForShorthandBaseURL(t *testing.T) {
	var authHeader string

	c, _, _ := newAPISyncCLI(t, protectedAPI("api.example.com", "", bearerAuth("test-token")))
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.example.com" && r.URL.Path == "/openapi.json" {
			authHeader = r.Header.Get("Authorization")
			return jsonResponse(200, minimalOpenAPI), nil
		}
		return textResponse(404, "text/plain", "not found", r), nil
	})

	if err := runProtectedAPISync(t, c); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	if authHeader != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want %q", authHeader, "Bearer test-token")
	}
}

func TestAPISyncDiscoveryFollowsLinkWithShorthandBaseURL(t *testing.T) {
	var mu sync.Mutex
	var linkedSpecAuth string

	c, _, _ := newAPISyncCLI(t, protectedAPI("api.example.com", "", bearerAuth("test-token")))
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "api.example.com" && (r.URL.Path == "" || r.URL.Path == "/"):
			resp := textResponse(200, "text/html", "<html></html>", r)
			resp.Header.Set("Link", `</openapi.yaml>; rel="service-desc"`)
			return resp, nil
		case r.URL.Host == "api.example.com" && r.URL.Path == "/openapi.yaml":
			mu.Lock()
			linkedSpecAuth = r.Header.Get("Authorization")
			mu.Unlock()
			return jsonResponse(200, minimalOpenAPI), nil
		default:
			return textResponse(404, "text/plain", "not found", r), nil
		}
	})

	if err := runProtectedAPISync(t, c); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if linkedSpecAuth != "Bearer test-token" {
		t.Fatalf("linked spec Authorization = %q, want %q", linkedSpecAuth, "Bearer test-token")
	}
}

func TestAPISyncDiscoveryResolvesExternalRefsWithShorthandSpecURL(t *testing.T) {
	var specAuth string
	var paramsAuth string
	var requests []string
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Protected", "version": "1.0"},
  "servers": [{"url": "https://api.example.com"}],
  "paths": {
    "/items/{id}": {
      "get": {
        "operationId": "getItem",
        "parameters": [{"$ref": "./params.yaml#/components/parameters/ID"}],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`
	paramsBody := `{
  "components": {
    "parameters": {
      "ID": {
        "name": "id",
        "in": "path",
        "required": true,
        "schema": {"type": "string"}
      }
    }
  }
}`

	c, _, _ := newAPISyncCLI(t, protectedAPI("api.example.com", "api.example.com/openapi.json", bearerAuth("test-token")))
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		switch {
		case r.URL.Host == "api.example.com" && r.URL.Path == "/openapi.json":
			specAuth = r.Header.Get("Authorization")
			return textResponse(200, "application/json", specBody, r), nil
		case r.URL.Host == "api.example.com" && r.URL.Path == "/params.yaml":
			paramsAuth = r.Header.Get("Authorization")
			return textResponse(200, "application/json", paramsBody, r), nil
		default:
			return textResponse(404, "text/plain", "not found", r), nil
		}
	})

	if err := runProtectedAPISync(t, c); err != nil {
		t.Fatalf("api sync: %v\nrequests: %v", err, requests)
	}
	if specAuth != "Bearer test-token" {
		t.Fatalf("spec Authorization = %q, want %q", specAuth, "Bearer test-token")
	}
	if paramsAuth != "Bearer test-token" {
		t.Fatalf("external ref Authorization = %q, want %q", paramsAuth, "Bearer test-token")
	}
}

func TestAPISyncDiscoveryDoesNotPersistQueryAuthInSourceURL(t *testing.T) {
	c, cfgFile, cacheDir := newAPISyncCLI(t, protectedAPI("api.example.com", "", apiKeyAuth("query", "api_key", "secret-token")))
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.example.com" && r.URL.Path == "/openapi.json" && r.URL.Query().Get("api_key") == "secret-token" {
			return textResponse(200, "application/json", minimalOpenAPI, r), nil
		}
		return textResponse(404, "text/plain", "not found", r), nil
	})

	if err := runProtectedAPISync(t, c); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	assertSyncedSpecURL(t, cfgFile, cacheDir, "https://api.example.com/openapi.json")
}

func TestAPISyncDiscoveryDoesNotPersistDiscoveredCredentialQuerySourceURL(t *testing.T) {
	c, cfgFile, cacheDir := newAPISyncCLI(t, protectedAPI("https://api.example.com", "", nil))
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "api.example.com" && (r.URL.Path == "" || r.URL.Path == "/"):
			resp := textResponse(200, "text/html", "<html></html>", r)
			resp.Header.Set("Link", `<https://api.example.com/openapi.json?api_key=secret-token&version=1>; rel="service-desc"`)
			return resp, nil
		case r.URL.Host == "api.example.com" && r.URL.Path == "/openapi.json" && r.URL.Query().Get("api_key") == "secret-token":
			return textResponse(200, "application/json", minimalOpenAPI, r), nil
		default:
			return textResponse(404, "text/plain", "not found", r), nil
		}
	})

	if err := runProtectedAPISync(t, c); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	assertSyncedSpecURL(t, cfgFile, cacheDir, "https://api.example.com/openapi.json?version=1")
}

func TestAPISyncDiscoveryRedactsCredentialQueryInErrorAndTrace(t *testing.T) {
	var hitSpec bool
	cfgFile := writeAPIConfigObject(t, "protected", protectedAPI("https://api.example.com", "https://api.example.com/openapi.json?api_key=secret-token&version=1", nil))
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.example.com" && r.URL.Path == "/openapi.json" && r.URL.Query().Get("api_key") == "secret-token" {
			hitSpec = true
			return textResponse(500, "text/plain", "server error", r), nil
		}
		return textResponse(404, "text/plain", "not found", r), nil
	})

	err := runProtectedAPISync(t, c, "-v")
	if err == nil {
		t.Fatal("expected api sync failure")
	}
	if !hitSpec {
		t.Fatal("expected request to include configured credential query")
	}
	combined := err.Error() + "\n" + errOut.String()
	if strings.Contains(combined, "secret-token") {
		t.Fatalf("credential query leaked in error/trace:\n%s", combined)
	}
	if !strings.Contains(combined, "https://api.example.com/openapi.json?version=1") {
		t.Fatalf("expected clean URL in error/trace, got:\n%s", combined)
	}
}

func TestAPISyncDiscoveryPersistsCleanRedirectSourceURL(t *testing.T) {
	tests := []struct {
		name       string
		location   string
		finalPath  string
		finalClean string
	}{
		{name: "path and query", location: "/specs/openapi.json?version=1&api_key=secret-token", finalPath: "/specs/openapi.json", finalClean: "https://api.example.com/specs/openapi.json?version=1"},
		{name: "query only", location: "/openapi.json?version=1&api_key=secret-token", finalPath: "/openapi.json", finalClean: "https://api.example.com/openapi.json?version=1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, cfgFile, cacheDir := newAPISyncCLI(t, protectedAPI("https://api.example.com", "", nil))
			useTransport(c, func(r *http.Request) (*http.Response, error) {
				switch {
				case r.URL.Host == "api.example.com" && r.URL.Path == "/openapi.json" && r.URL.RawQuery == "":
					resp := textResponse(302, "text/plain", "", r)
					resp.Header.Set("Location", tt.location)
					return resp, nil
				case r.URL.Host == "api.example.com" && r.URL.Path == tt.finalPath &&
					r.URL.Query().Get("version") == "1" && r.URL.Query().Get("api_key") == "secret-token":
					return textResponse(200, "application/json", minimalOpenAPI, r), nil
				default:
					return textResponse(404, "text/plain", "not found", r), nil
				}
			})

			if err := runProtectedAPISync(t, c); err != nil {
				t.Fatalf("api sync: %v", err)
			}
			assertSyncedSpecURL(t, cfgFile, cacheDir, tt.finalClean)
		})
	}
}

func TestAPISyncExplicitRedirectedSpecURLRemainsCacheable(t *testing.T) {
	c, _, cacheDir := newAPISyncCLI(t, protectedAPI("https://api.example.com", "https://api.example.com/openapi.json", nil))
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "api.example.com" && r.URL.Path == "/openapi.json" && r.URL.RawQuery == "":
			resp := textResponse(302, "text/plain", "", r)
			resp.Header.Set("Location", "/openapi.json?version=1&api_key=secret-token")
			return resp, nil
		case r.URL.Host == "api.example.com" && r.URL.Path == "/openapi.json" &&
			r.URL.Query().Get("version") == "1" && r.URL.Query().Get("api_key") == "secret-token":
			return textResponse(200, "application/json", minimalOpenAPI, r), nil
		default:
			return textResponse(404, "text/plain", "not found", r), nil
		}
	})
	if err := runProtectedAPISync(t, c); err != nil {
		t.Fatalf("api sync: %v", err)
	}

	cached, err := spec.Discover(context.Background(), spec.DiscoverConfig{
		APIName:  "protected",
		BaseURL:  "https://api.example.com",
		SpecURL:  "https://api.example.com/openapi.json",
		CacheDir: cacheDir,
		Version:  restishcli.Version,
		Fetch: func(context.Context, string, http.RoundTripper) (*http.Response, error) {
			return nil, errors.New("network should not be used")
		},
	}, spec.DefaultLoaders())
	if err != nil {
		t.Fatalf("cache-only Discover: %v", err)
	}
	if cached == nil || cached.SourceURL != "https://api.example.com/openapi.json?version=1" {
		t.Fatalf("cached SourceURL = %#v, want clean redirect target", cached)
	}
}

func TestAPISyncDiscoveryDoesNotSendAuthToCrossOriginLinkSpec(t *testing.T) {
	var linkedSpecAuth string

	api := protectedAPI("https://api.example.com", "", bearerAuth("test-token"))
	api.AllowCrossOriginSpec = true
	c, _, _ := newAPISyncCLI(t, api)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "api.example.com" && (r.URL.Path == "" || r.URL.Path == "/"):
			resp := textResponse(200, "text/html", "<html></html>", r)
			resp.Header.Set("Link", `<https://example.com/openapi.json>; rel="service-desc"`)
			return resp, nil
		case r.URL.Host == "api.example.com":
			return textResponse(404, "text/plain", "not found", r), nil
		case r.URL.Host == "example.com" && r.URL.Path == "/openapi.json":
			linkedSpecAuth = r.Header.Get("Authorization")
			return jsonResponse(200, minimalOpenAPI), nil
		default:
			return textResponse(404, "text/plain", "not found", r), nil
		}
	})

	if err := runProtectedAPISync(t, c); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	if linkedSpecAuth != "" {
		t.Fatalf("cross-origin spec Authorization = %q, want empty", linkedSpecAuth)
	}
}

func TestAPISyncDiscoveryUsesCachedOAuthForSpecURL(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotAuth != "Bearer cached-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"openapi":"3.1.0","info":{"title":"Protected","version":"1.0"},"servers":[{"url":%q}],"paths":{}}`, "http://"+r.Host)
	}))
	t.Cleanup(srv.Close)

	tokenPath := filepath.Join(t.TempDir(), "tokens.cbor")
	// inlineAuthCacheKey maps compatible auth-code configs to a shared key.
	cacheKey := oauthAuthCodeCacheKeyForTest(map[string]string{
		"client_id":     "client",
		"authorize_url": "https://auth.example.com/authorize",
		"token_url":     "https://auth.example.com/token",
	})
	if err := auth.NewTokenCache(tokenPath).Set(cacheKey, auth.CachedToken{AccessToken: "cached-token"}); err != nil {
		t.Fatalf("set token: %v", err)
	}
	cfgFile := writeAPIConfigObject(t, "protected", &config.APIConfig{
		BaseURL: srv.URL,
		SpecURL: srv.URL,
		Profiles: map[string]*config.ProfileConfig{
			"default": {
				Auth: &config.AuthConfig{Type: "oauth-authorization-code", Params: map[string]string{
					"client_id":     "client",
					"authorize_url": "https://auth.example.com/authorize",
					"token_url":     "https://auth.example.com/token",
				}},
			},
		},
	})

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	c.Hooks().TokenCachePath = tokenPath
	if err := c.Run([]string{"restish", "api", "sync", "protected"}); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	if gotAuth != "Bearer cached-token" {
		t.Fatalf("Authorization = %q, want cached OAuth token", gotAuth)
	}
}

func TestAPIConnectAllowCrossOriginSpec(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://api.example.com":
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Link": []string{`<https://spec.example.com/openapi.json>; rel="service-desc"`}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		case "https://spec.example.com/openapi.json":
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(specWithXCLIConfig("https://api.example.com"))),
				Request:    r,
			}, nil
		default:
			return &http.Response{
				StatusCode: 404,
				Proto:      "HTTP/1.1",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("not found")),
				Request:    r,
			}, nil
		}
	})

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com", "--allow-cross-origin-spec"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	api, ok := written.APIs["myapi"]
	if !ok {
		t.Fatal("expected myapi in config")
	}
	if !api.AllowCrossOriginSpec {
		t.Fatal("expected allow_cross_origin_spec to be persisted")
	}
}

func TestAPIConnectPersistsLinkDiscoveredSpecURL(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://api.example.com":
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Link": []string{`<https://api.example.com/linked-openapi.json>; rel="service-desc"`}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		case "https://api.example.com/linked-openapi.json":
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(specWithXCLIConfig("https://api.example.com"))),
				Request:    r,
			}, nil
		default:
			return &http.Response{
				StatusCode: 404,
				Proto:      "HTTP/1.1",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("not found")),
				Request:    r,
			}, nil
		}
	})

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["myapi"].SpecURL; got != "https://api.example.com/linked-openapi.json" {
		t.Fatalf("spec_url = %q, want discovered Link URL", got)
	}
}

func TestAPIConnectPreservesExistingProfilesByDefault(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()

	currentHeader := "Accept: application/json"
	useOpenAPISpecTransportFunc(c, func() string {
		return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "servers": [{"url": "https://api.example.com"}],
  "x-cli-config": {
    "profiles": {
      "default": {
        "headers": [%q]
      }
    }
  },
  "paths": {}
}`, currentHeader)
	})

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("first api connect: %v", err)
	}

	currentHeader = "Accept: application/vnd.api+json"
	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("second api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got, want := written.APIs["myapi"].Profiles["default"].Headers[0], "Accept: application/json"; got != want {
		t.Fatalf("expected existing profile header %q to be preserved, got %q", want, got)
	}
}

func TestAPIConnectReplaceRefreshesProfiles(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()

	currentHeader := "Accept: application/json"
	useOpenAPISpecTransportFunc(c, func() string {
		return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "servers": [{"url": "https://api.example.com"}],
  "x-cli-config": {
    "profiles": {
      "default": {
        "headers": [%q]
      }
    }
  },
  "paths": {}
}`, currentHeader)
	})

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("first api connect: %v", err)
	}

	currentHeader = "Accept: application/vnd.api+json"
	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com", "--replace"}); err != nil {
		t.Fatalf("second api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["myapi"].Profiles["default"].Headers[0]; got != currentHeader {
		t.Fatalf("expected refreshed x-cli-config header %q, got %q", currentHeader, got)
	}
}

func TestAPISyncUpdatesAllowedOperationOriginsAndPreservesProfiles(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "do", &config.APIConfig{
		BaseURL: "https://api.digitalocean.com",
		SpecURL: "https://api.digitalocean.com/openapi.json",
		Profiles: map[string]*config.ProfileConfig{
			"default": {
				Headers: []string{"Authorization: Bearer local-token"},
			},
		},
	})
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "DigitalOcean", "version": "1.0"},
  "servers": [{"url": "https://api.digitalocean.com"}],
  "x-cli-config": {
    "profiles": {
      "default": {
        "headers": ["Authorization: Bearer discovered-token"]
      }
    }
  },
  "paths": {
    "/v1/models": {
      "get": {
        "operationId": "inferenceListModels",
        "servers": [{"url": "https://inference.do-ai.run"}],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == "https://api.digitalocean.com/openapi.json" {
			return textResponse(200, "application/json", specBody, r), nil
		}
		return &http.Response{
			StatusCode: 404,
			Proto:      "HTTP/1.1",
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("not found")),
			Request:    r,
		}, nil
	})

	if err := c.Run([]string{"restish", "api", "sync", "do", "--yes"}); err != nil {
		t.Fatalf("api sync: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	api := written.APIs["do"]
	if got := api.AllowedOperationOrigins; !reflect.DeepEqual(got, []string{"https://*.do-ai.run"}) {
		t.Fatalf("allowed_operation_origins = %#v, want DigitalOcean wildcard", got)
	}
	if got := api.Profiles["default"].Headers; !reflect.DeepEqual(got, []string{"Authorization: Bearer local-token"}) {
		t.Fatalf("profile headers = %#v, want local profile preserved", got)
	}
	if !strings.Contains(out.String(), "Wrote config: "+cfgFile) || !strings.Contains(out.String(), `Synced spec for "do".`) {
		t.Fatalf("expected config write and sync confirmation, got %q", out.String())
	}
}

func TestAPISyncDoesNotPromptForOverriddenOperationServer(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "files", &config.APIConfig{
		BaseURL: "https://api.example.com",
		SpecURL: "https://api.example.com/openapi.json",
		URLOverrides: map[string]string{
			"https://upload.example.com/v2": "http://localhost:8080/upload",
		},
	})
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Files", "version": "1.0"},
  "servers": [{"url": "https://api.example.com"}],
  "paths": {
    "/files": {
      "post": {
        "operationId": "uploadFile",
        "servers": [{"url": "https://upload.example.com/v2"}],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == "https://api.example.com/openapi.json" {
			return textResponse(200, "application/json", specBody, r), nil
		}
		return textResponse(404, "text/plain", "not found", r), nil
	})

	if err := c.Run([]string{"restish", "api", "sync", "files"}); err != nil {
		t.Fatalf("api sync: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["files"].AllowedOperationOrigins; len(got) != 0 {
		t.Fatalf("allowed_operation_origins = %#v, want none when url_overrides covers operation server", got)
	}
	if strings.Contains(out.String(), "cross-origin operation servers ignored") || strings.Contains(out.String(), "allowed_operation_origins") {
		t.Fatalf("unexpected cross-origin warning with url_overrides:\n%s", out.String())
	}
}

func TestAPISyncPersistsDiscoveredSpecURLAndPreservesProfiles(t *testing.T) {
	cfgFile := writeAPIConfigObject(t, "myapi", &config.APIConfig{
		BaseURL: "https://api.example.com",
		Profiles: map[string]*config.ProfileConfig{
			"default": {
				Headers: []string{"X-API-Key: local-secret"},
			},
		},
	})

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://api.example.com":
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Link": []string{`<https://api.example.com/linked-openapi.json>; rel="service-desc"`}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		case "https://api.example.com/linked-openapi.json":
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(specWithXCLIConfig("https://api.example.com"))),
				Request:    r,
			}, nil
		default:
			return &http.Response{
				StatusCode: 404,
				Proto:      "HTTP/1.1",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("not found")),
				Request:    r,
			}, nil
		}
	})

	if err := c.Run([]string{"restish", "api", "sync", "myapi"}); err != nil {
		t.Fatalf("api sync: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	api := written.APIs["myapi"]
	if got := api.SpecURL; got != "https://api.example.com/linked-openapi.json" {
		t.Fatalf("spec_url = %q, want discovered Link URL", got)
	}
	if got := api.Profiles["default"].Headers; !reflect.DeepEqual(got, []string{"X-API-Key: local-secret"}) {
		t.Fatalf("profile headers = %#v, want local profile preserved", got)
	}
}

func TestAPIConnectLegacyXCLIConfigPrompt(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "components": {
    "securitySchemes": {
      "default": {
        "type": "oauth2",
        "flows": {
          "clientCredentials": {
            "tokenUrl": "https://auth.example.com/token",
            "scopes": {}
          }
        }
      }
    }
  },
  "x-cli-config": {
    "security": "default",
    "headers": {"X-Org": "{org}"},
    "prompt": {
      "client_id": {"description": "Client identifier", "example": "abc123"},
      "org": {"description": "Organization", "exclude": true}
    },
    "params": {
      "audience": "https://example.com/{org}"
    }
  },
  "paths": {}
}`

	c, _, stderr := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	c.Hooks().PassReader = strings.NewReader("abc123\nacme\n")
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	if !strings.Contains(stderr.String(), "Client identifier") || !strings.Contains(stderr.String(), "Organization") {
		t.Fatalf("expected connect-time prompts, got stderr %q", stderr.String())
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	prof := written.APIs["myapi"].Profiles["default"]
	if prof == nil || prof.Auth == nil {
		t.Fatalf("expected default auth profile, got %#v", prof)
	}
	if got := prof.Auth.Params["client_id"]; got != "abc123" {
		t.Fatalf("client_id = %q, want abc123", got)
	}
	if _, ok := prof.Auth.Params["org"]; ok {
		t.Fatalf("excluded prompt value was saved in auth params: %#v", prof.Auth.Params)
	}
	if got := prof.Auth.Params["audience"]; got != "https://example.com/acme" {
		t.Fatalf("audience = %q, want rendered org audience", got)
	}
	if got := prof.Headers; len(got) != 1 || got[0] != "X-Org: acme" {
		t.Fatalf("headers = %#v", got)
	}
	if prof.Credentials["default"] == nil || prof.Credentials["default"].Auth == nil {
		t.Fatalf("expected legacy x-cli-config security to also write credential binding, got %#v", prof.Credentials)
	}
}

func TestAPIConnectRetriesInvalidXCLIPromptInput(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "x-cli-config": {
    "profiles": {
      "default": {
        "headers": ["X-Env: {environment}"],
        "prompt": {
          "environment": {"description": "Environment", "enum": ["prod", "stage"]}
        }
      }
    }
  },
  "paths": {}
}`

	c, _, stderr := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	c.Hooks().PassReader = strings.NewReader("\nqa\nprod\n")
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	errText := stderr.String()
	if !strings.Contains(errText, "environment is required; please enter a non-empty value.") {
		t.Fatalf("expected required-value retry guidance, got %q", errText)
	}
	if !strings.Contains(errText, "environment must be one of: prod, stage.") {
		t.Fatalf("expected enum retry guidance, got %q", errText)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["myapi"].Profiles["default"].Headers; len(got) != 1 || got[0] != "X-Env: prod" {
		t.Fatalf("headers = %#v", got)
	}
}

func TestAPIConnectXCLIConfigCredentialPrompt(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "x-cli-config": {
    "profiles": {
      "default": {
        "credentials": {
          "PartnerKey": {
            "auth": {
              "type": "api-key",
              "params": {"in": "header", "name": "X-Partner-Key", "value": "{partner_key}"}
            },
            "prompt": {
              "partner_key": {"description": "Partner API key"}
            },
            "satisfies": ["reports:read"]
          }
        }
      }
    }
  },
  "paths": {}
}`

	c, _, stderr := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	c.Hooks().PassReader = strings.NewReader("secret-key\n")
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	if !strings.Contains(stderr.String(), "Partner API key") {
		t.Fatalf("expected credential prompt, got stderr %q", stderr.String())
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	credential := written.APIs["myapi"].Profiles["default"].Credentials["PartnerKey"]
	if credential == nil || credential.Auth == nil {
		t.Fatalf("credential = %#v", credential)
	}
	if credential.Auth.Params["value"] != "secret-key" {
		t.Fatalf("auth params = %#v", credential.Auth.Params)
	}
	if got := credential.Satisfies; !reflect.DeepEqual(got, []string{"reports:read"}) {
		t.Fatalf("satisfies = %#v", got)
	}
}

func TestAPIConnectV2ProfilePromptShape(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "x-cli-config": {
    "profiles": {
      "default": {
        "headers": ["X-Org: {org}", "Authorization: Bearer {auth_token}"],
        "auth": {
          "type": "bearer",
          "params": {
            "token": "{auth_token}",
            "audience": "https://example.com/{org}"
          }
        },
        "params": {
          "region": "{org}-west"
        },
        "prompt": {
          "auth_token": {"description": "API token"},
          "org": {"description": "Organization"}
        }
      }
    }
  },
  "paths": {}
}`

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	c.Hooks().PassReader = strings.NewReader("tok-{org}\nacme\n")
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	prof := written.APIs["myapi"].Profiles["default"]
	if prof == nil || prof.Auth == nil {
		t.Fatalf("expected default auth profile, got %#v", prof)
	}
	for _, want := range []string{"X-Org: acme", "Authorization: Bearer tok-{org}"} {
		if !containsString(prof.Headers, want) {
			t.Fatalf("headers missing %q: %#v", want, prof.Headers)
		}
	}
	for key, want := range map[string]string{
		"auth_token": "tok-{org}",
		"org":        "acme",
		"region":     "acme-west",
		"token":      "tok-{org}",
		"audience":   "https://example.com/acme",
	} {
		if got := prof.Auth.Params[key]; got != want {
			t.Fatalf("auth param %s = %q, want %q (all params %#v)", key, got, want, prof.Auth.Params)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestAPIInspect verifies that "api inspect" prints the API config as JSON.
func TestAPIInspect(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile

	if err := c.Run([]string{"restish", "api", "inspect", "myapi"}); err != nil {
		t.Fatalf("api inspect: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "api.example.com") {
		t.Errorf("expected base_url in output, got: %q", got)
	}
	// Validate that output is valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &parsed); err != nil {
		t.Errorf("api inspect output is not valid JSON: %v\n%s", err, got)
	}
}

func TestAPIInspectRejectsUnsupportedResponseTransformFlags(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unsupported format",
			args: []string{"restish", "api", "inspect", "myapi", "-o", "yaml"},
			want: "supports -o json for structured output, not -o yaml",
		},
		{
			name: "filter",
			args: []string{"restish", "api", "inspect", "myapi", "-f", "base_url"},
			want: "does not support -f/--rsh-filter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newTestCLI(t)
			c.Hooks().ConfigPath = cfgFile
			err := c.Run(tt.args)
			if err == nil {
				t.Fatalf("%v: expected unsupported transform flag error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("%v: expected error containing %q, got %v", tt.args, tt.want, err)
			}
		})
	}
}

func TestAPIInspectRedactsCredentialSecrets(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						Headers: []string{
							"Authorization: Bearer header-secret",
							"X-Env: staging",
						},
						Query: []string{
							"api_key=query-secret",
							"page=1",
						},
						Auth: &config.AuthConfig{
							Type: "bearer",
							Params: map[string]string{
								"token": "profile-secret",
							},
						},
						Credentials: map[string]*config.CredentialConfig{
							"service": {
								Auth: &config.AuthConfig{
									Type: "bearer",
									Params: map[string]string{
										"token": "credential-secret",
									},
								},
							},
						},
					},
				},
			},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile

	if err := c.Run([]string{"restish", "api", "inspect", "myapi"}); err != nil {
		t.Fatalf("api inspect: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "profile-secret") || strings.Contains(got, "credential-secret") {
		t.Fatalf("api inspect leaked secret:\n%s", got)
	}
	if strings.Contains(got, "header-secret") || strings.Contains(got, "query-secret") {
		t.Fatalf("api inspect leaked persistent request credentials:\n%s", got)
	}
	if count := strings.Count(got, `"token": "***"`); count != 2 {
		t.Fatalf("redacted token count = %d, want 2:\n%s", count, got)
	}
	for _, want := range []string{`"Authorization: ***"`, `"api_key=***"`, `"X-Env: staging"`, `"page=1"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("api inspect missing %q in redacted output:\n%s", want, got)
		}
	}
}

func TestAPIInspectHighlightsJSONWhenColorEnabled(t *testing.T) {
	t.Setenv("NOCOLOR", "")
	t.Setenv("NO_COLOR", "")
	t.Setenv("COLOR", "1")

	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile

	if err := c.Run([]string{"restish", "api", "inspect", "myapi"}); err != nil {
		t.Fatalf("api inspect: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ANSI highlighting, got %q", got)
	}
	stripped := stripANSI(got)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stripped)), &parsed); err != nil {
		t.Fatalf("api inspect output is not valid JSON after stripping ANSI: %v\n%s", err, stripped)
	}
	if !strings.Contains(stripped, "api.example.com") {
		t.Fatalf("expected base_url in output, got: %q", stripped)
	}
}

// TestAPISet verifies that "api set" updates a field and the change persists.
func TestAPISet(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://old.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	// Set a new base_url.
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `base_url: https://new.example.com`}); err != nil {
		t.Fatalf("api set: %v", err)
	}

	// Reload and verify persistence.
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["myapi"].BaseURL; got != "https://new.example.com" {
		t.Errorf("base_url after set: got %q, want https://new.example.com", got)
	}
}

func TestAPISetPreservesJSONCComments(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{
  // API registrations
  "apis": {
    // Main API
    "myapi": {
      "base_url": "https://old.example.com" // keep this note
    }
  }
}`)

	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `base_url: https://new.example.com`}); err != nil {
		t.Fatalf("api set: %v", err)
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "// API registrations") {
		t.Fatalf("expected top-level comment to be preserved:\n%s", got)
	}
	if !strings.Contains(got, "// Main API") {
		t.Fatalf("expected member comment to be preserved:\n%s", got)
	}
	if !strings.Contains(got, "// keep this note") {
		t.Fatalf("expected inline comment to be preserved:\n%s", got)
	}
	if strings.Contains(errOut.String(), "will not be preserved") {
		t.Fatalf("did not expect comment-loss warning, got %q", errOut.String())
	}
	if !strings.Contains(got, "https://new.example.com") {
		t.Fatalf("expected updated value in file:\n%s", got)
	}
}

func TestAPISetCreatesNestedJSONCPath(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{
  "apis": {
    // Main API
    "myapi": {
      "base_url": "https://api.example.com"
    }
  }
}`)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `profiles.default.auth.params.token: secret`}); err != nil {
		t.Fatalf("api set nested: %v", err)
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "// Main API") {
		t.Fatalf("expected existing comment to be preserved:\n%s", got)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["myapi"].Profiles["default"].Auth.Params["token"]; got != "secret" {
		t.Fatalf("token after set: got %q, want secret", got)
	}
}

func TestAPISetCreatesCredentialPath(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{
  "auth_profiles": {
    "shared": {
      "type": "oauth-client-credentials"
    }
  },
  "apis": {
    "myapi": {
      "base_url": "https://api.example.com"
    }
  }
}`)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{
		"restish", "api", "set", "myapi",
		"profiles.default.credentials.UserOAuth.auth_ref: shared",
		`profiles.default.credentials.UserOAuth.satisfies: ["items:read","items:write"]`,
	}); err != nil {
		t.Fatalf("api set credential: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	credential := written.APIs["myapi"].Profiles["default"].Credentials["UserOAuth"]
	if credential == nil {
		t.Fatal("expected UserOAuth credential")
	}
	if credential.AuthRef != "shared" {
		t.Fatalf("AuthRef = %q, want shared", credential.AuthRef)
	}
	if got, want := credential.Satisfies, []string{"items:read", "items:write"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Satisfies = %#v, want %#v", got, want)
	}
}

func TestAPISetShorthandExpression(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://old.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `allow_cross_origin_spec: true`}); err != nil {
		t.Fatalf("api set shorthand: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if !written.APIs["myapi"].AllowCrossOriginSpec {
		t.Fatalf("expected allow_cross_origin_spec to be true")
	}
}

func TestAPISetMultipleShorthandExpressions(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://old.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `allow_cross_origin_spec: true`, `pagination.items_path: "items"`}); err != nil {
		t.Fatalf("api set multi shorthand: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if !written.APIs["myapi"].AllowCrossOriginSpec {
		t.Fatalf("expected allow_cross_origin_spec to be true")
	}
	if got := written.APIs["myapi"].Pagination.ItemsPath; got != "items" {
		t.Fatalf("expected pagination.items_path to be items, got %q", got)
	}
}

func TestAPISetShorthandAppendHeaders(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `profiles.default.headers[]: "Authorization: Bearer abc"`}); err != nil {
		t.Fatalf("api set append headers: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["myapi"].Profiles["default"].Headers; len(got) != 1 || got[0] != "Authorization: Bearer abc" {
		t.Fatalf("unexpected headers after append: %#v", got)
	}
}

func TestAPISetRejectsInvalidPersistentHeaderAndQuery(t *testing.T) {
	tests := []struct {
		name      string
		patch     string
		wantError string
	}{
		{
			name:      "header",
			patch:     `profiles.default.headers[]: "X-Concurrent-A=1"`,
			wantError: `apis.myapi.profiles.default.headers[0]: invalid header "X-Concurrent-A=1": expected "Name: Value" format`,
		},
		{
			name:      "query",
			patch:     `profiles.default.query[]: brokenquery`,
			wantError: `apis.myapi.profiles.default.query[0]: invalid query param "brokenquery": expected "key=value" format`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfgData, _ := json.Marshal(&config.Config{
				APIs: map[string]*config.APIConfig{
					"myapi": {BaseURL: "https://api.example.com"},
				},
			})
			cfgFile := t.TempDir() + "/restish.json"
			_ = os.WriteFile(cfgFile, cfgData, 0o600)

			c, _, _ := newTestCLI(t)
			c.Hooks().ConfigPath = cfgFile
			err := c.Run([]string{"restish", "api", "set", "myapi", tc.patch})
			if err == nil {
				t.Fatal("expected api set to reject invalid persistent request option")
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %q, want to contain %q", err, tc.wantError)
			}
			written, err := config.Load(cfgFile)
			if err != nil {
				t.Fatalf("reload config: %v", err)
			}
			if prof := written.APIs["myapi"].Profiles["default"]; prof != nil {
				t.Fatalf("default profile was written despite validation failure: %#v", prof)
			}
		})
	}
}

func TestAPISetFullAuthObject(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{
		"restish", "api", "set", "myapi",
		`profiles.demo.auth: {type: http-basic, params: {username: demo, password: env:DEMO_PASSWORD}}`,
	}); err != nil {
		t.Fatalf("api set full auth object: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	auth := written.APIs["myapi"].Profiles["demo"].Auth
	if auth == nil || auth.Type != "http-basic" || auth.Params["username"] != "demo" || auth.Params["password"] != "env:DEMO_PASSWORD" {
		t.Fatalf("auth = %#v", auth)
	}
}

func TestAPISetRootedObjectPatch(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{
		"restish", "api", "set", "myapi",
		`{profiles: {demo: {headers: ["X-Debug: true"]}}}`,
	}); err != nil {
		t.Fatalf("api set rooted object patch: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	got := written.APIs["myapi"].Profiles["demo"].Headers
	if !reflect.DeepEqual(got, []string{"X-Debug: true"}) {
		t.Fatalf("headers = %#v", got)
	}
}

func TestAPISetSwapIsRootedAtAPI(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"default": {BaseURL: "https://profile.example.com"},
				},
			},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `base_url ^ profiles.default.base_url`}); err != nil {
		t.Fatalf("api set swap: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	api := written.APIs["myapi"]
	if api.BaseURL != "https://profile.example.com" || api.Profiles["default"].BaseURL != "https://api.example.com" {
		t.Fatalf("api after swap: base_url=%q profiles.default.base_url=%q", api.BaseURL, api.Profiles["default"].BaseURL)
	}
}

func TestAPISetSwapTreatsFullyQualifiedPathsAsAPILocal(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
			"other": {BaseURL: "https://other.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `apis.myapi.base_url ^ apis.other.base_url`}); err != nil {
		t.Fatalf("api set swap with fully qualified paths: %v", err)
	}

	written, loadErr := config.Load(cfgFile)
	if loadErr != nil {
		t.Fatalf("reload config: %v", loadErr)
	}
	if got := written.APIs["myapi"].BaseURL; got != "https://api.example.com" {
		t.Fatalf("myapi base_url = %q, want unchanged", got)
	}
	if got := written.APIs["other"].BaseURL; got != "https://other.example.com" {
		t.Fatalf("other base_url = %q, want unchanged", got)
	}
}

func TestAPISetRejectsNonPatchForm(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	err := c.Run([]string{"restish", "api", "set", "myapi", "base_url", "https://new.example.com"})
	if err == nil {
		t.Fatal("expected non-patch form to be rejected")
	}
	if !strings.Contains(err.Error(), "expected shorthand patch expression") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPISetShorthandDeleteKey(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com", OperationBase: "/v1"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `operation_base: undefined`}); err != nil {
		t.Fatalf("api set delete key: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["myapi"].OperationBase; got != "" {
		t.Fatalf("expected operation_base to be deleted, got %q", got)
	}
}

func TestAPISetValidatesOperationBasePath(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `operation_base: "/v1"`}); err != nil {
		t.Fatalf("expected absolute path operation_base to be accepted: %v", err)
	}
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["myapi"].OperationBase; got != "/v1" {
		t.Fatalf("OperationBase = %q, want /v1", got)
	}

	err = c.Run([]string{"restish", "api", "set", "myapi", `operation_base: "v1"`})
	if err == nil {
		t.Fatal("expected relative operation_base to be rejected")
	}
	if !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("unexpected error: %v", err)
	}

	err = c.Run([]string{"restish", "api", "set", "myapi", `operation_base: "https://api.example.com/v1"`})
	if err == nil {
		t.Fatal("expected URL operation_base to be rejected")
	}
	if !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPISetServerVariables(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{
  "apis": {
    "myapi": {
      "base_url": "https://api.example.com",
      "profiles": {
        "staging": {}
      }
    }
  }
}`)
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile

	if err := c.Run([]string{"restish", "api", "set", "myapi", `server_variables.env: staging`, `profiles.staging.server_variables.version: v2`}); err != nil {
		t.Fatalf("api set server variables: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	api := written.APIs["myapi"]
	if got := api.ServerVariables["env"]; got != "staging" {
		t.Fatalf("server_variables.env = %q, want staging", got)
	}
	if got := api.Profiles["staging"].ServerVariables["version"]; got != "v2" {
		t.Fatalf("profiles.staging.server_variables.version = %q, want v2", got)
	}

	if err := c.Run([]string{"restish", "api", "set", "myapi", `server_variables.env: undefined`, `profiles.staging.server_variables.version: undefined`}); err != nil {
		t.Fatalf("api remove server variables: %v", err)
	}
	written, err = config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload written config: %v", err)
	}
	if _, ok := written.APIs["myapi"].ServerVariables["env"]; ok {
		t.Fatal("expected server_variables.env to be deleted")
	}
	if prof := written.APIs["myapi"].Profiles["staging"]; prof != nil && prof.ServerVariables != nil {
		if _, ok := prof.ServerVariables["version"]; ok {
			t.Fatal("expected profile server variable to be deleted")
		}
	}
}

func TestAPISetServerVariableEnumMismatchWarnsAndSaves(t *testing.T) {
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	specBody := `openapi: "3.1.0"
info:
  title: Test
  version: "1.0.0"
servers:
  - url: https://api.example.com/{prefix}
    variables:
      prefix:
        default: anything
        enum: [anything]
paths:
  /items:
    get:
      operationId: listItems
      responses:
        "200":
          description: OK
`
	if err := os.WriteFile(specPath, []byte(specBody), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com", SpecFiles: []string{specPath}},
		},
	})
	cfgFile := filepath.Join(t.TempDir(), "restish.json")
	if err := os.WriteFile(cfgFile, cfgData, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "set", "myapi", `server_variables.prefix: other`}); err != nil {
		t.Fatalf("api set server variable enum mismatch should warn and save: %v", err)
	}
	if !strings.Contains(errOut.String(), "outside the OpenAPI enum") {
		t.Fatalf("expected enum warning, got:\n%s", errOut.String())
	}
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("load written config: %v", err)
	}
	if got := written.APIs["myapi"].ServerVariables["prefix"]; got != "other" {
		t.Fatalf("server_variables.prefix = %q, want other", got)
	}

	errOut.Reset()
	err = c.Run([]string{"restish", "api", "set", "myapi", `server_variables.unknown: value`})
	if err == nil {
		t.Fatal("expected unknown server variable to be rejected")
	}
	if !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected undeclared server variable error, got %v", err)
	}
	written, err = config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload written config: %v", err)
	}
	if _, ok := written.APIs["myapi"].ServerVariables["unknown"]; ok {
		t.Fatal("unknown server variable should not be saved")
	}
}

func TestAPISetInvalidatesSpecCacheForBaseFields(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)
	cacheDir := t.TempDir()
	cacheFile := cacheDir + "/myapi.cbor"
	if err := os.WriteFile(cacheFile, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	if err := c.Run([]string{"restish", "api", "set", "myapi", `base_url: "https://new.example.com"`}); err != nil {
		t.Fatalf("api set: %v", err)
	}
	if _, err := os.Stat(cacheFile); !os.IsNotExist(err) {
		t.Fatalf("expected spec cache to be invalidated, stat err=%v", err)
	}
}

func TestAPISetDoesNotInvalidateSpecCacheForUnrelatedFields(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)
	cacheDir := t.TempDir()
	cacheFile := cacheDir + "/myapi.cbor"
	if err := os.WriteFile(cacheFile, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	if err := c.Run([]string{"restish", "api", "set", "myapi", `profiles.default.headers[]: "X-Test: 1"`}); err != nil {
		t.Fatalf("api set: %v", err)
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("expected spec cache to remain, stat err=%v", err)
	}
}

func TestAPISetDoesNotInvalidateSpecCacheForOperationMetadataFields(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"staging": {},
				},
			},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)
	cacheDir := t.TempDir()
	cacheFile := cacheDir + "/myapi.cbor"
	if err := os.WriteFile(cacheFile, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = cacheDir
	if err := c.Run([]string{
		"restish", "api", "set", "myapi",
		`operation_base: "/override"`,
		`server_variables.version: v2`,
		`profiles.staging.server_variables.version: v3`,
	}); err != nil {
		t.Fatalf("api set: %v", err)
	}
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("expected raw spec cache to remain for regenerated operation metadata, stat err=%v", err)
	}
}

func TestAPISetRejectsUnknownAuthType(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	err := c.Run([]string{"restish", "api", "set", "myapi", `profiles.default.auth.type: "oauth-typo"`})
	if err == nil {
		t.Fatal("expected auth.type validation error")
	}
	if !strings.Contains(err.Error(), "invalid auth.type") {
		t.Fatalf("expected invalid auth.type error, got: %v", err)
	}
}

func TestAPISetRejectsUnknownTLSSigner(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"myapi": {BaseURL: "https://api.example.com"},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	err := c.Run([]string{"restish", "api", "set", "myapi", `profiles.default.tls_signer: "not-a-plugin"`})
	if err == nil {
		t.Fatal("expected tls_signer validation error")
	}
	if !strings.Contains(err.Error(), "not a registered tls-signer plugin") {
		t.Fatalf("expected tls_signer validation error, got: %v", err)
	}
}

func TestAPISetMixedShorthandPreservesComments(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{
  // API registrations
  "apis": {
    "myapi": {
      "base_url": "https://api.example.com" // important note
    }
  }
}`)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{
		"restish", "api", "set", "myapi",
		`profiles.default.headers[]: "X-Test: 1"`,
		`allow_cross_origin_spec: true`,
		`operation_base: undefined`,
	}); err != nil {
		t.Fatalf("api set mixed shorthand: %v", err)
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "// API registrations") || !strings.Contains(got, "// important note") {
		t.Fatalf("expected comments preserved, got:\n%s", got)
	}
}

func TestAPIConnectWithShorthand(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com", "--no-discover", `profiles.default.auth.type: "http-basic"`}); err != nil {
		t.Fatalf("api connect shorthand: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["myapi"].BaseURL; got != "https://api.example.com" {
		t.Fatalf("base_url after add: got %q", got)
	}
	if got := written.APIs["myapi"].Profiles["default"].Auth.Type; got != "http-basic" {
		t.Fatalf("auth.type after add: got %q", got)
	}
}

func TestAPIConnectCreatesMissingConfigFile(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "restish.json")

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com", "--no-discover"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["myapi"].BaseURL; got != "https://api.example.com" {
		t.Fatalf("base_url after add: got %q", got)
	}
}

func TestAPIConnectNormalizesSchemelessURL(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "connect", "remote", "api.example.com", "--no-discover"}); err != nil {
		t.Fatalf("api connect remote: %v", err)
	}
	if err := c.Run([]string{"restish", "api", "connect", "local", "localhost:8080", "--no-discover"}); err != nil {
		t.Fatalf("api connect local: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["remote"].BaseURL; got != "https://api.example.com" {
		t.Fatalf("remote base_url = %q, want https://api.example.com", got)
	}
	if got := written.APIs["local"].BaseURL; got != "http://localhost:8080" {
		t.Fatalf("local base_url = %q, want http://localhost:8080", got)
	}
}

func TestAPIConnectNoDiscoverPerformsNoNetworkDiscovery(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected network request during --no-discover: %s", r.URL)
		return nil, nil
	})

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com", "--no-discover"}); err != nil {
		t.Fatalf("api connect --no-discover: %v", err)
	}
	if !strings.Contains(out.String(), "discovery skipped") {
		t.Fatalf("expected discovery skipped summary, got %q", out.String())
	}
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["myapi"].BaseURL; got != "https://api.example.com" {
		t.Fatalf("base_url = %q", got)
	}
}

func TestAPIConnectSpecLocalFile(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specFile := filepath.Join(t.TempDir(), "openapi.json")
	if err := os.WriteFile(specFile, []byte(`{
		"openapi":"3.1.0",
		"info":{"title":"Local","version":"1.0"},
		"paths":{"/items":{"get":{"operationId":"list-items","responses":{"200":{"description":"OK"}}}}}
	}`), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected network request for local --spec: %s", r.URL)
		return nil, nil
	})

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com", "--spec", specFile}); err != nil {
		t.Fatalf("api connect --spec file: %v", err)
	}
	if !strings.Contains(out.String(), "1 operations discovered") {
		t.Fatalf("expected operation count in summary, got %q", out.String())
	}
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := written.APIs["myapi"].SpecFiles; !reflect.DeepEqual(got, []string{specFile}) {
		t.Fatalf("spec_files = %#v, want %q", got, specFile)
	}
}

func TestAPIConnectExplicitSpecRejectsNonOpenAPIJSON(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specFile := filepath.Join(t.TempDir(), "not-openapi.json")
	if err := os.WriteFile(specFile, []byte(`{"name":"not an API spec"}`), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected network request for local --spec: %s", r.URL)
		return nil, nil
	})

	err := c.Run([]string{"restish", "api", "connect", "badapi", "https://api.example.com", "--spec", specFile})
	if err == nil {
		t.Fatal("expected api connect --spec to fail for non-OpenAPI JSON")
	}
	if !strings.Contains(err.Error(), "unsupported API spec") || !strings.Contains(err.Error(), specFile) {
		t.Fatalf("expected unsupported explicit spec error with path, got: %v", err)
	}
	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if _, ok := written.APIs["badapi"]; ok {
		t.Fatalf("api connect wrote badapi despite invalid explicit spec: %#v", written.APIs["badapi"])
	}
}

func TestAPIConnectSetupExpressions(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "x-cli-config": {
    "profiles": {
      "default": {
        "headers": ["X-Client: {client_id}"],
        "auth": {"type": "bearer", "params": {"token": "{token}"}},
        "prompt": {
          "client_id": {"description": "Client ID"},
          "token": {"description": "Token"}
        }
      }
    }
  },
  "paths": {}
}`

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{
		"restish", "api", "connect", "myapi", "api.example.com",
		`prompt.client_id: abc123`,
		`prompt.token: secret-token`,
		`profiles.default.headers[]: "X-Env: prod"`,
	}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	api := written.APIs["myapi"]
	if got := api.BaseURL; got != "https://api.example.com" {
		t.Fatalf("base_url = %q, want https://api.example.com", got)
	}
	prof := api.Profiles["default"]
	for _, want := range []string{"X-Client: abc123", "X-Env: prod"} {
		if !containsString(prof.Headers, want) {
			t.Fatalf("headers missing %q: %#v", want, prof.Headers)
		}
	}
	if got := prof.Auth.Params["token"]; got != "secret-token" {
		t.Fatalf("token = %q, want secret-token", got)
	}
}

func TestAPIConnectFallbackAPIKeySetup(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "components": {
    "securitySchemes": {
      "key": {"type": "apiKey", "in": "header", "name": "X-API-Key"}
    }
  },
  "security": [{"key": []}],
  "paths": {}
}`

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{
		"restish", "api", "connect", "myapi", "api.example.com",
		`prompt.value: secret-key`,
	}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	prof := written.APIs["myapi"].Profiles["default"]
	if prof.Auth == nil || prof.Auth.Type != "api-key" || prof.Auth.Params["value"] != "secret-key" {
		t.Fatalf("api key fallback auth = %#v", prof.Auth)
	}
	if prof.Credentials["key"] == nil || prof.Credentials["key"].Auth == nil || prof.Credentials["key"].Auth.Type != "api-key" {
		t.Fatalf("api key fallback credential = %#v", prof.Credentials)
	}
}

func TestAPIConnectFallbackHTTPBasicPromptsCredentials(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "components": {
    "securitySchemes": {
      "basicAuth": {"type": "http", "scheme": "basic"}
    }
  },
  "security": [{"basicAuth": []}],
  "paths": {}
}`

	c, _, stderr := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	c.Hooks().PassReader = strings.NewReader("alice\nsecret\n")
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	if !strings.Contains(stderr.String(), "Username:") || !strings.Contains(stderr.String(), "Password:") {
		t.Fatalf("expected basic auth prompts, got stderr %q", stderr.String())
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	prof := written.APIs["myapi"].Profiles["default"]
	if prof.Auth == nil || prof.Auth.Type != "http-basic" || prof.Auth.Params["username"] != "alice" || prof.Auth.Params["password"] != "secret" {
		t.Fatalf("basic fallback auth = %#v", prof.Auth)
	}
	if prof.Credentials["basicAuth"] == nil || prof.Credentials["basicAuth"].Auth.Params["username"] != "alice" {
		t.Fatalf("basic fallback credential = %#v", prof.Credentials)
	}
}

func TestAPIConnectFallbackMultiCredentialSetup(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "components": {
    "securitySchemes": {
      "UserOAuth": {
        "type": "oauth2",
        "flows": {
          "clientCredentials": {
            "tokenUrl": "https://auth.example.com/token",
            "scopes": {"items:read": "Read items"}
          }
        }
      },
      "PartnerKey": {"type": "apiKey", "in": "header", "name": "X-Partner-Key"}
    }
  },
  "security": [{"UserOAuth": ["items:read"]}, {"PartnerKey": []}],
  "paths": {}
}`

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	c.Hooks().PassReader = strings.NewReader("n\n")
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{
		"restish", "api", "connect", "myapi", "api.example.com",
		`prompt.credentials.PartnerKey.value: partner-secret`,
	}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	if !strings.Contains(out.String(), "Auth coverage") {
		t.Fatalf("expected coverage summary, got %q", out.String())
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	creds := written.APIs["myapi"].Profiles["default"].Credentials
	if creds["UserOAuth"] != nil {
		t.Fatalf("expected UserOAuth to be skipped without an answer, got %#v", creds["UserOAuth"])
	}
	if creds["PartnerKey"] == nil || creds["PartnerKey"].Auth == nil || creds["PartnerKey"].Auth.Params["value"] != "partner-secret" {
		t.Fatalf("PartnerKey credential = %#v", creds["PartnerKey"])
	}
}

func TestAPIConnectFallbackRejectsUnknownCredentialSetupPaths(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "components": {
    "securitySchemes": {
      "bearerAuth": {"type": "http", "scheme": "bearer"}
    }
  },
  "security": [{"bearerAuth": []}],
  "paths": {}
}`

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specBody)

	err := c.Run([]string{
		"restish", "api", "connect", "myapi", "api.example.com", "--yes",
		`prompt.credentials.BearerAuth.token: env:API_TOKEN`,
		`prompt.credentials.bearerAuth.value: env:API_TOKEN`,
	})
	if err == nil {
		t.Fatal("expected unknown credential setup paths to fail")
	}
	got := err.Error()
	for _, want := range []string{
		"unused auth setup value(s)",
		"prompt.credentials.BearerAuth.token",
		"prompt.credentials.bearerAuth.value",
		"valid credential setup value(s)",
		"prompt.credentials.bearerAuth.token",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
	written, loadErr := config.Load(cfgFile)
	if loadErr != nil {
		t.Fatalf("load config: %v", loadErr)
	}
	if written.APIs["myapi"] != nil {
		t.Fatalf("api connect wrote config despite invalid setup paths: %#v", written.APIs["myapi"])
	}
}

func TestAPIConnectRejectsCredentialSetupWhenNoAuthSchemesConsumeIt(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "paths": {}
}`

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specBody)

	err := c.Run([]string{
		"restish", "api", "connect", "myapi", "api.example.com", "--yes",
		`prompt.credentials.BearerAuth.token: env:API_TOKEN`,
	})
	if err == nil {
		t.Fatal("expected unused credential setup path to fail")
	}
	if got := err.Error(); !strings.Contains(got, "unused auth setup value(s)") || !strings.Contains(got, "prompt.credentials.BearerAuth.token") {
		t.Fatalf("unexpected error: %v", err)
	}
	written, loadErr := config.Load(cfgFile)
	if loadErr != nil {
		t.Fatalf("load config: %v", loadErr)
	}
	if written.APIs["myapi"] != nil {
		t.Fatalf("api connect wrote config despite invalid setup path: %#v", written.APIs["myapi"])
	}
}

func TestAPIConnectFallbackExplicitCredentialWithAnonymousDefault(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Optional Auth API", "version": "1.0"},
  "components": {
    "securitySchemes": {
      "petstore_auth": {
        "type": "oauth2",
        "flows": {
          "authorizationCode": {
            "authorizationUrl": "https://auth.example.com/authorize",
            "tokenUrl": "https://auth.example.com/token",
            "scopes": {"read:pets": "Read pets"}
          }
        }
      },
      "api_key": {"type": "apiKey", "in": "header", "name": "X-API-Key"}
    }
  },
  "security": [{}],
  "paths": {
    "/oauth": {
      "get": {
        "operationId": "getOAuthOnly",
        "security": [{"petstore_auth": ["read:pets"]}],
        "responses": {"200": {"description": "OK"}}
      }
    },
    "/pets/{id}": {
      "get": {
        "operationId": "getPetById",
        "security": [{"api_key": []}],
        "responses": {"200": {"description": "OK"}}
      }
    },
    "/inventory": {
      "get": {
        "operationId": "getInventory",
        "security": [{"api_key": []}],
        "responses": {"200": {"description": "OK"}}
      }
    },
    "/status": {
      "get": {
        "operationId": "status",
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{
		"restish", "api", "connect", "optional", "api.example.com",
		`prompt.credentials.api_key.value:env:REDOC16_API_KEY`,
	}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	outText := out.String()
	for _, want := range []string{
		"configured: api_key",
		"unresolved: api_key (env missing: REDOC16_API_KEY)",
		"skipped:    petstore_auth",
		"callable:   0/3 secured operations",
	} {
		if !strings.Contains(outText, want) {
			t.Fatalf("expected stdout to contain %q, got:\n%s", want, outText)
		}
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	prof := written.APIs["optional"].Profiles["default"]
	credential := prof.Credentials["api_key"]
	if credential == nil || credential.Auth == nil {
		t.Fatalf("api_key credential = %#v", credential)
	}
	if got := credential.Auth.Params["value"]; got != "env:REDOC16_API_KEY" {
		t.Fatalf("api_key value = %q, want env:REDOC16_API_KEY", got)
	}
	if prof.Credentials["petstore_auth"] != nil {
		t.Fatalf("expected petstore_auth to be skipped, got %#v", prof.Credentials["petstore_auth"])
	}
}

func TestAPIConnectAuthCoverageCountsEnvBackedCredentialsOnlyWhenEnvIsReady(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Cookie API", "version": "1.0"},
  "servers": [{"url": "https://api.example.com"}],
  "components": {
    "securitySchemes": {
      "sid": {"type": "apiKey", "in": "cookie", "name": "SID"}
    }
  },
  "paths": {
    "/version": {
      "get": {
        "operationId": "appVersionGet",
        "security": [{"sid": []}],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`
	runConnect := func(t *testing.T, name string) string {
		t.Helper()
		c, out, _ := newTestCLI(t)
		c.Hooks().ConfigPath = cfgFile
		c.Hooks().SpecCachePath = t.TempDir()
		useOpenAPISpecTransport(c, specBody)
		if err := c.Run([]string{
			"restish", "api", "connect", name, "api.example.com",
			`prompt.credentials.sid.value:env:RESTISH_TEST_SID`,
		}); err != nil {
			t.Fatalf("api connect: %v", err)
		}
		return out.String()
	}

	t.Run("missing env", func(t *testing.T) {
		t.Setenv("RESTISH_TEST_SID", "")
		if err := os.Unsetenv("RESTISH_TEST_SID"); err != nil {
			t.Fatalf("unset env: %v", err)
		}
		outText := runConnect(t, "cookiemissing")
		for _, want := range []string{
			"configured: sid",
			"unresolved: sid (env missing: RESTISH_TEST_SID)",
			"callable:   0/1 secured operations",
		} {
			if !strings.Contains(outText, want) {
				t.Fatalf("expected stdout to contain %q, got:\n%s", want, outText)
			}
		}
	})

	t.Run("ready env", func(t *testing.T) {
		t.Setenv("RESTISH_TEST_SID", "cookie-secret")
		outText := runConnect(t, "cookieready")
		for _, want := range []string{
			"configured: sid",
			"callable:   1/1 secured operations",
		} {
			if !strings.Contains(outText, want) {
				t.Fatalf("expected stdout to contain %q, got:\n%s", want, outText)
			}
		}
	})
}

func TestAPIConnectFallbackAuthDiscoveryFlow(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{}`)
	specBody := `{
  "openapi": "3.1.0",
  "info": {"title": "Example API", "version": "1.0"},
  "components": {
    "securitySchemes": {
      "UserOAuth": {
        "type": "oauth2",
        "flows": {
          "authorizationCode": {
            "authorizationUrl": "https://auth.example.com/authorize",
            "tokenUrl": "https://auth.example.com/token",
            "scopes": {
              "items:read": "Read items",
              "items:write": "Write items"
            }
          }
        }
      },
      "AdminOAuth": {
        "type": "oauth2",
        "flows": {
          "authorizationCode": {
            "authorizationUrl": "https://auth.example.com/admin/authorize",
            "tokenUrl": "https://auth.example.com/admin/token",
            "scopes": {"admin:read": "Read admin"}
          }
        }
      },
      "PartnerKey": {"type": "apiKey", "in": "header", "name": "X-Partner-Key"}
    }
  },
  "security": [{"UserOAuth": ["items:read", "items:write"]}],
  "paths": {
    "/items": {"get": {"operationId": "list-items", "responses": {"200": {"description": "OK"}}}},
    "/admin": {"get": {"operationId": "get-admin", "security": [{"AdminOAuth": ["admin:read"]}], "responses": {"200": {"description": "OK"}}}},
    "/partner": {"get": {"operationId": "get-partner", "security": [{"PartnerKey": []}], "responses": {"200": {"description": "OK"}}}},
    "/either": {"get": {"operationId": "get-either", "security": [{"UserOAuth": ["items:read"]}, {"PartnerKey": []}], "responses": {"200": {"description": "OK"}}}},
    "/public": {"get": {"operationId": "get-public", "security": [], "responses": {"200": {"description": "OK"}}}}
  }
}`

	c, out, stderr := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	c.Hooks().PassReader = strings.NewReader("y\nuser-client\n\ny\nadmin-client\n\nn\n")
	useOpenAPISpecTransport(c, specBody)

	if err := c.Run([]string{"restish", "api", "connect", "example", "api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	outText := out.String()
	for _, want := range []string{
		"Discovered Example API",
		"This API declares 3 auth scheme(s)",
		"UserOAuth",
		"global default",
		"configured: AdminOAuth, UserOAuth",
		"unresolved: AdminOAuth (OAuth access token not cached), UserOAuth (OAuth access token not cached)",
		"skipped:    PartnerKey",
		"callable:   0/4 secured operations",
	} {
		if !strings.Contains(outText, want) {
			t.Fatalf("expected stdout to contain %q, got:\n%s", want, outText)
		}
	}
	errText := stderr.String()
	for _, want := range []string{
		"Configure UserOAuth? [Y/n]",
		"Client ID:",
		"Scopes [items:read items:write]:",
		"Configure AdminOAuth? [y/N]",
		"Configure PartnerKey? [y/N]",
	} {
		if !strings.Contains(errText, want) {
			t.Fatalf("expected stderr to contain %q, got:\n%s", want, errText)
		}
	}

	written, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	prof := written.APIs["example"].Profiles["default"]
	if prof.Auth == nil || prof.Auth.Type != "oauth-authorization-code" || prof.Auth.Params["client_id"] != "user-client" {
		t.Fatalf("profile auth = %#v", prof.Auth)
	}
	user := prof.Credentials["UserOAuth"]
	if user == nil || user.Auth == nil || user.Auth.Params["client_id"] != "user-client" {
		t.Fatalf("UserOAuth = %#v", user)
	}
	if got := user.Satisfies; !reflect.DeepEqual(got, []string{"items:read", "items:write"}) {
		t.Fatalf("UserOAuth satisfies = %#v", got)
	}
	admin := prof.Credentials["AdminOAuth"]
	if admin == nil || admin.Auth == nil || admin.Auth.Params["client_id"] != "admin-client" {
		t.Fatalf("AdminOAuth = %#v", admin)
	}
	if prof.Credentials["PartnerKey"] != nil {
		t.Fatalf("expected PartnerKey to be skipped, got %#v", prof.Credentials["PartnerKey"])
	}
}

func TestAPIConnectPreservesJSONCComments(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{
  // Existing APIs
  "apis": {
    "other": {
      "base_url": "https://other.example.com"
    }
  }
}`)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specWithXCLIConfig("https://api.example.com"))

	if err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}
	if !strings.Contains(out.String(), "myapi") {
		t.Fatalf("expected connect output, got %q", out.String())
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "// Existing APIs") {
		t.Fatalf("expected existing comment to be preserved:\n%s", got)
	}
	if !strings.Contains(got, `"myapi"`) {
		t.Fatalf("expected new api entry:\n%s", got)
	}
}

func TestAPIConnectDoesNotOverwriteInvalidConfig(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	invalid := "{\n  \"apis\": {\n"
	if err := os.WriteFile(cfgFile, []byte(invalid), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()

	err := c.Run([]string{"restish", "api", "connect", "myapi", "https://api.example.com"})
	if err == nil {
		t.Fatal("expected api connect to fail for invalid config")
	}
	if !strings.Contains(err.Error(), "invalid config") {
		t.Fatalf("expected invalid config error, got: %v", err)
	}

	data, readErr := os.ReadFile(cfgFile)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(data) != invalid {
		t.Fatalf("expected invalid config to remain unchanged, got:\n%s", data)
	}
}

func TestAPIConnectAdversarialSpecShapesFailGracefully(t *testing.T) {
	deep := `{"type":"object"}`
	for i := 0; i < 64; i++ {
		deep = fmt.Sprintf(`{"type":"object","properties":{"n":%s}}`, deep)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "openapi": "3.1.0",
  "info": {"title": "Adversarial", "version": "1.0"},
  "servers": [{"url": %q}],
  "components": {
    "securitySchemes": {
      "OddOAuth": {
        "type": "oauth2",
        "flows": {
          "authorizationCode": {"authorizationUrl": "https://auth.example.com/auth", "scopes": {"read": "Read"}},
          "clientCredentials": {"tokenUrl": "https://auth.example.com/token", "scopes": {"write": "Write"}}
        }
      }
    },
    "schemas": {"Deep": %s}
  },
  "paths": {
    "/items": {
      "post": {
        "operationId": "createItem",
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Deep"}}}},
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, "https://api.example.com", deep)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _, _ := newTestCLI(t)
	c.Hooks().SpecCachePath = t.TempDir()
	c.Hooks().PassReader = strings.NewReader("client-id\n")
	err := c.Run([]string{"restish", "api", "connect", "adversarial", "https://api.example.com", "--spec", srv.URL + "/openapi.json"})
	if err != nil {
		t.Fatalf("api connect should handle adversarial-but-parseable spec gracefully: %v", err)
	}
}

func TestAPIConnectRejectsRemovedCommandNames(t *testing.T) {
	for _, args := range [][]string{
		{"restish", "api", "add", "myapi", "https://api.example.com"},
		{"restish", "api", "delete", "myapi"},
	} {
		c, _, _ := newTestCLI(t)
		if err := c.Run(args); err == nil {
			t.Fatalf("%v: expected unknown command error", args)
		} else if !strings.Contains(err.Error(), "did you mean") {
			t.Fatalf("%v: expected suggestion in unknown command error, got %v", args, err)
		}
	}
}

func TestAPIConfigureShowsV1MigrationMessage(t *testing.T) {
	c, _, _ := newTestCLI(t)
	err := c.Run([]string{"restish", "api", "configure", "myapi", "https://api.example.com"})
	if err == nil {
		t.Fatal("api configure should fail with a v1 migration message")
	}
	requireContains(t, err.Error(),
		"api configure was a Restish v1 command",
		"restish api connect myapi https://api.example.com",
		"https://rest.sh/docs/getting-started/upgrade-from-v1/",
		"https://rest.sh/v1/",
	)
}

func TestAPIRemovePreservesJSONCComments(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{
  "apis": {
    // Keep this API
    "keep": {
      "base_url": "https://keep.example.com"
    },
    // Remove this API
    "remove": {
      "base_url": "https://remove.example.com"
    }
  }
}`)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	if err := c.Run([]string{"restish", "api", "remove", "remove"}); err != nil {
		t.Fatalf("api remove: %v", err)
	}
	if !strings.Contains(out.String(), "Wrote config: "+cfgFile) ||
		!strings.Contains(out.String(), `Removed API "remove" and cleared its local cache/auth state.`) {
		t.Fatalf("expected remove output, got %q", out.String())
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "// Keep this API") {
		t.Fatalf("expected kept comment to remain:\n%s", got)
	}
	if strings.Contains(got, "remove.example.com") {
		t.Fatalf("expected API to be removed:\n%s", got)
	}
}

func TestAPIRemoveClearsAPILocalState(t *testing.T) {
	cacheDir := t.TempDir()
	tokenPath := filepath.Join(t.TempDir(), "tokens.cbor")
	cfgFile := writeAPIConfig(t, `{
  "apis": {
    "remove": {
      "base_url": "https://remove.example.com",
      "profiles": {
        "default": {"auth_ref": "shared"},
        "admin": {
          "credentials": {
            "OAuth": {"auth_ref": "remove-only"}
          }
        }
      }
    },
    "keep": {
      "base_url": "https://keep.example.com"
    }
  },
  "auth_profiles": {
    "shared": {"type": "bearer", "params": {"token": "shared"}},
    "remove-only": {"type": "bearer", "params": {"token": "remove-only"}}
  }
}`)

	removeCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "remove:default")
	if err != nil {
		t.Fatalf("New remove cache: %v", err)
	}
	removeAdminCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "remove:admin")
	if err != nil {
		t.Fatalf("New remove admin cache: %v", err)
	}
	keepCache, err := cachepkg.New(cacheDir, cachepkg.DefaultMaxBytes, "keep:default")
	if err != nil {
		t.Fatalf("New keep cache: %v", err)
	}
	removeCache.Set("https://remove.example.com/items", []byte("remove"))
	removeAdminCache.Set("https://remove.example.com/admin", []byte("admin"))
	keepCache.Set("https://keep.example.com/items", []byte("keep"))

	tc := auth.NewTokenCache(tokenPath)
	if err := tc.Set("remove:default", auth.CachedToken{AccessToken: "remove-default"}); err != nil {
		t.Fatalf("set token: %v", err)
	}
	if err := tc.Set("remove:admin:credential:OAuth", auth.CachedToken{AccessToken: "remove-admin"}); err != nil {
		t.Fatalf("set credential token: %v", err)
	}
	if err := tc.Set("keep:default", auth.CachedToken{AccessToken: "keep"}); err != nil {
		t.Fatalf("set keep token: %v", err)
	}
	if err := tc.Set("auth_profile:shared:abc", auth.CachedToken{AccessToken: "shared"}); err != nil {
		t.Fatalf("set shared token: %v", err)
	}
	if err := tc.Set("auth_profile:remove-only:abc", auth.CachedToken{AccessToken: "remove-only"}); err != nil {
		t.Fatalf("set remove-only token: %v", err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().CachePath = cacheDir
	c.Hooks().TokenCachePath = tokenPath
	if err := c.Run([]string{"restish", "api", "remove", "remove"}); err != nil {
		t.Fatalf("api remove: %v", err)
	}

	if _, ok := removeCache.Get("https://remove.example.com/items"); ok {
		t.Fatal("expected removed API default HTTP cache to be cleared")
	}
	if _, ok := removeAdminCache.Get("https://remove.example.com/admin"); ok {
		t.Fatal("expected removed API admin HTTP cache to be cleared")
	}
	if got, ok := keepCache.Get("https://keep.example.com/items"); !ok || string(got) != "keep" {
		t.Fatal("expected unrelated HTTP cache to remain")
	}

	cache := auth.NewTokenCache(tokenPath)
	for _, key := range []string{"remove:default", "remove:admin:credential:OAuth", "auth_profile:shared:abc", "auth_profile:remove-only:abc"} {
		got, err := cache.Get(key)
		if err != nil {
			t.Fatalf("read token %s: %v", key, err)
		}
		if got != nil {
			t.Fatalf("expected token %s to be cleared", key)
		}
	}
	got, err := cache.Get("keep:default")
	if err != nil {
		t.Fatalf("read keep token: %v", err)
	}
	if got == nil {
		t.Fatal("expected unrelated auth token to remain")
	}
}

func TestAPIRemoveKeepsSharedAuthProfileTokenStillReferenced(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "tokens.cbor")
	cfgFile := writeAPIConfig(t, `{
  "apis": {
    "remove": {
      "base_url": "https://remove.example.com",
      "profiles": {"default": {"auth_ref": "shared"}}
    },
    "keep": {
      "base_url": "https://keep.example.com",
      "profiles": {"default": {"auth_ref": "shared"}}
    }
  },
  "auth_profiles": {
    "shared": {"type": "bearer", "params": {"token": "shared"}}
  }
}`)

	if err := auth.NewTokenCache(tokenPath).Set("auth_profile:shared:abc", auth.CachedToken{AccessToken: "shared"}); err != nil {
		t.Fatalf("set shared token: %v", err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().TokenCachePath = tokenPath
	if err := c.Run([]string{"restish", "api", "remove", "remove"}); err != nil {
		t.Fatalf("api remove: %v", err)
	}

	got, err := auth.NewTokenCache(tokenPath).Get("auth_profile:shared:abc")
	if err != nil {
		t.Fatalf("read shared token: %v", err)
	}
	if got == nil {
		t.Fatal("expected shared auth profile token to remain while another API references it")
	}
}

// TestAPISyncClearsCache (verifies api sync already tested in spec_test.go,
// but also that it reports success from the api subcommand path).
func TestAPISyncReportsSuccess(t *testing.T) {
	c := newSpecTestCLI(t, "syncapi", "https://api.example.com")
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/openapi.json":
			return jsonResponse(200, minimalOpenAPI), nil
		default:
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		}
	})
	var out strings.Builder
	c.Stdout = &out
	if err := c.Run([]string{"restish", "api", "sync", "syncapi"}); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	if !strings.Contains(out.String(), "Synced") {
		t.Errorf("expected Synced in output, got: %q", out.String())
	}
}

func TestAPISyncPrintsXCLIExtensionSummary(t *testing.T) {
	c, out, _ := newTestCLI(t)
	cfgFile := c.Hooks().ConfigPath
	cfgData := `{"apis":{"myapi":{"base_url":"https://api.example.com"}}}`
	if err := os.WriteFile(cfgFile, []byte(cfgData), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, specWithXCLIVisibility("https://api.example.com"))

	if err := c.Run([]string{"restish", "api", "sync", "myapi"}); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"OpenAPI x-cli extensions:",
		"x-cli-config",
		"1 hidden operation",
		"1 renamed operation",
		"1 renamed parameter",
		`Synced spec for "myapi".`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("api sync output missing %q:\n%s", want, got)
		}
	}
}

func TestAPISyncWarnsAboutUndeclaredSecurityScheme(t *testing.T) {
	c := newSpecTestCLI(t, "syncapi", "https://api.example.com")
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/openapi.json":
			return jsonResponse(200, `{
  "openapi": "3.1.0",
  "info": {"title": "Test API", "version": "1.0"},
  "paths": {
    "/audit": {
      "get": {
        "operationId": "getAudit",
        "security": [{"BearerAuth": []}],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`), nil
		default:
			return &http.Response{
				StatusCode: 200,
				Proto:      "HTTP/1.1",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		}
	})
	var out, errOut strings.Builder
	c.Stdout = &out
	c.Stderr = &errOut
	if err := c.Run([]string{"restish", "api", "sync", "syncapi"}); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	if !strings.Contains(out.String(), "Synced") {
		t.Errorf("expected Synced in output, got: %q", out.String())
	}
	if !strings.Contains(errOut.String(), `warning: OpenAPI security: security scheme "BearerAuth" is referenced`) {
		t.Fatalf("expected undeclared security warning, got:\n%s", errOut.String())
	}
}

func TestAPISyncRecognizesDPoPSecurityScheme(t *testing.T) {
	c := newSpecTestCLI(t, "syncapi", "https://api.example.com")
	useOpenAPISpecTransport(c, `{
  "openapi": "3.1.0",
  "info": {"title": "Test API", "version": "1.0"},
  "components": {
    "securitySchemes": {
      "DPoP": {"type": "http", "scheme": "DPoP"}
    }
  },
  "paths": {
    "/wallet": {
      "get": {
        "operationId": "getWallet",
        "security": [{"DPoP": []}],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`)
	var errOut strings.Builder
	c.Stderr = &errOut

	if err := c.Run([]string{"restish", "api", "sync", "syncapi"}); err != nil {
		t.Fatalf("api sync: %v", err)
	}
	if strings.Contains(errOut.String(), "unsupported") {
		t.Fatalf("unexpected unsupported security warning:\n%s", errOut.String())
	}
}

func TestAPISyncNetworkFailureLeavesRegistrationAndCache(t *testing.T) {
	c := newSpecTestCLI(t, "syncapi", "https://api.example.com")
	cacheFile := filepath.Join(c.Hooks().SpecCachePath, "syncapi.cbor")
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, []byte("existing-cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})

	err := c.Run([]string{"restish", "api", "sync", "syncapi"})
	if err == nil {
		t.Fatal("expected api sync failure")
	}
	if !strings.Contains(err.Error(), "left unchanged") {
		t.Fatalf("expected unchanged hint, got %v", err)
	}
	if _, statErr := os.Stat(cacheFile); statErr != nil {
		t.Fatalf("expected existing cache to remain: %v", statErr)
	}
	cfg, loadErr := config.Load(c.Hooks().ConfigPath)
	if loadErr != nil {
		t.Fatalf("load config: %v", loadErr)
	}
	if cfg.APIs["syncapi"] == nil {
		t.Fatal("expected API registration to remain")
	}
}

func TestAPIListJSONOutput(t *testing.T) {
	cfgFile := writeAPIConfig(t, `{
  "apis": {
    "example": {
      "base_url": "https://api.example.com",
      "profiles": {
        "default": {},
        "admin": {}
      }
    }
  }
}`)
	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile

	if err := c.Run([]string{"restish", "api", "list", "-o", "json"}); err != nil {
		t.Fatalf("api list -o json: %v", err)
	}
	var got []struct {
		Name         string   `json:"name"`
		BaseURL      string   `json:"base_url"`
		ProfileCount int      `json:"profile_count"`
		Profiles     []string `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("parse JSON output: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].Name != "example" || got[0].BaseURL != "https://api.example.com" || got[0].ProfileCount != 2 {
		t.Fatalf("api list JSON = %#v", got)
	}
	if !reflect.DeepEqual(got[0].Profiles, []string{"admin", "default"}) {
		t.Fatalf("profiles = %#v", got[0].Profiles)
	}
}

func TestAPIListIncludesOperationCount(t *testing.T) {
	cfgFile := t.TempDir() + "/restish.json"
	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	c.Hooks().SpecCachePath = t.TempDir()
	useOpenAPISpecTransport(c, `{
  "openapi": "3.1.0",
  "info": {"title": "Managed API", "version": "1.0"},
  "paths": {
    "/items": {
      "get": {
        "operationId": "listItems",
        "responses": {"200": {"description": "OK"}}
      },
      "post": {
        "operationId": "createItem",
        "responses": {"201": {"description": "Created"}}
      }
    }
  }
}`)

	if err := c.Run([]string{"restish", "api", "connect", "example", "https://api.example.com"}); err != nil {
		t.Fatalf("api connect: %v", err)
	}

	out.Reset()
	if err := c.Run([]string{"restish", "api", "list"}); err != nil {
		t.Fatalf("api list: %v", err)
	}
	wantList := "example  https://api.example.com  2 operations  0 profiles\n"
	if out.String() != wantList {
		t.Fatalf("api list output = %q, want %q", out.String(), wantList)
	}

	out.Reset()
	if err := c.Run([]string{"restish", "api", "list", "-o", "json"}); err != nil {
		t.Fatalf("api list -o json: %v", err)
	}
	var got []struct {
		Name           string `json:"name"`
		OperationCount int    `json:"operation_count"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse JSON output: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0].Name != "example" || got[0].OperationCount != 2 {
		t.Fatalf("api list JSON operation count = %#v", got)
	}
}
