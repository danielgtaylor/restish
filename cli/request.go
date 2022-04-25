package cli

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

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
		if c != nil && c.Base != "" {
			parts[0] = c.Base
			return strings.Join(parts, "/")
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

type requestOption struct {
	client     *http.Client
	disableLog bool
}

// WithClient sets the client to use for the request.
func WithClient(c *http.Client) requestOption {
	return requestOption{
		client: c,
	}
}

// WithoutLog disabled debug logging for the given request/response.
func WithoutLog() requestOption {
	return requestOption{
		disableLog: true,
	}
}

// MakeRequest makes an HTTP request using the default client. It adds the
// user-agent, auth, and any passed headers or query params to the request
// before sending it out on the wire. If verbose mode is enabled, it will
// print out both the request and response.
func MakeRequest(req *http.Request, options ...requestOption) (*http.Response, error) {
	start := time.Now()

	name, config := findAPI(req.URL.String())

	if config == nil {
		config = &APIConfig{Profiles: map[string]*APIProfile{
			"default": {},
		}}
	}

	profile := config.Profiles[viper.GetString("rsh-profile")]

	if profile == nil {
		if viper.GetString("rsh-profile") != "default" {
			panic("Invalid profile " + viper.GetString("rsh-profile"))
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

	// Save modified query string arguments.
	req.URL.RawQuery = query.Encode()

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

	log := true
	for _, option := range options {
		if option.client != nil {
			client = option.client
		}

		if option.disableLog {
			log = false
		}
	}

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
		if config.TLS.Cert != "" {
			cert, err := tls.LoadX509KeyPair(config.TLS.Cert, config.TLS.Key)
			if err != nil {
				return nil, err
			}
			t.TLSClientConfig.Certificates = append(t.TLSClientConfig.Certificates, cert)
		}
		if config.TLS.CACert != "" {
			caCert, err := ioutil.ReadFile(config.TLS.CACert)
			if err != nil {
				return nil, err
			}
			systemCerts := BestEffortSystemCertPool()
			if !systemCerts.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("Failed to append CACert %s RootCA list", config.TLS.CACert)
			}
			t.TLSClientConfig.RootCAs = systemCerts
		}
	}

	if log {
		LogDebugRequest(req)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if log {
		LogDebugResponse(start, resp)
	}

	return resp, nil
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
func (r Response) Map() map[string]interface{} {
	links := map[string][]map[string]interface{}{}

	for rel, list := range r.Links {
		if _, ok := links[rel]; !ok {
			links[rel] = []map[string]interface{}{}
		}

		for _, l := range list {
			links[rel] = append(links[rel], map[string]interface{}{
				"rel": l.Rel,
				"uri": l.URI,
			})
		}
	}

	return map[string]interface{}{
		"proto":   r.Proto,
		"status":  r.Status,
		"headers": r.Headers,
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

	data, _ := ioutil.ReadAll(resp.Body)

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
func GetParsedResponse(req *http.Request) (Response, error) {
	resp, err := MakeRequest(req)
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

		resp, err = MakeRequest(req)
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
