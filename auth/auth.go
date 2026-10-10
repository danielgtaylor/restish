// Package auth is the public auth API for the restish config and CLI.
//
// The token cache (CachedToken, TokenStore, TokenCache) and the
// auth-handler interfaces (Handler, Param, AuthContext, Prompter, Logger,
// ForceCapable, RequestBound) are part of the supported public surface. External tools
// that need to share the restish OAuth token cache, or embed restish in
// a Go binary and register custom auth handlers, use this package.
//
// The restish CLI's bundled auth-handler implementations (api-key, basic,
// bearer, oauth-*, and the external-tool approval flow) are not exposed
// here; they live in the unexported internal/auth package because their
// field shapes are tied to the restish auth registry and may change.
// External embedders that need OAuth should use golang.org/x/oauth2
// directly rather than depending on restish's handlers.
package auth

import (
	"context"
	"io"
	"net/http"
)

// Param describes a configuration parameter required by an auth handler.
type Param struct {
	Name        string
	Description string
	Required    bool
	Secret      bool // true -> don't echo when prompting
}

// TokenStore persists OAuth-style bearer tokens keyed by API/profile.
// *TokenCache satisfies this interface.
type TokenStore interface {
	Get(key string) (*CachedToken, error)
	Set(key string, token CachedToken) error
	Delete(key string) error
	DeletePrefix(prefix string) error
}

// Prompter reads interactive values from the user.
type Prompter interface {
	Prompt(prompt string) (string, error)
	PromptSecret(prompt string) (string, error)
}

// Logger is the minimal logging surface auth handlers use for diagnostics.
type Logger interface {
	Printf(format string, v ...any)
}

// AuthContext carries the request-scoped auth environment into a handler.
type AuthContext struct {
	APIName     string
	ProfileName string
	BaseURL     string
	CacheKey    string
	Params      map[string]string // user-supplied only
	TokenStore  TokenStore
	Prompter    Prompter
	Stderr      io.Writer
	HTTPClient  *http.Client
	Logger      Logger
	Force       bool // bypass cached access tokens for a single retry
}

// Handler is implemented by each auth mechanism.
type Handler interface {
	Parameters() []Param
	Authenticate(ctx context.Context, req *http.Request, ac AuthContext) error
}

// ForceCapable marks handlers that can meaningfully bypass cached credentials
// after a 401 and retry once with fresh auth state.
type ForceCapable interface {
	SupportsForce()
}

// RequestBound marks handlers whose authentication value is bound to the
// exact method and URI. Restish invokes such handlers again for every
// transport retry and same-origin redirect instead of replaying a proof.
type RequestBound interface {
	SupportsRequestBinding()
}

// Compile-time check that *TokenCache satisfies TokenStore.
var _ TokenStore = (*TokenCache)(nil)
