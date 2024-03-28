package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/ThalesIgnite/crypto11"
	"github.com/danielgtaylor/shorthand/v2"
	"github.com/spf13/viper"
)

// lastStatus is the last HTTP status code returned by a request.
var lastStatus int

// GetLastStatus returns the last HTTP status code returned by a request. A
// request can opt out of this via the IgnoreStatus option.
func GetLastStatus() int {
	return lastStatus
}

// FixAddress can convert `:8000` or `example.com` to a full URL.
func FixAddress(addr string) string {
	return fixAddress(addr)
}

// fixAddress can convert `:8000` or `example.com` to a full URL.
func fixAddress(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "http://localhost" + addr
	}

	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		// Does the first part match a known API? If so, replace it with
		// the base URL for that API.
		parts := strings.Split(addr, "/")
		c := configs[parts[0]]
		if c != nil {
			p := c.Profiles[viper.GetString("rsh-profile")]
			if p == nil {
				if viper.GetString("rsh-profile") != "default" {
					panic("invalid profile " + viper.GetString("rsh-profile"))
				}
			}
			if p != nil && p.Base != "" {
				parts[0] = p.Base
				return strings.Join(parts, "/")
			} else if c.Base != "" {
				parts[0] = c.Base
				return strings.Join(parts, "/")
			}
		}

		// Local traffic defaults to HTTP, everything else uses TLS.
		if strings.Contains(addr, "localhost") {
			addr = "http://" + addr
		} else {
			addr = "https://" + addr
		}
	}

	return addr
}

type requestConfig struct {
	client          *http.Client
	disableLog      bool
	ignoreStatus    bool
	ignoreCLIParams bool
}

type requestOption func(*requestConfig)

// WithClient sets the client to use for the request.
func WithClient(c *http.Client) requestOption {
	return func(conf *requestConfig) {
		conf.client = c
	}
}

// WithoutLog disabled debug logging for the given request/response.
func WithoutLog() requestOption {
	return func(conf *requestConfig) {
		conf.disableLog = true
	}
}

// IgnoreStatus ignores the response status code.
func IgnoreStatus() requestOption {
	return func(conf *requestConfig) {
		conf.ignoreStatus = true
	}
}

// IgnoreCLIParams only applies the profile, but ignores commandline and env params
func IgnoreCLIParams() requestOption {
	return func(conf *requestConfig) {
		conf.ignoreCLIParams = true
	}
}

// MakeRequest makes an HTTP request using the default client. It adds the
// user-agent, auth, and any passed headers or query params to the request
// before sending it out on the wire. If verbose mode is enabled, it will
// print out both the request and response.
func MakeRequest(req *http.Request, options ...requestOption) (*http.Response, error) {
	requestConf := &requestConfig{}
	for _, opt := range options {
		opt(requestConf)
	}

	name, config := findAPI(req.URL.String())

	if config == nil {
		config = &APIConfig{Profiles: map[string]*APIProfile{
			"default": {},
		}}
	}

	profile := config.Profiles[viper.GetString("rsh-profile")]

	if profile == nil {
		if viper.GetString("rsh-profile") != "default" {
			panic("invalid profile " + viper.GetString("rsh-profile"))
		}
		profile = &APIProfile{}
	}

	// Now that we have the profile, set up profile-based headers/params.
	query := req.URL.Query()
	for k, v := range profile.Headers {
		if req.Header.Get(k) == "" {
			req.Header.Add(k, os.ExpandEnv(v))
		}
	}

	for k, v := range profile.Query {
		if query.Get(k) == "" {
			query.Add(k, v)
		}
	}

	if !requestConf.ignoreCLIParams {
		// Allow env vars and commandline arguments to override config.
		for _, h := range viper.GetStringSlice("rsh-header") {
			parts := strings.SplitN(h, ":", 2)
			value := ""
			if len(parts) > 1 {
				value = parts[1]
			}

			req.Header.Add(parts[0], value)
		}

		for _, q := range viper.GetStringSlice("rsh-query") {
			parts := strings.SplitN(q, "=", 2)
			value := ""
			if len(parts) > 1 {
				value = parts[1]
			}

			query.Add(parts[0], value)
		}
	}

	// Save modified query string arguments.
	req.URL.RawQuery = query.Encode()

	// The assumption is that all Transport implementations eventually use the
	// default HTTP transport.
	// We can therefore inject the TLS config once here, along with all the other
	// config options, instead of modifying all the places where Transports are
	// created
	LogDebug("Adding TLS configuration")
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{}
		}
		if config.TLS == nil {
			config.TLS = &TLSConfig{}
		}

		// CLI flags overwrite profile options
		if viper.GetBool("rsh-insecure") {
			config.TLS.InsecureSkipVerify = true
		}
		if cert := viper.GetString("rsh-client-cert"); cert != "" {
			config.TLS.Cert = cert
		}
		if key := viper.GetString("rsh-client-key"); key != "" {
			config.TLS.Key = key
		}
		if caCert := viper.GetString("rsh-ca-cert"); caCert != "" {
			config.TLS.CACert = caCert
		}

		if config.TLS.InsecureSkipVerify {
			LogWarning("Disabling TLS security checks")
			t.TLSClientConfig.InsecureSkipVerify = config.TLS.InsecureSkipVerify
		}

		if config.TLS.PKCS11 != nil {
			t.TLSClientConfig.GetClientCertificate = getCertFromPkcs11(config.TLS.PKCS11)
		}

		if config.TLS.Cert != "" {
			cert, err := tls.LoadX509KeyPair(config.TLS.Cert, config.TLS.Key)
			if err != nil {
				return nil, err
			}
			t.TLSClientConfig.Certificates = append(t.TLSClientConfig.Certificates, cert)
		}
		if config.TLS.CACert != "" {
			caCert, err := os.ReadFile(config.TLS.CACert)
			if err != nil {
				return nil, err
			}
			systemCerts := BestEffortSystemCertPool()
			if !systemCerts.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to append CACert %s RootCA list", config.TLS.CACert)
			}
			t.TLSClientConfig.RootCAs = systemCerts
		}
	}

	// Add auth if needed.
	if profile.Auth != nil && profile.Auth.Name != "" {
		auth, ok := authHandlers[profile.Auth.Name]
		if ok {
			err := auth.OnRequest(req, name+":"+viper.GetString("rsh-profile"), profile.Auth.Params)
			if err != nil {
				panic(err)
			}
		}
	}

	if req.Header.Get("user-agent") == "" {
		req.Header.Set("user-agent", "restish-"+Root.Version)
	}

	if req.Header.Get("accept") == "" {
		req.Header.Set("accept", buildAcceptHeader())
	}

	if req.Header.Get("accept-encoding") == "" {
		req.Header.Set("accept-encoding", buildAcceptEncodingHeader())
	}

	if req.Header.Get("content-type") == "" && req.Body != nil {
		// We have a body but no content-type; default to JSON.
		req.Header.Set("content-type", "application/json; charset=utf-8")
	}

	client := CachedTransport().Client()
	if viper.GetBool("rsh-no-cache") {
		client = &http.Client{Transport: InvalidateCachedTransport()}
	}

	if requestConf.client != nil {
		client = requestConf.client
	}

	resp, err := doRequestWithRetry(!requestConf.disableLog, client, req)
	if err != nil {
		return nil, err
	}

	if !requestConf.ignoreStatus {
		lastStatus = resp.StatusCode
	}

	return resp, nil
}

func getCertFromPkcs11(config *PKCS11Config) func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	path := config.Path

	// Try to give a useful default if they don't give a path to the plugin.
	if path == "" {
		if _, err := os.Stat("/usr/lib/x86_64-linux-gnu/opensc-pkcs11.so"); !errors.Is(err, os.ErrNotExist) {
			path = "/usr/lib/x86_64-linux-gnu/opensc-pkcs11.so"
		}
		if _, err := os.Stat("/usr/lib/pkcs11/opensc-pkcs11.so"); !errors.Is(err, os.ErrNotExist) {
			path = "/usr/lib/pkcs11/opensc-pkcs11.so"
		}
		// macos
		if _, err := os.Stat("/opt/homebrew/lib/opensc-pkcs11.so"); !errors.Is(err, os.ErrNotExist) {
			path = "/opt/homebrew/lib/opensc-pkcs11.so"
		}
	}

	pin := os.Getenv("YBPIN")
	if pin == "" {
		err := survey.AskOne(&survey.Password{Message: "PIN for your PKCS11 device:"}, &pin)
		if err == terminal.InterruptErr {
			os.Exit(0)
		}
		if err != nil {
			return func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return nil, err }
		}
	}

	cfg := &crypto11.Config{
		Path:       path,
		TokenLabel: config.Label,
		Pin:        pin,
	}
	context, err := crypto11.Configure(cfg)
	if err != nil {
		return func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return nil, err }
	}

	certificates, err := context.FindAllPairedCertificates()
	if err != nil {
		return func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return nil, err }
	}

	if len(certificates) == 0 {
		return func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return nil, errors.New("no certificate found in your pkcs11 device")
		}
	}

	if len(certificates) > 1 {
		return func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return nil, errors.New("got more than one certificate")
		}
	}

	return func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &certificates[0], nil }
}

// isRetryable returns true if a request should be retried.
func isRetryable(code int) bool {
	if code == /* 408 */ http.StatusRequestTimeout ||
		code == /*  425 */ http.StatusTooEarly ||
		code == /*  429 */ http.StatusTooManyRequests ||
		code == /*  500 */ http.StatusInternalServerError ||
		code == /*  502 */ http.StatusBadGateway ||
		code == /*  503 */ http.StatusServiceUnavailable ||
		code == /*  504 */ http.StatusGatewayTimeout {
		return true
	}
	return false
}

// doRequestWithRetry logs and makes a request, retrying as needed (if
// configured) and returning the last response.
func doRequestWithRetry(log bool, client *http.Client, req *http.Request) (*http.Response, error) {
	retries := viper.GetInt("rsh-retry")

	if retries == 0 {
		return client.Do(req)
	}

	var bodyContents []byte
	if req.Body != nil {
		bodyContents, _ = io.ReadAll(req.Body)
	}

	var resp *http.Response
	var err error
	triesLeft := 1 + retries
	for triesLeft > 0 {
		triesLeft--

		if len(bodyContents) > 0 {
			// Reset the body reader for each retry.
			req.Body = io.NopCloser(bytes.NewReader(bodyContents))
		}

		if log {
			LogDebugRequest(req)
		}

		if timeout := viper.GetDuration("rsh-timeout"); timeout > 0 {
			ctx, cancel := context.WithTimeout(req.Context(), timeout)
			defer cancel()
			req = req.WithContext(ctx)
		}

		start := time.Now()
		resp, err = client.Do(req)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if triesLeft > 0 {
					// Try again after letting the user know.
					LogWarning("Got request timeout after %s, retrying", viper.GetDuration("rsh-timeout").Truncate(time.Millisecond))
					continue
				} else {
					// Add a human-friendly error before the original (context deadline
					// exceeded).
					err = fmt.Errorf("Request timed out after %s: %w", viper.GetDuration("rsh-timeout"), err)
				}
			}
			return resp, err
		}

		if log {
			LogDebugResponse(start, resp)
		}

		if triesLeft > 0 && isRetryable(resp.StatusCode) {
			// Attempt to parse when to retry! Default is 1 second.
			retryAfter := 1 * time.Second

			if v := resp.Header.Get("Retry-After"); v != "" {
				// Could be either an integer number of seconds, or an HTTP date.
				if d, err := strconv.ParseInt(v, 10, 64); err == nil {
					retryAfter = time.Duration(d) * time.Second
				}

				if d, err := http.ParseTime(v); err == nil {
					retryAfter = time.Until(d)
				}
			}

			if v := resp.Header.Get("X-Retry-In"); v != "" {
				if d, err := time.ParseDuration(v); err == nil {
					retryAfter = d
				}
			}

			LogWarning("Got %s, retrying in %s", resp.Status, retryAfter.Truncate(time.Millisecond))
			time.Sleep(retryAfter)

			continue
		}
		break
	}

	return resp, err
}

// Response describes a parsed HTTP response which can be marshalled to enable
// printing and filtering/projection.
type Response struct {
	Proto   string            `json:"proto"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Links   Links             `json:"links"`
	Body    interface{}       `json:"body"`
}

// Map returns a map representing this response matching the encoded JSON.
func (r Response) Map() map[string]any {
	links := map[string]any{}

	for rel, list := range r.Links {
		lrel := links[rel]
		if lrel == nil {
			lrel = []any{}
		}

		for _, l := range list {
			lrel = append(lrel.([]any), map[string]any{
				"rel": l.Rel,
				"uri": l.URI,
			})
		}

		links[rel] = lrel
	}

	headers := map[string]any{}
	for k, v := range r.Headers {
		headers[k] = v
	}

	return map[string]any{
		"proto":   r.Proto,
		"status":  r.Status,
		"headers": headers,
		"links":   links,
		"body":    r.Body,
	}
}

// ParseResponse takes an HTTP response and tries to parse it using the
// registered content types. It returns a map representing the request,
func ParseResponse(resp *http.Response) (Response, error) {
	var parsed interface{}

	// Handle content encodings
	defer resp.Body.Close()
	if err := DecodeResponse(resp); err != nil {
		return Response{}, err
	}

	data, _ := io.ReadAll(resp.Body)

	if len(data) > 0 {
		if viper.GetBool("rsh-raw") && viper.GetString("rsh-filter") == "" {
			// Raw mode without filtering, don't parse the response.
			parsed = data
		} else {
			ct := resp.Header.Get("content-type")
			if err := Unmarshal(ct, data, &parsed); err != nil {
				parsed = data
			}
		}
	}

	// Wrap the body to describe the entire response
	headers := map[string]string{}
	output := Response{
		Proto:   resp.Proto,
		Status:  resp.StatusCode,
		Headers: headers,
		Links:   Links{},
		Body:    parsed,
	}

	for k, v := range resp.Header {
		joiner := ", "
		if k == "Set-Cookie" {
			joiner = "\n"
		}
		headers[k] = strings.Join(v, joiner)
	}

	if err := ParseLinks(resp.Request.URL, &output); err != nil {
		LogWarning("Parse links failed")
		return Response{}, err
	}

	return output, nil
}

// GetParsedResponse makes a request and gets the parsed response back. It
// handles any auto-pagination or linking that needs to be done and may
// return a psuedo-responsse that is a combination of all responses.
func GetParsedResponse(req *http.Request, options ...requestOption) (Response, error) {
	resp, err := MakeRequest(req, options...)
	if err != nil {
		return Response{}, err
	}

	parsed, err := ParseResponse(resp)
	if err != nil {
		LogError("Parse response error")
		return Response{}, err
	}

	computedSize := int64(0)
	if s, err := strconv.ParseInt(parsed.Headers["Content-Length"], 10, 64); err == nil {
		computedSize = s
	}

	base := req.URL
	allLinks := parsed.Links
	for {
		links := parsed.Links
		if len(links["next"]) == 0 || viper.GetBool("rsh-no-paginate") {
			break
		}

		LogDebug("Found pagination via rel=next link: %s", links["next"][0].URI)

		if _, ok := parsed.Body.([]interface{}); !ok {
			// TODO: support non-list formats like JSON:API
			LogWarning("Skipping auto-pagination: response body not a list, not sure how to merge")
			break
		}

		// Make the next request
		next, _ := url.Parse(links["next"][0].URI)
		next = base.ResolveReference(next)
		req, _ = http.NewRequest(http.MethodGet, next.String(), nil)

		resp, err = MakeRequest(req, options...)
		if err != nil {
			return Response{}, err
		}

		// Merge the responses
		parsedNext, err := ParseResponse(resp)
		if err != nil {
			return Response{}, err
		}

		if l, ok := parsedNext.Body.([]interface{}); ok {
			// The last request in the chain will be the one that gets displayed
			// for the proto/status/headers, plus the merged body/links.
			parsed.Proto = parsedNext.Proto
			parsed.Status = parsedNext.Status
			parsed.Headers = parsedNext.Headers
			parsed.Links = parsedNext.Links
			parsed.Body = append(parsed.Body.([]interface{}), l...)

			for name, links := range parsedNext.Links {
				allLinks[name] = append(allLinks[name], links...)
			}

			// Update the total computed size to include the size of each individual
			// request if the content size is available.
			if s, err := strconv.ParseInt(parsedNext.Headers["Content-Length"], 10, 64); err == nil {
				computedSize += s
			}
		} else {
			LogWarning("Auto-pagination next page is not a list, aborting")
			break
		}
	}

	// Set the final response links as a combination of all.
	parsed.Links = allLinks

	if computedSize > 0 {
		parsed.Headers["Content-Length"] = fmt.Sprintf("%d", computedSize)
	}

	return parsed, nil
}

// MakeRequestAndFormat is a convenience function for calling `GetParsedResponse`
// and then calling the default formatter's `Format` function with the parsed
// response. Panics on error.
func MakeRequestAndFormat(req *http.Request) {
	parsed, err := GetParsedResponse(req)
	if err != nil {
		panic(err)
	}

	if err := Formatter.Format(parsed); err != nil {
		if e, ok := err.(shorthand.Error); ok {
			panic(e.Pretty())
		}
		panic(err)
	}
}

// BestEffortSystemCertPool returns system cert pool as best effort, otherwise an empty cert pool
func BestEffortSystemCertPool() *x509.CertPool {
	rootCAs, _ := x509.SystemCertPool()
	if rootCAs == nil {
		return x509.NewCertPool()
	}
	return rootCAs
}
