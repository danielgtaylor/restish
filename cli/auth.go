package cli

import "net/http"

// AuthHandler is used to register new authentication handlers that will apply
// auth to an outgoing request as needed.
type AuthHandler interface {
	OnRequest(req *http.Request, key string, params map[string]string) error
}

var authHandlers map[string]AuthHandler = map[string]AuthHandler{}

// AddAuth registers a new named auth handler.
func AddAuth(name string, h AuthHandler) {
	authHandlers[name] = h
}

// BasicAuth implements HTTP Basic authentication.
type BasicAuth struct{}

// OnRequest gets run before the request goes out on the wire.
func (a *BasicAuth) OnRequest(req *http.Request, key string, params map[string]string) error {
	req.SetBasicAuth(params["username"], params["password"])
	return nil
}
