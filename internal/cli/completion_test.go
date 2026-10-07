package cli_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rest-sh/restish/v2/config"
)

// enumSpec is an OpenAPI spec with a parameter that has enum values.
func enumSpec(baseURL string) string {
	return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Enum API", "version": "1.0"},
  "servers": [{"url": %q}],
  "paths": {
    "/items": {
      "get": {
        "operationId": "listItems",
        "summary": "List items",
        "parameters": [
          {
            "name": "status",
            "in": "query",
            "required": false,
            "schema": {
              "type": "string",
              "enum": ["active", "inactive", "pending"]
            },
            "description": "Filter by status"
          }
        ],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, baseURL)
}

func dynamicCompletionSpec(baseURL string) string {
	return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Completion API", "version": "1"},
  "servers": [{"url": %q}],
  "paths": {
    "/accounts": {"get": {"operationId": "listAccounts", "responses": {"200": {"description": "OK"}}}},
    "/accounts/{account}/projects": {"get": {
      "operationId": "listProjects",
      "parameters": [{"name": "account", "in": "path", "required": true, "schema": {"type": "string"}}],
      "responses": {"200": {"description": "OK"}}
    }},
	"/failures": {"get": {"operationId": "listFailures", "responses": {"200": {"description": "OK"}}}},
	"/redirects": {"get": {"operationId": "listRedirects", "responses": {"200": {"description": "OK"}}}},
    "/accounts/{account}/servers": {"get": {
      "operationId": "listServers",
      "parameters": [
        {"name": "account", "in": "path", "required": true, "schema": {"type": "string"},
         "x-cli-completion": {"operation_id": "listAccounts", "value_path": "body.items.id", "description_path": "body.items.name"}},
		{"name": "project", "in": "query", "schema": {"type": "string"},
		 "x-cli-completion": {"operation_id": "listProjects", "bindings": {"path.account": "path.account"}, "value_path": "body.items.id", "description_path": "body.items.name"}},
		{"name": "mode", "in": "query", "schema": {"type": "string", "enum": ["active"]},
		 "x-cli-completion": {"operation_id": "listAccounts", "value_path": "body.items.id"}},
		{"name": "failure", "in": "query", "schema": {"type": "string"},
		 "x-cli-completion": {"operation_id": "listFailures", "value_path": "body.items.id"}},
		{"name": "redirect", "in": "query", "schema": {"type": "string"},
		 "x-cli-completion": {"operation_id": "listRedirects", "value_path": "body.items.id"}}
      ],
      "responses": {"200": {"description": "OK"}}
    }}
  }
}`, baseURL)
}

func TestDynamicParameterCompletionAfterAPISync(t *testing.T) {
	var requests atomic.Int32
	var failures atomic.Int32
	var redirects atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Completion-Test") != "yes" {
			t.Errorf("missing profile header")
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[{"id":"acct-1","name":"Primary Account"}]}`)
	})
	mux.HandleFunc("/accounts/acct-1/projects", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", "</next>; rel=next")
		fmt.Fprint(w, `{"items":[{"id":"prod","name":"Production\tWest"},{"id":"preview","name":"Preview"},{"id":"bad\n:0","name":"Bad"}]}`)
	})
	mux.HandleFunc("/failures", func(w http.ResponseWriter, r *http.Request) {
		failures.Add(1)
		http.Error(w, "try later", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/redirects", func(w http.ResponseWriter, r *http.Request) {
		redirects.Add(1)
		http.Redirect(w, r, "/accounts", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	env := setupEnvWithSpec(t, mux, dynamicCompletionSpec)
	requests.Store(0)
	baseURL := env.baseURL(t)
	env.writeAPIConfig(t, &config.APIConfig{BaseURL: baseURL, Profiles: map[string]*config.ProfileConfig{
		"default": {Headers: []string{"X-Completion-Test: yes"}},
	}})

	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"restish", "__complete", "tapi", "list-servers", "ac"}, "acct-1\tPrimary Account"},
		{[]string{"restish", "__complete", "tapi", "list-servers", "acct-1", "--project", "pr"}, "prod\tProduction West"},
		{[]string{"restish", "__complete", "tapi", "list-servers", "acct-1", "--mode", "a"}, "active"},
	} {
		cli, out := env.newCaptureCLI()
		if err := cli.Run(test.args); err != nil {
			t.Fatalf("completion: %v", err)
		}
		if got := out.String(); !strings.Contains(got, test.want) || strings.Contains(got, "bad\n:0") {
			t.Fatalf("completion output = %q, requests = %d", got, requests.Load())
		}
	}
	cli, out := env.newCaptureCLI()
	if err := cli.Run([]string{"restish", "__complete", "tapi", "list-servers", "acct-1", "--project", "pr"}); err != nil {
		t.Fatalf("cached completion: %v", err)
	}
	if !strings.Contains(out.String(), "prod\tProduction West") {
		t.Fatalf("cached completion output = %q", out.String())
	}
	cli, out = env.newCaptureCLI()
	if err := cli.Run([]string{"restish", "__complete", "tapi", "list-servers", "acct-1", "--failure", ""}); err != nil {
		t.Fatalf("failed completion: %v", err)
	}
	if failures.Load() != 1 || strings.Contains(out.String(), "try later") {
		t.Fatalf("failed completion output = %q, requests = %d", out.String(), failures.Load())
	}
	cli, out = env.newCaptureCLI()
	if err := cli.Run([]string{"restish", "__complete", "tapi", "list-servers", "acct-1", "--redirect", ""}); err != nil {
		t.Fatalf("redirect completion: %v", err)
	}
	if redirects.Load() != 1 || strings.Contains(out.String(), "acct-1") {
		t.Fatalf("redirect completion output = %q, requests = %d", out.String(), redirects.Load())
	}
	if requests.Load() != 2 {
		t.Fatalf("provider requests = %d", requests.Load())
	}
}

// TestEnumFlagCompletion verifies that OpenAPI enum values are registered as
// completion candidates for the corresponding flag.
func TestEnumFlagCompletion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/items", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	env := setupEnvWithSpec(t, mux, enumSpec)

	// Cobra registers a hidden __complete command that we can invoke to test
	// completion. Run: restish __complete tapi list-items --status ""
	c, out := env.newCaptureCLI()
	err := c.Run([]string{"restish", "__complete", "tapi", "list-items", "--status", ""})
	// __complete always exits 0 (success) even when it returns no results.
	if err != nil {
		t.Fatalf("__complete: %v", err)
	}

	got := out.String()
	for _, want := range []string{"active", "inactive", "pending"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected completion candidate %q, got:\n%s", want, got)
		}
	}
}

func TestGeneratedSecurityFlagCompletion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/reports", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	env := setupEnvWithSpec(t, mux, func(baseURL string) string {
		return fmt.Sprintf(`{
  "openapi": "3.1.0",
  "info": {"title": "Security API", "version": "1.0"},
  "servers": [{"url": %q}],
  "components": {
    "securitySchemes": {
      "UserOAuth": {"type": "oauth2", "flows": {"authorizationCode": {"authorizationUrl": "https://auth.example.com/authorize", "tokenUrl": "https://auth.example.com/token", "scopes": {"read": "Read"}}}},
      "PartnerKey": {"type": "apiKey", "in": "header", "name": "X-Partner-Key"}
    }
  },
  "paths": {
    "/reports": {
      "get": {
        "operationId": "getReports",
        "security": [
          {"UserOAuth": ["read"]},
          {"PartnerKey": []},
          {}
        ],
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`, baseURL)
	})

	c, out := env.newCaptureCLI()
	err := c.Run([]string{"restish", "__complete", "tapi", "get-reports", "--rsh-auth", ""})
	if err != nil {
		t.Fatalf("__complete: %v", err)
	}

	got := out.String()
	for _, want := range []string{"UserOAuth", "PartnerKey", "anonymous"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected completion candidate %q, got:\n%s", want, got)
		}
	}
}
