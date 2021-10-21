package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/restish/cli"
	"golang.org/x/oauth2"
)

// PasswordHandler implements the Client Credentials OAuth2 flow.
type PasswordHandler struct{}

// Parameters returns a list of OAuth2 Authorization Code inputs.
func (h *PasswordHandler) Parameters() []cli.AuthParam {
	return []cli.AuthParam{
		{Name: "client_id", Required: true, Help: "OAuth 2.0 Client ID"},
		{Name: "client_secret", Required: true, Help: "OAuth 2.0 Client Secret"},
		{Name: "token_url", Required: true, Help: "OAuth 2.0 token URL, e.g. https://api.example.com/oauth/token"},
		{Name: "scopes", Help: "Optional scopes to request in the token"},
	}
}

// OnRequest gets run before the request goes out on the wire.
func (h *PasswordHandler) OnRequest(request *http.Request, key string, params map[string]string) error {
	if request.Header.Get("Authorization") == "" {
		if params["client_id"] == "" {
			return ErrInvalidProfile
		}

		if params["username"] == "" {
			return ErrInvalidProfile
		}

		if params["password"] == "" {
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

		conf := (&oauth2.Config{
			ClientID:     params["client_id"],
			ClientSecret: params["client_secret"],
			Endpoint: oauth2.Endpoint{
				TokenURL: params["token_url"],
			},
			Scopes: strings.Split(params["scopes"], ","),
		})

		token, err := conf.PasswordCredentialsToken(context.Background(), params["username"], params["password"])

		if err != nil {
			fmt.Println(err)
			return ErrInvalidProfile
		}

		return TokenHandler(conf.TokenSource(context.Background(), token), key, request)
	}

	return nil
}
