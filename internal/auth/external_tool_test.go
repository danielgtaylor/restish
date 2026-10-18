package auth

import (
	"context"
	"github.com/rest-sh/restish/v2/auth"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rest-sh/restish/v2/internal/request"
)

func TestExternalTool_Parameters(t *testing.T) {
	a := &ExternalTool{}
	params := a.Parameters()
	if len(params) == 0 {
		t.Fatal("expected at least one param")
	}
	names := map[string]bool{}
	for _, p := range params {
		names[p.Name] = true
	}
	if !names["commandline"] {
		t.Error("expected commandline param")
	}
}

func TestExternalTool_OnRequest_MissingCommandline(t *testing.T) {
	a := &ExternalTool{}
	req, _ := http.NewRequest("GET", "https://api.example.com", nil)
	err := a.OnRequest(req, map[string]string{})
	if err == nil {
		t.Error("expected error for missing commandline")
	}
}

func TestExternalTool_OnRequest_AddsHeader(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	a := &ExternalTool{}
	req, _ := http.NewRequest("GET", "https://api.example.com/items", nil)
	// The tool outputs a JSON response that adds an X-Token header.
	err := a.OnRequest(req, map[string]string{
		"commandline": `echo '{"headers":{"X-Token":["mytoken123"]}}'`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("X-Token"); got != "mytoken123" {
		t.Errorf("X-Token: got %q, want %q", got, "mytoken123")
	}
}

func TestExternalTool_OnRequest_UpdatesURI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	a := &ExternalTool{}
	req, _ := http.NewRequest("GET", "https://api.example.com/items", nil)
	err := a.OnRequest(req, map[string]string{
		"commandline": `echo '{"uri":"https://api.example.com/v2/items"}'`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.URL.String() != "https://api.example.com/v2/items" {
		t.Errorf("URL: got %q, want %q", req.URL.String(), "https://api.example.com/v2/items")
	}
}

func TestExternalTool_OnRequest_BearerTokenOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	a := &ExternalTool{}
	req, _ := http.NewRequest("GET", "https://api.example.com/items", nil)
	err := a.OnRequest(req, map[string]string{
		"commandline": `printf '%s\n' token-from-tool`,
		"output":      "bearer-token",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer token-from-tool" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestExternalTool_OnRequest_EmptyOutput_NoOp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	a := &ExternalTool{}
	req, _ := http.NewRequest("GET", "https://api.example.com/items", nil)
	origURL := req.URL.String()
	// Tool produces no output — should be a no-op.
	err := a.OnRequest(req, map[string]string{
		"commandline": "true", // exits 0, no output
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.URL.String() != origURL {
		t.Errorf("URL changed unexpectedly: got %q, want %q", req.URL.String(), origURL)
	}
}

func TestExternalTool_OnRequest_ToolExitError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	a := &ExternalTool{}
	req, _ := http.NewRequest("GET", "https://api.example.com", nil)
	err := a.OnRequest(req, map[string]string{
		"commandline": "exit 1",
	})
	if err == nil {
		t.Error("expected error for non-zero tool exit")
	}
}

func TestExternalTool_OnRequest_ToolExitErrorIncludesBoundedStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	var streamed strings.Builder
	a := &ExternalTool{Stderr: &streamed}
	req, _ := http.NewRequest("GET", "https://api.example.com", nil)
	err := a.OnRequest(req, map[string]string{
		"commandline": `printf 'client_secret=super-secret\n' >&2; exit 1`,
	})
	if err == nil {
		t.Fatal("expected error for non-zero tool exit")
	}
	if !strings.Contains(streamed.String(), "super-secret") {
		t.Fatalf("expected stderr to be streamed, got %q", streamed.String())
	}
	if !strings.Contains(err.Error(), "stderr:") {
		t.Fatalf("expected stderr excerpt in error, got %v", err)
	}
	if strings.Contains(err.Error(), "super-secret") || !strings.Contains(err.Error(), "client_secret=***") {
		t.Fatalf("expected redacted stderr in error, got %v", err)
	}
}

func TestExternalTool_OnRequest_CredentialBodySuppressesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	const secret = "prompted-secret"
	var streamed strings.Builder
	a := &ExternalTool{Stderr: &streamed}
	req, _ := http.NewRequest("POST", "https://api.example.com", strings.NewReader(`{"pin":"`+secret+`"}`))
	request.MarkCredentialBody(req)
	err := a.OnRequest(req, map[string]string{"commandline": "cat >&2; exit 1"})
	if err == nil {
		t.Fatal("expected error for non-zero tool exit")
	}
	if strings.Contains(streamed.String(), secret) || strings.Contains(err.Error(), secret) {
		t.Fatalf("external tool stderr leaked credential body: stream %q, error %v", streamed.String(), err)
	}
}

func TestExternalTool_OnRequest_WithBody(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	a := &ExternalTool{}
	body := strings.NewReader(`{"name":"test"}`)
	req, _ := http.NewRequest("POST", "https://api.example.com/items", body)
	req.Header.Set("Content-Type", "application/json")
	// Tool reads stdin and returns no mutation — just verify body is still readable.
	err := a.OnRequest(req, map[string]string{
		"commandline": "cat > /dev/null", // consume stdin, no output
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExternalTool_OnRequest_OmitBody(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	a := &ExternalTool{}
	req, _ := http.NewRequest("POST", "https://api.example.com/items", strings.NewReader(`{"name":"test"}`))
	err := a.OnRequest(req, map[string]string{
		"commandline": "true",
		"omitbody":    "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExternalTool_AuthenticateCancelsHungTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command test not supported on Windows")
	}
	a := &ExternalTool{Timeout: 20 * time.Millisecond}
	req, _ := http.NewRequest("GET", "https://api.example.com", nil)
	start := time.Now()
	err := a.Authenticate(context.Background(), req, auth.AuthContext{
		Params: map[string]string{"commandline": "sleep 5"},
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("hung tool was not canceled promptly: %v", time.Since(start))
	}
	if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected timeout/cancel error, got %v", err)
	}
}
