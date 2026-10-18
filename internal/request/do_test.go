package request_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rest-sh/restish/v2/internal/request"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Date": []string{time.Now().UTC().Format(http.TimeFormat)}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type closeCountingTransport struct {
	closeCount atomic.Int32
	idleCount  atomic.Int32
}

func (t *closeCountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return response(http.StatusOK, "ok"), nil
}

func (t *closeCountingTransport) Close() error {
	t.closeCount.Add(1)
	return nil
}

func (t *closeCountingTransport) CloseIdleConnections() {
	t.idleCount.Add(1)
}

type closeCountingWrapper struct {
	inner      http.RoundTripper
	closeCount atomic.Int32
	idleCount  atomic.Int32
}

func (t *closeCountingWrapper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.inner.RoundTrip(req)
}

func (t *closeCountingWrapper) Close() error {
	t.closeCount.Add(1)
	if closer, ok := t.inner.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (t *closeCountingWrapper) CloseIdleConnections() {
	t.idleCount.Add(1)
	if closer, ok := t.inner.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func TestRedactedURLRemovesUserinfoAndCredentialQuery(t *testing.T) {
	u, err := url.Parse("https://alice:s3cr3t@api.example.com/items?api_key=secret&page=1")
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	got := request.RedactedURL(u)
	if strings.Contains(got, "alice") || strings.Contains(got, "s3cr3t") || strings.Contains(got, "secret") {
		t.Fatalf("redacted URL leaked credentials: %s", got)
	}
	if want := "https://redacted@api.example.com/items?api_key=%3Credacted%3E&page=1"; got != want {
		t.Fatalf("redacted URL = %q, want %q", got, want)
	}
}

func TestDoNetworkErrorRedactsRequestURL(t *testing.T) {
	_, err := request.Do(context.Background(), "GET", "https://alice:s3cr3t@api.example.com/items?api_key=url-secret&page=1", nil, request.Options{
		Query: []string{"session=marked-secret", "token=flag-secret"},
		OnRequest: func(req *http.Request) error {
			request.MarkCredentialQueryParam(req, "session")
			return nil
		},
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial failed")
		}),
	})
	if err == nil {
		t.Fatal("expected network error")
	}
	got := err.Error()
	for _, leak := range []string{"alice", "s3cr3t", "url-secret", "marked-secret", "flag-secret"} {
		if strings.Contains(got, leak) {
			t.Fatalf("network error leaked %q:\n%s", leak, got)
		}
	}
	for _, want := range []string{"https://redacted@api.example.com/items?", "api_key=%3Credacted%3E", "session=%3Credacted%3E", "token=%3Credacted%3E", "page=1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("network error missing %q:\n%s", want, got)
		}
	}
}

func TestBuildTransportCloseFullStackOnce(t *testing.T) {
	base := &closeCountingTransport{}
	var wrapper *closeCountingWrapper
	rt := request.BuildTransport(request.Options{
		Transport:      base,
		CacheDir:       t.TempDir(),
		Retry:          1,
		RetryBaseDelay: time.Nanosecond,
		WrapTransport: func(inner http.RoundTripper) http.RoundTripper {
			wrapper = &closeCountingWrapper{inner: inner}
			return wrapper
		},
	})

	closer, ok := rt.(interface{ Close() error })
	if !ok {
		t.Fatal("transport does not implement Close")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := wrapper.closeCount.Load(); got != 1 {
		t.Fatalf("wrapper Close count = %d, want 1", got)
	}
	if got := base.closeCount.Load(); got != 1 {
		t.Fatalf("base Close count = %d, want 1", got)
	}
	if got := base.idleCount.Load(); got != 1 {
		t.Fatalf("base CloseIdleConnections count = %d, want 1", got)
	}
}

func TestBuildTransportOnResponseKeepsCloseChain(t *testing.T) {
	base := &closeCountingTransport{}
	var responses atomic.Int32
	rt := request.BuildTransport(request.Options{
		Transport: base,
		OnResponse: func(resp *http.Response) {
			if resp.Request == nil {
				t.Error("OnResponse should receive a response with Request set")
			}
			responses.Add(1)
		},
	})
	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/items", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if got := responses.Load(); got != 1 {
		t.Fatalf("OnResponse calls = %d, want 1", got)
	}
	closer, ok := rt.(interface{ Close() error })
	if !ok {
		t.Fatal("transport does not implement Close")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := base.closeCount.Load(); got != 1 {
		t.Fatalf("base Close count = %d, want 1", got)
	}
}

func TestBuildTransportRevalidatesMaxAgeZeroWithETag(t *testing.T) {
	var hits atomic.Int32
	var sawValidator atomic.Bool
	rt := request.BuildTransport(request.Options{
		CacheDir: t.TempDir(),
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			n := hits.Add(1)
			if n == 2 {
				if got := req.Header.Get("If-None-Match"); got != `"v1"` {
					t.Fatalf("If-None-Match = %q, want %q", got, `"v1"`)
				}
				sawValidator.Store(true)
				resp := response(http.StatusNotModified, "")
				resp.Header.Set("Cache-Control", "public, max-age=0")
				resp.Header.Set("ETag", `"v1"`)
				return resp, nil
			}
			resp := response(http.StatusOK, "hit-1")
			resp.Header.Set("Cache-Control", "public, max-age=0")
			resp.Header.Set("ETag", `"v1"`)
			return resp, nil
		}),
	})
	var bodies []string
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "https://api.example.com/items", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip %d: %v", i+1, err)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body %d: %v", i+1, err)
		}
		_ = resp.Body.Close()
		bodies = append(bodies, string(data))
	}
	if !sawValidator.Load() {
		t.Fatal("expected second request to revalidate with ETag")
	}
	if strings.Join(bodies, ",") != "hit-1,hit-1" {
		t.Fatalf("bodies = %v, want cached body reused after 304", bodies)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("origin hits = %d, want 2 including revalidation", got)
	}
}

func TestBuildTransportRevalidatesMaxAgeZeroWithLastModified(t *testing.T) {
	var hits atomic.Int32
	lastModified := time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Format(http.TimeFormat)
	var sawValidator atomic.Bool
	rt := request.BuildTransport(request.Options{
		CacheDir: t.TempDir(),
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			n := hits.Add(1)
			if n == 2 {
				if got := req.Header.Get("If-Modified-Since"); got != lastModified {
					t.Fatalf("If-Modified-Since = %q, want %q", got, lastModified)
				}
				sawValidator.Store(true)
				resp := response(http.StatusNotModified, "")
				resp.Header.Set("Cache-Control", "public, max-age=0")
				resp.Header.Set("Last-Modified", lastModified)
				return resp, nil
			}
			resp := response(http.StatusOK, "hit-1")
			resp.Header.Set("Cache-Control", "public, max-age=0")
			resp.Header.Set("Last-Modified", lastModified)
			return resp, nil
		}),
	})
	var bodies []string
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "https://api.example.com/items", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip %d: %v", i+1, err)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body %d: %v", i+1, err)
		}
		_ = resp.Body.Close()
		bodies = append(bodies, string(data))
	}
	if !sawValidator.Load() {
		t.Fatal("expected second request to revalidate with Last-Modified")
	}
	if strings.Join(bodies, ",") != "hit-1,hit-1" {
		t.Fatalf("bodies = %v, want cached body reused after 304", bodies)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("origin hits = %d, want 2 including revalidation", got)
	}
}

func TestBuildTransportCachesVaryAcceptVariants(t *testing.T) {
	var hits atomic.Int32
	rt := request.BuildTransport(request.Options{
		CacheDir: t.TempDir(),
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			hits.Add(1)
			body := "json"
			if req.Header.Get("Accept") == "application/xml" {
				body = "xml"
			}
			resp := response(http.StatusOK, body)
			resp.Header.Set("Cache-Control", "public, max-age=3600")
			resp.Header.Set("Vary", "Accept")
			return resp, nil
		}),
	})
	requests := []struct {
		accept string
		want   string
	}{
		{"application/json", "json"},
		{"application/xml", "xml"},
		{"application/json", "json"},
		{"application/xml", "xml"},
	}
	for i, tc := range requests {
		req := httptest.NewRequest(http.MethodGet, "https://api.example.com/items", nil)
		req.Header.Set("Accept", tc.accept)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip %d: %v", i+1, err)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body %d: %v", i+1, err)
		}
		_ = resp.Body.Close()
		if string(data) != tc.want {
			t.Fatalf("body %d = %q, want %q", i+1, string(data), tc.want)
		}
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("origin hits = %d, want 2 variants", got)
	}
}

func TestBuildTransportDoesNotReuseVaryWildcard(t *testing.T) {
	var hits atomic.Int32
	rt := request.BuildTransport(request.Options{
		CacheDir: t.TempDir(),
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			n := hits.Add(1)
			resp := response(http.StatusOK, fmt.Sprintf("hit-%d", n))
			resp.Header.Set("Cache-Control", "public, max-age=3600")
			resp.Header.Set("Vary", "*")
			return resp, nil
		}),
	})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "https://api.example.com/items", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip %d: %v", i+1, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("origin hits = %d, want 2 because Vary:* is not reusable", got)
	}
}

func TestDo_BasicGet(t *testing.T) {
	resp, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != "GET" {
				t.Errorf("expected GET, got %s", r.Method)
			}
			return response(200, "hello"), nil
		}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Errorf("expected body 'hello', got %q", body)
	}
}

func TestDo_Headers(t *testing.T) {
	var gotHeader string
	_, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			gotHeader = r.Header.Get("X-Custom")
			return response(200, ""), nil
		}),
		Headers: []string{"X-Custom: test-value"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeader != "test-value" {
		t.Errorf("expected X-Custom=test-value, got %q", gotHeader)
	}
}

func TestDo_HeaderWithColonInValue(t *testing.T) {
	var gotHeader string
	_, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			gotHeader = r.Header.Get("Authorization")
			return response(200, ""), nil
		}),
		Headers: []string{"Authorization: Bearer tok:en"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeader != "Bearer tok:en" {
		t.Errorf("expected 'Bearer tok:en', got %q", gotHeader)
	}
}

func TestDo_HostHeaderSetsRequestHost(t *testing.T) {
	var gotHost, gotHeader string
	_, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			gotHost = r.Host
			gotHeader = r.Header.Get("Host")
			return response(200, ""), nil
		}),
		Headers: []string{"Host: tenant.example.com"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != "tenant.example.com" {
		t.Errorf("expected req.Host tenant.example.com, got %q", gotHost)
	}
	if gotHeader != "" {
		t.Errorf("expected Host not to be stored as a regular header, got %q", gotHeader)
	}
}

func TestDo_PreserveHeaderCaseWritesRawHTTP1Header(t *testing.T) {
	raw := rawRequestForHeaders(t, request.Options{
		Headers:            []string{"X-SourceSystem: demo"},
		PreserveHeaderCase: true,
	})
	if !strings.Contains(raw, "\r\nX-SourceSystem: demo\r\n") {
		t.Fatalf("raw request missing preserved header case:\n%s", raw)
	}
	if strings.Contains(raw, "\r\nX-Sourcesystem: demo\r\n") {
		t.Fatalf("raw request used canonicalized header case:\n%s", raw)
	}
}

func TestDo_DefaultCanonicalizesHeaderCase(t *testing.T) {
	raw := rawRequestForHeaders(t, request.Options{
		Headers: []string{"X-SourceSystem: demo"},
	})
	if !strings.Contains(raw, "\r\nX-Sourcesystem: demo\r\n") {
		t.Fatalf("raw request missing canonicalized header case:\n%s", raw)
	}
}

func TestDo_PreservedUserAgentSuppressesDefaultUserAgent(t *testing.T) {
	raw := rawRequestForHeaders(t, request.Options{
		Headers:            []string{"user-agent: custom-agent"},
		PreserveHeaderCase: true,
		UserAgent:          "restish-default",
	})
	if !strings.Contains(raw, "\r\nuser-agent: custom-agent\r\n") {
		t.Fatalf("raw request missing preserved user-agent:\n%s", raw)
	}
	if strings.Contains(raw, "restish-default") {
		t.Fatalf("default user-agent was added despite preserved user-agent:\n%s", raw)
	}
}

func rawRequestForHeaders(t *testing.T, opts request.Options) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	rawCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		var raw strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errCh <- err
				return
			}
			raw.WriteString(line)
			if line == "\r\n" {
				break
			}
		}
		if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"); err != nil {
			errCh <- err
			return
		}
		rawCh <- raw.String()
	}()

	resp, err := request.Do(context.Background(), "GET", "http://"+ln.Addr().String()+"/items", nil, opts)
	if err != nil {
		t.Fatalf("Do() error: %v", err)
	}
	defer resp.Body.Close()

	select {
	case raw := <-rawCh:
		return raw
	case err := <-errCh:
		t.Fatalf("server error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for raw request")
	}
	return ""
}

func TestDo_InvalidHeader(t *testing.T) {
	_, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return response(200, ""), nil
		}),
		Headers: []string{"no-colon-at-all"},
	})
	if err == nil {
		t.Fatal("expected error for malformed header, got nil")
	}
}

func TestDo_Query(t *testing.T) {
	var gotQuery string
	_, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			gotQuery = r.URL.Query().Get("page")
			return response(200, ""), nil
		}),
		Query: []string{"page=2"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery != "2" {
		t.Errorf("expected page=2, got %q", gotQuery)
	}
}

func TestDo_InvalidQueryParam(t *testing.T) {
	_, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return response(200, ""), nil
		}),
		Query: []string{"no-equals-sign"},
	})
	if err == nil {
		t.Fatal("expected error for malformed query param, got nil")
	}
}

func TestDo_Post_WithBody(t *testing.T) {
	var gotBody string
	resp, err := request.Do(context.Background(), "POST", "https://api.example.com/items", strings.NewReader(`{"name":"test"}`), request.Options{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			return response(201, ""), nil
		}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}
	if gotBody != `{"name":"test"}` {
		t.Errorf("unexpected body: %q", gotBody)
	}
}

func TestDo_CrossOriginRedirectStripsCredentialHeaders(t *testing.T) {
	var gotAPIKey, gotTrace string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-API-Key")
		gotTrace = r.Header.Get("X-Trace")
		w.WriteHeader(200)
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/next", http.StatusFound)
	}))
	defer source.Close()

	resp, err := request.Do(context.Background(), "GET", source.URL, nil, request.Options{
		Headers: []string{"X-API-Key: secret", "X-Trace: keep"},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if gotAPIKey != "" {
		t.Fatalf("X-API-Key crossed origin: %q", gotAPIKey)
	}
	if gotTrace != "keep" {
		t.Fatalf("X-Trace = %q, want keep", gotTrace)
	}
}

func TestDo_SameOriginRedirectKeepsCredentialHeaders(t *testing.T) {
	var gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/next", http.StatusFound)
			return
		}
		gotAPIKey = r.Header.Get("X-API-Key")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := request.Do(context.Background(), "GET", srv.URL+"/start", nil, request.Options{
		Headers: []string{"X-API-Key: secret"},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if gotAPIKey != "secret" {
		t.Fatalf("X-API-Key = %q, want secret", gotAPIKey)
	}
}

func TestDo_SeeOtherRedirectStripsBodyHeaders(t *testing.T) {
	var gotMethod, gotContentType, gotContentEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/next", http.StatusSeeOther)
			return
		}
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotContentEncoding = r.Header.Get("Content-Encoding")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := request.Do(context.Background(), "POST", srv.URL+"/start", strings.NewReader(`{"ok":true}`), request.Options{
		Headers: []string{"Content-Type: application/json", "Content-Encoding: gzip"},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if gotMethod != http.MethodGet {
		t.Fatalf("redirect method = %q, want GET", gotMethod)
	}
	if gotContentType != "" || gotContentEncoding != "" {
		t.Fatalf("body headers crossed 303 redirect: Content-Type=%q Content-Encoding=%q", gotContentType, gotContentEncoding)
	}
}

func TestDo_CrossOriginRedirectStripsPreservedCaseCredentialHeader(t *testing.T) {
	var gotAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer target.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer start.Close()

	resp, err := request.Do(context.Background(), "GET", start.URL, nil, request.Options{
		Headers:            []string{"authorization: Bearer secret"},
		PreserveHeaderCase: true,
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if gotAuth != "" {
		t.Fatalf("preserved-case Authorization crossed redirect: %q", gotAuth)
	}
}

func TestDo_CrossOriginRedirectStripsMarkedPreservedCaseCredentialHeader(t *testing.T) {
	var gotSourceSystem string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSourceSystem = r.Header.Get("X-SourceSystem")
		w.WriteHeader(200)
	}))
	defer target.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer start.Close()

	resp, err := request.Do(context.Background(), "GET", start.URL, nil, request.Options{
		Headers:            []string{"X-SourceSystem: secret"},
		PreserveHeaderCase: true,
		OnRequest: func(req *http.Request) error {
			request.MarkCredentialHeader(req, "X-SourceSystem")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if gotSourceSystem != "" {
		t.Fatalf("marked preserved-case credential crossed redirect: %q", gotSourceSystem)
	}
}

func TestDo_TemporaryRedirectKeepsBodyHeaders(t *testing.T) {
	var gotMethod, gotContentType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/next", http.StatusTemporaryRedirect)
			return
		}
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(data)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	resp, err := request.Do(context.Background(), "POST", srv.URL+"/start", strings.NewReader(`{"ok":true}`), request.Options{
		Headers: []string{"Content-Type: application/json"},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if gotMethod != http.MethodPost {
		t.Fatalf("redirect method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody != `{"ok":true}` {
		t.Fatalf("body = %q, want original body", gotBody)
	}
}

func TestDo_CrossOriginRedirectRejectsMarkedCredentialBody(t *testing.T) {
	var targetHit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit.Store(true)
		w.WriteHeader(200)
	}))
	defer target.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer start.Close()

	resp, err := request.Do(context.Background(), "POST", start.URL, strings.NewReader(`{"pin":"secret"}`), request.Options{
		OnRequest: func(req *http.Request) error {
			request.MarkCredentialBody(req)
			return nil
		},
	})
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "refusing cross-origin redirect") {
		t.Fatalf("Do() error = %v, want credential body redirect refusal", err)
	}
	if targetHit.Load() {
		t.Fatal("credential-bearing request body reached cross-origin redirect target")
	}
}

func TestSameOriginUsesSchemeHostAndEffectivePort(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{name: "https default explicit", a: "https://example.com", b: "https://example.com:443/path", want: true},
		{name: "http default explicit", a: "http://example.com", b: "http://example.com:80/path", want: true},
		{name: "scheme differs", a: "https://example.com", b: "http://example.com", want: false},
		{name: "port differs", a: "https://example.com:444", b: "https://example.com", want: false},
		{name: "unknown scheme no port", a: "wss://example.com", b: "wss://example.com", want: false},
		{name: "unknown scheme explicit port", a: "wss://example.com:443", b: "wss://example.com:443", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := url.Parse(tc.a)
			if err != nil {
				t.Fatalf("parse a: %v", err)
			}
			b, err := url.Parse(tc.b)
			if err != nil {
				t.Fatalf("parse b: %v", err)
			}
			if got := request.SameOrigin(a, b); got != tc.want {
				t.Fatalf("SameOrigin(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestEffectivePort(t *testing.T) {
	tests := []struct {
		raw    string
		want   string
		wantOK bool
	}{
		{raw: "http://example.com", want: "80", wantOK: true},
		{raw: "https://example.com", want: "443", wantOK: true},
		{raw: "https://example.com:8443", want: "8443", wantOK: true},
		{raw: "wss://example.com", want: "", wantOK: false},
		{raw: "wss://example.com:443", want: "443", wantOK: true},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			u, err := url.Parse(tc.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, ok := request.EffectivePort(u)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("EffectivePort(%q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestDo_HeaderTimeout(t *testing.T) {
	_, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Timeout: 20 * time.Millisecond,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}),
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestDo_HeaderTimeoutDoesNotWaitForNonCooperativeTransport(t *testing.T) {
	release := make(chan struct{})
	closed := make(chan struct{})
	started := make(chan struct{})

	start := time.Now()
	_, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Timeout: 20 * time.Millisecond,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			close(started)
			<-release
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{},
				Body:       closeNotifyBody{closed: closed},
			}, nil
		}),
	})
	elapsed := time.Since(start)
	if err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want %v", err, context.DeadlineExceeded)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout waited for non-cooperative transport, elapsed = %v", elapsed)
	}
	<-started
	close(release)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("late response body was not closed")
	}
}

func TestDo_TimeoutDoesNotSpawnDrainWaiterForStuckTransport(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	_, err := request.Do(context.Background(), "GET", "https://api.example.com/items", nil, request.Options{
		Timeout: 20 * time.Millisecond,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			close(started)
			<-release
			return response(http.StatusOK, "late"), nil
		}),
	})
	defer close(release)
	if err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want %v", err, context.DeadlineExceeded)
	}
	<-started

	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	stack := string(buf[:n])
	if got := strings.Count(stack, "internal/request.doWithResponseTimeout.func"); got > 1 {
		t.Fatalf("doWithResponseTimeout goroutines = %d, want at most 1\n%s", got, stack)
	}
}

func TestDo_TimeoutCancelsBodyReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, err := request.Do(ctx, "GET", "https://api.example.com/items", nil, request.Options{
		Timeout: 20 * time.Millisecond,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{},
				Body: io.NopCloser(readerFunc(func(p []byte) (int, error) {
					select {
					case <-time.After(50 * time.Millisecond):
						copy(p, "hello")
						return 5, io.EOF
					case <-r.Context().Done():
						return 0, r.Context().Err()
					}
				})),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	_, err = io.ReadAll(resp.Body)
	if err == nil {
		t.Fatal("expected body read timeout")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("body read error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestDo_HeaderTimeoutOnlyBodyDeadlineCanBeDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, err := request.Do(ctx, "GET", "https://api.example.com/items", nil, request.Options{
		Timeout:           20 * time.Millisecond,
		HeaderTimeoutOnly: true,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{},
				Body: io.NopCloser(readerFunc(func(p []byte) (int, error) {
					select {
					case <-time.After(50 * time.Millisecond):
						copy(p, "hello")
						return 5, io.EOF
					case <-r.Context().Done():
						return 0, r.Context().Err()
					}
				})),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if !request.DisableResponseBodyDeadline(resp) {
		t.Fatal("expected body deadline to be disabled")
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body after disabling deadline: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("body = %q, want hello", data)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) {
	return f(p)
}

type closeNotifyBody struct {
	closed chan struct{}
}

func (b closeNotifyBody) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (b closeNotifyBody) Close() error {
	close(b.closed)
	return nil
}
