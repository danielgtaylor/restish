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

	"github.com/danielgtaylor/restish/cli"
	"golang.org/x/oauth2"
)

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
	h.c <- r.URL.Query().Get("code")
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte("<html><body><p>Login successful. Please return to the terminal. You may now close this window.</p></body></html>"))
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
	url := fmt.Sprintf("%s?response_type=code&code_challenge=%s&code_challenge_method=S256&client_id=%s&redirect_uri=http://localhost:8484/&scope=%s", ac.AuthorizeURL, challenge, ac.ClientID, strings.Join(ac.Scopes, `%20`))

	if len(*ac.EndpointParams) > 0 {
		url += "&" + ac.EndpointParams.Encode()
	}

	// Run server before opening the user's browser so we are ready for any redirect.
	codeChan := make(chan string)
	handler := authHandler{
		c: codeChan,
	}

	s := &http.Server{
		Addr:           ":8484",
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
	fmt.Println(url)
	open(url)

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

	payload := fmt.Sprintf("grant_type=authorization_code&client_id=%s&code_verifier=%s&code=%s&redirect_uri=http://localhost:8484/", ac.ClientID, verifier, code)

	return requestToken(ac.TokenURL, payload)
}

// AuthorizationCodeHandler sets up the OAuth 2.0 authorization code with PKCE authentication
// flow.
type AuthorizationCodeHandler struct{}

// OnRequest gets run before the request goes out on the wire.
func (h *AuthorizationCodeHandler) OnRequest(request *http.Request, key string, params map[string]string) error {
	if request.Header.Get("Authorization") == "" {
		endpointParams := url.Values{}
		for k, v := range params {
			if k == "client_id" || k == "scopes" || k == "authorize_url" || k == "token_url" {
				// Not a custom param...
				continue
			}

			endpointParams.Add(k, v)
		}

		source := &AuthorizationCodeTokenSource{
			ClientID:       params["client_id"],
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
