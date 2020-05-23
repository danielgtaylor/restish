package cli

import (
	"net/http"
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
	req.SetBasicAuth(params["username"], params["password"])
	return nil
}
