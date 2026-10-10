package cli_test

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestVerboseOutputToStderr verifies that -v writes request/response details
// to stderr and not to stdout.
func TestVerboseOutputToStderr(t *testing.T) {
	c, out, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"msg":"hi"}`)),
			Request:    r,
		}, nil
	})
	if err := c.Run([]string{"restish", "get", "-v", "https://api.example.com/hello"}); err != nil {
		t.Fatalf("get: %v", err)
	}

	stderr := errOut.String()
	stdout := out.String()

	// stderr must contain request line.
	if !strings.Contains(stderr, "> GET") {
		t.Errorf("expected '> GET' in stderr, got:\n%s", stderr)
	}
	// stderr must contain response status.
	if !strings.Contains(stderr, "< HTTP") || !strings.Contains(stderr, "200") {
		t.Errorf("expected '< HTTP/1.1 200' in stderr, got:\n%s", stderr)
	}
	// Verbose lines must NOT appear in stdout.
	if strings.Contains(stdout, "> GET") || strings.Contains(stdout, "< HTTP") {
		t.Errorf("verbose output leaked to stdout:\n%s", stdout)
	}
	// The response body should still appear in stdout (or be formatted normally).
	if !strings.Contains(stdout, "hi") {
		t.Errorf("expected response body in stdout, got:\n%s", stdout)
	}
}

func TestVerboseOutputSortsHeaders(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header: http.Header{
				"X-Zeta":       []string{"last"},
				"Content-Type": []string{"application/json"},
				"X-Alpha":      []string{"first"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"msg":"hi"}`)),
			Request: r,
		}, nil
	})

	if err := c.Run([]string{
		"restish", "get", "-v",
		"-H", "X-Zeta: last",
		"-H", "X-Alpha: first",
		"https://api.example.com/hello",
	}); err != nil {
		t.Fatalf("get: %v", err)
	}

	stderr := errOut.String()
	assertLineOrder(t, stderr, "> X-Alpha: first", "> X-Zeta: last")
	assertLineOrder(t, stderr, "< Content-Type: application/json", "< X-Alpha: first", "< X-Zeta: last")
}

func TestVerboseShowsAutoFilterLanguage(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "shorthand",
			args: []string{"restish", "get", "-v", "-f", "{id: body.id}", "https://api.example.com/hello"},
			want: "* Filter: shorthand (auto)",
		},
		{
			name: "jq",
			args: []string{"restish", "get", "-v", "-f", "{id: .body.id}", "https://api.example.com/hello"},
			want: "* Filter: jq (auto)",
		},
		{
			name: "forced jq",
			args: []string{"restish", "get", "-v", "--rsh-filter-lang", "jq", "-f", "{id: .body.id}", "https://api.example.com/hello"},
			want: "* Filter: jq",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, out, errOut := newTestCLI(t)
			c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
			useTransport(c, func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200,
					Proto:      "HTTP/1.1",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":42}`)),
					Request:    r,
				}, nil
			})

			if err := c.Run(tt.args); err != nil {
				t.Fatalf("get: %v", err)
			}
			if stderr := errOut.String(); !strings.Contains(stderr, tt.want) {
				t.Fatalf("expected verbose filter language %q, got:\n%s", tt.want, stderr)
			}
			if strings.Contains(out.String(), "* Filter:") {
				t.Fatalf("verbose filter language leaked to stdout:\n%s", out.String())
			}
		})
	}
}

func TestVerboseShowsRequestAndPipelineTrace(t *testing.T) {
	c, out, errOut := newTestCLI(t)
	configPath := t.TempDir() + "/restish.json"
	c.Hooks().ConfigPath = configPath
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":42}`)),
			Request:    r,
		}, nil
	})

	err := c.Run([]string{
		"restish", "post", "-v",
		"-H", "Authorization: Bearer secret",
		"-f", "{id: body.id}",
		"-o", "json",
		"https://api.example.com/items",
		"id:", "42",
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}

	stderr := errOut.String()
	for _, want := range []string{
		"* Config: " + configPath,
		"* Profile: default",
		"* Auth: enabled",
		"* Input: args",
		"* Request body: application/json",
		"* Decode: application/json",
		"* Filter: shorthand (auto)",
		"* Output: json",
		"* Pipeline: args -> application/json -> auth -> HTTP -> application/json -> shorthand(auto) -> json",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("expected verbose trace %q, got:\n%s", want, stderr)
		}
	}
	assertLineOrder(t, stderr, "* Config: "+configPath, "> POST")
	if strings.Contains(out.String(), "* Pipeline:") {
		t.Fatalf("verbose trace leaked to stdout:\n%s", out.String())
	}
}

// TestNonVerboseOutputClean verifies that without -v, stderr is empty for
// a successful request.
func TestNonVerboseOutputClean(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
		}, nil
	})
	if err := c.Run([]string{"restish", "get", "https://api.example.com/hello"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	if errOut.Len() != 0 {
		t.Errorf("expected empty stderr without -v, got: %q", errOut.String())
	}
}

func TestVerboseRedactsSensitiveQueryParams(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    r,
		}, nil
	})

	err := c.Run([]string{"restish", "get", "-v", "https://api.example.com/hello?api_key=secret&token=abc&id_token=id-secret&page=1&key=testing&auth=demo"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stderr := errOut.String()
	if strings.Contains(stderr, "api_key=secret") || strings.Contains(stderr, "token=abc") || strings.Contains(stderr, "id_token=id-secret") {
		t.Fatalf("expected sensitive query values redacted, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "api_key=%3Credacted%3E") || !strings.Contains(stderr, "token=%3Credacted%3E") || !strings.Contains(stderr, "id_token=%3Credacted%3E") {
		t.Fatalf("expected redacted query params in verbose output, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "key=testing") || !strings.Contains(stderr, "auth=demo") {
		t.Fatalf("expected low-entropy ambiguous params to remain visible, got:\n%s", stderr)
	}
}

func TestVerboseRedactsURLUserinfo(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
	var gotUserinfo string
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		if r.URL.User != nil {
			gotUserinfo = r.URL.User.String()
		}
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    r,
		}, nil
	})

	err := c.Run([]string{"restish", "get", "-v", "https://user:passwd@api.example.com/hello?api_key=secret&page=1"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotUserinfo != "user:passwd" {
		t.Fatalf("request userinfo = %q, want original userinfo preserved for transport", gotUserinfo)
	}
	stderr := errOut.String()
	for _, leaked := range []string{"user:passwd@", "user", "passwd", "api_key=secret"} {
		if strings.Contains(stderr, leaked) {
			t.Fatalf("verbose output leaked %q:\n%s", leaked, stderr)
		}
	}
	if !strings.Contains(stderr, "> GET https://redacted@api.example.com/hello?api_key=%3Credacted%3E&page=1") {
		t.Fatalf("expected redacted request URL, got:\n%s", stderr)
	}
}

func TestVerboseRedactsTokenLikeAmbiguousQueryParams(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    r,
		}, nil
	})

	err := c.Run([]string{"restish", "get", "-v", "https://api.example.com/hello?key=kh24g2j&auth=Bearer%20abc123def&subscription-key=sub-secret"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stderr := errOut.String()
	for _, leaked := range []string{"kh24g2j", "abc123def", "sub-secret"} {
		if strings.Contains(stderr, leaked) {
			t.Fatalf("verbose query leaked %q:\n%s", leaked, stderr)
		}
	}
	if strings.Count(stderr, "%3Credacted%3E") < 3 {
		t.Fatalf("expected ambiguous token-like query params redacted, got:\n%s", stderr)
	}
}

func TestVerboseRedactsSensitiveResponseHeaders(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header: http.Header{
				"Content-Type":           []string{"application/json"},
				"Set-Cookie":             []string{"session=secret"},
				"X-Varied-Authorization": []string{"DPoP secret-token"},
				"X-Request-Id":           []string{"visible"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request: r,
		}, nil
	})

	if err := c.Run([]string{"restish", "get", "-v", "https://api.example.com/hello"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	stderr := errOut.String()
	if strings.Contains(stderr, "session=secret") || strings.Contains(stderr, "secret-token") {
		t.Fatalf("verbose response leaked Set-Cookie:\n%s", stderr)
	}
	if !strings.Contains(stderr, "< Set-Cookie: <redacted>") {
		t.Fatalf("expected redacted Set-Cookie, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "< X-Varied-Authorization: <redacted>") {
		t.Fatalf("expected derived Authorization header to be redacted, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "< X-Request-Id: visible") {
		t.Fatalf("expected non-sensitive header to remain visible, got:\n%s", stderr)
	}
}

func TestVerboseRedactsJSONBodies(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"token":"response-secret","Authorization":"bearer-secret","token_type":"bearer","key":"testing","apiUrl":"https://example.com/path?key=kh24g2j&page=1","data":"{\"api_key\":\"nested-secret\",\"name\":\"visible\"}","ok":true}`)),
			Request:    r,
		}, nil
	})

	err := c.Run([]string{"restish", "post", "-v", "https://api.example.com/hello", "token: request-secret, password2: confirm-secret, max_tokens: 128, token_budget: 42, key: testing"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	stderr := errOut.String()
	if strings.Contains(stderr, "request-secret") || strings.Contains(stderr, "confirm-secret") || strings.Contains(stderr, "response-secret") || strings.Contains(stderr, "bearer-secret") || strings.Contains(stderr, "kh24g2j") || strings.Contains(stderr, "nested-secret") {
		t.Fatalf("verbose body leaked secret:\n%s", stderr)
	}
	if !strings.Contains(stderr, `"max_tokens": 128`) || !strings.Contains(stderr, `"token_budget": 42`) {
		t.Fatalf("non-secret token counters should remain visible, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, `"token_type": "bearer"`) {
		t.Fatalf("token_type should not be redacted, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, `"key": "testing"`) {
		t.Fatalf("low-entropy key value should remain visible, got:\n%s", stderr)
	}
	if strings.Count(stderr, `\u003credacted\u003e`) < 2 && strings.Count(stderr, "<redacted>") < 2 {
		t.Fatalf("expected redacted request and response bodies, got:\n%s", stderr)
	}
}

func TestVerboseRedactsFormBodies(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}},
			Body:       io.NopCloser(strings.NewReader(`token=response-secret&name=visible`)),
			Request:    r,
		}, nil
	})

	err := c.Run([]string{"restish", "post", "-v", "--rsh-content-type", "application/x-www-form-urlencoded", "https://api.example.com/hello", "token: request-secret", "name: visible"})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	stderr := errOut.String()
	if strings.Contains(stderr, "request-secret") || strings.Contains(stderr, "response-secret") {
		t.Fatalf("verbose form body leaked secret:\n%s", stderr)
	}
	if !strings.Contains(stderr, "token=%3Credacted%3E") || !strings.Contains(stderr, "name=visible") {
		t.Fatalf("expected redacted form body with visible fields, got:\n%s", stderr)
	}
}

func TestVerboseNonTextBodyUsesPlaceholder(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
			Body:       io.NopCloser(strings.NewReader("secret-bytes")),
			Request:    r,
		}, nil
	})

	if err := c.Run([]string{"restish", "get", "-v", "https://api.example.com/blob"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	stderr := errOut.String()
	if strings.Contains(stderr, "secret-bytes") {
		t.Fatalf("verbose non-text body leaked raw bytes:\n%s", stderr)
	}
	if !strings.Contains(stderr, "<12 bytes of application/octet-stream body>") {
		t.Fatalf("expected binary placeholder, got:\n%s", stderr)
	}
}

func TestVerboseLogsRequestBodyBeforeResponseHeaders(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 201,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    r,
		}, nil
	})

	if err := c.Run([]string{"restish", "post", "-v", "https://api.example.com/hello", "name: Ada"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	stderr := errOut.String()
	bodyAt := strings.Index(stderr, "> body:")
	responseAt := strings.Index(stderr, "< HTTP/1.1 201")
	if bodyAt < 0 || responseAt < 0 {
		t.Fatalf("expected request body and response headers in verbose output, got:\n%s", stderr)
	}
	if bodyAt > responseAt {
		t.Fatalf("expected request body before response headers, got:\n%s", stderr)
	}
}

func TestVerboseLogsRequestBeforeResponseArrives(t *testing.T) {
	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
	requestReachedTransport := make(chan struct{})
	releaseResponse := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseResponse) })
	}
	defer release()
	c.Stderr = &signalWriter{needle: []byte("> GET https://api.example.com/slow"), seen: make(chan struct{})}
	useTransport(c, func(r *http.Request) (*http.Response, error) {
		close(requestReachedTransport)
		<-releaseResponse
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    r,
		}, nil
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Run([]string{"restish", "get", "-v", "https://api.example.com/slow"})
	}()

	select {
	case <-requestReachedTransport:
	case <-time.After(time.Second):
		t.Fatal("request did not reach transport")
	}

	select {
	case <-c.Stderr.(*signalWriter).seen:
	case err := <-errCh:
		t.Fatalf("command finished before response was released: %v", err)
	case <-time.After(time.Second):
		t.Fatal("verbose request was not printed before response arrived")
	}

	release()
	if err := <-errCh; err != nil {
		t.Fatalf("get: %v", err)
	}
}

func TestVerboseLogsCachedResponses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	cacheDir := t.TempDir()
	c1, _, errOut1 := newTestCLI(t)
	c1.Hooks().CachePath = cacheDir
	if err := c1.Run([]string{"restish", "get", "-v", srv.URL}); err != nil {
		t.Fatalf("first get: %v", err)
	}
	if strings.Contains(errOut1.String(), "* Cache: HIT") {
		t.Fatalf("first request should not be marked as cache hit:\n%s", errOut1.String())
	}

	c2, _, errOut2 := newTestCLI(t)
	c2.Hooks().CachePath = cacheDir
	if err := c2.Run([]string{"restish", "get", "-v", srv.URL}); err != nil {
		t.Fatalf("second get: %v", err)
	}
	stderr := errOut2.String()
	for _, want := range []string{"> GET ", "< HTTP/1.1 200 OK", "* Cache: HIT"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("cached verbose output missing %q:\n%s", want, stderr)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("server hits = %d, want 1", got)
	}
}

func TestVerboseLogsEveryPaginatedRequest(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"
	useThreePageTransport(c)

	if err := c.Run([]string{"restish", "get", "-v", "https://api.example.com/items", "-o", "json"}); err != nil {
		t.Fatalf("get: %v", err)
	}

	stderr := errOut.String()
	if got := strings.Count(stderr, "> GET "); got != 3 {
		t.Fatalf("expected verbose output for 3 requests, got %d:\n%s", got, stderr)
	}
	if got := strings.Count(stderr, "< HTTP/1.1 200 OK"); got != 3 {
		t.Fatalf("expected verbose output for 3 responses, got %d:\n%s", got, stderr)
	}
	for _, want := range []string{
		"https://api.example.com/items",
		"https://api.example.com/items?page=2",
		"https://api.example.com/items?page=3",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("expected verbose output to include %s, got:\n%s", want, stderr)
		}
	}
}

// TestVerboseTLSDetailsAtLevel2 verifies that -vv (verbose >= 2) prints TLS
// version, cipher suite, and peer certificate information to stderr.
func TestVerboseTLSDetailsAtLevel2(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"

	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
			TLS: &tls.ConnectionState{
				Version:     tls.VersionTLS13,
				CipherSuite: tls.TLS_AES_256_GCM_SHA384,
			},
		}, nil
	})

	if err := c.Run([]string{"restish", "get", "-vv", "https://api.example.com"}); err != nil {
		t.Fatalf("get: %v", err)
	}

	stderr := errOut.String()
	if !strings.Contains(stderr, "TLS 1.3") {
		t.Errorf("expected TLS version in -vv stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "TLS_AES_256_GCM_SHA384") {
		t.Errorf("expected cipher suite in -vv stderr, got:\n%s", stderr)
	}
}

// TestVerboseTLSDetailsNotAtLevel1 verifies that -v (verbose == 1) does NOT
// print TLS details, keeping the output concise.
func TestVerboseTLSDetailsNotAtLevel1(t *testing.T) {
	c, _, errOut := newTestCLI(t)
	c.Hooks().ConfigPath = t.TempDir() + "/restish.json"

	useTransport(c, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    r,
			TLS: &tls.ConnectionState{
				Version:     tls.VersionTLS13,
				CipherSuite: tls.TLS_AES_256_GCM_SHA384,
			},
		}, nil
	})

	if err := c.Run([]string{"restish", "get", "-v", "https://api.example.com"}); err != nil {
		t.Fatalf("get: %v", err)
	}

	stderr := errOut.String()
	if strings.Contains(stderr, "TLS 1.3") {
		t.Errorf("unexpected TLS details at -v level:\n%s", stderr)
	}
}

func assertLineOrder(t *testing.T, text string, lines ...string) {
	t.Helper()
	previous := -1
	for _, line := range lines {
		index := strings.Index(text, line)
		if index < 0 {
			t.Fatalf("expected %q in output:\n%s", line, text)
		}
		if index <= previous {
			t.Fatalf("expected %q after previous line in output:\n%s", line, text)
		}
		previous = index
	}
}
