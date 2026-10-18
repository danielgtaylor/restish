package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/cli"
	requestpkg "github.com/rest-sh/restish/v2/internal/request"
)

// requestRecorder is a small helper that captures the last HTTP request
// a test server received, safe for concurrent access.
type requestRecorder struct {
	mu   sync.Mutex
	last *http.Request
	body []byte
}

func (rr *requestRecorder) capture(r *http.Request) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	// Copy the fields we need; the original request is owned by the server.
	rr.last = &http.Request{
		Method: r.Method,
		URL:    r.URL,
		Host:   r.Host,
		Header: r.Header.Clone(),
	}
	if r.Body != nil {
		rr.body, _ = io.ReadAll(r.Body)
	}
}

func (rr *requestRecorder) Last() *http.Request {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.last
}

func useTransport(c *cli.CLI, fn roundTripperFunc) {
	c.Hooks().HTTPTransport = fn
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Proto:      "HTTP/1.1",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) {
	return f(p)
}

// TestHTTPVerbs verifies that each lowercase verb sends the correct HTTP method.
func TestHTTPVerbs(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var rr requestRecorder
			c, _, _ := newTestCLI(t)
			useTransport(c, func(r *http.Request) (*http.Response, error) {
				rr.capture(r)
				return jsonResponse(200, `{}`), nil
			})
			if err := c.Run([]string{"restish", strings.ToLower(method), "https://api.example.com/items"}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := rr.Last().Method; got != method {
				t.Errorf("expected method %q, got %q", method, got)
			}
		})
	}
}

// TestHTTPVerbUppercaseAlias verifies that the uppercase alias (e.g. GET) also works.
func TestHTTPVerbUppercaseAlias(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "GET", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().Method; got != "GET" {
		t.Errorf("expected GET, got %q", got)
	}
}

func TestHTTPPromptSecretBodyValue(t *testing.T) {
	const secret = `arbitrary: @value, "quoted"`
	const secondSecret = "second-secret"
	var rr requestRecorder
	var prompts []string
	c, out, errOut := newTestCLI(t)
	c.Hooks().SecretFunc = func(_ context.Context, label string) (string, error) {
		prompts = append(prompts, label)
		if strings.Contains(label, "backup") {
			return secondSecret, nil
		}
		return secret, nil
	}
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		if !requestpkg.IsMarkedCredentialBody(r) {
			t.Error("prompted request body was not marked as credential-bearing")
		}
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})

	err := c.Run([]string{"restish", "post", "-v", "--rsh-print", "B", "--rsh-prompt-secret", "credentials.codes[0]", "--rsh-prompt-secret", "credentials.backup", "https://api.example.com/items", "name: visible"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.body, &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	credentials, ok := body["credentials"].(map[string]any)
	codes, codesOK := credentials["codes"].([]any)
	if !ok || !codesOK || len(codes) != 1 || codes[0] != secret || credentials["backup"] != secondSecret || body["name"] != "visible" {
		t.Fatalf("request body = %#v, prompts = %q, stderr = %q", body, prompts, errOut.String())
	}
	wantPrompts := []string{"Secret for credentials.codes[0]: ", "Secret for credentials.backup: "}
	if !reflect.DeepEqual(prompts, wantPrompts) {
		t.Fatalf("prompts = %q, want %q", prompts, wantPrompts)
	}
	stderr := errOut.String()
	redacted := strings.Contains(stderr, `"<redacted>"`) || strings.Contains(stderr, `"\u003credacted\u003e"`)
	if strings.Contains(stderr, secret) || strings.Contains(stderr, secondSecret) || !redacted {
		t.Fatalf("verbose output did not redact prompted secret:\n%s", stderr)
	}
	stdout := out.String()
	printedRedacted := strings.Contains(stdout, `"<redacted>"`) || strings.Contains(stdout, `"\u003credacted\u003e"`)
	if strings.Contains(stdout, secret) || strings.Contains(stdout, secondSecret) || !printedRedacted {
		t.Fatalf("printed request body did not redact prompted secret:\n%s", stdout)
	}
}

func TestHTTPPromptSecretRedactsRequestError(t *testing.T) {
	const secret = "request\t\"error\\secret<"
	escapedSecret := strconv.Quote(secret)
	c, _, _ := newTestCLI(t)
	c.Hooks().SecretFunc = func(context.Context, string) (string, error) { return secret, nil }
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("request failed for " + escapedSecret)
	})

	err := c.Run([]string{"restish", "post", "--rsh-prompt-secret", "pin", "https://api.example.com/items"})
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), escapedSecret) || !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("request error was not redacted: %v", err)
	}
	for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(cause.Error(), secret) || strings.Contains(cause.Error(), escapedSecret) {
			t.Fatalf("unwrapped request error leaked prompted secret: %v", cause)
		}
	}
}

// TestBareURL verifies that a URL without an explicit verb and without a body
// is treated as GET.
func TestBareURL(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().Method; got != "GET" {
		t.Errorf("expected GET for bare URL, got %q", got)
	}
}

func TestMalformedBareTargetFailsBeforeTransport(t *testing.T) {
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("transport should not be called for malformed target: %s", r.URL)
		return nil, nil
	})
	err := c.Run([]string{"restish", "get", "://bad"})
	if err == nil {
		t.Fatal("expected malformed target error")
	}
	if !strings.Contains(err.Error(), `invalid URL "://bad"`) ||
		!strings.Contains(err.Error(), "bare port shorthand") {
		t.Fatalf("unexpected malformed target error: %v", err)
	}
}

func TestAuthEnvErrorIsNotReportedAsNetworkError(t *testing.T) {
	app := newTestApp(t)
	app.WriteConfigObject(&config.Config{APIs: map[string]*config.APIConfig{
		"svc": {
			BaseURL: "https://api.example.com",
			Profiles: map[string]*config.ProfileConfig{
				"default": {
					Auth: &config.AuthConfig{
						Type:   "bearer",
						Params: map[string]string{"token": "env:RESTISH_TEST_MISSING_TOKEN"},
					},
				},
			},
		},
	}})
	app.UseTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("transport should not be called when auth env is missing")
		return nil, nil
	})

	err := app.RunErr("svc/auth")
	if err == nil {
		t.Fatal("expected missing env auth error")
	}
	requireContains(t, err.Error(), `auth param "token"`, "RESTISH_TEST_MISSING_TOKEN")
	requireNotContains(t, err.Error(), "network error")
}

func TestRawBodyReadErrorIncludesRequestContextAndHint(t *testing.T) {
	app := newTestApp(t)
	app.UseResponse(&http.Response{
		StatusCode: 200,
		Proto:      "HTTP/1.1",
		Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
		Body: io.NopCloser(readerFunc(func(p []byte) (int, error) {
			return 0, io.ErrUnexpectedEOF
		})),
	})

	err := app.RunErr("get", "https://api.example.com/drop")
	if err == nil {
		t.Fatal("expected body read error")
	}
	requireContains(t, err.Error(),
		"reading response body for GET https://api.example.com/drop: unexpected EOF",
		"hint: response ended early")
}

func TestBareURLWithShorthandInfersPOST(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "https://api.example.com/items", "name:", "Alice"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := rr.Last()
	if got := req.Method; got != "POST" {
		t.Fatalf("method = %q, want POST", got)
	}
	if got := req.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.body, &body); err != nil {
		t.Fatalf("body is not valid JSON: %v — body: %s", err, rr.body)
	}
	if body["name"] != "Alice" {
		t.Fatalf("body[name] = %#v, want Alice", body["name"])
	}
}

func TestBareURLWithStdinInfersPOST(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	c.Stdin = strings.NewReader(`{"from":"stdin"}`)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := rr.Last().Method; got != "POST" {
		t.Fatalf("method = %q, want POST", got)
	}
	if !bytes.Contains(rr.body, []byte(`"from":"stdin"`)) {
		t.Fatalf("body = %s, want stdin JSON", rr.body)
	}
}

func TestExplicitGETWithShorthandKeepsGET(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "https://api.example.com/items", "q:", "widgets"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := rr.Last().Method; got != "GET" {
		t.Fatalf("method = %q, want GET", got)
	}
	if !bytes.Contains(rr.body, []byte(`"q":"widgets"`)) {
		t.Fatalf("body = %s, want shorthand JSON", rr.body)
	}
}

func TestAPIShortNameAcceptsQueryAndFragmentSuffix(t *testing.T) {
	tests := []struct {
		name string
		args []string
		path string
		raw  string
		frag string
	}{
		{name: "explicit query", args: []string{"restish", "get", "svc?limit=1"}, path: "/api", raw: "limit=1"},
		{name: "explicit fragment", args: []string{"restish", "get", "svc#frag"}, path: "/api", frag: "frag"},
		{name: "explicit path query", args: []string{"restish", "get", "svc/path?limit=1"}, path: "/api/path", raw: "limit=1"},
		{name: "bare query", args: []string{"restish", "svc?limit=1"}, path: "/api", raw: "limit=1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var rr requestRecorder
			c, _, _ := newTestCLI(t)
			if err := os.WriteFile(c.Hooks().ConfigPath, []byte(`{"apis":{"svc":{"base_url":"https://api.example.com/api"}}}`), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			useTransport(c, func(r *http.Request) (*http.Response, error) {
				rr.capture(r)
				return jsonResponse(200, `{}`), nil
			})
			if err := c.Run(tc.args); err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := rr.Last().URL
			if got.Path != tc.path || got.RawQuery != tc.raw || got.Fragment != tc.frag {
				t.Fatalf("URL = path:%q query:%q fragment:%q, want path:%q query:%q fragment:%q", got.Path, got.RawQuery, got.Fragment, tc.path, tc.raw, tc.frag)
			}
		})
	}
}

func TestAPIShortNameWithShorthandInfersPOST(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	if err := os.WriteFile(c.Hooks().ConfigPath, []byte(`{"apis":{"svc":{"base_url":"https://api.example.com/items"}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "svc", "name:", "Alice"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := rr.Last().Method; got != "POST" {
		t.Fatalf("method = %q, want POST", got)
	}
	if !bytes.Contains(rr.body, []byte(`"name":"Alice"`)) {
		t.Fatalf("body = %s, want shorthand JSON", rr.body)
	}
}

func TestHTTPResponseContentTypeIdentityDefaultsToRawRedirect(t *testing.T) {
	c, out, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"identity"}},
			Body:       io.NopCloser(strings.NewReader("plain response")),
			Request:    r,
		}, nil
	})
	if err := c.Run([]string{"restish", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "plain response" {
		t.Fatalf("output = %q, want plain response", got)
	}
}

func TestConfiguredAPIMissingProfileErrors(t *testing.T) {
	cfgData, _ := json.Marshal(&config.Config{
		APIs: map[string]*config.APIConfig{
			"testapi": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"default": {},
				},
			},
		},
	})
	cfgFile := t.TempDir() + "/restish.json"
	if err := os.WriteFile(cfgFile, cfgData, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgFile
	err := c.Run([]string{"restish", "get", "--rsh-profile", "missing", "testapi/items"})
	if err == nil {
		t.Fatal("expected missing configured profile to error")
	}
	if !strings.Contains(err.Error(), `profile "missing" not found for API "testapi"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestHTTPHeader verifies that -H adds the header to the request.
func TestHTTPHeader(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "-H", "X-Test: hello", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().Header.Get("X-Test"); got != "hello" {
		t.Errorf("expected X-Test=hello, got %q", got)
	}
}

func TestHTTPPreserveHeaderCaseForProfileAndFlagHeaders(t *testing.T) {
	cfg := `{
		"apis": {
			"myapi": {
				"base_url": "https://api.example.com",
				"preserve_header_case": true,
				"profiles": {
					"default": {
						"headers": ["X-SourceSystem: profile"]
					}
				}
			}
		}
	}`
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = writeAPIConfig(t, cfg)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})

	if err := c.Run([]string{"restish", "get", "-H", "X-RequestID: flag", "myapi/items"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	header := rr.Last().Header
	if got := header["X-SourceSystem"]; len(got) != 1 || got[0] != "profile" {
		t.Fatalf("X-SourceSystem = %#v, want preserved profile header", got)
	}
	if got := header["X-RequestID"]; len(got) != 1 || got[0] != "flag" {
		t.Fatalf("X-RequestID = %#v, want preserved flag header", got)
	}
	if _, ok := header["X-Sourcesystem"]; ok {
		t.Fatalf("profile header was canonicalized: %#v", header)
	}
	if _, ok := header["X-Requestid"]; ok {
		t.Fatalf("flag header was canonicalized: %#v", header)
	}
}

func TestHTTPPreserveHeaderCaseForAPIKeyAuth(t *testing.T) {
	cfg := `{
		"apis": {
			"myapi": {
				"base_url": "https://api.example.com",
				"preserve_header_case": true,
				"profiles": {
					"default": {
						"auth": {
							"type": "api-key",
							"params": {
								"in": "header",
								"name": "X-SourceSystem",
								"value": "secret"
							}
						}
					}
				}
			}
		}
	}`
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = writeAPIConfig(t, cfg)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})

	if err := c.Run([]string{"restish", "get", "myapi/items"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	header := rr.Last().Header
	if got := header["X-SourceSystem"]; len(got) != 1 || got[0] != "secret" {
		t.Fatalf("X-SourceSystem = %#v, want preserved api-key header", got)
	}
	if _, ok := header["X-Sourcesystem"]; ok {
		t.Fatalf("api-key header was canonicalized: %#v", header)
	}
}

func TestHTTPPreserveHeaderCaseDoesNotRewriteManualAPIKeyHeader(t *testing.T) {
	cfg := `{
		"apis": {
			"myapi": {
				"base_url": "https://api.example.com",
				"preserve_header_case": true,
				"profiles": {
					"default": {
						"auth": {
							"type": "api-key",
							"params": {
								"in": "header",
								"name": "X-SourceSystem",
								"value": "secret"
							}
						}
					}
				}
			}
		}
	}`
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = writeAPIConfig(t, cfg)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})

	if err := c.Run([]string{"restish", "get", "-H", "x-sourcesystem: manual", "myapi/items"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	header := rr.Last().Header
	if got := header["x-sourcesystem"]; len(got) != 1 || got[0] != "manual" {
		t.Fatalf("x-sourcesystem = %#v, want manual header with user casing", got)
	}
	if got := header["X-SourceSystem"]; len(got) != 0 {
		t.Fatalf("manual API-key header was rewritten: %#v", header)
	}
	if got := header["X-Sourcesystem"]; len(got) != 0 {
		t.Fatalf("auth added canonicalized duplicate: %#v", header)
	}
}

func TestHTTPHostHeader(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "-H", "Host: tenant.example.com", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().Host; got != "tenant.example.com" {
		t.Errorf("expected Host tenant.example.com, got %q", got)
	}
}

// TestHTTPHeaderRepeatable verifies that multiple -H flags all take effect.
func TestHTTPHeaderRepeatable(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	err := c.Run([]string{"restish", "get",
		"-H", "X-First: one",
		"-H", "X-Second: two",
		"https://api.example.com/items",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := rr.Last()
	if got := req.Header.Get("X-First"); got != "one" {
		t.Errorf("expected X-First=one, got %q", got)
	}
	if got := req.Header.Get("X-Second"); got != "two" {
		t.Errorf("expected X-Second=two, got %q", got)
	}
}

// TestHTTPQuery verifies that -q appends a query parameter to the request.
func TestHTTPQuery(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "-q", "foo=bar", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().URL.Query().Get("foo"); got != "bar" {
		t.Errorf("expected query foo=bar, got %q", got)
	}
}

func TestHTTPNetworkErrorRedactsURLCredentials(t *testing.T) {
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})

	err := c.Run([]string{
		"restish", "get",
		"http://alice:s3cr3t@api.example.com/items?api_key=url-secret&normal=ok",
		"--rsh-query", "token=flag-secret",
		"--rsh-retry", "0",
	})
	if err == nil {
		t.Fatal("expected network error")
	}
	got := err.Error()
	for _, leak := range []string{"alice", "s3cr3t", "url-secret", "flag-secret"} {
		if strings.Contains(got, leak) {
			t.Fatalf("network error leaked %q:\n%s", leak, got)
		}
	}
	for _, want := range []string{
		"network error for GET http://redacted@api.example.com/items?api_key=%3Credacted%3E&normal=ok",
		"token=%3Credacted%3E",
		"connection refused",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("network error missing %q:\n%s", want, got)
		}
	}
}

func TestHTTPAcceptHeaderOverride(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "-H", "Accept: application/json", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	values := rr.Last().Header.Values("Accept")
	if len(values) != 1 || values[0] != "application/json" {
		t.Fatalf("Accept headers = %#v, want only application/json", values)
	}
}

func TestHTTPDefaultAcceptIncludesWildcardFallback(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		if !strings.Contains(r.Header.Get("Accept"), "*/*") {
			return &http.Response{
				StatusCode: http.StatusNotAcceptable,
				Proto:      "HTTP/1.1",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":"xml only"}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/xml"}},
			Body:       io.NopCloser(strings.NewReader(`<ok/>`)),
		}, nil
	})

	if err := c.Run([]string{"restish", "get", "https://api.example.com/metadata", "-o", "json", "-f", "headers.Content-Type"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().Header.Get("Accept"); !strings.Contains(got, "*/*;q=0.1") {
		t.Fatalf("Accept = %q, want wildcard fallback", got)
	}
}

func TestHTTPHeaderEnvSplitsCommaSeparatedValues(t *testing.T) {
	t.Setenv("RSH_HEADER", "X-One: 1,X-Two: value:with:colons")
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().Header.Get("X-One"); got != "1" {
		t.Fatalf("X-One = %q", got)
	}
	if got := rr.Last().Header.Get("X-Two"); got != "value:with:colons" {
		t.Fatalf("X-Two = %q", got)
	}
}

func TestHTTPHeaderEnvSupportsEscapedCommaValues(t *testing.T) {
	t.Setenv("RSH_HEADER", `X-One: 1,X-List: a\,b`)
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().Header.Get("X-One"); got != "1" {
		t.Fatalf("X-One = %q", got)
	}
	if got := rr.Last().Header.Get("X-List"); got != "a,b" {
		t.Fatalf("X-List = %q, want a,b", got)
	}
}

func TestHTTPHeaderEnvMergesWithFlagByHeaderName(t *testing.T) {
	t.Setenv("RSH_HEADER", `X-Foo: env,X-List: a\,b,X-Keep: yes`)
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{
		"restish", "get", "https://api.example.com/items",
		"--rsh-header", "x-foo: cli",
		"--rsh-header", "X-New: flag",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := rr.Last()
	if got := req.Header.Values("X-Foo"); len(got) != 1 || got[0] != "cli" {
		t.Fatalf("X-Foo values = %#v, want [cli]", got)
	}
	if got := req.Header.Get("X-List"); got != "a,b" {
		t.Fatalf("X-List = %q, want a,b", got)
	}
	if got := req.Header.Get("X-Keep"); got != "yes" {
		t.Fatalf("X-Keep = %q, want yes", got)
	}
	if got := req.Header.Get("X-New"); got != "flag" {
		t.Fatalf("X-New = %q, want flag", got)
	}
}

func TestHTTPQueryEnvSupportsEscapedCommaValues(t *testing.T) {
	t.Setenv("RSH_QUERY", `env=one,list=a\,b`)
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	values := rr.Last().URL.Query()
	if got := values.Get("env"); got != "one" {
		t.Fatalf("env = %q", got)
	}
	if got := values.Get("list"); got != "a,b" {
		t.Fatalf("list = %q, want a,b", got)
	}
	if got := rr.Last().URL.RawQuery; !strings.Contains(got, "list=a%2Cb") {
		t.Fatalf("RawQuery = %q, want escaped comma", got)
	}
}

func TestHTTPEnvHeaderAndQueryValidationFailsBeforeRequest(t *testing.T) {
	tests := []struct {
		name    string
		envName string
		value   string
		want    string
	}{
		{name: "header", envName: "RSH_HEADER", value: "X-One: 1,bare", want: "invalid RSH_HEADER entry"},
		{name: "query", envName: "RSH_QUERY", value: "env=one,bare", want: "invalid RSH_QUERY entry"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envName, tc.value)
			var hit bool
			c, _, _ := newTestCLI(t)
			useTransport(c, func(r *http.Request) (*http.Response, error) {
				hit = true
				return jsonResponse(200, `{}`), nil
			})
			err := c.Run([]string{"restish", "get", "https://api.example.com/items"})
			if err == nil {
				t.Fatal("expected env validation error")
			}
			if hit {
				t.Fatal("request should not be sent with invalid env defaults")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "network error") {
				t.Fatalf("env validation should not be reported as a network error: %v", err)
			}
		})
	}
}

// TestHTTPServerOverride verifies that -s replaces the scheme and host.
func TestHTTPServerOverride(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	// The URL argument points nowhere meaningful; -s redirects to our test server.
	err := c.Run([]string{"restish", "get", "-s", "https://staging.example.com", "https://api.example.com/items"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().URL.Path; got != "/items" {
		t.Errorf("expected path /items after server override, got %q", got)
	}
}

func TestHTTPServerOverridePrefixesPath(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	err := c.Run([]string{"restish", "get", "-s", "https://staging.example.com/v2", "https://api.example.com/items"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().URL.String(); got != "https://staging.example.com/v2/items" {
		t.Errorf("URL = %q, want override path prefix", got)
	}
}

func TestHTTPServerOverrideSuppliesHostForRootRelativePath(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	err := c.Run([]string{"restish", "get", "-s", "https://staging.example.com/v2", "/items?page=2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := rr.Last().URL.String(); got != "https://staging.example.com/v2/items?page=2" {
		t.Errorf("URL = %q, want override origin plus root-relative path", got)
	}
}

func TestHTTPRootRelativePathWithoutContextFailsFast(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		t.Fatal("request should not be sent for root-relative path without context")
		return nil, nil
	})
	err := c.Run([]string{"restish", "get", "/relative/path", "-o", "json", "--rsh-no-cache"})
	if err == nil {
		t.Fatal("expected root-relative path without context to fail")
	}
	if !strings.Contains(err.Error(), `relative path "/relative/path" requires an API short name or --rsh-server`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(errOut.String(), "retry") {
		t.Fatalf("relative path validation should fail before retry warnings, got stderr:\n%s", errOut.String())
	}
}

// TestHTTPResponseBody verifies that the response body is written to stdout.
// Uses a JSON content-type so the body is decoded and re-encoded as an object.
func TestHTTPResponseBody(t *testing.T) {
	c, out, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"hello":"world"}`), nil
	})
	if err := c.Run([]string{"restish", "get", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), `"hello"`) {
		t.Errorf("expected response body in stdout, got: %q", out.String())
	}
}

// TestHTTPTimeout verifies that --rsh-timeout causes the request to fail
// when the server is too slow.
func TestHTTPTimeout(t *testing.T) {
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	err := c.Run([]string{"restish", "get", "--rsh-timeout", "50ms", "https://api.example.com/items"})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("expected 'network' in error, got: %v", err)
	}
}

func TestHTTPTimeoutShorthand(t *testing.T) {
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	err := c.Run([]string{"restish", "get", "-t", "50ms", "https://api.example.com/items"})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestHTTPTimeoutCoversBodyRead(t *testing.T) {
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(readerFunc(func([]byte) (int, error) {
				<-r.Context().Done()
				return 0, r.Context().Err()
			})),
			Request: r,
		}, nil
	})
	err := c.Run([]string{"restish", "get", "--rsh-timeout", "50ms", "https://api.example.com/items"})
	if err == nil {
		t.Fatal("expected body read timeout")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected deadline exceeded error, got %v", err)
	}
}

func TestHTTPDefaultUserAgentAndOverride(t *testing.T) {
	var got []string
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		got = append(got, r.Header.Get("User-Agent"))
		return jsonResponse(200, `{"ok":true}`), nil
	})
	if err := c.Run([]string{"restish", "get", "https://api.example.com/items"}); err != nil {
		t.Fatalf("get default UA: %v", err)
	}

	c2, _, _ := newTestCLI(t)
	useTransport(c2, func(r *http.Request) (*http.Response, error) {
		got = append(got, r.Header.Get("User-Agent"))
		return jsonResponse(200, `{"ok":true}`), nil
	})
	if err := c2.Run([]string{"restish", "get", "-H", "User-Agent: custom", "https://api.example.com/items"}); err != nil {
		t.Fatalf("get custom UA: %v", err)
	}

	if len(got) != 2 || !strings.HasPrefix(got[0], "restish/") || got[1] != "custom" {
		t.Fatalf("User-Agent values = %v", got)
	}
}

// TestHTTPInsecure verifies that --rsh-insecure disables TLS verification.
// The test server uses TLS; without --rsh-insecure the request should fail,
// with it the request should succeed.
func TestHTTPInsecure(t *testing.T) {
	var rr requestRecorder
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr.capture(r)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	// Without --rsh-insecure: TLS verification fails.
	c, _, _ := newTestCLI(t)
	if err := c.Run([]string{"restish", "get", srv.URL}); err == nil {
		t.Error("expected TLS error without --rsh-insecure, got nil")
	}

	// With --rsh-insecure: request succeeds.
	c2, _, _ := newTestCLI(t)
	if err := c2.Run([]string{"restish", "get", "--rsh-insecure", srv.URL}); err != nil {
		t.Errorf("unexpected error with --rsh-insecure: %v", err)
	}
}

// TestShorthandBody verifies that positional args are parsed as shorthand and
// sent as a JSON body with the correct Content-Type.
func TestShorthandBody(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	// Shell would split "name: Alice, age: 30" into tokens; simulate that here.
	if err := c.Run([]string{"restish", "post", "https://api.example.com/items", "name:", "Alice,", "age:", "30"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := rr.Last()
	ct := req.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.body, &body); err != nil {
		t.Fatalf("body is not valid JSON: %v — body: %s", err, rr.body)
	}
	if body["name"] != "Alice" {
		t.Errorf("name: got %v, want Alice", body["name"])
	}
}

func TestShorthandBodyRequiresCommasBetweenFields(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want map[string]any
	}{
		{
			name: "comma separated fields",
			args: []string{"name:", "Alice,", "enabled:", "true"},
			want: map[string]any{"name": "Alice", "enabled": true},
		},
		{
			name: "missing comma is one value",
			args: []string{"name:", "Alice", "enabled:", "true"},
			want: map[string]any{"name": "Alice enabled: true"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var rr requestRecorder
			c, _, _ := newTestCLI(t)
			useTransport(c, func(r *http.Request) (*http.Response, error) {
				rr.capture(r)
				return jsonResponse(200, `{}`), nil
			})
			args := append([]string{"restish", "post", "https://api.example.com/items"}, tc.args...)
			if err := c.Run(args); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(rr.body, &body); err != nil {
				t.Fatalf("body is not valid JSON: %v - body: %s", err, rr.body)
			}
			if !reflect.DeepEqual(body, tc.want) {
				t.Fatalf("body = %#v, want %#v", body, tc.want)
			}
		})
	}
}

// TestShorthandBodyNested verifies deep shorthand paths.
func TestShorthandBodyNested(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "post", "https://api.example.com/items", "user.address.city:", "NYC"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.body, &body); err != nil {
		t.Fatalf("body is not valid JSON: %v — body: %s", err, rr.body)
	}
	user, _ := body["user"].(map[string]any)
	addr, _ := user["address"].(map[string]any)
	if addr["city"] != "NYC" {
		t.Errorf("city: got %v, want NYC", addr["city"])
	}
}

// TestNoBodyWhenNoArgs verifies that GET requests with no positional args
// send no body and no Content-Type.
func TestNoBodyWhenNoArgs(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	if err := c.Run([]string{"restish", "get", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(rr.body) != 0 {
		t.Errorf("expected no body for GET with no args, got %q", rr.body)
	}
	if ct := rr.Last().Header.Get("Content-Type"); ct != "" {
		t.Errorf("expected no Content-Type for GET with no args, got %q", ct)
	}
}

// TestStdinBody verifies that piped stdin is sent as the request body.
func TestStdinBody(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	c.Stdin = strings.NewReader(`{"from":"stdin"}`)
	if err := c.Run([]string{"restish", "post", "https://api.example.com/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.body, &body); err != nil {
		t.Fatalf("body not valid JSON: %v — body: %s", err, rr.body)
	}
	if body["from"] != "stdin" {
		t.Errorf("from: got %v, want stdin", body["from"])
	}
}

func TestFormBody(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	err := c.Run([]string{
		"restish", "post", "-c", "form", "https://api.example.com/items",
		"username:", "alice,", "password:", "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := rr.Last()
	if got := req.Header.Get("Content-Type"); !strings.Contains(got, "application/x-www-form-urlencoded") {
		t.Fatalf("expected form content type, got %q", got)
	}
	body := string(rr.body)
	if body != "password=secret&username=alice" && body != "username=alice&password=secret" {
		t.Fatalf("unexpected form body: %q", body)
	}
}

func TestMultipartBody(t *testing.T) {
	var rr requestRecorder
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		rr.capture(r)
		return jsonResponse(200, `{}`), nil
	})
	uploadPath := filepath.Join("testdata", "upload.txt")
	err := c.Run([]string{
		"restish", "post", "-c", "multipart", "https://api.example.com/items",
		"name:", "alice,", "file:", "@" + uploadPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := rr.Last()
	contentType := req.Header.Get("Content-Type")
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse media type: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data;") {
		t.Fatalf("expected multipart content type, got %q", contentType)
	}

	reader := multipart.NewReader(bytes.NewReader(rr.body), params["boundary"])
	parts := map[string]string{}
	filenames := map[string]string{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		content, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		parts[part.FormName()] = string(content)
		filenames[part.FormName()] = part.FileName()
	}

	if parts["name"] != "alice" {
		t.Fatalf("name part: got %q", parts["name"])
	}
	if parts["file"] != "hello from upload\n" {
		t.Fatalf("file part: got %q", parts["file"])
	}
	if filenames["file"] != "upload.txt" {
		t.Fatalf("expected upload.txt filename, got %q", filenames["file"])
	}
}

func TestMultipartPromptSecretKeepsAtPrefixLiteral(t *testing.T) {
	secret := "@" + filepath.Join(t.TempDir(), "existing-secret")
	if err := os.WriteFile(strings.TrimPrefix(secret, "@"), []byte("file contents"), 0o600); err != nil {
		t.Fatalf("write secret-named file: %v", err)
	}
	c, _, _ := newTestCLI(t)
	c.Hooks().SecretFunc = func(context.Context, string) (string, error) { return secret, nil }
	var got string
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		got = r.FormValue("pin")
		return jsonResponse(200, `{}`), nil
	})

	if err := c.Run([]string{"restish", "post", "-c", "multipart", "--rsh-prompt-secret", "pin", "https://api.example.com/items"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if got != secret {
		t.Fatalf("pin = %q, want literal %q", got, secret)
	}
}

func TestMultipartBodyRejectsMissingFileBeforeRequest(t *testing.T) {
	requested := false
	c, _, _ := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		requested = true
		return jsonResponse(200, `{}`), nil
	})
	missing := filepath.Join(t.TempDir(), "missing.txt")
	err := c.Run([]string{
		"restish", "post", "-c", "multipart", "https://api.example.com/items",
		"name:", "alice,", "file:", "@" + missing,
	})
	if err == nil {
		t.Fatal("expected missing multipart file reference to fail")
	}
	if !strings.Contains(err.Error(), "unable to read multipart file") ||
		!strings.Contains(err.Error(), missing) {
		t.Fatalf("unexpected error: %v", err)
	}
	if requested {
		t.Fatal("request was sent despite missing multipart file reference")
	}
}
