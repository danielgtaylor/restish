package cli

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	urlpath "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/content"
	"github.com/rest-sh/restish/v2/internal/filter"
	"github.com/rest-sh/restish/v2/internal/input"
	"github.com/rest-sh/restish/v2/internal/output"
	internalplugin "github.com/rest-sh/restish/v2/internal/plugin"
	"github.com/rest-sh/restish/v2/internal/request"
	"github.com/rest-sh/restish/v2/internal/secrets"
	"github.com/spf13/cobra"
)

// cacheDir returns the effective HTTP response cache directory, checking the
// CachePath override (used in tests), then RSH_CACHE_DIR, then the default.
func (c *CLI) cacheDir() string {
	if c.hooks.CachePath != "" {
		return c.hooks.CachePath
	}
	return c.configScopedCacheDir(c.paths().Cache())
}

// maxBodyBytes returns the response body size cap derived from the
// --rsh-max-body-size flag (MiB). Zero or negative means use the default.
func maxBodyBytes(cmd *cobra.Command) int64 {
	gf := globalFlagsFromContext(requestContext(cmd))
	if gf.MaxBodySize <= 0 {
		return output.DefaultMaxBodyBytes
	}
	return int64(gf.MaxBodySize) * 1024 * 1024
}

// addHTTPCommands registers the generic HTTP verb commands on root.
func (c *CLI) addHTTPCommands(root *cobra.Command) {
	verbs := []struct {
		name  string
		short string
	}{
		{"get", "Perform an HTTP GET request"},
		{"head", "Perform an HTTP HEAD request"},
		{"options", "Perform an HTTP OPTIONS request"},
		{"post", "Perform an HTTP POST request"},
		{"put", "Perform an HTTP PUT request"},
		{"patch", "Perform an HTTP PATCH request"},
		{"delete", "Perform an HTTP DELETE request"},
	}

	for _, v := range verbs {
		v := v
		method := strings.ToUpper(v.name)
		use := v.name + " <url>"
		switch method {
		case "POST", "PUT", "PATCH":
			use += " [body...]"
		}
		cmd := &cobra.Command{
			Use:     use,
			Aliases: []string{method},
			Short:   v.short,
			Long:    genericHTTPLong(method),
			Example: genericHTTPExamples(c.commandName, v.name),
			GroupID: rootGroupHTTP,
			Annotations: map[string]string{
				requestHelpAnnotation: "true",
			},
			Args:              usageMinimumNArgs(1),
			ValidArgsFunction: c.completeHTTPURL(method),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.runHTTP(cmd, method, args)
			},
		}
		root.AddCommand(cmd)
	}
}

func genericHTTPLong(method string) string {
	switch method {
	case "GET", "HEAD", "OPTIONS":
		return fmt.Sprintf("Perform an HTTP `%s` request against a full URL or registered API short-name URL.\n\n", method) +
			"Use generic HTTP commands for one-off requests, scripting, and APIs that are not registered with `api connect`. Restish still applies global request flags, profile settings for registered API short names, response normalization, output formatting, filtering, retries, caching, pagination, and plugin hooks.\n\n" +
			"Pass request headers with `-H`, query parameters with `-q`, filters with `-f`, and output format with `-o`."
	case "POST", "PUT", "PATCH":
		return fmt.Sprintf("Perform an HTTP `%s` request with optional shorthand, file, or stdin body input.\n\n", method) +
			"Use generic HTTP commands for one-off writes, scripting, and APIs that are not registered with `api connect`. Body arguments use Restish shorthand by default; pass `@file.json`, pipe stdin, or set `--rsh-content-type` when you need a specific wire format.\n\n" +
			"Restish still applies global request flags, response normalization, output formatting, filtering, retries, caching, pagination, and plugin hooks. Unsafe methods are not retried unless you opt in with `--rsh-retry-unsafe`."
	case "DELETE":
		return "Perform an HTTP `DELETE` request against a full URL or registered API short-name URL.\n\n" +
			"Use this for direct delete requests when a generated OpenAPI command is not available or would add friction. Restish still applies global request flags, profile settings for registered API short names, response normalization, output formatting, filtering, and plugin hooks.\n\n" +
			"By default, HTTP error statuses produce non-zero exit codes. Use `--rsh-ignore-status-code` only when a script intentionally handles those responses."
	default:
		return fmt.Sprintf("Perform an HTTP `%s` request with Restish's request, output, and plugin pipeline.", method)
	}
}

func genericHTTPExamples(commandName, verb string) string {
	if commandName == "" {
		commandName = "restish"
	}
	switch verb {
	case "get":
		return fmt.Sprintf("  %s get https://api.example.com/items\n  %s get https://api.example.com/items -f body.items -o table", commandName, commandName)
	case "post":
		return fmt.Sprintf("  %s post https://api.example.com/items 'name: Ada, active: true'\n  %s post -c json https://api.example.com/items @item.json", commandName, commandName)
	case "put":
		return fmt.Sprintf("  %s put https://api.example.com/items/123 'name: Ada'\n  %s put -c json https://api.example.com/items/123 @item.json", commandName, commandName)
	case "patch":
		return fmt.Sprintf("  %s patch https://api.example.com/items/123 'active: false'\n  %s patch https://api.example.com/items/123 -H 'If-Match: abc123' 'active: false'", commandName, commandName)
	case "delete":
		return fmt.Sprintf("  %s delete https://api.example.com/items/123\n  %s delete https://api.example.com/items/123 --rsh-ignore-status-code", commandName, commandName)
	default:
		return fmt.Sprintf("  %s %s https://api.example.com/items", commandName, verb)
	}
}

// runHTTP reads global flags, executes the HTTP request, normalizes the
// response, formats it, and handles exit codes.
func (c *CLI) runHTTP(cmd *cobra.Command, method string, args []string) error {
	return c.runHTTPWithOptions(cmd, method, args, false, nil, false, "", "", requestBodyOptions{})
}

func (c *CLI) runInferredHTTP(cmd *cobra.Command, args []string) error {
	return c.runHTTPWithOptions(cmd, "", args, false, nil, false, "", "", requestBodyOptions{})
}

func (c *CLI) validateHTTPOutputFlags(cmd *cobra.Command, gf GlobalFlags) error {
	if _, err := c.resolvePrintSpec(gf, c.stdoutIsTerminal(), printBoundedResponse); err != nil {
		return err
	}
	if gf.OutputFormat == "" {
		return nil
	}
	_, err := c.selectFormatter(cmd, gf.OutputFormat, c.stdoutIsTerminal())
	return err
}

type requestBodyOptions struct {
	multipartPartContentTypes map[string]string
	acceptOverride            string
	operationAuth             *operationAuthPolicy
	explicitAPIName           string
	validationSchema          map[string]any
	validationMediaType       string
	validationSchemaDialect   string
	validationRequested       bool
	bodyRequired              bool
	rawBinaryBody             bool
	bodyOverrideSet           bool
	bodyOverride              any
}

// runHTTPWithOptions executes one HTTP request through the full pipeline:
// auth, request middleware, retries, streaming, response middleware,
// pagination, and formatting.
//
// followMode=true is used for follow-up requests triggered by
// response-middleware plugins; in that mode, response-middleware is skipped to
// prevent infinite loops. extraHeaders holds additional "Name: Value" strings
// injected by generated commands (e.g. OpenAPI header/cookie parameters) that
// must not be stored on the cobra.Command itself since command objects are
// reused across invocations. noAuth strips authentication when following a
// redirect to a different host, preventing credentialed SSRF via a compromised
// response-middleware plugin.
func (c *CLI) runHTTPWithOptions(cmd *cobra.Command, method string, args []string, followMode bool, extraHeaders []string, noAuth bool, firstPartyHost string, contentTypeOverride string, bodyOpts requestBodyOptions) error {
	gf := globalFlagsFromContext(requestContext(cmd))
	if err := c.validateHTTPOutputFlags(cmd, gf); err != nil {
		return err
	}
	c.requestExecutionStarted = true
	trace := ensureRequestTrace(cmd)
	rawURL := args[0]
	bodyArgs := args[1:] // positional args after the URL are shorthand body input

	opts, err := c.httpOptsFromFlags(cmd)
	if err != nil {
		return err
	}
	if bodyOpts.explicitAPIName != "" && c.apiPreservesHeaderCase(bodyOpts.explicitAPIName) {
		opts.PreserveHeaderCase = true
	}
	if opts.ContentType == "" && contentTypeOverride != "" {
		opts.ContentType = contentTypeOverride
	}
	if bodyOpts.acceptOverride != "" {
		opts.AcceptHeader = bodyOpts.acceptOverride
	}

	// Resolve API short names and merge persistent profile settings.
	profileName := c.profileFromCmd(cmd)
	authOpts, err := c.authHandlerOptionsFromCmd(cmd)
	if err != nil {
		return err
	}
	var apiName string

	var bodyVal any
	var bodyInfo input.BodyInfo
	if bodyOpts.bodyOverrideSet {
		bodyVal = bodyOpts.bodyOverride
	} else {
		// Build request body from shorthand args and/or piped stdin.
		stdinIsTTY := output.IsTerminalReader(c.Stdin)
		var err error
		bodyVal, bodyInfo, err = input.BodyWithInfo(c.Stdin, stdinIsTTY, bodyArgs, opts.ContentType, input.BodyOptions{
			Warnf: c.warnf,
		})
		if err != nil {
			return fmt.Errorf("building request body: %w", err)
		}
	}
	if bodyOpts.bodyRequired && bodyVal == nil {
		return fmt.Errorf("request body is required; pass body arguments, pipe a body on stdin, or run %q for an example", cmd.CommandPath()+" --rsh-generate-body")
	}
	if len(bodyOpts.multipartPartContentTypes) > 0 && strings.HasPrefix(strings.ToLower(opts.ContentType), "multipart/form-data") {
		bodyVal = content.MultipartBody{Value: bodyVal, ContentTypes: bodyOpts.multipartPartContentTypes}
	}
	if bodyOpts.validationRequested && bodyVal != nil {
		if err := validateGeneratedJSONBody(bodyVal, opts.ContentType, bodyOpts.validationMediaType, bodyOpts.validationSchema, bodyOpts.validationSchemaDialect, output.ColorEnabled(c.Stderr)); err != nil {
			return err
		}
	}
	inputSource := traceInputSource(bodyInfo, bodyVal != nil)
	if bodyVal != nil {
		trace.Step(inputSource)
	}
	if method == "" {
		method = "GET"
		if bodyVal != nil {
			method = "POST"
		}
	}

	prepared, err := c.prepareRequest(requestContext(cmd), method, rawURL, profileName, opts, bodyVal, extraHeaders, noAuth, authOpts, bodyOpts.operationAuth, bodyOpts.rawBinaryBody, bodyOpts.explicitAPIName)
	if err != nil {
		return err
	}
	defer c.closePreparedTransport(prepared)
	rawURL = prepared.rawURL
	apiName = prepared.apiName
	opts = prepared.opts
	c.populateRequestTrace(trace, apiName, profileName, inputSource, prepared)
	trace.RenderBefore(c.Stderr, globalFlagsFromContext(requestContext(cmd)).Verbose)
	c.warnRetryUnsafe(method, opts)
	if firstPartyHost == "" {
		if u, parseErr := url.Parse(prepared.rawURL); parseErr == nil {
			firstPartyHost = u.Scheme + "://" + u.Host
		}
	}

	httpResp, err := c.sendPreparedRequest(requestContext(cmd), method, prepared)
	if err != nil {
		if isLocalRequestExecutionError(err) {
			return err
		}
		networkURL := redactedNetworkErrorURL(rawURL, opts.Server)
		if hint := networkErrorHint(err); hint != "" {
			return fmt.Errorf("network error for %s %s: %w\nhint: %s", method, networkURL, err, hint)
		}
		return fmt.Errorf("network error for %s %s: %w", method, networkURL, err)
	}
	trace.Step("HTTP")

	// Streaming responses (SSE, NDJSON) are handled before body normalization.
	if kind := streamingContentType(httpResp.Header.Get("Content-Type")); kind != "" {
		request.DisableResponseBodyDeadline(httpResp)
		traceContentDecode(trace, httpResp.Header.Get("Content-Type"))
		gf := globalFlagsFromContext(requestContext(cmd))
		if gf.Silent {
			_ = httpResp.Body.Close()
			return c.statusError(cmd, httpResp.StatusCode)
		}
		spec, specErr := c.resolvePrintSpec(gf, c.stdoutIsTerminal(), printStreamResponse)
		if specErr != nil {
			_ = httpResp.Body.Close()
			return specErr
		}
		if spec.rawBodyOnly() {
			if err := c.statusError(cmd, httpResp.StatusCode); err != nil {
				_ = httpResp.Body.Close()
				return err
			}
			reader, err := c.decompressedResponseBody(httpResp)
			if err != nil {
				_ = httpResp.Body.Close()
				return fmt.Errorf("decompressing response: %w", err)
			}
			defer reader.Close()
			trace.Info("Output", "raw")
			trace.Step("raw")
			trace.RenderAfter(c.Stderr, gf.Verbose)
			_, err = io.Copy(c.Stdout, reader)
			return err
		}
		if err := c.statusError(cmd, httpResp.StatusCode); err != nil {
			_ = httpResp.Body.Close()
			return err
		}
		if spec.includesResponseBody() {
			body, err := c.decompressedResponseBody(httpResp)
			if err != nil {
				_ = httpResp.Body.Close()
				return fmt.Errorf("decompressing response: %w", err)
			}
			httpResp.Body = body
		}
		var streamErr error
		switch kind {
		case "sse":
			streamErr = c.handleSSE(cmd, httpResp, prepared, spec)
		case "ndjson":
			streamErr = c.handleNDJSON(cmd, httpResp, prepared, spec)
		}
		if streamErr != nil {
			return streamErr
		}
		return nil
	}

	// Decide whether this response needs decoded/interpreted body handling
	// before normalizing. Raw downloads must not unmarshal JSON/CBOR/YAML, and
	// explicit body-free print specs should not block on or validate the body.
	tty := c.stdoutIsTerminal()
	printSpec, err := c.resolvePrintSpec(gf, tty, printBoundedResponse)
	if err != nil {
		_ = httpResp.Body.Close()
		return err
	}
	if gf.Silent {
		_ = httpResp.Body.Close()
		return c.statusError(cmd, httpResp.StatusCode)
	}
	if printSpec.rawBodyOnly() {
		defer httpResp.Body.Close()
		var progress *downloadProgress
		if gf.Verbose == 0 {
			progress = c.newDownloadProgress(requestContext(cmd), httpResp)
		}
		raw, err := c.rawResponseBodyBytes(httpResp, maxBodyBytes(cmd), progress)
		if err != nil {
			progress.finish(err)
			return responseBodyReadError(method, rawURL, err)
		}
		if gf.Verbose >= 1 {
			c.logVerboseBody("< body", raw, httpResp.Header.Get("Content-Type"))
		}
		trace.Info("Output", "raw")
		trace.Step("raw")
		trace.RenderAfter(c.Stderr, gf.Verbose)
		if err := c.writeRawBytes(raw); err != nil {
			progress.finish(err)
			return err
		}
		progress.finish(nil)
		return c.statusError(cmd, httpResp.StatusCode)
	}
	if c.canPrintWithoutResponseBody(gf, printSpec) {
		resp := responseMetadataOnly(httpResp)
		if err := c.formatResponse(cmd, resp, prepared); err != nil {
			_ = httpResp.Body.Close()
			return err
		}
		_ = httpResp.Body.Close()
		return c.statusError(cmd, resp.Status)
	}
	if handled, streamErr := c.handleMislabeledJSONLines(cmd, httpResp, prepared, printSpec); handled || streamErr != nil {
		if handled {
			traceContentDecode(trace, httpResp.Header.Get("Content-Type"))
		}
		return streamErr
	}

	resp, err := c.normalizeHTTPResponse(httpResp, maxBodyBytes(cmd))
	if err != nil {
		return responseBodyReadError(method, rawURL, err)
	}
	traceContentDecode(trace, output.Header(resp.Headers, "Content-Type"))
	if v := globalFlagsFromContext(requestContext(cmd)).Verbose; v >= 1 {
		c.logVerboseResponseBody(resp)
	}

	// Response-middleware plugins: can modify, drop, or follow.
	// Skipped in follow mode to prevent infinite loops.
	if !followMode && httpResp.Request != nil && !printSpec.rawBodyOnly() {
		drop, followReq, mwErr := c.runResponseMiddlewarePlugins(requestContext(cmd), httpResp.Request, resp)
		if mwErr != nil {
			return mwErr
		}
		if drop {
			return nil
		}
		if followReq != nil {
			crossHost := followCrossesFirstPartyHost(firstPartyHost, followReq.URI)
			if crossHost {
				c.warnf("response-middleware follow to different host %q — stripping credentials", followReq.URI)
			}
			followHeaders, followContentType := followRequestHeaders(followReq)
			if followReq.ContentType != "" {
				followContentType = followReq.ContentType
			}
			return c.runHTTPWithOptions(cmd, followReq.Method, []string{followReq.URI}, true, followHeaders, crossHost, firstPartyHost, followContentType, requestBodyOptions{
				bodyOverrideSet: true,
				bodyOverride:    followReq.Body,
			})
		}
	}

	// Pagination: if this is a GET and there's a next link, paginate.
	if method == "GET" && printSpec.includesResponseBody() && !printSpec.rawBodyOnly() && !gf.HeadersShorthand && !filterRequestsResponseMetadata(gf.Filter) {
		var pagCfg *config.PaginationConfig
		if apiName != "" && c.cfg != nil && c.cfg.APIs[apiName] != nil {
			pagCfg = c.cfg.APIs[apiName].Pagination
		}
		did, err := c.tryPaginate(cmd, resp, rawURL, opts, pagCfg, prepared, bodyOpts.explicitAPIName != "")
		if err != nil {
			return err
		}
		if did {
			return nil
		}
	}

	if err := c.formatResponse(cmd, resp, prepared); err != nil {
		return err
	}

	return c.statusError(cmd, resp.Status)
}

func (c *CLI) statusError(cmd *cobra.Command, status int) error {
	if globalFlagsFromContext(requestContext(cmd)).IgnoreStatus {
		return nil
	}
	if code := output.StatusToExitCode(status); code != 0 {
		return &ExitCodeError{Code: code}
	}
	return nil
}

func followCrossesFirstPartyHost(firstPartyOrigin, followURI string) bool {
	if firstPartyOrigin == "" {
		return false
	}
	followU, err := url.Parse(followURI)
	if err != nil {
		return false
	}
	baseU, err := url.Parse(firstPartyOrigin)
	if err != nil || baseU.Scheme == "" {
		return !strings.EqualFold(followU.Hostname(), firstPartyOrigin)
	}
	return !request.SameOrigin(baseU, followU)
}

func followRequestHeaders(followReq *HookFollowRequest) ([]string, string) {
	if followReq == nil || len(followReq.Headers) == 0 {
		return nil, ""
	}
	headers := make([]string, 0, len(followReq.Headers))
	var contentType string
	for name, value := range followReq.Headers {
		if strings.EqualFold(name, "Content-Type") && followReq.Body != nil {
			contentType = value
			continue
		}
		headers = append(headers, name+": "+value)
	}
	return headers, contentType
}

// formatResponse applies any filter then selects and runs the formatter.
func (c *CLI) formatResponse(cmd *cobra.Command, resp *output.Response, prepared *preparedRequest) error {
	// Silent mode: suppress all output.
	gf := globalFlagsFromContext(requestContext(cmd))
	if gf.Silent {
		return nil
	}
	printPlan, err := c.resolvePrintSpec(gf, c.stdoutIsTerminal(), printBoundedResponse)
	if err != nil {
		return err
	}

	filterExpr := gf.Filter
	explicitFilter := explicitOutputFilter(gf)
	filterLang := gf.FilterLang
	headersOnly := gf.HeadersShorthand
	statusOnly := gf.StatusShorthand

	if printPlan.rawBodyOnly() {
		if resp.Raw == nil && resp.Body != nil && !gf.PrintSet {
			printPlan = printSpec{order: []rune{printRenderedBody}}
		} else {
			trace := requestTraceFromContext(requestContext(cmd))
			trace.Info("Output", "raw")
			trace.Step("raw")
			trace.RenderAfter(c.Stderr, gf.Verbose)
			return c.writeRawBytes(resp.Raw)
		}
	}

	if headersOnly && filterExpr != "" {
		c.warnf(`--rsh-headers overrides -f; using "headers"`)
	}
	if statusOnly && filterExpr != "" {
		c.warnf(`--rsh-status overrides -f; using "status"`)
	}
	if headersOnly && statusOnly {
		c.warnf(`--rsh-status overrides --rsh-headers; using "status"`)
		headersOnly = false
	}
	if headersOnly {
		filterExpr = "headers"
	}
	if statusOnly {
		filterExpr = "status"
	}

	if filterExpr == "" {
		if err := c.printResponseParts(cmd, resp, prepared, printPlan, nil, false); err != nil {
			return err
		}
		if trace := requestTraceFromContext(requestContext(cmd)); trace != nil {
			trace.RenderAfter(c.Stderr, gf.Verbose)
		}
		return nil
	}

	if explicitFilter && filterNeedsLinks(filterExpr) {
		c.ensureBodyLinks(resp)
	}

	if filterExpr == "@" {
		doc := normalizedResponseDoc(resp)
		return c.printResponseParts(cmd, resp, prepared, printPlan, doc, true)
	}

	lang := resolveFilterLang(filterLang)

	doc := normalizedResponseDoc(resp)

	filterResult, err := filter.ApplyWithInfo(filterExpr, doc, lang)
	if err != nil {
		return fmt.Errorf("filter: %w", err)
	}
	filtered := filterResult.Value
	if explicitFilter && gf.Verbose >= 1 {
		trace := requestTraceFromContext(requestContext(cmd))
		traceFilter(trace, lang, filterResult.Lang)
	}

	if filtered == nil && shouldSuggestBodyPrefix(filterExpr) {
		c.hintBodyPrefixOnce(filterExpr)
	} else if filtered == nil && shouldSuggestJQBodyRoot(filterExpr, lang) {
		c.hintJQBodyRootOnce(filterExpr)
	}

	return c.printResponseParts(cmd, resp, prepared, printPlan, filtered, explicitFilter)
}

func normalizedResponseDoc(resp *output.Response) map[string]any {
	return map[string]any{
		"proto":       resp.Proto,
		"status":      resp.Status,
		"headers":     firstHeaderValues(resp.Headers),
		"headers_all": allHeaderValues(resp.Headers),
		"links":       resp.Links,
		"body":        resp.Body,
	}
}

func firstHeaderValues(headers map[string][]string) map[string]any {
	out := make(map[string]any, len(headers))
	for k, values := range headers {
		if len(values) > 0 {
			out[k] = values[0]
		}
	}
	return out
}

func allHeaderValues(headers map[string][]string) map[string]any {
	out := make(map[string]any, len(headers))
	for k, values := range headers {
		all := make([]any, len(values))
		for i, value := range values {
			all[i] = value
		}
		out[k] = all
	}
	return out
}

func resolveFilterLang(filterLang string) filter.Lang {
	switch strings.ToLower(filterLang) {
	case "shorthand":
		return filter.LangShorthand
	case "jq":
		return filter.LangJQ
	default:
		return filter.LangAuto
	}
}

func (c *CLI) populateRequestTrace(trace *requestTrace, apiName, profileName, inputSource string, prepared *preparedRequest) {
	if trace == nil || prepared == nil {
		return
	}
	trace.InfoBefore("Config", c.configFilePath())
	if apiName != "" {
		trace.InfoBefore("API", apiName)
	}
	trace.InfoBefore("Profile", profileName)
	if prepared.authEnabled {
		trace.InfoBefore("Auth", "enabled")
	} else {
		trace.InfoBefore("Auth", "none")
	}
	trace.InfoBefore("Input", inputSource)
	if prepared.bodyContentType != "" {
		trace.InfoBefore("Request body", traceMediaType(prepared.bodyContentType))
		trace.Step(traceMediaType(prepared.bodyContentType))
	} else {
		trace.InfoBefore("Request body", "none")
	}
	if prepared.authEnabled {
		trace.Step("auth")
	}
	if len(c.pluginsByHook["request-middleware"]) > 0 {
		trace.DebugBefore("Request plugins", pluginNameList(c.pluginsByHook["request-middleware"]))
	}
}

func (c *CLI) runPrintSpec(cmd *cobra.Command, resp *output.Response, prepared *preparedRequest, spec printSpec, renderBody func() error) error {
	for _, part := range spec.order {
		switch part {
		case printRequestHeaders:
			if err := c.writeRequestPreamble(prepared, spec.color); err != nil {
				return err
			}
		case printRequestBody:
			if err := c.writeRequestBody(prepared, spec); err != nil {
				return err
			}
		case printResponseHeaders:
			if err := c.writeResponsePreamble(resp, spec.color); err != nil {
				return err
			}
		case printRenderedBody:
			if renderBody != nil {
				if err := renderBody(); err != nil {
					return err
				}
			}
		case printRawBody:
			if resp != nil {
				if err := c.writeRawBytes(resp.Raw); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *CLI) rawResponseBodyBytes(resp *http.Response, maxBytes int64, progress *downloadProgress) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	if output.ResponseHasNoBody(resp) {
		return nil, nil
	}
	if maxBytes <= 0 {
		maxBytes = output.DefaultMaxBodyBytes
	}
	body := io.Reader(resp.Body)
	if progress != nil {
		body = downloadProgressReader{reader: body, progress: progress}
	}
	reader, err := c.content.Decompress(resp.Header.Get("Content-Encoding"), body)
	if err != nil {
		return nil, fmt.Errorf("decompressing response: %w", err)
	}
	defer reader.Close()

	limited := io.LimitReader(reader, maxBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("response body exceeds limit of %d bytes; use --rsh-max-body-size to increase", maxBytes)
	}
	return raw, nil
}

func (c *CLI) decompressedResponseBody(resp *http.Response) (io.ReadCloser, error) {
	if resp == nil || resp.Body == nil {
		return io.NopCloser(strings.NewReader("")), nil
	}
	original := resp.Body
	if output.ResponseHasNoBody(resp) {
		return &readCloser{
			Reader: strings.NewReader(""),
			Closer: original,
		}, nil
	}
	reader, err := c.content.Decompress(resp.Header.Get("Content-Encoding"), original)
	if err != nil {
		return nil, err
	}
	return &readCloser{
		Reader: reader,
		Closer: closeBoth{
			first:  reader,
			second: original,
		},
	}, nil
}

type closeBoth struct {
	first  io.Closer
	second io.Closer
}

func (c closeBoth) Close() error {
	return errors.Join(c.first.Close(), c.second.Close())
}

func (c *CLI) canPrintWithoutResponseBody(gf GlobalFlags, spec printSpec) bool {
	if spec.has(printRawBody) {
		return false
	}
	if spec.includesResponseBody() && !filterCanRenderWithoutResponseBody(gf) {
		return false
	}
	if len(c.pluginsByHook["response-middleware"]) > 0 {
		return false
	}
	return true
}

func filterCanRenderWithoutResponseBody(gf GlobalFlags) bool {
	if gf.HeadersShorthand || gf.StatusShorthand {
		return true
	}
	if strings.TrimSpace(gf.Filter) == "" {
		return false
	}
	return bodyFreeMetadataFilter(gf.Filter)
}

func bodyFreeMetadataFilter(expr string) bool {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimPrefix(expr, ".")
	return expr == "headers" || strings.HasPrefix(expr, "headers.") || strings.HasPrefix(expr, "headers[") ||
		expr == "headers_all" || strings.HasPrefix(expr, "headers_all.") || strings.HasPrefix(expr, "headers_all[") ||
		expr == "status" || strings.HasPrefix(expr, "status.") || strings.HasPrefix(expr, "status[") ||
		expr == "proto" || strings.HasPrefix(expr, "proto.") || strings.HasPrefix(expr, "proto[")
}

func responseMetadataOnly(resp *http.Response) *output.Response {
	if resp == nil {
		return &output.Response{}
	}
	headers := make(map[string][]string, len(resp.Header))
	for k, vals := range resp.Header {
		if len(vals) > 0 {
			headers[k] = append([]string(nil), vals...)
		}
	}
	out := &output.Response{
		Proto:   resp.Proto,
		Status:  resp.StatusCode,
		Headers: headers,
	}
	if resp.Request != nil && resp.Request.URL != nil {
		out.URL = resp.Request.URL.String()
	}
	return out
}

func (c *CLI) printResponseParts(cmd *cobra.Command, resp *output.Response, prepared *preparedRequest, spec printSpec, value any, valueSet bool) error {
	renderBody := func() error {
		if valueSet {
			return c.renderValueWithPrint(cmd, value, spec, valueSet)
		}
		return c.formatResponseBodyWithPrint(cmd, resp, spec)
	}
	return c.runPrintSpec(cmd, resp, prepared, spec, renderBody)
}

func (c *CLI) writeResponsePreamble(resp *output.Response, color bool) error {
	if resp == nil {
		return nil
	}
	proto := resp.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	var preamble strings.Builder
	fmt.Fprintf(&preamble, "%s %d %s\n", proto, resp.Status, http.StatusText(resp.Status))

	for _, key := range sortedHeaderKeys(http.Header(resp.Headers)) {
		for _, value := range resp.Headers[key] {
			if secrets.IsHeaderName(key) {
				value = "<redacted>"
			}
			fmt.Fprintf(&preamble, "%s: %s\n", key, value)
		}
	}
	fmt.Fprintln(&preamble)

	if color {
		highlighted, err := output.HighlightWithLexer(output.HTTPPreambleLexer, []byte(preamble.String()))
		if err == nil {
			_, err = c.Stdout.Write(highlighted)
			return err
		}
	}
	_, err := io.WriteString(c.Stdout, preamble.String())
	return err
}

func (c *CLI) writeRequestPreamble(prepared *preparedRequest, color bool) error {
	if prepared == nil || prepared.actualRequest == nil {
		return nil
	}
	req := prepared.actualRequest
	uri := "/"
	if req.URL != nil {
		uri = request.RedactedRequestURI(req)
		if uri == "" {
			uri = "/"
		}
	}
	proto := req.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	var preamble strings.Builder
	fmt.Fprintf(&preamble, "%s %s %s\n", req.Method, uri, proto)
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	if host != "" {
		fmt.Fprintf(&preamble, "Host: %s\n", host)
	}
	keys := sortedHeaderKeys(req.Header)
	for _, key := range keys {
		for _, value := range req.Header[key] {
			if secrets.IsHeaderName(key) || request.IsMarkedCredentialHeader(req, key) {
				value = "<redacted>"
			}
			fmt.Fprintf(&preamble, "%s: %s\n", key, value)
		}
	}
	fmt.Fprintln(&preamble)

	if color {
		highlighted, err := output.HighlightWithLexer(output.HTTPPreambleLexer, []byte(preamble.String()))
		if err == nil {
			_, err = c.Stdout.Write(highlighted)
			return err
		}
	}
	_, err := io.WriteString(c.Stdout, preamble.String())
	return err
}

func (c *CLI) writeRequestBody(prepared *preparedRequest, spec printSpec) error {
	if prepared == nil || len(prepared.bodyRaw) == 0 {
		return nil
	}
	data := prepared.bodyRaw
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(prepared.bodyContentType, ";")[0]))
	if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
		var value any
		if err := json.Unmarshal(prepared.bodyRaw, &value); err == nil {
			redactSensitiveJSON(value)
			var encoded []byte
			var marshalErr error
			if spec.pretty {
				encoded, marshalErr = json.MarshalIndent(value, "", "  ")
			} else {
				encoded, marshalErr = json.Marshal(value)
			}
			if marshalErr == nil {
				data = encoded
			}
		}
	} else if mediaType == "application/x-www-form-urlencoded" {
		if rendered := redactVerboseBody(prepared.bodyRaw, prepared.bodyContentType); rendered != "" {
			data = []byte(rendered)
		}
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(append([]byte(nil), data...), '\n')
	}
	if spec.color {
		highlighted, err := output.HighlightWithLexer(output.ReadableLexer, data)
		if err == nil {
			_, err = c.Stdout.Write(highlighted)
			return err
		}
	}
	_, err := c.Stdout.Write(data)
	return err
}

func (c *CLI) formatResponseBodyWithPrint(cmd *cobra.Command, resp *output.Response, spec printSpec) error {
	gf := globalFlagsFromContext(requestContext(cmd))
	fmtName := gf.OutputFormat
	tty := c.stdoutIsTerminal()
	if responseBodyAbsent(resp) {
		return nil
	}
	if !spec.pretty && fmtName == "" && autoBodyShouldHandleCompact(resp) {
		traceOutputFormatter(cmd, fmtName, tty, &output.AutoFormatter{})
		return (&output.AutoFormatter{}).Format(c.Stdout, resp, renderedBodyColor(resp, fmtName, tty, spec.color))
	}
	if !spec.pretty && (fmtName == "" || fmtName == "json") && compactAutoBody(resp) {
		return c.writeJSONValue(resp.Body, false, spec.color)
	}
	formatter, err := c.selectFormatter(cmd, fmtName, tty)
	if err != nil {
		return err
	}
	traceOutputFormatter(cmd, fmtName, tty, formatter)
	return formatter.Format(c.Stdout, resp, renderedBodyColor(resp, fmtName, tty, spec.color))
}

func responseBodyAbsent(resp *output.Response) bool {
	return resp == nil || (resp.Body == nil && len(resp.Raw) == 0)
}

func autoBodyShouldHandleCompact(resp *output.Response) bool {
	if resp == nil {
		return false
	}
	if strings.HasPrefix(output.Header(resp.Headers, "Content-Type"), "image/") && len(resp.Raw) > 0 {
		return true
	}
	switch resp.Body.(type) {
	case string, []byte:
		return true
	default:
		return false
	}
}

func compactAutoBody(resp *output.Response) bool {
	if resp == nil {
		return false
	}
	switch resp.Body.(type) {
	case map[string]any, []any, bool, float64, int, int64, uint64, nil:
		return true
	default:
		return false
	}
}

func renderedBodyColor(resp *output.Response, fmtName string, tty, color bool) bool {
	if color {
		return true
	}
	if !tty {
		return false
	}
	contentType := output.Header(resp.Headers, "Content-Type")
	return strings.HasPrefix(contentType, "image/") && (fmtName == "" || fmtName == "auto" || fmtName == "image")
}

func (c *CLI) renderValueWithPrint(cmd *cobra.Command, value any, spec printSpec, plainScalars bool) error {
	renderer, err := c.newValueRendererWithPrint(cmd, nil, plainScalars, spec)
	if err != nil {
		return err
	}
	defer renderer.Close()
	c.traceValueOutput(cmd, value, plainScalars)
	if trace := requestTraceFromContext(requestContext(cmd)); trace != nil {
		trace.RenderAfter(c.Stderr, globalFlagsFromContext(requestContext(cmd)).Verbose)
	}
	return renderer.Render(value)
}

func traceInputSource(info input.BodyInfo, hasBody bool) string {
	switch {
	case !hasBody:
		return "none"
	case info.UsedStdin && info.UsedArgs:
		return "stdin + args"
	case info.UsedStdin:
		return "stdin"
	case info.UsedArgs:
		return "args"
	default:
		return "none"
	}
}

func traceContentDecode(trace *requestTrace, contentType string) {
	mediaType := traceMediaType(contentType)
	if mediaType == "" {
		mediaType = "identity"
	}
	trace.Info("Decode", mediaType)
	trace.Step(mediaType)
}

func traceFilter(trace *requestTrace, requested, resolved filter.Lang) {
	if trace == nil {
		return
	}
	if requested == filter.LangAuto {
		value := fmt.Sprintf("%s (auto)", resolved)
		trace.Info("Filter", value)
		trace.Step(filterPipelineStep(resolved, true))
		return
	}
	trace.Info("Filter", requested.String())
	trace.Step(filterPipelineStep(requested, false))
}

func filterPipelineStep(lang filter.Lang, auto bool) string {
	if auto {
		return fmt.Sprintf("%s(auto)", lang)
	}
	return lang.String()
}

func (c *CLI) filterBodyValue(cmd *cobra.Command, item any) (any, error) {
	gf := globalFlagsFromContext(requestContext(cmd))
	if gf.Filter == "" {
		return item, nil
	}
	doc := map[string]any{"body": item}
	lang := resolveFilterLang(gf.FilterLang)
	filterResult, err := filter.ApplyWithInfo(gf.Filter, doc, lang)
	if err != nil {
		return nil, fmt.Errorf("filter: %w", err)
	}
	traceFilter(requestTraceFromContext(requestContext(cmd)), lang, filterResult.Lang)
	if filterResult.Value == nil && bodyPrefixTargetExists(item, gf.Filter) {
		c.hintBodyPrefixOnce(gf.Filter)
	} else if filterResult.Value == nil && shouldSuggestJQBodyRoot(gf.Filter, lang) {
		c.hintJQBodyRootOnce(gf.Filter)
	}
	return filterResult.Value, nil
}

func traceMediaType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return ""
	}
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
		return mediaType
	}
	return strings.Split(contentType, ";")[0]
}

func pluginNameList(plugins []internalplugin.Plugin) string {
	names := make([]string, 0, len(plugins))
	for _, p := range plugins {
		if p.Manifest.Name != "" {
			names = append(names, p.Manifest.Name)
			continue
		}
		names = append(names, filepath.Base(p.Path))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func traceOutputFormatter(cmd *cobra.Command, fmtName string, tty bool, formatter output.Formatter) {
	trace := requestTraceFromContext(requestContext(cmd))
	if trace == nil {
		return
	}
	name := resolvedOutputFormatName(fmtName, tty)
	value := outputTraceLabel(name, formatter)
	if fmtName == "" && name != "auto" {
		value += " (auto)"
	}
	trace.Info("Output", value)
	if _, ok := formatter.(*output.PluginFormatter); ok {
		trace.AddInfo("Plugin", "formatter "+name)
	}
	trace.Step(outputPipelineStep(name, formatter))
}

func (c *CLI) traceValueOutput(cmd *cobra.Command, value any, plainScalars bool) {
	trace := requestTraceFromContext(requestContext(cmd))
	if trace == nil {
		return
	}
	gf := globalFlagsFromContext(requestContext(cmd))
	if gf.Silent {
		trace.Info("Output", "silent")
		trace.Step("silent")
		return
	}
	fmtName := gf.OutputFormat
	if fmtName == "" && plainScalars && output.IsLineScalar(value) {
		trace.Info("Output", "lines (auto)")
		trace.Step("lines")
		return
	}
	if fmtName == "" {
		if c.stdoutIsTerminal() {
			trace.Info("Output", "auto")
			trace.Step("auto")
			return
		}
		trace.Info("Output", "json (auto)")
		trace.Step("json")
		return
	}
	if formatter := c.formatters[fmtName]; formatter != nil {
		trace.Info("Output", outputTraceLabel(fmtName, formatter))
		if _, ok := formatter.(*output.PluginFormatter); ok {
			trace.AddInfo("Plugin", "formatter "+fmtName)
		}
		trace.Step(outputPipelineStep(fmtName, formatter))
		return
	}
	trace.Info("Output", fmtName)
	trace.Step(fmtName)
}

func resolvedOutputFormatName(fmtName string, tty bool) string {
	if fmtName != "" {
		return fmtName
	}
	if tty {
		return "auto"
	}
	return "json"
}

func outputTraceLabel(name string, formatter output.Formatter) string {
	if _, ok := formatter.(*output.PluginFormatter); ok {
		return name + " plugin"
	}
	return name
}

func outputPipelineStep(name string, formatter output.Formatter) string {
	if _, ok := formatter.(*output.PluginFormatter); ok {
		return name + "(plugin)"
	}
	return name
}

func explicitOutputFilter(gf GlobalFlags) bool {
	return gf.Filter != "" || gf.HeadersShorthand || gf.StatusShorthand
}

func filterNeedsLinks(filterExpr string) bool {
	filterExpr = strings.TrimSpace(filterExpr)
	return filterExpr == "@" || filterExpr == "links" ||
		strings.HasPrefix(filterExpr, "links.") ||
		strings.HasPrefix(filterExpr, "links[") ||
		strings.HasPrefix(filterExpr, ".links")
}

// renderValue writes a filtered/subselected value using the active print and
// formatter selection rules.
func (c *CLI) renderValue(cmd *cobra.Command, value any, plainScalars bool) error {
	gf := globalFlagsFromContext(requestContext(cmd))
	spec, err := c.resolvePrintSpec(gf, c.stdoutIsTerminal(), printValueResponse)
	if err != nil {
		return err
	}
	renderer, err := c.newValueRendererWithPrint(cmd, nil, plainScalars, spec)
	if err != nil {
		return err
	}
	defer renderer.Close()
	c.traceValueOutput(cmd, value, plainScalars)
	if trace := requestTraceFromContext(requestContext(cmd)); trace != nil {
		trace.RenderAfter(c.Stderr, globalFlagsFromContext(requestContext(cmd)).Verbose)
	}
	return renderer.Render(value)
}

type valueRenderer interface {
	Render(value any) error
	Close() error
}

type valueRendererFunc struct {
	render func(value any) error
}

func (r valueRendererFunc) Render(value any) error {
	return r.render(value)
}

func (r valueRendererFunc) Close() error {
	return nil
}

type valueStreamRenderer struct {
	stream output.ValueStream
}

func (r valueStreamRenderer) Render(value any) error {
	return r.stream.WriteValue(value)
}

func (r valueStreamRenderer) Close() error {
	return r.stream.Close()
}

func (c *CLI) newValueRenderer(cmd *cobra.Command, base *output.Response, plainScalars bool) (valueRenderer, error) {
	gf := globalFlagsFromContext(requestContext(cmd))
	spec, err := c.resolvePrintSpec(gf, c.stdoutIsTerminal(), printValueResponse)
	if err != nil {
		return nil, err
	}
	return c.newValueRendererWithPrint(cmd, base, plainScalars, spec)
}

func (c *CLI) newValueRendererWithPrint(cmd *cobra.Command, base *output.Response, plainScalars bool, spec printSpec) (valueRenderer, error) {
	streaming := base != nil
	if base == nil {
		base = &output.Response{}
	}

	gf := globalFlagsFromContext(requestContext(cmd))
	if gf.Silent {
		return valueRendererFunc{render: func(any) error { return nil }}, nil
	}

	if spec.rawBodyOnly() {
		return nil, fmt.Errorf("raw body output cannot render a filtered value")
	}

	fmtName := gf.OutputFormat
	explicitAuto := explicitAutoOutputFormat(gf)
	tty := c.stdoutIsTerminal()
	color := spec.color

	if fmtName == "" && plainScalars && (!explicitAuto || !spec.pretty) {
		return valueRendererFunc{render: func(value any) error {
			if output.IsLineScalar(value) {
				return output.WriteLinesValue(c.Stdout, value)
			}
			return c.writeJSONValue(value, spec.pretty, color)
		}}, nil
	}

	if !spec.pretty && fmtName == "json" {
		return valueRendererFunc{render: func(value any) error {
			return c.writeJSONValue(value, false, color)
		}}, nil
	}

	// Explicit --rsh-print=b keeps default body/sub-value rendering compact for
	// scripts. Auto mode sets spec.pretty for transformed redirected output.
	if !spec.pretty && fmtName == "" {
		return valueRendererFunc{render: func(value any) error {
			return c.writeJSONValue(value, false, color)
		}}, nil
	}

	if fmtName == "" && spec.pretty && !explicitAuto {
		return valueRendererFunc{render: func(value any) error {
			return c.writeJSONValue(value, true, color)
		}}, nil
	}

	formatter, err := c.selectFormatter(cmd, fmtName, tty)
	if err != nil {
		return nil, err
	}
	if streaming {
		if streamFormatter, ok := formatter.(output.ValueStreamFormatter); ok {
			stream, err := streamFormatter.StartValueStream(c.Stdout, base, color)
			if err != nil {
				return nil, err
			}
			if stream != nil {
				return valueStreamRenderer{stream: stream}, nil
			}
		}
	}
	if valueFormatter, ok := formatter.(output.ValueFormatter); ok {
		return valueRendererFunc{render: func(value any) error {
			return valueFormatter.FormatValue(c.Stdout, value, color)
		}}, nil
	}

	return valueRendererFunc{render: func(value any) error {
		resp := &output.Response{
			Proto:   base.Proto,
			Status:  base.Status,
			Headers: base.Headers,
			Links:   base.Links,
			Body:    value,
		}
		return formatter.Format(c.Stdout, resp, color)
	}}, nil
}

func (c *CLI) writeJSONValue(value any, pretty, color bool) error {
	var (
		encoded []byte
		err     error
	)
	if pretty {
		encoded, err = json.MarshalIndent(value, "", "  ")
	} else {
		encoded, err = json.Marshal(value)
	}
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if color {
		highlighted, err := output.HighlightWithLexer(output.ReadableLexer, encoded)
		if err == nil {
			_, err = c.Stdout.Write(highlighted)
			return err
		}
	}
	_, err = c.Stdout.Write(encoded)
	return err
}

func (c *CLI) selectFormatter(cmd *cobra.Command, fmtName string, tty bool) (output.Formatter, error) {
	if fmtName == "auto" {
		fmtName = ""
	}

	fmts := c.formatters
	if fmts == nil {
		fmts = output.DefaultFormatters()
	}

	// For the table format, configure it from flags before selecting.
	// Copy the map first so we don't mutate the shared c.formatters.
	if fmtName == "table" {
		gfTable := globalFlagsFromContext(requestContext(cmd))
		cols := gfTable.Columns
		sortBy := gfTable.SortBy
		tf := &output.TableFormatter{SortBy: sortBy}
		if cols != "" {
			tf.Columns = strings.Split(cols, ",")
		}
		copied := make(map[string]output.Formatter, len(fmts))
		for k, v := range fmts {
			copied[k] = v
		}
		copied["table"] = tf
		fmts = copied
	}

	if fmtName == "" {
		if explicitAutoOutputFormat(globalFlagsFromContext(requestContext(cmd))) {
			return fmts["auto"], nil
		}
		if tty {
			return fmts["auto"], nil
		}
		return fmts["json"], nil
	}
	formatter, ok := fmts[fmtName]
	if !ok {
		if suggestion := outputFormatSuggestion(fmtName, fmts); suggestion != "" {
			return nil, fmt.Errorf("unknown output format %q; did you mean %q?; available: %s", fmtName, suggestion, output.FormatterNames(fmts))
		}
		return nil, fmt.Errorf("unknown output format %q; available: %s", fmtName, output.FormatterNames(fmts))
	}
	return formatter, nil
}

// outputFormatSuggestion returns the format name closest to a mistyped name, or
// "" when nothing is within edit distance 2 or the nearest distance is shared by
// more than one format (an ambiguous guess is worse than none). Picking the
// strictly nearest match keeps suggestions stable as more formats are added.
func outputFormatSuggestion(name string, fmts map[string]output.Formatter) string {
	best := ""
	bestDist := 3 // only distances <= 2 are considered
	tie := false
	for candidate := range fmts {
		switch d := levenshteinDistance(name, candidate); {
		case d > 2:
		case d < bestDist:
			best, bestDist, tie = candidate, d, false
		case d == bestDist:
			tie = true
		}
	}
	if tie {
		return ""
	}
	return best
}

func explicitAutoOutputFormat(gf GlobalFlags) bool {
	return gf.OutputFormat == "" && gf.OutputFormatSet
}

func (c *CLI) writeRawBytes(data []byte) error {
	_, err := c.Stdout.Write(data)
	return err
}

func (c *CLI) writePlainValue(value any) error {
	return output.WriteLinesValue(c.Stdout, value)
}

func shouldSuggestBodyPrefix(filterExpr string) bool {
	filterExpr = strings.TrimSpace(filterExpr)
	if filterExpr == "" || strings.HasPrefix(filterExpr, "@") {
		return false
	}
	if strings.HasPrefix(filterExpr, ".") || strings.ContainsAny(filterExpr, "|()") {
		return false
	}
	roots := []string{"body", "headers", "links", "status", "proto"}
	for _, root := range roots {
		if filterExpr == root ||
			strings.HasPrefix(filterExpr, root+".") ||
			strings.HasPrefix(filterExpr, root+"[") ||
			strings.HasPrefix(filterExpr, "."+root) {
			return false
		}
	}
	return true
}

func shouldSuggestJQBodyRoot(filterExpr string, requested filter.Lang) bool {
	if requested == filter.LangShorthand {
		return false
	}
	filterExpr = strings.TrimSpace(filterExpr)
	if filterExpr == "" || strings.Contains(filterExpr, ".body") {
		return false
	}
	for _, prefix := range []string{"map(", "select(", "length", "keys", "has(", "sort", "sort_by(", "group_by(", "unique", "unique_by("} {
		if filterExpr == prefix || strings.HasPrefix(filterExpr, prefix) {
			return true
		}
	}
	return false
}

func (c *CLI) hintJQBodyRootOnce(filterExpr string) {
	if c.bodyPrefixHinted {
		return
	}
	c.bodyPrefixHinted = true
	c.hintf("filter returned no results; this looks like jq over the response wrapper, so try '.body | %s' or pass --rsh-filter-lang jq with an explicit root", strings.TrimSpace(filterExpr))
}

func (c *CLI) hintBodyPrefixOnce(filterExpr string) {
	if c.bodyPrefixHinted {
		return
	}
	c.bodyPrefixHinted = true
	c.hintf("filter returned no results; to access response body fields use '%s'", bodyPrefixHint(filterExpr))
}

func isLocalRequestExecutionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "auth: ") ||
		strings.HasPrefix(msg, "building request: ") ||
		strings.HasPrefix(msg, "invalid header ") ||
		strings.HasPrefix(msg, "invalid query ")
}

func responseBodyReadError(method, rawURL string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("reading response body for %s %s: unexpected EOF\nhint: response ended early; retry or check server/proxy stability", method, rawURL)
	}
	if strings.HasPrefix(err.Error(), "reading response body: ") {
		return fmt.Errorf("reading response body for %s %s: %w", method, rawURL, err)
	}
	return err
}

func bodyPrefixHint(filterExpr string) string {
	filterExpr = strings.TrimSpace(filterExpr)
	if index, rest, ok := leadingArrayIndexFilter(filterExpr); ok {
		return fmt.Sprintf("body[%s]%s", index, rest)
	}
	return "body." + filterExpr
}

func leadingArrayIndexFilter(filterExpr string) (index, rest string, ok bool) {
	dot := strings.IndexByte(filterExpr, '.')
	if dot <= 0 {
		return "", "", false
	}
	index = filterExpr[:dot]
	for _, r := range index {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	rest = filterExpr[dot:]
	return index, rest, true
}

func bodyPrefixTargetExists(item any, filterExpr string) bool {
	filterExpr = strings.TrimSpace(filterExpr)
	if !shouldSuggestBodyPrefix(filterExpr) {
		return false
	}
	field := filterExpr
	for i, r := range field {
		if r == '.' || r == '[' {
			field = field[:i]
			break
		}
	}
	if field == "" {
		return false
	}
	switch obj := item.(type) {
	case map[string]any:
		_, ok := obj[field]
		return ok
	case map[string]string:
		_, ok := obj[field]
		return ok
	default:
		return false
	}
}

func filterRequestsResponseMetadata(expr string) bool {
	expr = strings.TrimSpace(expr)
	return expr == "@" ||
		expr == "headers" || strings.HasPrefix(expr, "headers.") || strings.HasPrefix(expr, "headers[") ||
		expr == "links" || strings.HasPrefix(expr, "links.") || strings.HasPrefix(expr, "links[") ||
		expr == "status" || strings.HasPrefix(expr, "status.") || strings.HasPrefix(expr, "status[") ||
		expr == "proto" || strings.HasPrefix(expr, "proto.") || strings.HasPrefix(expr, "proto[")
}

// logVerboseRequest prints request summary lines to stderr.
// Sensitive request headers (Authorization, Cookie, Set-Cookie,
// Proxy-Authorization) are redacted to avoid leaking credentials.
func (c *CLI) logVerboseRequest(req *http.Request) {
	if req == nil {
		return
	}
	style := humanTextStyleFor(c.Stderr)
	reqMark := style.info(">")
	fmt.Fprintf(c.Stderr, "%s %s %s\n", reqMark, req.Method, request.RedactedRequestURL(req))
	for _, k := range sortedHeaderKeys(req.Header) {
		vs := req.Header[k]
		for _, v := range vs {
			if isSensitiveHeaderValue(k, v) || request.IsMarkedCredentialHeader(req, k) {
				v = "<redacted>"
			}
			fmt.Fprintf(c.Stderr, "%s %s: %s\n", reqMark, k, v)
		}
	}
	c.logVerboseRequestBody(req)
	fmt.Fprintln(c.Stderr, reqMark)
}

// logVerboseResponse prints response summary lines to stderr. At verbose >= 2
// it also dumps TLS version, cipher suite, and peer certificate chain (subject,
// issuer, expiry).
func (c *CLI) logVerboseResponse(resp *http.Response, verbose int) {
	if resp == nil {
		return
	}
	style := humanTextStyleFor(c.Stderr)
	infoMark := style.info("*")
	respMark := style.info("<")
	if resp.Header.Get("X-From-Cache") != "" {
		fmt.Fprintf(c.Stderr, "%s %s %s\n", infoMark, style.key("Cache:"), style.ok("HIT"))
	}
	fmt.Fprintf(c.Stderr, "%s %s %s %s\n", respMark, resp.Proto, style.httpStatus(resp.StatusCode, fmt.Sprintf("%d", resp.StatusCode)), http.StatusText(resp.StatusCode))
	for _, k := range sortedHeaderKeys(resp.Header) {
		vs := resp.Header[k]
		for _, v := range vs {
			if isSensitiveHeader(k) {
				v = "<redacted>"
			}
			fmt.Fprintf(c.Stderr, "%s %s: %s\n", respMark, k, v)
		}
	}
	fmt.Fprintln(c.Stderr, respMark)

	if verbose >= 2 && resp.TLS != nil {
		tlsVersionName := map[uint16]string{
			tls.VersionTLS10: "TLS 1.0",
			tls.VersionTLS11: "TLS 1.1",
			tls.VersionTLS12: "TLS 1.2",
			tls.VersionTLS13: "TLS 1.3",
		}
		ver, ok := tlsVersionName[resp.TLS.Version]
		if !ok {
			ver = fmt.Sprintf("TLS 0x%04x", resp.TLS.Version)
		}
		fmt.Fprintf(c.Stderr, "%s %s %s %s\n", infoMark, style.key("TLS:"), ver, tls.CipherSuiteName(resp.TLS.CipherSuite))
		for i, cert := range resp.TLS.PeerCertificates {
			label := "Leaf"
			if i > 0 {
				label = fmt.Sprintf("Chain %d", i)
			}
			fmt.Fprintf(c.Stderr, "%s %s Subject: %s\n", infoMark, label, cert.Subject)
			fmt.Fprintf(c.Stderr, "%s %s Issuer: %s\n", infoMark, label, cert.Issuer)
			fmt.Fprintf(c.Stderr, "%s %s Expiry: %s (%s)\n", infoMark, label, cert.NotAfter.Format(time.RFC3339), relativeExpiry(cert.NotAfter))
		}
	}
}

func sortedHeaderKeys(headers http.Header) []string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

const verboseBodyLimit = 4096

func (c *CLI) logVerboseRequestBody(req *http.Request) {
	if req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			data, _ := io.ReadAll(io.LimitReader(body, verboseBodyLimit+1))
			_ = body.Close()
			c.logVerboseBody("> body", data, req.Header.Get("Content-Type"))
		}
	}
}

func (c *CLI) logVerboseResponseBody(resp *output.Response) {
	if resp != nil && len(resp.Raw) > 0 {
		c.logVerboseBody("< body", resp.Raw, output.Header(resp.Headers, "Content-Type"))
	}
}

func (c *CLI) logVerboseBody(label string, data []byte, contentType string) {
	if len(data) == 0 {
		return
	}
	truncated := len(data) > verboseBodyLimit
	if truncated {
		data = data[:verboseBodyLimit]
	}
	rendered := redactVerboseBody(data, contentType)
	if rendered == "" {
		return
	}
	fmt.Fprintf(c.Stderr, "%s:\n%s\n", label, rendered)
	if truncated {
		fmt.Fprintf(c.Stderr, "%s truncated after %d bytes\n", label, verboseBodyLimit)
	}
}

func redactVerboseBody(data []byte, contentType string) string {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
		var value any
		if err := json.Unmarshal(data, &value); err == nil {
			redactSensitiveJSON(value)
			if out, err := json.MarshalIndent(value, "", "  "); err == nil {
				return string(out)
			}
		}
	}
	if mediaType == "application/x-www-form-urlencoded" {
		values, err := url.ParseQuery(string(data))
		if err == nil {
			for key, items := range values {
				for i, item := range items {
					if secrets.IsQueryParamValue(key, item) {
						items[i] = "<redacted>"
					}
				}
				values[key] = items
			}
			return values.Encode()
		}
	}
	if mediaType != "" && mediaType != "text/plain" && !strings.HasPrefix(mediaType, "text/") && !json.Valid(data) {
		return fmt.Sprintf("<%d bytes of %s body>", len(data), mediaType)
	}
	if !json.Valid(data) && strings.ContainsRune(string(data), '\x00') {
		if mediaType == "" {
			return fmt.Sprintf("<%d bytes of binary body>", len(data))
		}
		return fmt.Sprintf("<%d bytes of %s body>", len(data), mediaType)
	}
	return string(data)
}

func redactSensitiveJSON(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if stringItem, ok := item.(string); ok {
				if redacted, changed := redactSensitiveStringValue(key, stringItem); changed {
					v[key] = redacted
					continue
				}
			}
			if secrets.IsJSONBodyKey(key) || secrets.IsHeaderName(key) {
				v[key] = "<redacted>"
				continue
			}
			redactSensitiveJSON(item)
		}
	case []any:
		for _, item := range v {
			redactSensitiveJSON(item)
		}
	}
}

func redactSensitiveStringValue(key, value string) (string, bool) {
	if secrets.IsJSONBodyValue(key, value) {
		return "<redacted>", true
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		redacted := request.RedactedURL(parsed)
		if redacted != value {
			return redacted, true
		}
	}
	if json.Valid([]byte(value)) {
		var nested any
		if err := json.Unmarshal([]byte(value), &nested); err == nil {
			before, _ := json.Marshal(nested)
			redactSensitiveJSON(nested)
			if out, err := json.Marshal(nested); err == nil {
				redacted := string(out)
				if string(before) != redacted {
					return redacted, true
				}
			}
		}
	}
	return value, false
}

func networkErrorHint(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "x509:"):
		return "check the server certificate, use --rsh-ca-cert for a private CA, or --rsh-insecure only for deliberate testing"
	case strings.Contains(msg, "no such host"):
		return "check the hostname, DNS, VPN, or proxy settings"
	case strings.Contains(msg, "connection refused"):
		return "check that the service is running and that the host and port are correct"
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return "check network reachability or increase --rsh-timeout"
	default:
		return ""
	}
}

func redactedNetworkErrorURL(rawURL, serverOverride string) string {
	normalized, err := request.Normalize(rawURL, serverOverride)
	if err != nil {
		if parsed, parseErr := url.Parse(rawURL); parseErr == nil {
			return request.RedactedURL(parsed)
		}
		return rawURL
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return normalized
	}
	return request.RedactedURL(parsed)
}

func isSensitiveQueryParam(name string) bool {
	return secrets.IsQueryParamName(name)
}

// isSensitiveHeader reports whether a header name carries credentials and
// should be redacted in verbose output.
func isSensitiveHeader(name string) bool {
	return secrets.IsHeaderName(name)
}

func isSensitiveHeaderValue(name, value string) bool {
	return secrets.IsHeaderValue(name, value)
}

// isAPIShortName reports whether arg begins with a registered API name and an
// API-name delimiter or exactly matches a registered API name.
func (c *CLI) isAPIShortName(arg string) bool {
	if c.cfg == nil {
		return false
	}
	apiName, _ := splitAPIShortNameSuffix(arg)
	return c.cfg.APIs[apiName] != nil
}

// applyAPIProfile checks whether rawURL begins with a registered API short
// name and, if so, expands it to the full URL and prepends persistent headers
// and query params from the active profile.
//
// Returns (expandedURL, apiName, opts). apiName is empty when rawURL is not
// an API short name.
func (c *CLI) applyAPIProfile(rawURL, profileName string, opts request.Options, authOpts authHandlerOptions) (string, string, request.Options, error) {
	if c.cfg == nil || len(c.cfg.APIs) == 0 {
		return rawURL, "", opts, nil
	}

	match, ok, err := c.matchAPIProfile(rawURL, profileName)
	if err != nil {
		var ambiguous *ambiguousAPIProfileMatchError
		if errors.As(err, &ambiguous) && profileName == "default" {
			c.warnf("%v; proceeding without API profile, auth, or cache metadata", ambiguous)
			return rawURL, "", opts, nil
		}
		return rawURL, "", opts, err
	}
	if !ok {
		return rawURL, "", opts, nil
	}
	rawURL = match.rawURL

	if opts.RetryMaxWait == 0 && strings.TrimSpace(match.api.RetryMaxWait) != "" {
		retryMaxWait, parseErr := time.ParseDuration(match.api.RetryMaxWait)
		if parseErr != nil || retryMaxWait <= 0 {
			if parseErr == nil {
				parseErr = fmt.Errorf("must be greater than 0")
			}
			return rawURL, match.apiName, opts, fmt.Errorf("invalid retry_max_wait for API %q: %w", match.apiName, parseErr)
		}
		opts.RetryMaxWait = retryMaxWait
	}
	if match.api.PreserveHeaderCase {
		opts.PreserveHeaderCase = true
	}

	if match.profile == nil {
		if profileName != "default" {
			return rawURL, match.apiName, opts, fmt.Errorf("profile %q not found for API %q; configured profiles: %s", profileName, match.apiName, profileNames(match.api.Profiles))
		}
		callbacks := c.authOnRequest(match.apiName, profileName, nil, authOpts)
		opts.OnRequest = callbacks.OnRequest
		opts.OnUnauthorized = callbacks.OnUnauthorized
	}

	// Merge persistent profile headers with request-specific headers so
	// request-specific values replace matching profile defaults. Query params
	// keep append semantics because repeated query keys are often intentional.
	if match.profile != nil {
		opts.Headers, err = mergeHeaderOptions(match.profile.Headers, opts.Headers)
		if err != nil {
			return rawURL, match.apiName, opts, err
		}
		opts.Query = append(append([]string(nil), match.profile.Query...), opts.Query...)
		callbacks := c.authOnRequest(match.apiName, profileName, match.profile, authOpts)
		opts.OnRequest = callbacks.OnRequest
		opts.OnUnauthorized = callbacks.OnUnauthorized
		if opts.TLSSignerName == "" {
			opts.TLSSignerName = match.profile.TLSSigner
		}
		opts.TLSSignerParams = mergeTLSSignerParams(opts.TLSSignerParams, match.profile.TLSSignerParams)
		if opts.CACertPath == "" {
			opts.CACertPath = match.profile.CACertPath
		}
		if opts.ClientCertPath == "" {
			opts.ClientCertPath = match.profile.ClientCertPath
		}
		if opts.ClientKeyPath == "" {
			opts.ClientKeyPath = match.profile.ClientKeyPath
		}
	}
	if match.apiName != "" {
		opts.CacheNamespace = c.apiCacheNamespace(match.apiName, profileName)
	}

	return rawURL, match.apiName, opts, nil
}

type apiProfileMatch struct {
	apiName string
	api     *config.APIConfig
	profile *config.ProfileConfig
	rawURL  string
	score   int
}

type ambiguousAPIProfileMatchError struct {
	url   string
	names []string
}

func (e *ambiguousAPIProfileMatchError) Error() string {
	return fmt.Sprintf("ambiguous API match for %s: %s all match with the same base URL score; use the API short-name form instead", e.url, strings.Join(e.names, ", "))
}

func (c *CLI) matchAPIProfile(rawURL, profileName string) (apiProfileMatch, bool, error) {
	apiName, suffix := splitAPIShortNameSuffix(rawURL)
	if api := c.cfg.APIs[apiName]; api != nil {
		baseURL := api.BaseURL
		prof := profileForName(api, profileName)
		if prof != nil && prof.BaseURL != "" {
			baseURL = prof.BaseURL
		}
		expanded := strings.TrimRight(baseURL, "/")
		expanded += suffix
		expanded = cleanExpandedAPIURL(expanded)
		return apiProfileMatch{apiName: apiName, api: api, profile: prof, rawURL: expanded, score: len(apiName)}, true, nil
	}

	matchURL := rawURL
	if normalized, err := request.Normalize(rawURL, ""); err == nil {
		matchURL = normalized
	}

	var best apiProfileMatch
	var ties []string
	tieSeen := map[string]bool{}
	for name, api := range c.cfg.APIs {
		if api == nil {
			continue
		}
		prof := profileForName(api, profileName)
		bases, err := apiMatchBases(api, prof)
		if err != nil {
			c.warnf("API %q: %v", name, err)
		}
		for _, base := range bases {
			score, ok := matchURLBase(matchURL, base)
			if !ok || score < best.score {
				continue
			}
			if score == best.score && best.apiName != "" {
				if name != best.apiName && !tieSeen[name] {
					ties = append(ties, name)
					tieSeen[name] = true
				}
				continue
			}
			best = apiProfileMatch{apiName: name, api: api, profile: prof, rawURL: matchURL, score: score}
			ties = []string{name}
			tieSeen = map[string]bool{name: true}
		}
	}
	if best.apiName != "" && len(ties) > 1 {
		sort.Strings(ties)
		return apiProfileMatch{}, false, &ambiguousAPIProfileMatchError{url: matchURL, names: ties}
	}
	return best, best.apiName != "", nil
}

func splitAPIShortNameSuffix(raw string) (string, string) {
	idx := strings.IndexAny(raw, "/?#")
	if idx < 0 {
		return raw, ""
	}
	return raw[:idx], raw[idx:]
}

func profileForName(api *config.APIConfig, profileName string) *config.ProfileConfig {
	if api == nil || api.Profiles == nil {
		return nil
	}
	return api.Profiles[profileName]
}

func effectiveProfileBaseURL(api *config.APIConfig, profileName string) string {
	if api == nil {
		return ""
	}
	if prof := profileForName(api, profileName); prof != nil && prof.BaseURL != "" {
		return prof.BaseURL
	}
	return api.BaseURL
}

func effectiveOperationBase(api *config.APIConfig, profileName string) string {
	if api == nil {
		return ""
	}
	if prof := profileForName(api, profileName); prof != nil && prof.OperationBase != "" {
		return prof.OperationBase
	}
	return api.OperationBase
}

func cleanExpandedAPIURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if u.Path == "" {
		return rawURL
	}
	cleaned := urlpath.Clean(u.Path)
	if strings.HasSuffix(u.Path, "/") && cleaned != "/" {
		cleaned += "/"
	}
	u.Path = cleaned
	return u.String()
}

func apiMatchBases(api *config.APIConfig, prof *config.ProfileConfig) ([]string, error) {
	var bases []string
	seen := map[string]bool{}
	addBase := func(raw string) {
		if raw == "" {
			return
		}
		key := raw
		if normalized, err := request.Normalize(raw, ""); err == nil {
			key = normalized
		}
		if seen[key] {
			return
		}
		seen[key] = true
		bases = append(bases, raw)
	}
	if api.BaseURL != "" {
		addBase(api.BaseURL)
	}
	if prof != nil && prof.BaseURL != "" {
		addBase(prof.BaseURL)
	}
	operationBase := effectiveOperationBase(api, "")
	if prof != nil && prof.OperationBase != "" {
		operationBase = prof.OperationBase
	}
	baseURL := api.BaseURL
	if prof != nil && prof.BaseURL != "" {
		baseURL = prof.BaseURL
	}
	if operationBase != "" {
		resolved, err := config.ResolveOperationBaseURL(baseURL, operationBase)
		if err != nil {
			return bases, fmt.Errorf("operation_base: %w", err)
		}
		addBase(resolved)
	}
	return bases, nil
}

func matchURLBase(rawURL, rawBase string) (int, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() {
		return 0, false
	}
	base, err := url.Parse(rawBase)
	if err != nil || !base.IsAbs() {
		return 0, false
	}
	if !sameURLOrigin(base, u) {
		return 0, false
	}
	basePath := strings.TrimRight(base.EscapedPath(), "/")
	if basePath == "" {
		basePath = "/"
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if basePath != "/" {
		if path != basePath && !strings.HasPrefix(path, basePath+"/") {
			return 0, false
		}
	}
	return len(base.Scheme) + len(base.Host) + len(basePath), true
}

func sameURLOrigin(a, b *url.URL) bool {
	return request.SameOrigin(a, b)
}

// mergeTLSSignerParams merges src entries into dst, not overwriting existing
// keys. Returns the (possibly newly allocated) dst map.
func mergeTLSSignerParams(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]string, len(src))
	}
	for k, v := range src {
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}
	return dst
}

func parseKVStrings(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, item := range values {
		key, value, ok := strings.Cut(item, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%q: expected \"key=value\" format", item)
		}
		out[strings.TrimSpace(key)] = value
	}
	return out, nil
}

func (c *CLI) warnRetryUnsafe(method string, opts request.Options) {
	if c == nil || c.retryUnsafeWarned || !opts.RetryUnsafe || opts.Retry <= 0 {
		return
	}
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead:
		return
	}
	c.retryUnsafeWarned = true
	c.warnf("retrying unsafe HTTP methods can repeat side effects; --rsh-retry-unsafe is enabled for POST, PUT, PATCH, or DELETE")
}

// httpOptsFromFlags reads the global HTTP flags from cmd and builds an Options.
func (c *CLI) httpOptsFromFlags(cmd *cobra.Command) (request.Options, error) {
	gf := globalFlagsFromContext(requestContext(cmd))

	if gf.Insecure {
		c.warnf("TLS certificate verification is disabled (--rsh-insecure); connections are not secure")
	}

	tlsMinVersion, err := request.TLSVersionFromString(gf.TLSMinVersion)
	if err != nil {
		return request.Options{}, err
	}

	// --rsh-timeout default is 2 when not set by user (flag sentinel = -1).
	var timeout time.Duration
	if gf.Timeout != "" {
		var parseErr error
		timeout, parseErr = time.ParseDuration(gf.Timeout)
		if parseErr != nil {
			return request.Options{}, fmt.Errorf("invalid timeout %q: %w", gf.Timeout, parseErr)
		}
		if timeout < 0 {
			return request.Options{}, fmt.Errorf("invalid timeout %q: must be greater than or equal to 0", gf.Timeout)
		}
	}

	// --rsh-retry default is 2 when not set by user (flag sentinel = -1).
	retry := 2
	if gf.Retry >= 0 {
		retry = gf.Retry
	}
	var retryMaxWait time.Duration
	if gf.RetryMaxWait != "" {
		retryMaxWait, err = time.ParseDuration(gf.RetryMaxWait)
		if err != nil || retryMaxWait <= 0 {
			if err == nil {
				err = fmt.Errorf("must be greater than 0")
			}
			return request.Options{}, fmt.Errorf("invalid retry max wait %q: %w", gf.RetryMaxWait, err)
		}
	}

	tlsSignerParams, err := parseKVStrings(gf.TLSSignerParams)
	if err != nil {
		return request.Options{}, fmt.Errorf("invalid tls signer param: %w", err)
	}
	cacheMaxBytes, err := c.cacheMaxBytes()
	if err != nil {
		return request.Options{}, err
	}
	var logger io.Writer = diagnosticPrefixWriter(c.Stderr)
	if gf.Silent {
		logger = nil
	}

	return request.Options{
		Headers:              gf.Headers,
		Query:                gf.Query,
		Server:               gf.Server,
		Insecure:             gf.Insecure,
		ClientCertPath:       gf.ClientCert,
		ClientKeyPath:        gf.ClientKey,
		TLSSignerName:        gf.TLSSigner,
		TLSSignerParams:      tlsSignerParams,
		CACertPath:           gf.CACert,
		TLSMinVersion:        tlsMinVersion,
		Timeout:              timeout,
		AcceptHeader:         c.content.AcceptHeader(),
		AcceptEncodingHeader: c.content.AcceptEncodingHeader(),
		ContentType:          gf.ContentType,
		UserAgent:            "restish/" + Version,
		Transport:            c.baseHTTPTransport(),
		CacheDir:             c.cacheDir(),
		CacheMaxBytes:        cacheMaxBytes,
		NoCache:              gf.NoCache,
		Retry:                retry,
		RetryUnsafe:          gf.RetryUnsafe,
		RetryBaseDelay:       c.hooks.RetryBaseDelay,
		RetryMaxWait:         retryMaxWait,
		Logger:               logger,
		OnBeforeRequest: func(req *http.Request) {
			if gf.Verbose > 0 {
				c.logVerboseRequest(req)
			}
		},
		OnResponse: func(resp *http.Response) {
			if gf.Verbose > 0 {
				c.logVerboseResponse(resp, gf.Verbose)
			}
		},
	}, nil
}

func (c *CLI) cacheMaxBytes() (int64, error) {
	if c == nil || c.cfg == nil {
		return 0, nil
	}
	return cacheSizeStringToBytes(c.cfg.Cache.MaxSize)
}

func cacheSizeStringToBytes(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	parsed, err := parseByteSize(s)
	if err != nil {
		return 0, fmt.Errorf("invalid cache.max_size %q: %w", s, err)
	}
	return parsed, nil
}

// parseByteSize parses strings like "100MB", "64MiB", "1024", and returns bytes.
func parseByteSize(s string) (int64, error) {
	v := strings.TrimSpace(strings.ToUpper(s))
	if v == "" {
		return 0, fmt.Errorf("empty size")
	}
	if strings.HasPrefix(v, "-") {
		return 0, fmt.Errorf("size must not be negative")
	}

	mult := int64(1)
	switch {
	case strings.HasSuffix(v, "TIB"):
		mult = 1024 * 1024 * 1024 * 1024
		v = strings.TrimSuffix(v, "TIB")
	case strings.HasSuffix(v, "GIB"):
		mult = 1024 * 1024 * 1024
		v = strings.TrimSuffix(v, "GIB")
	case strings.HasSuffix(v, "MIB"):
		mult = 1024 * 1024
		v = strings.TrimSuffix(v, "MIB")
	case strings.HasSuffix(v, "KIB"):
		mult = 1024
		v = strings.TrimSuffix(v, "KIB")
	case strings.HasSuffix(v, "TB"):
		mult = 1000 * 1000 * 1000 * 1000
		v = strings.TrimSuffix(v, "TB")
	case strings.HasSuffix(v, "GB"):
		mult = 1000 * 1000 * 1000
		v = strings.TrimSuffix(v, "GB")
	case strings.HasSuffix(v, "MB"):
		mult = 1000 * 1000
		v = strings.TrimSuffix(v, "MB")
	case strings.HasSuffix(v, "KB"):
		mult = 1000
		v = strings.TrimSuffix(v, "KB")
	case strings.HasSuffix(v, "B"):
		v = strings.TrimSuffix(v, "B")
	}

	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("size must be >= 0")
	}
	return n * mult, nil
}
