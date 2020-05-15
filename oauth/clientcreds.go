package oauth

import (
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// ClientCredentialsHandler implements the Client Credentials OAuth2 flow.
type ClientCredentialsHandler struct{}

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
