package oauth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/cgardens/restish/cli"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// ClientCredentialsHandler implements the Client Credentials OAuth2 flow.
type ClientCredentialsHandler struct{}

// Parameters returns a list of OAuth2 Authorization Code inputs.
func (h *ClientCredentialsHandler) Parameters() []cli.AuthParam {
	return []cli.AuthParam{
		{Name: "client_id", Required: true, Help: "OAuth 2.0 Client ID"},
		{Name: "client_secret", Required: true, Help: "OAuth 2.0 Client Secret"},
		{Name: "token_url", Required: true, Help: "OAuth 2.0 token URL, e.g. https://api.example.com/oauth/token"},
		{Name: "scopes", Help: "Optional scopes to request in the token"},
	}
}

// OnRequest gets run before the request goes out on the wire.
func (h *ClientCredentialsHandler) OnRequest(request *http.Request, key string, params map[string]string) error {
	if request.Header.Get("Authorization") == "" {
		if params["client_id"] == "" {
			return ErrInvalidProfile
		}

		if params["client_secret"] == "" {
			return ErrInvalidProfile
		}

		if params["token_url"] == "" {
			return ErrInvalidProfile
		}

		endpointParams := url.Values{}
		for k, v := range params {
			if k == "client_id" || k == "client_secret" || k == "scopes" || k == "token_url" {
				// Not a custom param...
				continue
			}

			endpointParams.Add(k, v)
		}

		source := (&clientcredentials.Config{
			ClientID:       params["client_id"],
			ClientSecret:   params["client_secret"],
			TokenURL:       params["token_url"],
			EndpointParams: endpointParams,
			Scopes:         strings.Split(params["scopes"], ","),
		}).TokenSource(oauth2.NoContext)

		return TokenHandler(source, key, request)
	}

	return nil
}
