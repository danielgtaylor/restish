package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// AuthParam describes an auth input parameter for an AuthHandler.
type AuthParam struct {
	Name     string
	Help     string
	Required bool
}

// AuthHandler is used to register new authentication handlers that will apply
// auth to an outgoing request as needed.
type AuthHandler interface {
	// Parameters returns an ordered list of required and optional input
	// parameters for this auth handler. Used when configuring an API.
	Parameters() []AuthParam

	// OnRequest applies auth to an outgoing request before it hits the wire.
	OnRequest(req *http.Request, key string, params map[string]string) error
}

var authHandlers map[string]AuthHandler = map[string]AuthHandler{}

// AddAuth registers a new named auth handler.
func AddAuth(name string, h AuthHandler) {
	authHandlers[name] = h
}

// BasicAuth implements HTTP Basic authentication.
type BasicAuth struct{}

// Parameters define the HTTP Basic Auth parameter names.
func (a *BasicAuth) Parameters() []AuthParam {
	return []AuthParam{
		{Name: "username", Required: true},
		{Name: "password", Required: true},
	}
}

// OnRequest gets run before the request goes out on the wire.
func (a *BasicAuth) OnRequest(req *http.Request, key string, params map[string]string) error {
	_, usernamePresent := params["username"]
	_, passwordPresent := params["password"]

	if usernamePresent && !passwordPresent {
		fmt.Print("password: ")
		inputPassword, err := term.ReadPassword(int(syscall.Stdin))
		if err == nil {
			params["password"] = string(inputPassword)
		}
		fmt.Println()
	}

	req.SetBasicAuth(params["username"], params["password"])
	return nil
}

// ExternalToolAuth defers authentication to a third party tool.
// This avoids baking all possible authentication implementations
// inside restish itself.
type ExternalToolAuth struct{}

// Request is used to exchange requests with the external tool.
type Request struct {
	Method string      `json:"method"`
	URI    string      `json:"uri"`
	Header http.Header `json:"headers"`
	Body   string      `json:"body"`
}

// Parameters defines the ExternalToolAuth parameter names.
// A single parameter is supported and required: `commandline` which
// points to the tool to call to authenticate a request.
func (a *ExternalToolAuth) Parameters() []AuthParam {
	return []AuthParam{
		{Name: "commandline", Required: true},
		{Name: "omitbody", Required: false},
	}
}

// OnRequest gets run before the request goes out on the wire.
// The supplied commandline argument is ran with a JSON input
// and expects a JSON output on stdout
func (a *ExternalToolAuth) OnRequest(req *http.Request, key string, params map[string]string) error {
	commandLine, _ := params["commandline"]
	omitBodyStr, omitBodyPresent := params["omitbody"]
	omitBody := false
	if omitBodyPresent && strings.EqualFold(omitBodyStr, "true") {
		omitBody = true
	}
	shell, shellPresent := os.LookupEnv("SHELL")
	if !shellPresent {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-c", commandLine)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	bodyStr := ""
	if req.Body != nil && !omitBody {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return err
		}
		bodyStr = string(bodyBytes)
		req.Body = io.NopCloser(strings.NewReader(bodyStr))
	}

	textRequest := Request{
		Method: req.Method,
		URI:    req.URL.String(),
		Header: req.Header,
		Body:   bodyStr,
	}
	requestBytes, err := json.Marshal(textRequest)
	if err != nil {
		return err
	}
	_, err = stdin.Write(requestBytes)
	if err != nil {
		return err
	}
	stdin.Close()
	outBytes, err := cmd.Output()
	if err != nil {
		return err
	}
	if len(outBytes) <= 0 {
		return nil
	}
	var requestUpdates Request
	err = json.Unmarshal(outBytes, &requestUpdates)
	if err != nil {
		return err
	}

	if len(requestUpdates.URI) > 0 {
		req.URL, err = url.Parse(requestUpdates.URI)
		if err != nil {
			return err
		}
	}

	for k, vs := range requestUpdates.Header {
		for _, v := range vs {
			// A single value is supported for each header
			req.Header.Set(k, v)
		}
	}
	return nil
}
