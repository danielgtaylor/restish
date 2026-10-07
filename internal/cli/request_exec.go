package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/hypermedia"
	"github.com/rest-sh/restish/v2/internal/output"
	"github.com/rest-sh/restish/v2/internal/request"
)

type preparedRequest struct {
	rawURL          string
	apiName         string
	opts            request.Options
	body            io.Reader
	bodyRaw         []byte
	bodyContentType string
	actualRequest   *http.Request
	authEnabled     bool
	closer          io.Closer
	stopClose       func() bool
}

func (c *CLI) prepareRequest(
	ctx context.Context,
	method string,
	rawURL, profileName string,
	opts request.Options,
	bodyValue any,
	extraHeaders []string,
	noAuth bool,
	authOpts authHandlerOptions,
	operationAuth *operationAuthPolicy,
	rawBinaryBody bool,
	explicitAPIName string,
) (*preparedRequest, error) {
	opts = cloneRequestOptions(opts)
	opts.HeaderTimeoutOnly = true
	if len(extraHeaders) > 0 {
		opts.Headers = append(opts.Headers, extraHeaders...)
	}
	explicitCredentialContext := requestOptionHeadersContainCredentials(opts.Headers) ||
		requestOptionQueryContainsCredentials(opts.Query) ||
		rawURLQueryContainsCredentials(rawURL)

	var apiName string
	var err error
	if explicitAPIName != "" {
		if c.cfg == nil || c.cfg.APIs == nil || c.cfg.APIs[explicitAPIName] == nil {
			return nil, fmt.Errorf("API %q is not configured", explicitAPIName)
		}
		if shortName, _ := splitAPIShortNameSuffix(rawURL); shortName == explicitAPIName {
			rawURL, _, opts, err = c.applyAPIProfile(rawURL, profileName, opts, authOpts)
		} else {
			_, _, opts, err = c.applyAPIProfile(explicitAPIName, profileName, opts, authOpts)
		}
		if err != nil {
			return nil, err
		}
		apiName = explicitAPIName
	} else {
		rawURL, apiName, opts, err = c.applyAPIProfile(rawURL, profileName, opts, authOpts)
		if err != nil {
			return nil, err
		}
	}
	if !noAuth && operationAuth == nil && apiName != "" && !explicitCredentialContext {
		if matched, ok := c.operationAuthForGenericRequest(ctx, method, rawURL, apiName, profileName); ok {
			noAuth = matched.noAuth
			operationAuth = matched.policy
		}
	}
	explicitOperationAuthOverride := operationAuth != nil && strings.TrimSpace(operationAuth.Override) != ""
	if (!noAuth || explicitOperationAuthOverride) && operationAuth != nil && apiName != "" {
		prof := profileForName(c.cfg.APIs[apiName], profileName)
		policy := *operationAuth
		if noAuth {
			policy.NoAuth = true
		}
		policy.Transport = opts
		selected, handled, err := c.planOperationAuth(apiName, profileName, prof, &policy)
		if err != nil {
			return nil, err
		}
		if handled {
			if explicitOperationAuthOverride {
				noAuth = false
			}
			callbacks, err := c.operationAuthCallbacks(apiName, profileName, selected, authOpts)
			if err != nil {
				return nil, err
			}
			opts.OnRequest = callbacks.OnRequest
			opts.OnUnauthorized = callbacks.OnUnauthorized
		}
	}
	if apiName != "" {
		rewritten, _, err := config.ApplyURLOverrides(rawURL, effectiveURLOverrides(c.cfg.APIs[apiName], profileName))
		if err != nil {
			return nil, fmt.Errorf("url_overrides: %w", err)
		}
		rawURL = rewritten
	}
	if authOpts.NonInteractive && (opts.TLSSignerName != "" || opts.TLSSignerPath != "") {
		return nil, fmt.Errorf("TLS signer plugins are not started during shell completion")
	}
	opts, err = c.resolveTLSSigner(opts)
	if err != nil {
		return nil, err
	}
	if opts.TLSSignerPath != "" {
		if trace := requestTraceFromContext(ctx); trace != nil {
			name := opts.TLSSignerName
			if name == "" {
				name = opts.TLSSignerPath
			}
			trace.AddInfo("Plugin", "tls-signer "+name)
			trace.Step("tls-signer")
		}
	}

	authEnabled := !noAuth && (opts.OnRequest != nil ||
		opts.OnUnauthorized != nil ||
		requestOptionHeadersContainCredentials(opts.Headers) ||
		requestOptionQueryContainsCredentials(opts.Query) ||
		rawURLQueryContainsCredentials(rawURL))

	// When following a cross-host redirect, strip credentials to prevent
	// a compromised plugin from issuing credentialed requests to arbitrary hosts.
	if noAuth {
		opts.OnRequest = nil
		opts.OnUnauthorized = nil
		filtered := opts.Headers[:0]
		for _, h := range opts.Headers {
			name, _, _ := strings.Cut(h, ":")
			if !isSensitiveHeader(name) {
				filtered = append(filtered, h)
			}
		}
		opts.Headers = filtered
		opts.Query = filterCredentialQueryParams(opts.Query)
	}

	hasCredentialContext := opts.OnRequest != nil ||
		requestOptionHeadersContainCredentials(opts.Headers) ||
		requestOptionQueryContainsCredentials(opts.Query) ||
		rawURLQueryContainsCredentials(rawURL) ||
		(!noAuth && len(c.pluginsByHook["request-middleware"]) > 0)
	if explicitCredentialContext || (hasCredentialContext && opts.CacheNamespace == "") {
		opts.NoCache = true
	}

	// Build the transport once so follow-up requests can reuse the same
	// connection pool via the returned opts value.
	opts.Transport = request.BuildTransport(opts)
	var transportCloser io.Closer
	var stopTransportClose func() bool
	if closer, ok := opts.Transport.(io.Closer); ok {
		transportCloser = closer
		stopTransportClose = context.AfterFunc(ctx, func() {
			_ = closer.Close()
		})
	}

	// Chain request-middleware plugins after auth.
	if !noAuth && !authOpts.NonInteractive {
		origOnReq := opts.OnRequest
		opts.OnRequest = func(req *http.Request) error {
			if origOnReq != nil {
				if err := origOnReq(req); err != nil {
					return err
				}
			}
			return c.runRequestMiddlewarePlugins(req)
		}
	}
	var prepared *preparedRequest
	origBeforeRequest := opts.OnBeforeRequest
	opts.OnBeforeRequest = func(req *http.Request) {
		if origBeforeRequest != nil {
			origBeforeRequest(req)
		}
		preparedReq := req.Clone(req.Context())
		preparedReq.Header = req.Header.Clone()
		preparedReq.Body = nil
		preparedReq.GetBody = req.GetBody
		prepared.actualRequest = preparedReq
	}

	bodyRaw, bodyContentType, err := c.requestBodyBytes(opts.ContentType, bodyValue, rawBinaryBody, &opts.Headers)
	if err != nil {
		return nil, fmt.Errorf("encoding request body: %w", err)
	}
	var body io.Reader
	if len(bodyRaw) > 0 {
		body = bytes.NewReader(bodyRaw)
	}

	prepared = &preparedRequest{
		rawURL:          rawURL,
		apiName:         apiName,
		opts:            opts,
		body:            body,
		bodyRaw:         bodyRaw,
		bodyContentType: bodyContentType,
		authEnabled:     authEnabled,
		closer:          transportCloser,
		stopClose:       stopTransportClose,
	}
	return prepared, nil
}

func filterCredentialQueryParams(query []string) []string {
	if len(query) == 0 {
		return query
	}
	filtered := query[:0]
	for _, kv := range query {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			filtered = append(filtered, kv)
			continue
		}
		decoded, err := url.QueryUnescape(name)
		if err == nil {
			name = decoded
		}
		if !isSensitiveQueryParam(name) {
			filtered = append(filtered, kv)
		}
	}
	return filtered
}

func requestOptionHeadersContainCredentials(headers []string) bool {
	for _, h := range headers {
		name, _, _ := strings.Cut(h, ":")
		if isSensitiveHeader(name) {
			return true
		}
	}
	return false
}

func requestOptionQueryContainsCredentials(query []string) bool {
	for _, kv := range query {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		decoded, err := url.QueryUnescape(name)
		if err == nil {
			name = decoded
		}
		if request.IsCredentialQueryParam(name) {
			return true
		}
	}
	return false
}

func rawURLQueryContainsCredentials(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return request.HasCredentialQuery(u)
}

func cloneRequestOptions(opts request.Options) request.Options {
	cloned := opts
	if len(opts.Headers) > 0 {
		cloned.Headers = append([]string(nil), opts.Headers...)
	}
	if len(opts.Query) > 0 {
		cloned.Query = append([]string(nil), opts.Query...)
	}
	if len(opts.TLSSignerParams) > 0 {
		cloned.TLSSignerParams = make(map[string]string, len(opts.TLSSignerParams))
		for k, v := range opts.TLSSignerParams {
			cloned.TLSSignerParams[k] = v
		}
	}
	return cloned
}

func (c *CLI) requestBodyBytes(contentType string, bodyValue any, rawBinary bool, headers *[]string) ([]byte, string, error) {
	if bodyValue == nil {
		return nil, "", nil
	}

	ct := contentType
	if ct == "" {
		ct = "application/json"
	}
	mimeType := c.content.MIMETypeForName(ct)
	if mimeType == "" {
		mimeType = ct
	}
	if rawBinary && isRawBinaryContentType(mimeType) {
		encoded, err := rawBinaryBodyBytes(bodyValue)
		if err != nil {
			return nil, "", err
		}
		*headers = append(*headers, "Content-Type: "+mimeType)
		return encoded, mimeType, nil
	}
	encoded, actualContentType, err := c.content.EncodeWithType(mimeType, bodyValue)
	if err != nil {
		return nil, "", err
	}
	*headers = append(*headers, "Content-Type: "+actualContentType)
	return encoded, actualContentType, nil
}

func rawBinaryBodyBytes(bodyValue any) ([]byte, error) {
	switch value := bodyValue.(type) {
	case nil:
		return nil, nil
	case []byte:
		return value, nil
	case string:
		return []byte(value), nil
	default:
		return nil, fmt.Errorf("raw binary request bodies require string, @file, or redirected stdin input; got %T", bodyValue)
	}
}

func isRawBinaryContentType(contentType string) bool {
	base, _, err := mime.ParseMediaType(contentType)
	if err != nil || base == "" {
		base = strings.TrimSpace(contentType)
	}
	base = strings.ToLower(base)
	return base == "*/*" ||
		base == "application/*" ||
		base == "application/octet-stream" ||
		strings.HasSuffix(base, "+octet-stream") ||
		strings.HasSuffix(base, "/octet-stream")
}

func (c *CLI) sendPreparedRequest(ctx context.Context, method string, prepared *preparedRequest) (*http.Response, error) {
	bodyReader := func() io.Reader {
		if len(prepared.bodyRaw) == 0 {
			return nil
		}
		return bytes.NewReader(prepared.bodyRaw)
	}
	resp, err := request.Do(ctx, method, prepared.rawURL, bodyReader(), prepared.opts)
	if err != nil || resp == nil || resp.StatusCode != http.StatusUnauthorized || prepared.opts.OnUnauthorized == nil {
		return resp, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	retryOpts := cloneRequestOptions(prepared.opts)
	retryOpts.Transport = prepared.opts.Transport
	onUnauthorized := retryOpts.OnUnauthorized
	retryOpts.OnRequest = func(req *http.Request) error {
		if err := onUnauthorized(req); err != nil {
			return err
		}
		return c.runRequestMiddlewarePlugins(req)
	}
	return request.Do(ctx, method, prepared.rawURL, bodyReader(), retryOpts)
}

func (c *CLI) closePreparedTransport(prepared *preparedRequest) {
	if prepared == nil || prepared.closer == nil {
		return
	}
	if prepared.stopClose == nil || prepared.stopClose() {
		_ = prepared.closer.Close()
	}
	prepared.closer = nil
	prepared.stopClose = nil
}

func (c *CLI) normalizeHTTPResponse(httpResp *http.Response, maxBodyBytes int64) (*output.Response, error) {
	resp, err := output.Normalize(httpResp, c.content, maxBodyBytes)
	if err != nil {
		return nil, err
	}

	// httpResp headers/request are still accessible after Normalize has closed
	// and consumed the body.
	if httpResp.Request != nil {
		if links := hypermedia.Parse(httpResp.Request.URL, httpResp.Header, nil, []hypermedia.Parser{hypermedia.LinkHeaderParser{}}); len(links) > 0 {
			resp.Links = make(map[string]any, len(links))
			for k, v := range links {
				resp.Links[k] = v
			}
		}
	}

	return resp, nil
}

func (c *CLI) ensureBodyLinks(resp *output.Response) {
	if resp == nil || resp.URL == "" {
		return
	}
	base, err := url.Parse(resp.URL)
	if err != nil {
		return
	}
	headers := make(http.Header, len(resp.Headers))
	for k, values := range resp.Headers {
		headers[k] = append([]string(nil), values...)
	}
	links := hypermedia.Parse(base, headers, resp.Body, c.linkParsers)
	if len(links) == 0 {
		return
	}
	if resp.Links == nil {
		resp.Links = make(map[string]any, len(links))
	}
	for k, v := range links {
		resp.Links[k] = v
	}
}
