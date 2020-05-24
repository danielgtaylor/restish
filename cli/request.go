package cli

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/peterhellberg/link"
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

// MakeRequest makes an HTTP request using the default client. It adds the
// user-agent, auth, and any passed headers or query params to the request
// before sending it out on the wire. If verbose mode is enabled, it will
// print out both the request and response.
func MakeRequest(req *http.Request) (*http.Response, error) {
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

	if profile.Auth != nil && profile.Auth.Name != "" {
		auth, ok := authHandlers[profile.Auth.Name]
		if ok {
			auth.OnRequest(req, name+":"+viper.GetString("rsh-profile"), profile.Auth.Params)
		}
	}

	if req.Header.Get("user-agent") == "" {
		req.Header.Set("user-agent", "restish-"+Root.Version)
	}

	for _, h := range viper.GetStringSlice("rsh-header") {
		parts := strings.SplitN(h, ":", 2)
		value := ""
		if len(parts) > 1 {
			value = parts[1]
		}

		req.Header.Add(parts[0], value)
	}

	for k, v := range profile.Headers {
		if req.Header.Get(k) == "" {
			req.Header.Add(k, v)
		}
	}

	if req.Header.Get("accept") == "" {
		req.Header.Set("accept", buildAcceptHeader())
	}

	if req.Header.Get("accept-encoding") == "" {
		req.Header.Set("accept-encoding", buildAcceptEncodingHeader())
	}

	query := req.URL.Query()
	for _, q := range viper.GetStringSlice("rsh-query") {
		parts := strings.SplitN(q, "=", 2)
		value := ""
		if len(parts) > 1 {
			value = parts[1]
		}

		query.Add(parts[0], value)
	}

	for k, v := range profile.Query {
		if query.Get(k) == "" {
			query.Add(k, v)
		}
	}

	req.URL.RawQuery = query.Encode()

	LogDebugRequest(req)

	client := CachedTransport().Client()
	if viper.GetBool("rsh-no-cache") {
		client = &http.Client{Transport: InvalidateCachedTransport()}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	LogDebugResponse(start, resp)

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
		ct := resp.Header.Get("content-type")
		if err := Unmarshal(ct, data, &parsed); err != nil {
			parsed = data
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
		return Response{}, err
	}

	computedSize := int64(0)
	if s, err := strconv.ParseInt(parsed.Headers["Content-Length"], 10, 64); err == nil {
		computedSize = s
	}

	base := req.URL
	for {
		links := link.ParseResponse(resp)
		if links["next"] == nil || viper.GetBool("rsh-no-paginate") {
			break
		}

		LogDebug("Found pagination via rel=next link: %s", links["next"].URI)

		if _, ok := parsed.Body.([]interface{}); !ok {
			// TODO: support non-list formats like JSON:API
			LogWarning("Skipping auto-pagination: response body not a list, not sure how to merge")
			break
		}

		// Make the next request
		next, _ := url.Parse(links["next"].URI)
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
			// for the proto/status/headers, plus the merged body.
			parsed.Proto = parsedNext.Proto
			parsed.Status = parsedNext.Status
			parsed.Headers = parsedNext.Headers
			parsed.Body = append(parsed.Body.([]interface{}), l...)

			// Update the total computed size to include the size of each individual
			// request.
			if s, err := strconv.ParseInt(parsedNext.Headers["Content-Length"], 10, 64); err == nil {
				computedSize += s
			} else {
				LogError("%v", err)
			}
		} else {
			LogWarning("Auto-pagination next page is not a list, aborting")
			break
		}
	}

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
