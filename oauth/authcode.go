package oauth

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"context"

	"github.com/cgardens/restish/cli"
	"golang.org/x/oauth2"
)

var htmlSuccess = `
<html>
  <style>
    @keyframes bg {
      from {background: white;}
      to {background: #5fafd7;}
    }
    @keyframes x {
      from {transform: rotate(0deg) skew(30deg, 20deg);}
      to {transform: rotate(-45deg);}
    }
    @keyframes fade {
      from {opacity: 0;}
      to {opacity: 1;}
    }
    body { font-family: sans-serif; margin-top: 8%; animation: bg 1.5s ease-out; animation-fill-mode: forwards; }
    p { width: 80%; }
    .check {
      margin: auto;
      width: 18%;
      height: 15%;
      border-left: 16px solid white;
      border-bottom: 16px solid white;
      animation: x 0.7s cubic-bezier(0.175, 0.885, 0.32, 1.275);
      animation-fill-mode: forwards;
    }
    .msg {
      margin: auto;
      margin-top: 180px;
      width: 40%;
      background: white;
      padding: 20px 32px;
      border-radius: 10px;
      animation: fade 2s;
      animation-fill-mode: forwards;
      box-shadow: 0px 15px 15px -15px rgba(0, 0, 0, 0.5);
    }
  </style>
  <body>
    <div class="check"></div>
    <div class="msg">
        <h1>Login Successful!</h1>
        Please return to the terminal. You may now close this window.
      </p>
    </div>
  </body>
</html>
`

var htmlError = `
<html>
  <style>
    @keyframes bg {
      from {background: white;}
      to {background: #E94F37;}
    }
    @keyframes x {
      from {transform: scaleY(0);}
      to {transform: scaleY(1) rotate(-90deg);}
    }
    @keyframes fade {
      from {opacity: 0;}
      to {opacity: 1;}
    }
    body { font-family: sans-serif; margin-top: 15%; animation: bg 1.5s ease-out; animation-fill-mode: forwards; }
    p { width: 80%; }
    .x, .x:after {
      margin: auto;
      background: white;
      width: 20%;
      height: 16px;
      border-radius: 3px;
      transform: rotate(-45deg);
      animation: x 0.7s cubic-bezier(0.175, 0.885, 0.32, 1.275);
      animation-fill-mode: forwards;
    }
    .x:after {
      content: "";
      display: block;
      width: 100%;
      transform: rotate(90deg);
    }
    .msg {
      margin: auto;
      margin-top: 200px;
      width: 40%;
      background: white;
      padding: 20px 32px;
      border-radius: 10px;
      animation: fade 2s;
      animation-fill-mode: forwards;
      box-shadow: 0px 15px 15px -15px rgba(0, 0, 0, 0.5);
    }
  </style>
  <body>
    <div style="transform: rotate(-45deg);"><div class="x"></div></div>
    <div class="msg">
        <h1>Error: $ERROR</h1>
        $DETAILS
      </p>
    </div>
  </body>
</html>
`

// open opens the specified URL in the default browser regardless of OS.
func open(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin": // mac, ios
		cmd = "open"
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}

// getInput waits for user input and sends it to the input channel with the
// trailing newline removed.
func getInput(input chan string) {
	r := bufio.NewReader(os.Stdin)
	result, err := r.ReadString('\n')
	if err != nil {
		panic(err)
	}

	input <- strings.TrimRight(result, "\n")
}

// authHandler is an HTTP handler that takes a channel and sends the `code`
// query param when it gets a request.
type authHandler struct {
	c chan string
}

func (h authHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	if err := r.URL.Query().Get("error"); err != "" {
		details := r.URL.Query().Get("error_description")
		rendered := strings.Replace(strings.Replace(htmlError, "$ERROR", err, 1), "$DETAILS", details, 1)
		w.Write([]byte(rendered))
		h.c <- ""
		return
	}

	h.c <- r.URL.Query().Get("code")
	w.Write([]byte(htmlSuccess))
}

// AuthorizationCodeTokenSource with PKCE as described in:
// https://www.oauth.com/oauth2-servers/pkce/
// This works by running a local HTTP server on port 8484 and then having the
// user log in through a web browser, which redirects to the local server with
// an authorization code. That code is then used to make another HTTP request
// to fetch an auth token (and refresh token). That token is then in turn
// used to make requests against the API.
type AuthorizationCodeTokenSource struct {
	ClientID       string
	ClientSecret   string
	AuthorizeURL   string
	TokenURL       string
	EndpointParams *url.Values
	Scopes         []string
}

// Token generates a new token using an authorization code.
func (ac *AuthorizationCodeTokenSource) Token() (*oauth2.Token, error) {
	// Generate a random code verifier string
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, err
	}

	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Generate a code challenge. Only the challenge is sent when requesting a
	// code which allows us to keep it secret for now.
	shaBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(shaBytes[:])

	// Generate a URL with the challenge to have the user log in.
	authorizeURL, err := url.Parse(ac.AuthorizeURL)
	if err != nil {
		panic(err)
	}

	aq := authorizeURL.Query()
	aq.Set("response_type", "code")
	aq.Set("code_challenge", challenge)
	aq.Set("code_challenge_method", "S256")
	aq.Set("client_id", ac.ClientID)
	aq.Set("redirect_uri", "http://localhost:8484/")
	aq.Set("scope", strings.Join(ac.Scopes, " "))
	if ac.EndpointParams != nil {
		for k, v := range *ac.EndpointParams {
			aq.Set(k, v[0])
		}
	}
	authorizeURL.RawQuery = aq.Encode()

	// Run server before opening the user's browser so we are ready for any redirect.
	codeChan := make(chan string)
	handler := authHandler{
		c: codeChan,
	}

	s := &http.Server{
		Addr:           "localhost:8484",
		Handler:        handler,
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   5 * time.Second,
		MaxHeaderBytes: 1024,
	}

	go func() {
		// Run in a goroutine until the server is closed or we get an error.
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			panic(err)
		}
	}()

	// Open auth URL in browser, print for manual use in case open fails.
	fmt.Println("Open your browser to log in using the URL:")
	fmt.Println(authorizeURL.String())
	open(authorizeURL.String())

	// Provide a way to manually enter the code, e.g. for remote SSH sessions.
	fmt.Print("Alternatively, enter the code manually: ")
	manualCodeChan := make(chan string)
	go getInput(manualCodeChan)

	// Get code from handler, exchange it for a token, and then return it. This
	// select blocks until one code becomes available.
	// There is currently no timeout.
	var code string
	select {
	case code = <-codeChan:
	case code = <-manualCodeChan:
	}
	fmt.Println("")
	s.Shutdown(context.Background())

	if code == "" {
		fmt.Println("Unable to get a code. See browser for details. Aborting!")
		os.Exit(1)
	}

	payload := url.Values{}
	payload.Set("grant_type", "authorization_code")
	payload.Set("client_id", ac.ClientID)
	payload.Set("code_verifier", verifier)
	payload.Set("code", code)
	payload.Set("redirect_uri", "http://localhost:8484/")
	if ac.ClientSecret != "" {
		payload.Set("client_secret", ac.ClientSecret)
	}

	return requestToken(ac.TokenURL, payload.Encode())
}

// AuthorizationCodeHandler sets up the OAuth 2.0 authorization code with PKCE authentication
// flow.
type AuthorizationCodeHandler struct{}

// Parameters returns a list of OAuth2 Authorization Code inputs.
func (h *AuthorizationCodeHandler) Parameters() []cli.AuthParam {
	return []cli.AuthParam{
		{Name: "client_id", Required: true, Help: "OAuth 2.0 Client ID"},
		{Name: "client_secret", Required: false, Help: "OAuth 2.0 Client Secret if exists"},
		{Name: "authorize_url", Required: true, Help: "OAuth 2.0 authorization URL, e.g. https://api.example.com/oauth/authorize"},
		{Name: "token_url", Required: true, Help: "OAuth 2.0 token URL, e.g. https://api.example.com/oauth/token"},
		{Name: "scopes", Help: "Optional scopes to request in the token"},
	}
}

// OnRequest gets run before the request goes out on the wire.
func (h *AuthorizationCodeHandler) OnRequest(request *http.Request, key string, params map[string]string) error {
	if request.Header.Get("Authorization") == "" {
		endpointParams := url.Values{}
		for k, v := range params {
			if k == "client_id" || k == "client_secret" || k == "scopes" || k == "authorize_url" || k == "token_url" {
				// Not a custom param...
				continue
			}

			endpointParams.Add(k, v)
		}

		source := &AuthorizationCodeTokenSource{
			ClientID:       params["client_id"],
			ClientSecret:   params["client_secret"],
			AuthorizeURL:   params["authorize_url"],
			TokenURL:       params["token_url"],
			EndpointParams: &endpointParams,
			Scopes:         strings.Split(params["scopes"], ","),
		}

		// Try to get a cached refresh token from the current profile and use
		// it to wrap the auth code token source with a refreshing source.
		refreshKey := key + ".refresh"
		refreshSource := RefreshTokenSource{
			ClientID:       params["client_id"],
			TokenURL:       params["token_url"],
			EndpointParams: &endpointParams,
			RefreshToken:   cli.Cache.GetString(refreshKey),
			TokenSource:    source,
		}

		return TokenHandler(refreshSource, key, request)
	}

	return nil
}
