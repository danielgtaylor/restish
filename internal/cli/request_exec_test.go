package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/hypermedia"
	"github.com/rest-sh/restish/v2/internal/request"
)

type testAuthHandler struct{}

func (testAuthHandler) Parameters() []auth.Param { return nil }

func (testAuthHandler) OnRequest(req *http.Request, params map[string]string) error {
	req.Header.Set("Authorization", "Bearer "+params["token"])
	return nil
}

func (h testAuthHandler) Authenticate(_ context.Context, req *http.Request, ac auth.AuthContext) error {
	return h.OnRequest(req, ac.Params)
}

func (testAuthHandler) SupportsForce() {}

type countingCloser struct {
	closes int
}

func (c *countingCloser) Close() error {
	c.closes++
	return nil
}

func TestSendPreparedRequestMarksCredentialBodyOnUnauthorizedRetry(t *testing.T) {
	var marked []bool
	var attempts int
	prepared := &preparedRequest{
		rawURL:      "https://api.example.com/items",
		bodyRaw:     []byte(`{"pin":"secret"}`),
		bodySecrets: []string{"secret"},
		opts: request.Options{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				status := http.StatusOK
				if attempts == 1 {
					status = http.StatusUnauthorized
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			}),
			OnRequest: func(req *http.Request) error {
				request.MarkCredentialBody(req)
				return nil
			},
			OnBeforeRequest: func(req *http.Request) {
				marked = append(marked, request.IsMarkedCredentialBody(req))
			},
			OnUnauthorized: func(*http.Request) error { return nil },
		},
	}

	resp, err := New().sendPreparedRequest(context.Background(), http.MethodPost, prepared)
	if err != nil {
		t.Fatalf("sendPreparedRequest: %v", err)
	}
	defer resp.Body.Close()
	if attempts != 2 || len(marked) != 2 || !marked[0] || !marked[1] {
		t.Fatalf("attempts = %d, marked = %v", attempts, marked)
	}
}

func TestPrepareRequestBuildsSharedRequestState(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						Headers: []string{"X-Profile: yes"},
						Query:   []string{"from=profile"},
					},
				},
			},
		},
	}

	prepared, err := c.prepareRequest(
		context.Background(),
		"GET",
		"svc/items",
		"default",
		request.Options{
			ContentType: "json",
			Headers:     []string{"X-Flag: 1"},
			Query:       []string{"flag=1"},
		},
		map[string]any{"name": "Alice"},
		[]string{"X-Extra: 2"},
		false,
		authHandlerOptions{},
		nil,
		false,
		"",
	)
	if err != nil {
		t.Fatalf("prepareRequest() error = %v", err)
	}

	if got, want := prepared.rawURL, "https://api.example.com/items"; got != want {
		t.Fatalf("rawURL = %q, want %q", got, want)
	}
	if got, want := prepared.apiName, "svc"; got != want {
		t.Fatalf("apiName = %q, want %q", got, want)
	}
	if prepared.opts.Transport == nil {
		t.Fatal("expected transport to be pre-built")
	}
	if got, want := strings.Join(prepared.opts.Query, ","), "from=profile,flag=1"; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}

	headers := strings.Join(prepared.opts.Headers, "\n")
	for _, want := range []string{
		"X-Profile: yes",
		"X-Flag: 1",
		"X-Extra: 2",
		"Content-Type: application/json",
	} {
		if !strings.Contains(headers, want) {
			t.Fatalf("headers missing %q:\n%s", want, headers)
		}
	}

	body, err := io.ReadAll(prepared.body)
	if err != nil {
		t.Fatalf("ReadAll(body) error = %v", err)
	}
	if got, want := string(body), `{"name":"Alice"}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestPrepareRequestAppliesURLOverridesAfterAPIExpansion(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://api.example.com",
				URLOverrides: map[string]string{
					"https://api.example.com/": "http://localhost:8080/root/",
				},
				Profiles: map[string]*config.ProfileConfig{
					"local": {
						URLOverrides: map[string]string{
							"https://api.example.com/": "http://localhost:9090/profile/",
						},
					},
				},
			},
		},
	}

	prepared, err := c.prepareRequest(
		context.Background(),
		"GET",
		"svc/items",
		"local",
		request.Options{},
		nil,
		nil,
		false,
		authHandlerOptions{},
		nil,
		false,
		"",
	)
	if err != nil {
		t.Fatalf("prepareRequest() error = %v", err)
	}
	if got, want := prepared.rawURL, "http://localhost:9090/profile/items"; got != want {
		t.Fatalf("rawURL = %q, want %q", got, want)
	}
	if got, want := prepared.apiName, "svc"; got != want {
		t.Fatalf("apiName = %q, want %q", got, want)
	}
}

func TestPrepareRequestBypassesCacheBeforeTransportForUnnamespacedAuth(t *testing.T) {
	c := New()
	prepared, err := c.prepareRequest(
		context.Background(),
		"GET",
		"https://api.example.com/items",
		"default",
		request.Options{
			CacheDir: t.TempDir(),
			OnRequest: func(req *http.Request) error {
				req.Header.Set("Authorization", "Bearer secret")
				return nil
			},
		},
		nil,
		nil,
		false,
		authHandlerOptions{},
		nil,
		false,
		"",
	)
	if err != nil {
		t.Fatalf("prepareRequest() error = %v", err)
	}
	if !prepared.opts.NoCache {
		t.Fatal("expected auth-bearing request without cache namespace to bypass cache before transport construction")
	}
	if prepared.opts.Transport == nil {
		t.Fatal("expected transport to be built")
	}
}

func TestApplyAPIProfileMergesProfileTLSWithFlagPrecedence(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						CACertPath:     "profile-ca.pem",
						ClientCertPath: "profile-client.pem",
						ClientKeyPath:  "profile-key.pem",
					},
				},
			},
		},
	}

	_, _, opts, err := c.applyAPIProfile("svc/items", "default", request.Options{}, authHandlerOptions{})
	if err != nil {
		t.Fatalf("applyAPIProfile: %v", err)
	}
	if opts.CACertPath != "profile-ca.pem" || opts.ClientCertPath != "profile-client.pem" || opts.ClientKeyPath != "profile-key.pem" {
		t.Fatalf("profile TLS options not applied: %#v", opts)
	}

	_, _, opts, err = c.applyAPIProfile("svc/items", "default", request.Options{
		CACertPath:     "flag-ca.pem",
		ClientCertPath: "flag-client.pem",
		ClientKeyPath:  "flag-key.pem",
	}, authHandlerOptions{})
	if err != nil {
		t.Fatalf("applyAPIProfile with flags: %v", err)
	}
	if opts.CACertPath != "flag-ca.pem" || opts.ClientCertPath != "flag-client.pem" || opts.ClientKeyPath != "flag-key.pem" {
		t.Fatalf("CLI flag TLS options should win over profile values: %#v", opts)
	}
}

func TestApplyAPIProfileTreatsMissingDefaultProfileAsImplicit(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"staging": {BaseURL: "https://staging.example.com"},
				},
			},
		},
	}
	rawURL, apiName, _, err := c.applyAPIProfile("svc/items", "default", request.Options{}, authHandlerOptions{})
	if err != nil {
		t.Fatalf("applyAPIProfile default: %v", err)
	}
	if apiName != "svc" || rawURL != "https://api.example.com/items" {
		t.Fatalf("match = %q %q, want svc default URL", apiName, rawURL)
	}
	_, _, _, err = c.applyAPIProfile("svc/items", "missing", request.Options{}, authHandlerOptions{})
	if err == nil || !strings.Contains(err.Error(), `profile "missing" not found`) {
		t.Fatalf("missing named profile error = %v", err)
	}
}

func TestClosePreparedTransportStopsContextCloser(t *testing.T) {
	c := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closer := &countingCloser{}
	stopClose := context.AfterFunc(ctx, func() {
		_ = closer.Close()
	})

	c.closePreparedTransport(&preparedRequest{closer: closer, stopClose: stopClose})
	if closer.closes != 1 {
		t.Fatalf("Close calls = %d, want 1", closer.closes)
	}

	cancel()
	if closer.closes != 1 {
		t.Fatalf("context cancellation closed transport again: %d", closer.closes)
	}
}

func TestPrepareRequestNoAuthStripsCredentials(t *testing.T) {
	c := New()
	c.AddAuthHandler("test", testAuthHandler{})
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						Headers: []string{
							"Authorization: stale",
							"Cookie: session=abc",
							"X-Profile: kept",
						},
						Query: []string{
							"api_key=secret",
							"token=secret",
							"view=summary",
						},
						Auth: &config.AuthConfig{
							Type: "test",
							Params: map[string]string{
								"token": "secret",
							},
						},
					},
				},
			},
		},
	}

	prepared, err := c.prepareRequest(context.Background(), "GET", "svc/items", "default", request.Options{}, nil, nil, true, authHandlerOptions{}, nil, false, "")
	if err != nil {
		t.Fatalf("prepareRequest() error = %v", err)
	}

	headers := strings.Join(prepared.opts.Headers, "\n")
	if strings.Contains(strings.ToLower(headers), "authorization:") {
		t.Fatalf("authorization header should have been stripped:\n%s", headers)
	}
	if strings.Contains(strings.ToLower(headers), "cookie:") {
		t.Fatalf("cookie header should have been stripped:\n%s", headers)
	}
	if !strings.Contains(headers, "X-Profile: kept") {
		t.Fatalf("expected non-sensitive header to remain:\n%s", headers)
	}
	if got, want := strings.Join(prepared.opts.Query, ","), "view=summary"; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}

	req, err := http.NewRequest(http.MethodGet, prepared.rawURL, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if prepared.opts.OnRequest != nil {
		if err := prepared.opts.OnRequest(req); err != nil {
			t.Fatalf("OnRequest() error = %v", err)
		}
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization header = %q, want empty", got)
	}
	if prepared.opts.OnUnauthorized != nil {
		t.Fatal("OnUnauthorized should be nil when noAuth strips credentials")
	}
}

func TestNormalizeHTTPResponseParsesLinks(t *testing.T) {
	c := New()
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/items", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	httpResp := &http.Response{
		StatusCode: 200,
		Proto:      "HTTP/1.1",
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Link":         []string{`<https://api.example.com/items?page=2>; rel="next"`},
		},
		Body:    io.NopCloser(strings.NewReader(`{"items":[1]}`)),
		Request: req,
	}

	resp, err := c.normalizeHTTPResponse(httpResp, 0)
	if err != nil {
		t.Fatalf("normalizeHTTPResponse() error = %v", err)
	}
	if got, want := resp.Links["next"], "https://api.example.com/items?page=2"; got != want {
		t.Fatalf("resp.Links[next] = %v, want %q", got, want)
	}
}

func TestNormalizeHTTPResponseDecodeErrorIncludesPrintableExcerpt(t *testing.T) {
	c := New()
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/items", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	httpResp := &http.Response{
		StatusCode: 200,
		Proto:      "HTTP/1.1",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader("not actually json")),
		Request:    req,
	}

	_, err = c.normalizeHTTPResponse(httpResp, 0)
	if err == nil {
		t.Fatal("expected decode error")
	}
	got := err.Error()
	if !strings.Contains(got, "decoding response body as application/json") || !strings.Contains(got, `body starts with "not actually json"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

type countingBodyLinkParser struct {
	calls int
}

func (p *countingBodyLinkParser) ParseLinks(_ *url.URL, _ http.Header, body any) []hypermedia.Link {
	p.calls++
	if body == nil {
		return nil
	}
	return []hypermedia.Link{{Rel: "self", URI: "https://api.example.com/items/1"}}
}

func TestNormalizeHTTPResponseDefersBodyLinkParsers(t *testing.T) {
	parser := &countingBodyLinkParser{}
	c := New()
	c.linkParsers = []hypermedia.Parser{hypermedia.LinkHeaderParser{}, parser}
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/items", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	httpResp := &http.Response{
		StatusCode: 200,
		Proto:      "HTTP/1.1",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"_links":{"self":{"href":"/items/1"}}}`)),
		Request:    req,
	}

	resp, err := c.normalizeHTTPResponse(httpResp, 0)
	if err != nil {
		t.Fatalf("normalizeHTTPResponse() error = %v", err)
	}
	if parser.calls != 0 {
		t.Fatalf("body parser called during normalize: %d", parser.calls)
	}
	c.ensureBodyLinks(resp)
	if parser.calls != 1 {
		t.Fatalf("body parser calls = %d, want 1", parser.calls)
	}
	if got := resp.Links["self"]; got != "https://api.example.com/items/1" {
		t.Fatalf("resp.Links[self] = %v", got)
	}
}

func TestPrepareRequestMissingProfileReturnsError(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"default": {},
				},
			},
		},
	}

	_, err := c.prepareRequest(context.Background(), "GET", "svc/items", "missing", request.Options{}, nil, nil, false, authHandlerOptions{}, nil, false, "")
	if err == nil {
		t.Fatal("expected missing profile to return an error")
	}
	if !strings.Contains(err.Error(), `profile "missing" not found for API "svc"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFollowCrossesFirstPartyHost(t *testing.T) {
	if !followCrossesFirstPartyHost("https://api.example.com", "https://redirect.example.com/follow") {
		t.Fatal("expected different host to be treated as cross-host")
	}
	if followCrossesFirstPartyHost("https://api.example.com", "https://api.example.com/follow") {
		t.Fatal("expected same host to stay first-party")
	}
	if !followCrossesFirstPartyHost("https://origin.example.com", "https://redirect.example.com/follow") {
		t.Fatal("expected follow host comparison to use the original first-party host")
	}
	if !followCrossesFirstPartyHost("https://api.example.com:8443", "https://api.example.com/follow") {
		t.Fatal("expected same hostname with a different effective port to be cross-origin")
	}
}

func TestApplyAPIProfilePrefersLongestOperationBasePrefix(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"short": {
				BaseURL:       "https://api.example.com/root",
				OperationBase: "/v1",
				Profiles: map[string]*config.ProfileConfig{
					"default": {Headers: []string{"X-API: short"}},
				},
			},
			"long": {
				BaseURL:       "https://api.example.com/root",
				OperationBase: "/v1/admin",
				Profiles: map[string]*config.ProfileConfig{
					"default": {Headers: []string{"X-API: long"}},
				},
			},
		},
	}

	rawURL, apiName, opts, err := c.applyAPIProfile("https://api.example.com/v1/admin/users", "default", request.Options{}, authHandlerOptions{})
	if err != nil {
		t.Fatalf("applyAPIProfile() error = %v", err)
	}
	if rawURL != "https://api.example.com/v1/admin/users" {
		t.Fatalf("rawURL = %q", rawURL)
	}
	if apiName != "long" {
		t.Fatalf("apiName = %q, want %q", apiName, "long")
	}
	if got := strings.Join(opts.Headers, "\n"); !strings.Contains(got, "X-API: long") {
		t.Fatalf("expected longest-prefix headers, got %q", got)
	}
}

func TestApplyAPIProfileMergesHeadersByName(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						Headers: []string{"X-Foo: profile", "X-Keep: profile"},
					},
				},
			},
		},
	}

	_, _, opts, err := c.applyAPIProfile(
		"https://api.example.com/items",
		"default",
		request.Options{Headers: []string{"x-foo: flag", "X-New: flag"}},
		authHandlerOptions{},
	)
	if err != nil {
		t.Fatalf("applyAPIProfile() error = %v", err)
	}
	headers := strings.Join(opts.Headers, "\n")
	for _, want := range []string{"x-foo: flag", "X-Keep: profile", "X-New: flag"} {
		if !strings.Contains(headers, want) {
			t.Fatalf("headers missing %q:\n%s", want, headers)
		}
	}
	if strings.Contains(headers, "X-Foo: profile") {
		t.Fatalf("profile header should be replaced by request header:\n%s", headers)
	}
}

func TestApplyAPIProfileAmbiguousDuplicateBaseURLRunsUnaffiliated(t *testing.T) {
	c := New()
	var errOut bytes.Buffer
	c.Stderr = &errOut
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"a": {
				BaseURL: "https://api.example.com/v1",
				Profiles: map[string]*config.ProfileConfig{
					"default": {},
				},
			},
			"b": {
				BaseURL: "https://api.example.com/v1",
				Profiles: map[string]*config.ProfileConfig{
					"default": {},
				},
			},
		},
	}

	rawURL, apiName, opts, err := c.applyAPIProfile("https://api.example.com/v1/items", "default", request.Options{}, authHandlerOptions{})
	if err != nil {
		t.Fatalf("applyAPIProfile() error = %v", err)
	}
	if rawURL != "https://api.example.com/v1/items" || apiName != "" {
		t.Fatalf("match = (%q, %q), want original URL and no API", rawURL, apiName)
	}
	if opts.CacheNamespace != "" || len(opts.Headers) != 0 || opts.OnRequest != nil {
		t.Fatalf("ambiguous full URL should not apply API metadata: %#v", opts)
	}
	if got := errOut.String(); !strings.Contains(got, "ambiguous API match") || !strings.Contains(got, "proceeding without API profile") {
		t.Fatalf("expected ambiguity warning, got %q", got)
	}
}

func TestApplyAPIProfileAmbiguousDuplicateBaseURLWithExplicitProfileFails(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"a": {BaseURL: "https://api.example.com/v1"},
			"b": {BaseURL: "https://api.example.com/v1"},
		},
	}

	_, _, _, err := c.applyAPIProfile("https://api.example.com/v1/items", "staging", request.Options{}, authHandlerOptions{})
	if err == nil || !strings.Contains(err.Error(), "ambiguous API match") {
		t.Fatalf("expected explicit-profile ambiguity error, got %v", err)
	}
}

func TestApplyAPIProfileMatchesFullBaseURLWithAuthAndNamespace(t *testing.T) {
	c := New()
	c.AddAuthHandler("test", testAuthHandler{})
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://api.example.com/v1",
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						Headers: []string{"X-Profile: yes"},
						Auth: &config.AuthConfig{
							Type:   "test",
							Params: map[string]string{"token": "abc"},
						},
					},
				},
			},
		},
	}

	rawURL, apiName, opts, err := c.applyAPIProfile("https://api.example.com/v1/items", "default", request.Options{}, authHandlerOptions{})
	if err != nil {
		t.Fatalf("applyAPIProfile() error = %v", err)
	}
	if rawURL != "https://api.example.com/v1/items" {
		t.Fatalf("rawURL = %q", rawURL)
	}
	if apiName != "svc" {
		t.Fatalf("apiName = %q, want svc", apiName)
	}
	if got := opts.CacheNamespace; got != "svc:default" {
		t.Fatalf("CacheNamespace = %q, want svc:default", got)
	}
	req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
	if err := opts.OnRequest(req); err != nil {
		t.Fatalf("OnRequest() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer abc" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestApplyAPIProfileMatchesProfileBaseURL(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://prod.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"staging": {
						BaseURL: "https://staging.example.com/api",
						Headers: []string{"X-Stage: yes"},
					},
				},
			},
		},
	}

	_, apiName, opts, err := c.applyAPIProfile("https://staging.example.com/api/items", "staging", request.Options{}, authHandlerOptions{})
	if err != nil {
		t.Fatalf("applyAPIProfile() error = %v", err)
	}
	if apiName != "svc" {
		t.Fatalf("apiName = %q, want svc", apiName)
	}
	if got := strings.Join(opts.Headers, "\n"); !strings.Contains(got, "X-Stage: yes") {
		t.Fatalf("expected staging profile headers, got %q", got)
	}
}

func TestApplyAPIProfilePreservesMixedCaseProfileQuery(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL: "https://api.example.com",
				Profiles: map[string]*config.ProfileConfig{
					"default": {
						Query: []string{"camelCase=value", "X-ID=123"},
					},
				},
			},
		},
	}

	_, _, opts, err := c.applyAPIProfile("svc/items", "default", request.Options{}, authHandlerOptions{})
	if err != nil {
		t.Fatalf("applyAPIProfile() error = %v", err)
	}
	got := strings.Join(opts.Query, ",")
	if !strings.Contains(got, "camelCase=value") || !strings.Contains(got, "X-ID=123") {
		t.Fatalf("profile query = %q, want original case preserved", got)
	}
}

func TestApplyAPIProfileRejectsHostAndPathLookalikes(t *testing.T) {
	c := New()
	c.cfg = &config.Config{
		APIs: map[string]*config.APIConfig{
			"svc": {
				BaseURL:       "https://api.example.com/v1",
				OperationBase: "/api",
				Profiles: map[string]*config.ProfileConfig{
					"default": {Headers: []string{"X-Profile: yes"}},
				},
			},
		},
	}

	for _, rawURL := range []string{
		"https://api.example.com.evil/v1/items",
		"https://api.example.com/v10/items",
		"https://api.example.com/apis/items",
	} {
		_, apiName, opts, err := c.applyAPIProfile(rawURL, "default", request.Options{}, authHandlerOptions{})
		if err != nil {
			t.Fatalf("applyAPIProfile(%q) error = %v", rawURL, err)
		}
		if apiName != "" || len(opts.Headers) != 0 {
			t.Fatalf("applyAPIProfile(%q) matched apiName=%q headers=%v", rawURL, apiName, opts.Headers)
		}
	}
}
