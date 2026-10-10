package request

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rest-sh/restish/v2/internal/cache"
	"github.com/rest-sh/restish/v2/internal/secrets"
	"github.com/sandrolain/httpcache"
)

type closeableTransport struct {
	inner    http.RoundTripper
	closeFns []func() error
}

func (t *closeableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.inner.RoundTrip(req)
}

func (t *closeableTransport) Close() error {
	var firstErr error
	for i := len(t.closeFns) - 1; i >= 0; i-- {
		if err := t.closeFns[i](); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type closeAfterBody struct {
	io.ReadCloser
	closeFn func() error
}

func (c *closeAfterBody) Close() error {
	err := c.ReadCloser.Close()
	if closeErr := c.closeFn(); err == nil {
		err = closeErr
	}
	return err
}

func (c *closeAfterBody) DisableDeadline() bool {
	if d, ok := c.ReadCloser.(interface{ DisableDeadline() bool }); ok {
		return d.DisableDeadline()
	}
	return false
}

type deadlineBody struct {
	io.ReadCloser
	stopTimer func() bool
	fired     *atomic.Bool
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && b.fired != nil && b.fired.Load() && errors.Is(err, context.Canceled) {
		return n, context.DeadlineExceeded
	}
	return n, err
}

func (b *deadlineBody) Close() error {
	if b.stopTimer != nil {
		b.stopTimer()
	}
	return b.ReadCloser.Close()
}

func (b *deadlineBody) DisableDeadline() bool {
	if b.stopTimer == nil {
		return false
	}
	stopped := b.stopTimer()
	b.stopTimer = nil
	return stopped
}

// DisableResponseBodyDeadline removes a body-read deadline installed by Do when
// Options.HeaderTimeoutOnly is set. Stream callers use this after classifying a
// response by headers so a healthy stream is governed by root cancellation,
// EOF, or explicit stream limits rather than by the header wait timeout.
func DisableResponseBodyDeadline(resp *http.Response) bool {
	if resp == nil || resp.Body == nil {
		return false
	}
	if d, ok := resp.Body.(interface{ DisableDeadline() bool }); ok {
		return d.DisableDeadline()
	}
	return false
}

// Options controls per-request behavior derived from CLI flags.
type Options struct {
	// Headers is a list of "Name: Value" header strings to add to the request.
	Headers []string
	// Query is a list of "key=value" query parameter strings to append.
	Query []string
	// Server overrides the scheme and host (e.g. "https://staging.example.com").
	Server string
	// Insecure disables TLS certificate verification.
	Insecure bool
	// Timeout bounds the full request lifetime, including response body reads.
	// Zero means no timeout.
	Timeout time.Duration
	// HeaderTimeoutOnly treats Timeout as a time-to-first-response deadline.
	// Do still installs a body-read deadline by default so bounded callers keep
	// whole-request behavior; stream callers can remove it after reading
	// response headers with DisableResponseBodyDeadline.
	HeaderTimeoutOnly bool
	// ClientCertPath is the PEM client certificate path for mTLS.
	ClientCertPath string
	// ClientKeyPath is the PEM client private key path for mTLS.
	ClientKeyPath string
	// TLSSignerPath is the executable path of a tls-signer plugin for mTLS.
	TLSSignerPath string
	// TLSSignerName records the logical signer name before CLI resolution.
	TLSSignerName string
	// TLSSignerParams holds plugin-specific configuration for the tls-signer.
	TLSSignerParams map[string]string
	// CACertPath is an optional PEM CA bundle to trust in addition to system roots.
	CACertPath string
	// TLSMinVersion constrains the minimum TLS version when connecting over HTTPS.
	TLSMinVersion uint16
	// AcceptHeader, if non-empty, is sent as the Accept request header.
	AcceptHeader string
	// AcceptEncodingHeader, if non-empty, is sent as the Accept-Encoding header.
	AcceptEncodingHeader string
	// ContentType overrides the Content-Type header when a body is present.
	// If empty and a body is present, the caller is responsible for setting
	// the header via Headers.
	ContentType string
	// PreserveHeaderCase keeps caller-supplied header names in Headers as-is
	// instead of using net/http's canonical MIME casing. This is only useful
	// for broken HTTP/1.x servers; HTTP/2 lowercases header names by protocol.
	PreserveHeaderCase bool
	// UserAgent, if non-empty, is sent when the caller has not supplied a
	// User-Agent header.
	UserAgent string
	// OnRequest, if non-nil, is called after all standard headers and query
	// params have been applied, immediately before the request is sent.
	// Auth handlers use this hook to inject credentials.
	OnRequest func(*http.Request) error
	// OnResponse, if non-nil, is called with the raw HTTP response before it is
	// returned to the caller.
	OnResponse func(*http.Response)
	// OnBeforeRequest, if non-nil, is called after all headers, query params,
	// auth, and request middleware have been applied, immediately before the
	// request is sent through the transport.
	OnBeforeRequest func(*http.Request)
	// OnUnauthorized, when non-nil, is used by callers that want to retry once
	// after a 401 with freshly acquired credentials.
	OnUnauthorized func(*http.Request) error
	// OnRetryRequest, when non-nil, refreshes request-bound authentication before
	// each transport retry and same-origin redirect. Dynamic proof schemes use
	// this to avoid replaying a signature bound to another request target.
	OnRetryRequest func(*http.Request) error
	// CacheDir, if non-empty, enables RFC 7234 response caching in that
	// directory.  NoCache overrides this and skips the cache entirely.
	CacheDir string
	// NoCache, when true, bypasses the response cache for this request
	// (no read, no write).
	NoCache bool
	// CacheNamespace partitions cache entries for one API/profile tuple.
	// Embedders that set CacheDir and inject auth headers or credential query
	// params should set this to a stable value such as "<api>:<profile>" or set
	// NoCache for ad hoc credentialed requests.
	CacheNamespace string
	// CacheMaxBytes is the maximum size of the HTTP response cache in bytes.
	// If zero, defaults to cache.DefaultMaxBytes.
	CacheMaxBytes int64
	// Retry is the maximum number of retry attempts for network errors and
	// selected transient HTTP responses. Zero disables retries.
	Retry int
	// RetryUnsafe allows retrying methods other than GET and HEAD. When false,
	// Retry applies only to safe methods.
	RetryUnsafe bool
	// RetryBaseDelay is the base delay for the first retry backoff interval.
	// Defaults to 1 s when zero.
	RetryBaseDelay time.Duration
	// RetryMaxWait caps server-provided Retry-After/X-Retry-In delays.
	// Defaults to DefaultRetryMaxWait when zero.
	RetryMaxWait time.Duration
	// Logger receives retry progress warnings on stderr-style output.
	Logger io.Writer
	// WrapTransport, when non-nil, wraps the final transport after TLS, retry,
	// and cache layers are applied.
	WrapTransport func(http.RoundTripper) http.RoundTripper
	// Transport, when passed to BuildTransport, is the underlying transport to
	// wrap with TLS/cache/retry behavior. When passed to Do, it is treated as a
	// fully built transport and reused as-is. Callers that make multiple
	// requests with the same options (e.g. pagination) should pre-build one via
	// BuildTransport and set it here so connection pools are reused.
	Transport http.RoundTripper
}

// Do executes an HTTP request and returns the response.
// The caller is responsible for closing resp.Body.
func Do(ctx context.Context, method, rawURL string, body io.Reader, opts Options) (*http.Response, error) {
	u, err := Normalize(rawURL, opts.Server)
	if err != nil {
		return nil, err
	}

	requestCtx := ctx
	var cancelRequest context.CancelFunc
	cancelOnReturn := false
	var responseDeadline time.Time
	if opts.Timeout > 0 {
		if opts.HeaderTimeoutOnly {
			requestCtx, cancelRequest = context.WithCancel(ctx)
			responseDeadline = time.Now().Add(opts.Timeout)
		} else {
			requestCtx, cancelRequest = context.WithTimeout(ctx, opts.Timeout)
		}
		cancelOnReturn = true
		defer func() {
			if cancelOnReturn && cancelRequest != nil {
				cancelRequest()
			}
		}()
	}

	req, err := http.NewRequestWithContext(requestCtx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	if opts.AcceptHeader != "" {
		req.Header.Set("Accept", opts.AcceptHeader)
	}
	if opts.AcceptEncodingHeader != "" {
		req.Header.Set("Accept-Encoding", opts.AcceptEncodingHeader)
	}

	// Apply extra headers. First colon separates name from value so header
	// values that contain colons are handled correctly.
	for _, h := range opts.Headers {
		name, value, err := ParseHeaderOption(h)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(name, "Accept") || strings.EqualFold(name, "Accept-Encoding") {
			setRequestHeader(req.Header, name, value, opts.PreserveHeaderCase)
			continue
		}
		if strings.EqualFold(name, "Host") {
			req.Host = value
			continue
		}
		addRequestHeader(req.Header, name, value, opts.PreserveHeaderCase)
	}

	// Append extra query parameters.
	if len(opts.Query) > 0 {
		q := req.URL.Query()
		for _, kv := range opts.Query {
			key, value, err := ParseQueryOption(kv)
			if err != nil {
				return nil, err
			}
			q.Add(key, value)
		}
		req.URL.RawQuery = q.Encode()
	}
	if opts.OnRequest != nil {
		if err := opts.OnRequest(req); err != nil {
			return nil, fmt.Errorf("auth: %w", err)
		}
	}
	if opts.UserAgent != "" && getRequestHeader(req.Header, "User-Agent") == "" {
		req.Header.Set("User-Agent", opts.UserAgent)
	}
	if opts.OnBeforeRequest != nil {
		opts.OnBeforeRequest(req)
	}
	if opts.CacheNamespace == "" && (requestHasCredentialHeaders(req) || HasCredentialQuery(req.URL)) {
		// This late cache bypass only affects callers that have not already built
		// opts.Transport. The CLI decides its cache namespace before constructing
		// the shared transport so authenticated API-profile requests can cache
		// within their profile-specific namespace.
		opts.NoCache = true
	}

	transport := opts.Transport
	builtTransport := false
	if transport == nil {
		transport = BuildTransport(opts)
		builtTransport = true
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(redirected *http.Request, via []*http.Request) error {
			if err := credentialStrippingRedirectPolicy(redirected, via); err != nil {
				return err
			}
			if len(via) > 0 && SameOrigin(via[len(via)-1].URL, redirected.URL) && opts.OnRetryRequest != nil {
				return opts.OnRetryRequest(redirected)
			}
			return nil
		},
	}

	resp, err := doWithResponseTimeout(client, req, opts.Timeout, opts.HeaderTimeoutOnly, cancelRequest)
	if err != nil {
		if cancelRequest != nil {
			cancelRequest()
		}
		if builtTransport {
			if closer, ok := transport.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
		return nil, redactedRequestError(err, req)
	}
	var closeFns []func() error
	if cancelRequest != nil {
		closeFns = append(closeFns, func() error {
			cancelRequest()
			return nil
		})
	}
	if builtTransport {
		if closer, ok := transport.(interface{ Close() error }); ok {
			closeFns = append(closeFns, closer.Close)
		}
	}
	if len(closeFns) > 0 {
		if resp.Body != nil {
			if opts.Timeout > 0 && opts.HeaderTimeoutOnly && !responseDeadline.IsZero() {
				remaining := time.Until(responseDeadline)
				if remaining <= 0 {
					remaining = time.Nanosecond
				}
				deadlineFired := &atomic.Bool{}
				timer := time.AfterFunc(remaining, func() {
					deadlineFired.Store(true)
					if cancelRequest != nil {
						cancelRequest()
					}
				})
				resp.Body = &deadlineBody{ReadCloser: resp.Body, stopTimer: timer.Stop, fired: deadlineFired}
			}
			resp.Body = &closeAfterBody{ReadCloser: resp.Body, closeFn: func() error {
				var firstErr error
				for _, closeFn := range closeFns {
					if err := closeFn(); err != nil && firstErr == nil {
						firstErr = err
					}
				}
				return firstErr
			}}
		} else {
			for _, closeFn := range closeFns {
				_ = closeFn()
			}
		}
	}
	cancelOnReturn = false
	return resp, nil
}

func setRequestHeader(header http.Header, name, value string, preserveCase bool) {
	if !preserveCase {
		header.Set(name, value)
		return
	}
	deleteHeaderCaseInsensitive(header, name)
	header[name] = []string{value}
}

func addRequestHeader(header http.Header, name, value string, preserveCase bool) {
	if !preserveCase {
		header.Add(name, value)
		return
	}
	header[name] = append(header[name], value)
}

func deleteHeaderCaseInsensitive(header http.Header, name string) {
	for existing := range header {
		if strings.EqualFold(existing, name) {
			delete(header, existing)
		}
	}
}

func getRequestHeader(header http.Header, name string) string {
	if value := header.Get(name); value != "" {
		return value
	}
	for existing, values := range header {
		if strings.EqualFold(existing, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

type doResult struct {
	resp *http.Response
	err  error
}

func doWithResponseTimeout(client *http.Client, req *http.Request, timeout time.Duration, headerOnly bool, cancel context.CancelFunc) (*http.Response, error) {
	if cancel == nil {
		return client.Do(req)
	}

	resultCh := make(chan doResult, 1)
	go func() {
		resp, err := client.Do(req)
		result := doResult{resp: resp, err: err}
		select {
		case <-req.Context().Done():
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			return
		default:
		}
		select {
		case resultCh <- result:
		case <-req.Context().Done():
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
	}()

	if !headerOnly {
		select {
		case result := <-resultCh:
			return result.resp, result.err
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-resultCh:
		return result.resp, result.err
	case <-timer.C:
		cancel()
		return nil, context.DeadlineExceeded
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func credentialStrippingRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	stripBodyHeadersAfterBodylessRedirect(req, via)
	prev := via[len(via)-1]
	if prev == nil || SameOrigin(prev.URL, req.URL) {
		return nil
	}
	for name := range req.Header {
		if IsCredentialHeader(name) || IsMarkedCredentialHeader(req, name) {
			delete(req.Header, name)
		}
	}
	return nil
}

// ParseHeaderOption parses a CLI/config "Name: Value" header option.
func ParseHeaderOption(h string) (name, value string, err error) {
	name, value, ok := strings.Cut(h, ":")
	if !ok {
		return "", "", fmt.Errorf("invalid header %q: expected \"Name: Value\" format", h)
	}
	return strings.TrimSpace(name), strings.TrimSpace(value), nil
}

// ParseQueryOption parses a CLI/config "key=value" query option.
func ParseQueryOption(kv string) (key, value string, err error) {
	key, value, ok := strings.Cut(kv, "=")
	if !ok {
		return "", "", fmt.Errorf("invalid query param %q: expected \"key=value\" format", kv)
	}
	return key, value, nil
}

func stripBodyHeadersAfterBodylessRedirect(req *http.Request, via []*http.Request) {
	if req == nil || req.Body != nil || len(via) == 0 {
		return
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return
	}
	prev := via[len(via)-1]
	if prev == nil || prev.Method == req.Method {
		return
	}
	req.Header.Del("Content-Type")
	req.Header.Del("Content-Encoding")
	req.Header.Del("Content-Length")
}

func requestHasCredentialHeaders(req *http.Request) bool {
	if req == nil {
		return false
	}
	for name := range req.Header {
		if IsCredentialHeader(name) {
			return true
		}
	}
	return false
}

// IsCredentialHeader reports whether a header commonly carries credentials or
// other secrets and should be redacted or stripped at trust boundaries.
func IsCredentialHeader(name string) bool {
	return secrets.IsHeaderName(name)
}

// HasCredentialQuery reports whether u contains query parameters that commonly
// carry credentials or other secrets.
func HasCredentialQuery(u *url.URL) bool {
	if u == nil {
		return false
	}
	for name := range u.Query() {
		if IsCredentialQueryParam(name) {
			return true
		}
	}
	return false
}

// IsCredentialQueryParam reports whether a query parameter commonly carries
// credentials or other secrets.
func IsCredentialQueryParam(name string) bool {
	return secrets.IsQueryParamName(name)
}

// RedactedURL returns u as a string with URL userinfo and credential query
// values replaced by placeholders. Non-sensitive query parameters and URL
// structure are preserved.
func RedactedURL(u *url.URL) string {
	return redactedURL(u, nil)
}

func redactedRequestError(err error, req *http.Request) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	redacted := *urlErr
	redacted.URL = RedactedRequestURL(req)
	return &redacted
}

// SameOrigin reports whether a and b share scheme, hostname, and effective port.
func SameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	aPort, aOK := EffectivePort(a)
	bPort, bOK := EffectivePort(b)
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		aOK && bOK &&
		aPort == bPort
}

// EffectivePort returns the explicit or scheme-default port used for origin
// comparison. Unknown schemes without an explicit port have no safe effective
// port and return ok=false.
func EffectivePort(u *url.URL) (port string, ok bool) {
	if u == nil {
		return "", false
	}
	if port := u.Port(); port != "" {
		return port, true
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80", true
	case "https":
		return "443", true
	default:
		return "", false
	}
}

// newTransport returns an HTTP transport based on http.DefaultTransport.
// Cloning from the default preserves proxy settings (HTTP_PROXY, HTTPS_PROXY,
// NO_PROXY) and other production-appropriate defaults.
func newTransport(opts Options) (http.RoundTripper, error) {
	if opts.Transport != nil {
		if tr, ok := opts.Transport.(*http.Transport); ok {
			cloned := tr.Clone()
			cfg, cleanup, err := TLSConfigWithCleanupFromOptions(opts)
			if err != nil {
				return nil, err
			}
			if cfg.InsecureSkipVerify || cfg.MinVersion != 0 || len(cfg.Certificates) > 0 || cfg.RootCAs != nil {
				cloned.TLSClientConfig = cfg
			}
			return wrapTransportWithCleanup(cloned, cleanup), nil
		}
		if hasTLSOptions(opts) {
			return nil, fmt.Errorf("custom base transport does not support TLS option overrides")
		}
		return opts.Transport, nil
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	cfg, cleanup, err := TLSConfigWithCleanupFromOptions(opts)
	if err != nil {
		return nil, err
	}
	if cfg.InsecureSkipVerify || cfg.MinVersion != 0 || len(cfg.Certificates) > 0 || cfg.RootCAs != nil {
		tr.TLSClientConfig = cfg
	}
	return wrapTransportWithCleanup(tr, cleanup), nil
}

func hasTLSOptions(opts Options) bool {
	return opts.Insecure ||
		opts.ClientCertPath != "" ||
		opts.ClientKeyPath != "" ||
		opts.TLSSignerPath != "" ||
		opts.CACertPath != "" ||
		opts.TLSMinVersion != 0
}

// BuildTransport returns the appropriate RoundTripper for opts.
// Layer order (outermost → innermost):
//
//	httpcache.Transport → retryTransport → http.Transport
//
// The retry transport sits below the cache so that only cache misses (real
// server requests) are retried.
func BuildTransport(opts Options) http.RoundTripper {
	base, err := newTransport(opts)
	if err != nil {
		// TLS config invalid; use a small transport that returns the config error.
		return roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, err
		})
	}
	// Wrap with retry if requested.
	var inner http.RoundTripper = base
	if opts.Retry > 0 {
		delay := opts.RetryBaseDelay
		if delay == 0 {
			delay = time.Second
		}
		inner = retryTransport{inner: base, maxRetry: opts.Retry, retryUnsafe: opts.RetryUnsafe, baseDelay: delay, maxWait: opts.RetryMaxWait, logger: opts.Logger, onRetryRequest: opts.OnRetryRequest}
	}

	if opts.NoCache || opts.CacheDir == "" {
		return finalizeTransport(inner, opts)
	}
	maxBytes := opts.CacheMaxBytes
	if maxBytes == 0 {
		maxBytes = cache.DefaultMaxBytes
	}
	dc, err := cache.NewWithLogger(opts.CacheDir, maxBytes, opts.CacheNamespace, opts.Logger)
	if err != nil {
		// Cache unavailable; fall back without caching.
		return finalizeTransport(inner, opts)
	}
	ct := httpcache.NewTransport(restishResponsePolicyCache{Cache: dc})
	ct.Transport = inner
	ct.EnableVarySeparation = true
	ct.IsPublicCache = false
	return finalizeTransport(wrapTransportWithCloseFns(ct, transportCleanup(inner)...), opts)
}

type restishResponsePolicyCache struct {
	httpcache.Cache
}

func (c restishResponsePolicyCache) Set(key string, data []byte) {
	if cachedResponseHasCredentialHeader(data) {
		c.Delete(key)
		return
	}
	c.Cache.Set(key, data)
}

func cachedResponseHasCredentialHeader(data []byte) bool {
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(data)), nil)
	if err != nil {
		return false
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	for name := range resp.Header {
		if IsCredentialHeader(name) {
			return true
		}
	}
	return false
}

func finalizeTransport(final http.RoundTripper, opts Options) http.RoundTripper {
	if opts.WrapTransport != nil {
		final = opts.WrapTransport(final)
	}
	if opts.OnResponse != nil {
		final = responseHookTransport{inner: final, onResponse: opts.OnResponse}
	}
	return final
}

type responseHookTransport struct {
	inner      http.RoundTripper
	onResponse func(*http.Response)
}

func (t responseHookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if resp != nil {
		if resp.Request == nil {
			resp.Request = req
		}
		t.onResponse(resp)
	}
	return resp, err
}

func (t responseHookTransport) Close() error {
	if closer, ok := t.inner.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (t responseHookTransport) CloseIdleConnections() {
	if closer, ok := t.inner.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t responseHookTransport) Unwrap() http.RoundTripper {
	return t.inner
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func wrapTransportWithCleanup(rt http.RoundTripper, cleanup io.Closer) http.RoundTripper {
	closeFns := transportCleanup(rt)
	if cleanup != nil {
		closeFns = append(closeFns, cleanup.Close)
	}
	return wrapTransportWithCloseFns(rt, closeFns...)
}

func wrapTransportWithCloseFns(rt http.RoundTripper, closeFns ...func() error) http.RoundTripper {
	if len(closeFns) == 0 {
		return rt
	}
	return &closeableTransport{inner: rt, closeFns: closeFns}
}

func transportCleanup(rt http.RoundTripper) []func() error {
	var closeFns []func() error
	if closer, ok := rt.(interface{ Close() error }); ok {
		closeFns = append(closeFns, closer.Close)
	}
	if idleCloser, ok := rt.(interface{ CloseIdleConnections() }); ok {
		closeFns = append(closeFns, func() error {
			idleCloser.CloseIdleConnections()
			return nil
		})
	}
	return closeFns
}
