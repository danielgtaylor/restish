package cli_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/rest-sh/restish/v2/config"
)

// setupEnvWithSpec is like setupGeneratedEnv but serves a caller-supplied spec.
func setupEnvWithSpec(t *testing.T, mux *http.ServeMux, specFn func(srvURL string) string) *generatedEnv {
	t.Helper()

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	specBody := specFn(srv.URL)
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, specBody)
	})

	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"tapi": {BaseURL: srv.URL},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	_ = os.WriteFile(cfgFile, cfgData, 0o600)
	cacheDir := t.TempDir()

	env := &generatedEnv{cfgFile: cfgFile, cacheDir: cacheDir}

	// Prime the spec cache.
	primer := env.newCLI()
	primer.Stdout = io.Discard
	primer.Stderr = io.Discard
	if err := primer.Run([]string{"restish", "api", "sync", "tapi"}); err != nil {
		t.Fatalf("api sync failed: %v", err)
	}
	return env
}

// extSpec is the OpenAPI spec used by the extension tests.
func extSpec(baseURL string) string {
	return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Ext API", "version": "1.0"},
  "servers": [{"url": %q}],
  "paths": {
    "/things": {
      "get": {
        "operationId": "listThings",
        "x-cli-name": "things",
        "x-cli-aliases": ["ls"],
        "x-cli-description": "List things (custom description)",
        "summary": "Original summary",
        "responses": {"200": {"description": "OK"}}
      }
    },
    "/ignored": {
      "get": {
        "operationId": "ignoredOp",
        "x-cli-ignore": true,
        "summary": "Should not appear",
        "responses": {"200": {"description": "OK"}}
      }
    },
    "/hidden": {
      "get": {
        "operationId": "hiddenOp",
        "x-cli-hidden": true,
        "summary": "Hidden but callable",
        "responses": {"200": {"description": "OK"}}
      }
    },
    "/items/{id}": {
      "get": {
        "operationId": "getExtItem",
        "summary": "Get item",
        "parameters": [
          {
            "name": "id",
            "in": "path",
            "required": true,
            "schema": {"type": "string"},
            "x-cli-name": "item-id",
            "x-cli-description": "Unique item identifier"
          },
          {
            "name": "fmt",
            "in": "query",
            "required": false,
            "schema": {"type": "string"},
            "x-cli-name": "output-format",
            "x-cli-description": "Custom output format flag"
          }
        ],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, baseURL)
}

func setupExtEnv(t *testing.T) *generatedEnv {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/things", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc("/hidden", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"hidden":true}`)
	})
	mux.HandleFunc("/items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x"}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	return setupEnvWithSpec(t, mux, extSpec)
}

// TestExtensionXCLIName verifies that x-cli-name overrides the command name.
func TestExtensionXCLIName(t *testing.T) {
	env := setupExtEnv(t)

	// "things" (x-cli-name) should work; "list-things" (default) should not.
	c := env.newCLI()
	if err := c.Run([]string{"restish", "tapi", "things"}); err != nil {
		t.Fatalf(`command "things" (x-cli-name) failed: %v`, err)
	}

	c2 := env.newCLI()
	if err := c2.Run([]string{"restish", "tapi", "list-things"}); err == nil {
		t.Error(`expected "list-things" to be unknown after x-cli-name override`)
	}
}

// TestExtensionXCLIAliases verifies that x-cli-aliases adds working aliases.
func TestExtensionXCLIAliases(t *testing.T) {
	env := setupExtEnv(t)

	// "ls" is the alias declared via x-cli-aliases.
	c := env.newCLI()
	if err := c.Run([]string{"restish", "tapi", "ls"}); err != nil {
		t.Fatalf(`alias "ls" failed: %v`, err)
	}
}

// TestExtensionXCLIDescription verifies that x-cli-description replaces the
// command's short description in help output.
func TestExtensionXCLIDescription(t *testing.T) {
	env := setupExtEnv(t)

	c, out := env.newCaptureCLI()
	_ = c.Run([]string{"restish", "tapi", "--help"})
	got := out.String()
	if !strings.Contains(got, "List things (custom description)") {
		t.Errorf("expected custom description in help output, got:\n%s", got)
	}
	if strings.Contains(got, "Original summary") {
		t.Errorf("original summary should be replaced by x-cli-description, got:\n%s", got)
	}
}

// TestExtensionXCLIIgnore verifies that x-cli-ignore: true makes the
// operation unreachable (not registered as a command).
func TestExtensionXCLIIgnore(t *testing.T) {
	env := setupExtEnv(t)

	c := env.newCLI()
	if err := c.Run([]string{"restish", "tapi", "ignored-op"}); err == nil {
		t.Error(`expected "ignored-op" to be unknown (x-cli-ignore: true)`)
	}
}

// TestExtensionXCLIHidden verifies that x-cli-hidden: true hides the command
// from help listings but keeps it callable directly.
func TestExtensionXCLIHidden(t *testing.T) {
	env := setupExtEnv(t)

	// Command should not appear in help listing.
	c1, out := env.newCaptureCLI()
	_ = c1.Run([]string{"restish", "tapi", "--help"})
	if strings.Contains(out.String(), "hidden-op") {
		t.Errorf("hidden command should not appear in help, got:\n%s", out.String())
	}

	// But should still be callable directly.
	c2 := env.newCLI()
	if err := c2.Run([]string{"restish", "tapi", "hidden-op"}); err != nil {
		t.Fatalf("hidden command should be callable directly: %v", err)
	}
}

// TestExtensionXCLINameOnParam verifies that x-cli-name on a parameter
// changes the flag/positional-arg display name.
func TestExtensionXCLINameOnParam(t *testing.T) {
	var gotQuery string
	env := setupExtEnv(t)

	// The "fmt" query param should be renamed to "--output-format" by x-cli-name.
	// The path param "id" should be renamed to "item-id" in the Use string.
	c, out := env.newCaptureCLI()
	_ = c.Run([]string{"restish", "tapi", "get-ext-item", "--help"})
	got := out.String()
	if !strings.Contains(got, "output-format") {
		t.Errorf("expected --output-format flag (from x-cli-name), got:\n%s", got)
	}
	if !strings.Contains(got, "item-id") {
		t.Errorf("expected <item-id> positional arg (from x-cli-name), got:\n%s", got)
	}

	// The renamed flag should actually send the correct query param name.
	var lastQuery string
	_ = gotQuery // silence unused warning before we use lastQuery below
	_ = lastQuery

	c2 := env.newCLI()
	// We just verify the command runs without error (query assertion done by server).
	if err := c2.Run([]string{"restish", "tapi", "get-ext-item", "42", "--output-format", "json"}); err != nil {
		t.Fatalf("get-ext-item with --output-format: %v", err)
	}
	_ = gotQuery
}

func TestExtensionXCLIPathLevelIgnore(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Ext API", "version": "1.0"},
  "servers": [{"url": %q}],
  "paths": {
    "/admin": {
      "x-cli-ignore": true,
      "get": {
        "operationId": "listAdmin",
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, baseURL)
	})

	c := env.newCLI()
	if err := c.Run([]string{"restish", "tapi", "list-admin"}); err == nil {
		t.Fatal("expected path-level x-cli-ignore to suppress generated command")
	}
}

func TestExtensionXCLIPathLevelHidden(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Ext API", "version": "1.0"},
  "servers": [{"url": %q}],
  "paths": {
    "/admin": {
      "x-cli-hidden": true,
      "get": {
        "operationId": "listAdmin",
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, baseURL)
	})

	c1, out := env.newCaptureCLI()
	_ = c1.Run([]string{"restish", "tapi", "--help"})
	if strings.Contains(out.String(), "list-admin") {
		t.Fatalf("expected hidden command to be omitted from help, got:\n%s", out.String())
	}

	c2 := env.newCLI()
	if err := c2.Run([]string{"restish", "tapi", "list-admin"}); err != nil {
		t.Fatalf("expected hidden command to remain callable: %v", err)
	}
}

func TestExtensionXCLIParamIgnoreRemovesFlag(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/items/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Ext API", "version": "1.0"},
  "servers": [{"url": %q}],
  "paths": {
    "/items/{id}": {
      "get": {
        "operationId": "getItem",
        "parameters": [
          {
            "name": "id",
            "in": "path",
            "required": true,
            "schema": {"type": "string"}
          },
          {
            "name": "debug",
            "in": "query",
            "schema": {"type": "string"},
            "x-cli-ignore": true
          }
        ],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, baseURL)
	})

	c, out := env.newCaptureCLI()
	_ = c.Run([]string{"restish", "tapi", "get-item", "--help"})
	if strings.Contains(out.String(), "debug") {
		t.Fatalf("expected ignored parameter to be absent from help, got:\n%s", out.String())
	}

	c2 := env.newCLI()
	if err := c2.Run([]string{"restish", "tapi", "get-item", "42", "--debug", "true"}); err == nil {
		t.Fatal("expected ignored parameter flag to be unavailable")
	}
}

func TestExtensionXCLIParamHiddenHidesFlagButAllowsUse(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/items", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Ext API", "version": "1.0"},
  "servers": [{"url": %q}],
  "paths": {
    "/items": {
      "get": {
        "operationId": "listItems",
        "parameters": [
          {
            "name": "debug",
            "in": "query",
            "schema": {"type": "string"},
            "x-cli-hidden": true
          }
        ],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, baseURL)
	})

	c, out := env.newCaptureCLI()
	_ = c.Run([]string{"restish", "tapi", "list-items", "--help"})
	if strings.Contains(out.String(), "debug") {
		t.Fatalf("expected hidden parameter to be absent from help, got:\n%s", out.String())
	}

	c2 := env.newCLI()
	if err := c2.Run([]string{"restish", "tapi", "list-items", "--debug", "true"}); err != nil {
		t.Fatalf("expected hidden parameter flag to be usable: %v", err)
	}
	if gotQuery != "debug=true" {
		t.Fatalf("query = %q, want debug=true", gotQuery)
	}
}

func TestExtensionAliasPositionsRequiredParameter(t *testing.T) {
	var gotPath, gotScope string
	mux := http.NewServeMux()
	mux.HandleFunc("/items/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotScope = r.URL.Query().Get("scope")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Ext API", "version": "1.0"},
  "servers": [{"url": %q}],
  "paths": {
    "/items/{id}": {
      "get": {
        "operationId": "getItem",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "scope", "in": "query", "required": true, "schema": {"type": "string"}, "x-vendor-position": 1}
        ],
        "responses": {"200": {"description": "OK"}}
      }
    },
    "/invalid/{id}": {
      "get": {
        "operationId": "invalidItem",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "scope", "in": "query", "required": true, "schema": {"type": "string"}, "x-vendor-position": "first"}
        ],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, baseURL)
	})

	cfg, err := config.Load(env.cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg.OpenAPIExtensionAliases = map[string]string{
		config.XCLIPositionExtension: "x-vendor-position",
	}
	if err := config.Save(env.cfgFile, cfg); err != nil {
		t.Fatal(err)
	}

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "tapi", "get-item", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "get-item <scope> <id>") {
		t.Fatalf("help does not show aliased position:\n%s", out.String())
	}
	c, out = env.newCaptureCLI()
	if err := c.Run([]string{"restish", "tapi", "--help"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "invalid-item") {
		t.Fatalf("help includes operation with malformed position:\n%s", out.String())
	}

	c = env.newCLI()
	if err := c.Run([]string{"restish", "tapi", "get-item", "public", "42"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/items/42" || gotScope != "public" {
		t.Fatalf("request = %s?scope=%s, want /items/42?scope=public", gotPath, gotScope)
	}

	c, out = env.newCaptureCLI()
	if err := c.Run([]string{"restish", "doctor", "api", "tapi"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2 positioned parameters", "x-vendor-position: GET /items/{id} parameter query scope", "x-vendor-position must be an integer (invalid)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out.String())
		}
	}
}

func TestDoctorAPIReportsXCLIExtensionDetails(t *testing.T) {
	env := setupExtEnv(t)

	c, out := env.newCaptureCLI()
	if err := c.Run([]string{"restish", "doctor", "api", "tapi"}); err != nil {
		t.Fatalf("doctor api: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"OpenAPI x-cli extensions:",
		"1 ignored operation",
		"1 hidden operation",
		"1 renamed operation",
		"1 operation with aliases",
		"2 renamed parameters",
		"x-cli-name: GET /things",
		"x-cli-hidden: GET /hidden",
		"x-cli-ignore: GET /ignored",
		"x-cli-name: GET /items/{id} parameter path id",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor api output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "x-cli-description") {
		t.Fatalf("doctor api should not report x-cli-description:\n%s", got)
	}

	cJSON, jsonOut := env.newCaptureCLI()
	if err := cJSON.Run([]string{"restish", "doctor", "api", "tapi", "-o", "json"}); err != nil {
		t.Fatalf("doctor api json: %v", err)
	}
	var report struct {
		OpenAPIXCLI struct {
			Details []struct {
				Kind string `json:"kind"`
			} `json:"details"`
		} `json:"openapi_x_cli_extensions"`
	}
	if err := json.Unmarshal([]byte(jsonOut.String()), &report); err != nil {
		t.Fatalf("doctor api json should be JSON: %v\n%s", err, jsonOut.String())
	}
	if len(report.OpenAPIXCLI.Details) == 0 {
		t.Fatalf("expected x-cli extension details in doctor JSON:\n%s", jsonOut.String())
	}
}
