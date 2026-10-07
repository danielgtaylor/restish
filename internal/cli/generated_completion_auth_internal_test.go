package cli

import (
	"context"
	"net/http"
	"testing"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/request"
)

func TestCompletionRejectsExternalToolAuth(t *testing.T) {
	cli := New()
	_, err := cli.authHandlerFor(&config.AuthConfig{Type: "external-tool"}, authHandlerOptions{NonInteractive: true})
	if err == nil {
		t.Fatal("external-tool auth accepted during shell completion")
	}
}

func TestExplicitAPIIdentityWinsForOperationServer(t *testing.T) {
	cli := New()
	cli.cfg = &config.Config{APIs: map[string]*config.APIConfig{
		"source": {BaseURL: "https://source.example", Profiles: map[string]*config.ProfileConfig{"default": {Headers: []string{"X-Source: yes"}}}},
		"other":  {BaseURL: "https://other.example", Profiles: map[string]*config.ProfileConfig{"default": {Headers: []string{"X-Other: no"}}}},
	}}
	prepared, err := cli.prepareRequest(context.Background(), http.MethodGet, "https://other.example/items", "default", request.Options{NoCache: true}, nil, nil, true, authHandlerOptions{NonInteractive: true}, &operationAuthPolicy{NoAuth: true}, false, "source")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.closePreparedTransport(prepared)
	if prepared.apiName != "source" || len(prepared.opts.Headers) != 1 || prepared.opts.Headers[0] != "X-Source: yes" {
		t.Fatalf("prepared API = %q, headers = %#v", prepared.apiName, prepared.opts.Headers)
	}
}

func TestCompletionProfileAuthIsNonInteractive(t *testing.T) {
	cli := New()
	for _, authConfig := range []*config.AuthConfig{
		{Type: "bearer", Params: map[string]string{"token": "command:get-token"}},
		{Type: "oauth-device-code", Params: map[string]string{"client_id": "client"}},
	} {
		callbacks := cli.authOnRequest("api", "default", &config.ProfileConfig{Auth: authConfig}, authHandlerOptions{NonInteractive: true})
		request, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
		if callbacks.OnRequest == nil || callbacks.OnRequest(request) == nil {
			t.Fatalf("auth type %q was allowed to start interactively", authConfig.Type)
		}
	}
}

func TestCompletionRejectsCommandSecretSources(t *testing.T) {
	if err := rejectNonInteractiveCommandParams(map[string]string{"token": "command:get-token"}, true); err == nil {
		t.Fatal("command secret source accepted during shell completion")
	}
}
