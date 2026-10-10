package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rest-sh/restish/v2/config"
)

type operationBrowseRow struct {
	Command     string `json:"command"`
	Summary     string `json:"summary"`
	OperationID string `json:"operation_id"`
}

func TestGenericHTTPBrowsesOperationCommandsBelowAPIPath(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})
	useTransport(c, func(*http.Request) (*http.Response, error) {
		t.Fatal("operation browsing must not send an HTTP request")
		return nil, nil
	})

	if err := c.Run([]string{"restish", "get", "demo/items/my-item/"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var rows []operationBrowseRow
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	want := []operationBrowseRow{
		{
			Command:     "restish get demo/items/my-item/hidden",
			Summary:     "Hidden item",
			OperationID: "hiddenItem",
		},
		{
			Command:     "restish get demo/items/my-item/tags",
			Summary:     "List item tags",
			OperationID: "listItemTags",
		},
		{
			Command:     "restish get demo/items/my-item/tags/{tag-id}",
			Summary:     "Get tag details",
			OperationID: "getTagDetails",
		},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("rows[%d] = %#v, want %#v", i, rows[i], want[i])
		}
	}
}

func TestGenericHTTPBrowseFiltersByMethod(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})
	useTransport(c, func(*http.Request) (*http.Response, error) {
		t.Fatal("operation browsing must not send an HTTP request")
		return nil, nil
	})

	if err := c.Run([]string{"restish", "post", "demo/"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var rows []operationBrowseRow
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	want := operationBrowseRow{
		Command:     "restish post demo/widgets",
		Summary:     "Create widget",
		OperationID: "createWidget",
	}
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("rows = %#v, want %#v", rows, []operationBrowseRow{want})
	}
}

func TestGenericHTTPBrowseUsesProfileOperationBase(t *testing.T) {
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
	useTransport(c, func(*http.Request) (*http.Response, error) {
		t.Fatal("operation browsing must not send an HTTP request")
		return nil, nil
	})

	if err := c.Run([]string{"restish", "get", "--rsh-profile", "staging", "demo/"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var rows []operationBrowseRow
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	found := false
	for _, row := range rows {
		if row.Command == "restish get demo/../v2/users" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rows = %#v, want profile operation_base command", rows)
	}
}

func TestGenericHTTPBrowsePreservesMissingProfileError(t *testing.T) {
	c, _ := newCompletionFixtureCLI(t, completionFixtureConfig{
		Profiles: map[string]*config.ProfileConfig{"staging": {}},
	})
	useTransport(c, func(*http.Request) (*http.Response, error) {
		t.Fatal("missing profile must fail before sending an HTTP request")
		return nil, nil
	})

	err := c.Run([]string{"restish", "get", "--rsh-profile", "missing", "demo/"})
	if err == nil || !strings.Contains(err.Error(), `profile "missing" not found for API "demo"`) {
		t.Fatalf("error = %v, want missing profile error", err)
	}
}

func TestGenericHTTPBrowseUsesTableOnTerminal(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})
	c.Hooks().StdoutIsTerminal = func(io.Writer) bool { return true }
	useTransport(c, func(*http.Request) (*http.Response, error) {
		t.Fatal("operation browsing must not send an HTTP request")
		return nil, nil
	})

	if err := c.Run([]string{"restish", "get", "demo/items/my-item/"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		"COMMAND",
		"SUMMARY",
		"OPERATION ID",
		"restish get demo/items/my-item/hidden",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %s, want %q", out.String(), want)
		}
	}
}

func TestGenericHTTPBrowseSupportsExplicitTableOutput(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})
	useTransport(c, func(*http.Request) (*http.Response, error) {
		t.Fatal("operation browsing must not send an HTTP request")
		return nil, nil
	})

	if err := c.Run([]string{"restish", "get", "-o", "table", "demo/items/my-item/"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"┌", "command", "summary", "operation_id"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output = %s, want %q", out.String(), want)
		}
	}
}

func TestGenericHTTPExactOperationStillSendsRequest(t *testing.T) {
	c, _ := newCompletionFixtureCLI(t, completionFixtureConfig{})
	var got *http.Request
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	if err := c.Run([]string{"restish", "get", "demo/users"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got == nil {
		t.Fatal("expected an HTTP request")
	}
	if got.Method != http.MethodGet || got.URL.Path != "/users" {
		t.Fatalf("request = %s %s, want GET /users", got.Method, got.URL.Path)
	}
}

func TestGenericHTTPTrailingSlashBrowsesBelowExactOperation(t *testing.T) {
	c, out := newCompletionFixtureCLI(t, completionFixtureConfig{})
	useTransport(c, func(*http.Request) (*http.Response, error) {
		t.Fatal("trailing-slash parent browsing must not send an HTTP request")
		return nil, nil
	})

	if err := c.Run([]string{"restish", "get", "demo/users/"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var rows []operationBrowseRow
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	want := operationBrowseRow{
		Command:     "restish get demo/users/{user-id}",
		Summary:     "Get user",
		OperationID: "getUser",
	}
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("rows = %#v, want %#v", rows, []operationBrowseRow{want})
	}
}

func TestGenericHTTPExactTemplateOperationStillSendsRequest(t *testing.T) {
	c, _ := newCompletionFixtureCLI(t, completionFixtureConfig{})
	var got *http.Request
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	if err := c.Run([]string{"restish", "get", "demo/formats/json"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got == nil || got.URL.Path != "/formats/json" {
		t.Fatalf("request URL = %v, want /formats/json", got)
	}
}

func TestGenericHTTPBodyStillSendsParentRequest(t *testing.T) {
	c, _ := newCompletionFixtureCLI(t, completionFixtureConfig{})
	var got *http.Request
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	if err := c.Run([]string{"restish", "post", "demo/", "name:", "Ada"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got == nil {
		t.Fatal("expected an HTTP request")
	}
	if got.Method != http.MethodPost || got.URL.Path != "/" {
		t.Fatalf("request = %s %s, want POST /", got.Method, got.URL.Path)
	}
}

func TestGenericHTTPStdinBodyStillSendsParentRequest(t *testing.T) {
	c, _ := newCompletionFixtureCLI(t, completionFixtureConfig{})
	c.Stdin = strings.NewReader(`{"name":"Ada"}`)
	var got *http.Request
	var body string
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		got = r
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		body = string(data)
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	if err := c.Run([]string{"restish", "post", "-c", "json", "demo/"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got == nil {
		t.Fatal("expected an HTTP request")
	}
	if got.Method != http.MethodPost || body != `{"name":"Ada"}` {
		t.Fatalf("request = %s body %q, want POST with stdin body", got.Method, body)
	}
}

func TestGenericHTTPQueryStillSendsParentRequest(t *testing.T) {
	c, _ := newCompletionFixtureCLI(t, completionFixtureConfig{})
	var got *http.Request
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	if err := c.Run([]string{"restish", "get", "demo/?limit=1"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got == nil {
		t.Fatal("expected an HTTP request")
	}
	if got.URL.Path != "/" || got.URL.RawQuery != "limit=1" {
		t.Fatalf("URL = %s, want /?limit=1", got.URL)
	}
}

func TestGenericHTTPUnknownParentStillSendsRequest(t *testing.T) {
	c, _ := newCompletionFixtureCLI(t, completionFixtureConfig{})
	var got *http.Request
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	if err := c.Run([]string{"restish", "get", "demo/unknown"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got == nil || !strings.HasSuffix(got.URL.Path, "/unknown") {
		t.Fatalf("request URL = %v, want path ending in /unknown", got)
	}
}

func TestGenericHTTPFullURLParentStillSendsRequest(t *testing.T) {
	c, _ := newCompletionFixtureCLI(t, completionFixtureConfig{})
	var got *http.Request
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		got = r
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	if err := c.Run([]string{"restish", "get", "https://api.example.com/items/"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got == nil || got.URL.Path != "/items/" {
		t.Fatalf("request URL = %v, want /items/", got)
	}
}
