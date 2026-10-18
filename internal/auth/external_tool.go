package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/rest-sh/restish/v2/auth"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rest-sh/restish/v2/internal/procutil"
	"github.com/rest-sh/restish/v2/internal/request"
)

const (
	maxExternalToolStderrBytes = 4096
	maxExternalToolBodyBytes   = 1 << 20
)

// ExternalTool delegates authentication to an external program. The program
// receives the outbound request as JSON on stdin and returns header updates
// (and optionally a new URI) as JSON on stdout.
//
// Config params:
//
//	commandline  (required) shell command to run; executed via cmd /c on
//	             Windows or /bin/sh -c on other platforms
//	omitbody     (optional) "true" skips sending the request body to the tool;
//	             use for binary bodies because body is v1-compatible JSON text
//	output       (optional) "bearer-token" treats stdout as a bearer token
//
// Wire format (stdin → tool):
//
//	{"method":"GET","uri":"https://...","headers":{...},"body":"..."}
//
// Wire format (tool → stdout):
//
//	{"headers":{"X-Sig":"..."}}          — merge headers only
//	{"uri":"https://...","headers":{...}} — also rewrite the URI
//
// An empty or absent stdout response is a no-op (tool declined to mutate).
// Compatible with the v1 external-tool auth wire format.
type ExternalTool struct {
	Stderr  io.Writer
	Timeout time.Duration
}

func (a *ExternalTool) Parameters() []auth.Param {
	return []auth.Param{
		{Name: "commandline", Description: "Shell command to run for auth (executed via cmd /c on Windows or /bin/sh -c elsewhere)", Required: true},
		{Name: "omitbody", Description: "Set to \"true\" to skip sending the request body to the tool"},
		{Name: "output", Description: "Set to \"bearer-token\" to treat stdout as an OAuth bearer token"},
	}
}

// externalToolRequest is the JSON structure sent to the tool on stdin.
type externalToolRequest struct {
	Method  string      `json:"method"`
	URI     string      `json:"uri"`
	Headers http.Header `json:"headers"`
	Body    string      `json:"body"`
}

// externalToolResponse is the JSON structure read from the tool's stdout.
type externalToolResponse struct {
	URI     string      `json:"uri,omitempty"`
	Headers http.Header `json:"headers,omitempty"`
}

func (a *ExternalTool) OnRequest(req *http.Request, params map[string]string) error {
	return a.run(req.Context(), req, params)
}

func (a *ExternalTool) run(ctx context.Context, req *http.Request, params map[string]string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	commandLine := params["commandline"]
	if commandLine == "" {
		return fmt.Errorf("external-tool auth: missing required param \"commandline\"")
	}
	omitBody := strings.EqualFold(params["omitbody"], "true")

	// Read and restore the request body if we need to forward it.
	bodyStr := ""
	if req.Body != nil && !omitBody {
		bodyBytes, err := io.ReadAll(io.LimitReader(req.Body, maxExternalToolBodyBytes+1))
		if err != nil {
			return fmt.Errorf("external-tool auth: reading request body: %w", err)
		}
		if len(bodyBytes) > maxExternalToolBodyBytes {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes[:maxExternalToolBodyBytes]))
			return fmt.Errorf("external-tool auth: request body exceeds %d bytes; set omitbody=true or pass a digest instead", maxExternalToolBodyBytes)
		}
		bodyStr = string(bodyBytes)
		req.Body = io.NopCloser(strings.NewReader(bodyStr))
	}

	payload, err := json.Marshal(externalToolRequest{
		Method:  req.Method,
		URI:     req.URL.String(),
		Headers: req.Header,
		Body:    bodyStr,
	})
	if err != nil {
		return fmt.Errorf("external-tool auth: marshalling request: %w", err)
	}

	timeout := a.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if _, ok := ctx.Deadline(); !ok && timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := procutil.ShellCommand(ctx, commandLine)
	procutil.ConfigureCommandTreeKill(ctx, cmd)
	// Assign stdin directly so exec's internals copy the bytes after Start.
	// Using StdinPipe + manual write before Start would deadlock for payloads
	// larger than the OS pipe buffer (~64 KB).
	cmd.Stdin = bytes.NewReader(payload)
	stderrCapture := &limitedBuffer{limit: maxExternalToolStderrBytes}
	credentialBody := request.IsMarkedCredentialBody(req) && !omitBody
	if a.Stderr != nil && !credentialBody {
		cmd.Stderr = io.MultiWriter(a.Stderr, stderrCapture)
	} else {
		cmd.Stderr = stderrCapture
	}

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("external-tool auth: tool timed out or was canceled: %w", ctx.Err())
		}
		if excerpt := redactExternalToolStderr(stderrCapture.String()); excerpt != "" && !credentialBody {
			return fmt.Errorf("external-tool auth: tool exited with error: %w: stderr: %s", err, excerpt)
		}
		return fmt.Errorf("external-tool auth: tool exited with error: %w", err)
	}
	if len(out) == 0 {
		return nil
	}

	if strings.EqualFold(params["output"], "bearer-token") {
		token := strings.TrimSpace(string(out))
		if token == "" {
			return nil
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}

	var updates externalToolResponse
	if err := json.Unmarshal(out, &updates); err != nil {
		return fmt.Errorf("external-tool auth: parsing tool response: %w", err)
	}

	if updates.URI != "" {
		parsed, err := req.URL.Parse(updates.URI)
		if err != nil {
			return fmt.Errorf("external-tool auth: parsing updated URI: %w", err)
		}
		req.URL = parsed
		req.Host = parsed.Host
	}

	// Use Del+Add so multi-value headers are fully replaced, not overwritten
	// one value at a time (which would only keep the last value).
	for key, vals := range updates.Headers {
		req.Header.Del(key)
		for _, v := range vals {
			req.Header.Add(key, v)
		}
	}
	return nil
}

func (a *ExternalTool) Authenticate(ctx context.Context, req *http.Request, ac auth.AuthContext) error {
	stderr := a.Stderr
	if stderr == nil {
		stderr = ac.Stderr
	}
	if ctx == nil && req.Context() != nil {
		ctx = req.Context()
	}
	return (&ExternalTool{Stderr: stderr, Timeout: a.Timeout}).run(ctx, req, ac.Params)
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 || b.buf.Len() < b.limit {
		remaining := b.limit - b.buf.Len()
		if b.limit <= 0 || remaining > len(p) {
			remaining = len(p)
		}
		if remaining > 0 {
			_, _ = b.buf.Write(p[:remaining])
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return strings.TrimSpace(b.buf.String())
}

func redactExternalToolStderr(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	redactedFields := []string{"access_token", "refresh_token", "id_token", "client_secret", "password", "authorization"}
	for _, field := range redactedFields {
		value = redactFieldAssignments(value, field)
	}
	return value
}

func redactFieldAssignments(value, field string) string {
	for _, sep := range []string{"=", ":"} {
		lower := strings.ToLower(value)
		needle := strings.ToLower(field + sep)
		searchFrom := 0
		for {
			idxRel := strings.Index(lower[searchFrom:], needle)
			if idxRel < 0 {
				break
			}
			idx := searchFrom + idxRel
			start := idx + len(needle)
			end := start
			for end < len(value) && !strings.ContainsRune(" \t\r\n,;&", rune(value[end])) {
				end++
			}
			value = value[:start] + "***" + value[end:]
			lower = strings.ToLower(value)
			searchFrom = start + len("***")
		}
	}
	return value
}
