package oauth

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/restish/cli"
	"golang.org/x/oauth2"
)

// ErrInvalidProfile is thrown when a profile is missing or invalid.
var ErrInvalidProfile = errors.New("invalid profile")

// TokenHandler takes a token source, gets a token, and modifies a request to
// add the token auth as a header. Uses the CLI cache to store tokens on a per-
// profile basis between runs.
func TokenHandler(source oauth2.TokenSource, key string, request *http.Request) error {
	var cached *oauth2.Token

	// Load any existing token from the CLI's cache file.
	expiresKey := key + ".expires"
	typeKey := key + ".type"
	tokenKey := key + ".token"
	refreshKey := key + ".refresh"

	expiry := cli.Cache.GetTime(expiresKey)
	if !expiry.IsZero() {
		cli.LogDebug("Loading OAuth2 token from cache.")
		cached = &oauth2.Token{
			AccessToken:  cli.Cache.GetString(tokenKey),
			RefreshToken: cli.Cache.GetString(refreshKey),
			TokenType:    cli.Cache.GetString(typeKey),
			Expiry:       expiry,
		}
	}

	if cached != nil {
		// Wrap the token source preloaded with our cached token.
		source = oauth2.ReuseTokenSource(cached, source)
	}

	// Get the next available token from the source.
	token, err := source.Token()
	if err != nil {
		return err
	}

	if cached == nil || (token.AccessToken != cached.AccessToken) {
		// Token either didn't exist in the cache or has changed, so let's write
		// the new values to the CLI cache.
		cli.LogDebug("Token refreshed. Updating cache.")

		cli.Cache.Set(expiresKey, token.Expiry)
		cli.Cache.Set(typeKey, token.Type())
		cli.Cache.Set(tokenKey, token.AccessToken)

		if token.RefreshToken != "" {
			// Only set the refresh token if present. This prevents overwriting it
			// after using a refresh token, because the newly returned token won't
			// have another refresh token set on it (you keep using the same one).
			cli.Cache.Set(refreshKey, token.RefreshToken)
		}

		// Save the cache to disk.
		if err := cli.Cache.WriteConfig(); err != nil {
			return err
		}
	}

	// Set the auth header so the request can be made.
	token.SetAuthHeader(request)
	return nil
}
