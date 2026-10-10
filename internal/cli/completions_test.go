package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/cli"
)

// runCompletion invokes cobra's __complete subcommand and returns stdout.
func runCompletion(t *testing.T, args ...string) string {
	t.Helper()
	c, out, _ := newTestCLI(t)
	return runCompletionForCLI(t, c, out, args...)
}

func runCompletionForCLI(t *testing.T, c *cli.CLI, out interface {
	String() string
}, args ...string) string {
	t.Helper()
	full := append([]string{"restish", "__complete"}, args...)
	// Errors from __complete are expected when no match; ignore them.
	_ = c.Run(full)
	return out.String()
}

// TestOutputFormatCompletions verifies that -o / --rsh-output-format lists
// the registered formatter names.
func TestOutputFormatCompletions(t *testing.T) {
	got := runCompletion(t, "get", "--rsh-output-format", "")
	for _, want := range []string{"auto", "json", "lines", "yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in -o completions, got:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"raw", "readable"} {
		if strings.Contains(got, notWant) {
			t.Errorf("did not expect %s in -o completions, got:\n%s", notWant, got)
		}
	}
}

// TestProfileCompletions verifies that -p / --rsh-profile always returns at
// least "default".
func TestProfileCompletions(t *testing.T) {
	got := runCompletion(t, "get", "--rsh-profile", "")
	if !strings.Contains(got, "default") {
		t.Errorf("expected 'default' in -p completions, got:\n%s", got)
	}
}

// TestContentTypeCompletions verifies that -c / --rsh-content-type returns
// names of the registered content types.
func TestContentTypeCompletions(t *testing.T) {
	got := runCompletion(t, "get", "--rsh-content-type", "")
	for _, want := range []string{"json", "yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in -c completions, got:\n%s", want, got)
		}
	}
}

// TestFilterLangCompletions verifies that --rsh-filter-lang returns exactly
// "shorthand" and "jq".
func TestFilterLangCompletions(t *testing.T) {
	got := runCompletion(t, "get", "--rsh-filter-lang", "")
	for _, want := range []string{"shorthand", "jq"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in --rsh-filter-lang completions, got:\n%s", want, got)
		}
	}
}

func TestTLSMinVersionCompletions(t *testing.T) {
	got := runCompletion(t, "get", "--rsh-tls-min-version", "")
	for _, want := range []string{"TLS1.2", "TLS1.3"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in --rsh-tls-min-version completions, got:\n%s", want, got)
		}
	}
}

func TestURLCompletionsForBareGet(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})

	got := runCompletionForCLI(t, c, out, "demo/items/my-item")
	for _, want := range []string{
		"demo/items/my-item/tags\tList item tags",
		"demo/items/my-item/tags/\tGet tag details",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in bare URL completions, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "{tag-id}") {
		t.Fatalf("path parameter placeholders should not be completed:\n%s", got)
	}
	if strings.Contains(got, "demo/items/my-item/hidden") {
		t.Fatalf("hidden operations should not be completed:\n%s", got)
	}
}

func TestURLCompletionsForHTTPVerbsFilterByMethod(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})

	got := runCompletionForCLI(t, c, out, "get", "demo/items/my-item")
	if !strings.Contains(got, "demo/items/my-item/tags\tList item tags") {
		t.Fatalf("expected GET operation completion, got:\n%s", got)
	}

	c, out = newCompletionFixtureCLI(t, completionFixtureConfig{})
	got = runCompletionForCLI(t, c, out, "delete", "demo/items/my-item/tags/tag-1")
	if !strings.Contains(got, "demo/items/my-item/tags/tag-1\tDelete tag") {
		t.Fatalf("expected DELETE operation completion, got:\n%s", got)
	}
	if strings.Contains(got, "Get tag details") {
		t.Fatalf("GET operation should not appear for DELETE completion:\n%s", got)
	}

	c, out = newCompletionFixtureCLI(t, completionFixtureConfig{})
	got = runCompletionForCLI(t, c, out, "post", "demo/wid")
	if !strings.Contains(got, "demo/widgets\tCreate widget") {
		t.Fatalf("expected POST operation completion, got:\n%s", got)
	}
}

func TestURLCompletionsPreserveFullAndSchemeLessURLForms(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})

	got := runCompletionForCLI(t, c, out, "get", "https://api.example.com/items/my-item")
	if !strings.Contains(got, "https://api.example.com/items/my-item/tags\tList item tags") {
		t.Fatalf("expected full URL completion, got:\n%s", got)
	}

	c, out = newCompletionFixtureCLI(t, completionFixtureConfig{})
	got = runCompletionForCLI(t, c, out, "get", "api.example.com/items/my-item")
	if !strings.Contains(got, "api.example.com/items/my-item/tags\tList item tags") {
		t.Fatalf("expected scheme-less URL completion, got:\n%s", got)
	}
}

func TestURLCompletionsUseProfileBaseURLAndOperationBase(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{
		BaseURL:       "https://api.example.com/root",
		OperationBase: "/",
		Profiles: map[string]*config.ProfileConfig{
			"staging": {
				BaseURL:       "https://staging.example.com/root",
				OperationBase: "/v2",
			},
		},
	})

	got := runCompletionForCLI(t, c, out, "get", "--rsh-profile", "staging", "https://staging.example.com/v2/ite")
	if !strings.Contains(got, "https://staging.example.com/v2/items/\tList item tags") {
		t.Fatalf("expected profile/operation_base full URL completion, got:\n%s", got)
	}
}

func TestURLCompletionsStopBeforePathParamsAndCompleteEnums(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})

	got := runCompletionForCLI(t, c, out, "demo/for")
	if !strings.Contains(got, "demo/formats/\tGet format") {
		t.Fatalf("expected completion to stop before enum path param, got:\n%s", got)
	}
	if strings.Contains(got, "{format}") {
		t.Fatalf("path parameter placeholder should not be completed:\n%s", got)
	}

	c, out = newCompletionFixtureCLI(t, completionFixtureConfig{})
	got = runCompletionForCLI(t, c, out, "demo/formats/")
	for _, want := range []string{
		"demo/formats/cbor\tGet format",
		"demo/formats/json\tGet format",
		"demo/formats/yaml\tGet format",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected enum completion %q, got:\n%s", want, got)
		}
	}

	c, out = newCompletionFixtureCLI(t, completionFixtureConfig{})
	got = runCompletionForCLI(t, c, out, "demo/formats/j")
	if !strings.Contains(got, "demo/formats/json\tGet format") {
		t.Fatalf("expected filtered enum completion, got:\n%s", got)
	}
	if strings.Contains(got, "demo/formats/yaml") {
		t.Fatalf("unexpected non-matching enum completion:\n%s", got)
	}
}

func TestURLCompletionsSuggestAPIPathNamespaceForVerbs(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})

	got := runCompletionForCLI(t, c, out, "get", "de")
	if !strings.Contains(got, "demo/\tAPI URL paths") {
		t.Fatalf("expected API URL namespace seed, got:\n%s", got)
	}
}

type completionFixtureConfig struct {
	BaseURL       string
	OperationBase string
	Profiles      map[string]*config.ProfileConfig
}

func newCompletionFixtureCLI(t *testing.T, opts completionFixtureConfig) (*cli.CLI, interface {
	String() string
}) {
	t.Helper()
	specPath := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(specPath, []byte(completionFixtureSpec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "https://api.example.com"
	}
	cfgData, err := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"demo": {
				BaseURL:       baseURL,
				OperationBase: opts.OperationBase,
				SpecFiles:     []string{specPath},
				Profiles:      opts.Profiles,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	c, out, _ := newTestCLI(t)
	if err := os.WriteFile(c.Hooks().ConfigPath, cfgData, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return c, out
}

const completionFixtureSpec = `openapi: "3.1.0"
info:
  title: Completion fixture
  version: "1.0.0"
paths:
  /users:
    get:
      operationId: listUsers
      summary: List users
      responses:
        "200":
          description: OK
  /users/{user-id}:
    get:
      operationId: getUser
      summary: Get user
      parameters:
        - name: user-id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
  /items/{item-id}/tags:
    get:
      operationId: listItemTags
      summary: List item tags
      parameters:
        - name: item-id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
  /items/{item-id}/tags/{tag-id}:
    get:
      operationId: getTagDetails
      summary: Get tag details
      parameters:
        - name: item-id
          in: path
          required: true
          schema:
            type: string
        - name: tag-id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
    delete:
      operationId: deleteTag
      summary: Delete tag
      parameters:
        - name: item-id
          in: path
          required: true
          schema:
            type: string
        - name: tag-id
          in: path
          required: true
          schema:
            type: string
      responses:
        "204":
          description: Deleted
  /items/{item-id}/hidden:
    get:
      operationId: hiddenItem
      summary: Hidden item
      x-cli-hidden: true
      parameters:
        - name: item-id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
  /widgets:
    post:
      operationId: createWidget
      summary: Create widget
      responses:
        "201":
          description: Created
  /formats/{format}:
    get:
      operationId: getFormat
      summary: Get format
      parameters:
        - name: format
          in: path
          required: true
          schema:
            type: string
            enum: [json, cbor, yaml]
      responses:
        "200":
          description: OK
`
