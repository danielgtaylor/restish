package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/spec"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
	"github.com/spf13/cobra"
)

func TestHandleCommandPluginMessageRejectsMalformedDone(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cli := &CLI{Stdout: &stdout, Stderr: &stderr}
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&stderr)

	raw, err := cbor.Marshal(map[string]any{
		"type":      "done",
		"exit_code": "boom",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	done, gotErr := cli.handleCommandPluginMessage(cmd, context.Background(), nil, nil, nil, false, "done", raw)
	if !done {
		t.Fatal("expected malformed done message to stop processing")
	}
	if gotErr == nil {
		t.Fatal("expected malformed done message to return an error")
	}
	if !strings.Contains(gotErr.Error(), "decode done") {
		t.Fatalf("expected decode error, got: %v", gotErr)
	}
}

func TestHandleCommandPluginMessageRejectsOversizedStdoutData(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cli := &CLI{Stdout: &stdout, Stderr: &stderr}
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&stderr)

	raw, err := cbor.Marshal(pluginwire.StdoutDataMsg{
		Type: pluginwire.MsgTypeStdoutData,
		Data: bytes.Repeat([]byte("x"), maxCommandPluginDataBytes+1),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	done, gotErr := cli.handleCommandPluginMessage(cmd, context.Background(), nil, nil, nil, false, pluginwire.MsgTypeStdoutData, raw)
	if done {
		t.Fatal("stdout-data should not mark command plugin done")
	}
	if gotErr == nil {
		t.Fatal("expected oversized stdout-data to return an error")
	}
	if !strings.Contains(gotErr.Error(), "stdout data exceeded") {
		t.Fatalf("expected output cap error, got: %v", gotErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("oversized stdout data was written: %d bytes", stdout.Len())
	}
}

func TestHandleCommandPluginMessageRejectsOversizedStderrData(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cli := &CLI{Stdout: &stdout, Stderr: &stderr}
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&stderr)

	raw, err := cbor.Marshal(pluginwire.StderrDataMsg{
		Type: pluginwire.MsgTypeStderrData,
		Data: bytes.Repeat([]byte("x"), maxCommandPluginDataBytes+1),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	done, gotErr := cli.handleCommandPluginMessage(cmd, context.Background(), nil, nil, nil, false, pluginwire.MsgTypeStderrData, raw)
	if done {
		t.Fatal("stderr-data should not mark command plugin done")
	}
	if gotErr == nil {
		t.Fatal("expected oversized stderr-data to return an error")
	}
	if !strings.Contains(gotErr.Error(), "stderr data exceeded") {
		t.Fatalf("expected output cap error, got: %v", gotErr)
	}
	if stderr.Len() != 0 {
		t.Fatalf("oversized stderr data was written: %d bytes", stderr.Len())
	}
}

func TestHandleCommandPluginSelect(t *testing.T) {
	var stderr, response bytes.Buffer
	cli := &CLI{Stderr: &stderr}
	cli.hooks.PassReader = strings.NewReader("\x1b[B\r")
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&stderr)
	request := pluginwire.SelectMsg{
		Type:      pluginwire.MsgTypeSelect,
		RequestID: "select-1",
		Message:   "Choose a pet",
		Options: []pluginwire.SelectOption{
			{Label: "Mochi", Value: "42"},
			{Label: "Pixel", Value: "7"},
		},
	}
	raw, err := cbor.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	writer := &commandPluginWriter{w: &response}
	var requestWG sync.WaitGroup
	var interactionMu sync.Mutex
	if done, err := cli.handleCommandPluginMessage(cmd, context.Background(), writer, &requestWG, &interactionMu, false, pluginwire.MsgTypeSelect, raw); err != nil || done {
		t.Fatalf("handle select = done %v, err %v", done, err)
	}
	requestWG.Wait()
	var reply pluginwire.SelectResponseMsg
	if err := pluginwire.NewDecoder(&response).ReadMessage(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.RequestID != "select-1" || reply.Value != "7" || reply.Error != "" {
		t.Fatalf("select response = %#v", reply)
	}

	response.Reset()
	if done, err := cli.handleCommandPluginMessage(cmd, context.Background(), writer, &requestWG, &interactionMu, true, pluginwire.MsgTypeSelect, raw); err != nil || done {
		t.Fatalf("handle passthrough select = done %v, err %v", done, err)
	}
	if err := pluginwire.NewDecoder(&response).ReadMessage(&reply); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply.Error, "passthrough_stdio") {
		t.Fatalf("passthrough response = %#v", reply)
	}
}

func TestLoadCommandPluginCommandsReturnsExecError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "restish-broken")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write plugin: %v", err)
	}

	_, err := loadCommandPluginCommands(context.Background(), path)
	if err == nil {
		t.Fatal("expected command discovery error")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("plugin %s: command discovery", filepath.Base(path))) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCommandPluginCommandsRejectsOversizedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "restish-huge-commands")
	script := fmt.Sprintf("#!/bin/sh\ndd if=/dev/zero bs=%d count=1 2>/dev/null\n", maxCommandPluginDiscoveryOutputBytes+1)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write plugin: %v", err)
	}

	_, err := loadCommandPluginCommands(context.Background(), path)
	if err == nil {
		t.Fatal("expected oversized command discovery output to fail")
	}
	if !strings.Contains(err.Error(), "command discovery output exceeded") {
		t.Fatalf("expected output cap error, got: %v", err)
	}
}

func TestLoadCommandPluginCommandsReturnsExecErrorWithStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "restish-broken-stderr")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho discovery exploded >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write plugin: %v", err)
	}

	_, err := loadCommandPluginCommands(context.Background(), path)
	if err == nil {
		t.Fatal("expected command discovery error")
	}
	if !strings.Contains(err.Error(), "stderr: discovery exploded") {
		t.Fatalf("expected stderr excerpt, got: %v", err)
	}
}

func TestDecodeCommandPluginDiscoveryRejectsFutureProtocol(t *testing.T) {
	raw, err := cbor.Marshal(pluginwire.CommandDiscoveryResponse{
		ProtocolVersion: pluginwire.CommandPluginProtocolVersion + 1,
		Commands:        []pluginwire.CommandDecl{{Name: "future"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	_, err = decodeCommandPluginDiscovery("restish-future", raw)
	if err == nil {
		t.Fatal("expected future command plugin protocol to be rejected")
	}
	if !strings.Contains(err.Error(), "plugin requires restish >=") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCommandPluginReturnsOnContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "restish-block")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write plugin: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cli := &CLI{Stdout: &stdout, Stderr: &stderr}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{Use: "block"}
	cmd.SetContext(ctx)
	cmd.SetErr(&stderr)

	errCh := make(chan error, 1)
	go func() {
		errCh <- cli.runCommandPlugin(cmd, path, pluginwire.CommandDecl{Name: "block"}, nil)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "command plugin restish-block canceled") {
			t.Fatalf("runCommandPlugin error = %v, want plugin cancellation message", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runCommandPlugin did not return after context cancellation")
	}
}

func TestStreamPluginStdinCancellationStabilizesPipeReaders(t *testing.T) {
	base := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		var out bytes.Buffer
		cli := &CLI{Stdin: r}
		done := make(chan struct{})
		exited := make(chan struct{})
		go func() {
			defer close(exited)
			cli.streamPluginStdin(&commandPluginWriter{w: &out}, done)
		}()
		close(done)
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			t.Fatal("streamPluginStdin did not return after cancellation")
		}
		if _, err := r.Stat(); err != nil {
			t.Fatalf("stdin reader was closed by streamPluginStdin: %v", err)
		}
		_ = r.Close()
		_ = w.Close()
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutine count did not stabilize: before=%d after=%d", base, runtime.NumGoroutine())
}

func TestCommandPluginWaiterReturnsSameResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "restish-wait")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	cmd := exec.Command(path)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waiter := newCommandPluginWaiter(cmd)
	first := waiter.Wait(time.Second)
	second := waiter.Wait(time.Second)
	if first == nil || second == nil {
		t.Fatalf("expected exit errors, got first=%v second=%v", first, second)
	}
	if first.Error() != second.Error() {
		t.Fatalf("wait results differ: first=%v second=%v", first, second)
	}
}

func TestLineBufferedCommandPluginStderrDoesNotFlushPartialLines(t *testing.T) {
	var display bytes.Buffer
	var capture bytes.Buffer
	syncDisplay := &commandPluginWriter{w: &display}
	stderr := &lineBufferedCommandPluginStderr{display: syncDisplay, capture: &capture}

	if _, err := stderr.Write([]byte("plugin partial")); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	if display.Len() != 0 {
		t.Fatalf("partial plugin line was displayed early: %q", display.String())
	}
	if _, err := syncDisplay.Write([]byte("cobra warning\n")); err != nil {
		t.Fatalf("write cobra: %v", err)
	}
	if _, err := stderr.Write([]byte(" line\n")); err != nil {
		t.Fatalf("write rest: %v", err)
	}
	if got := display.String(); got != "cobra warning\nplugin partial line\n" {
		t.Fatalf("display = %q", got)
	}
	if got := capture.String(); got != "plugin partial line\n" {
		t.Fatalf("capture = %q", got)
	}
}

func TestStripHostPersistentFlagsPreservesPluginArgs(t *testing.T) {
	root := &cobra.Command{Use: "restish"}
	root.PersistentFlags().String("rsh-config", "", "")
	root.PersistentFlags().StringP("rsh-profile", "p", "", "")
	root.PersistentFlags().CountP("rsh-verbose", "v", "")
	cmd := &cobra.Command{Use: "plug"}
	root.AddCommand(cmd)

	args := []string{"--rsh-config", "cfg.json", "-p", "prod", "-vv", "-f", "title", "--plugin-flag", "value", "--", "--rsh-config", "plugin-owned"}
	got := stripHostPersistentFlags(cmd, args)
	want := []string{"-p", "prod", "-vv", "-f", "title", "--plugin-flag", "value", "--", "--rsh-config", "plugin-owned"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("stripHostPersistentFlags = %#v, want %#v", got, want)
	}
}

func TestValidatePluginCommandNameRejectsCollisions(t *testing.T) {
	c := &CLI{cfg: &config.Config{APIs: map[string]*config.APIConfig{"svc": {}}}}
	root := &cobra.Command{Use: "restish"}
	root.AddCommand(&cobra.Command{Use: "get"})

	cases := []struct {
		name string
	}{
		{name: "get"},
		{name: "svc"},
		{name: "Bad_Name"},
	}
	for _, tc := range cases {
		if err := c.validatePluginCommandName(root, map[string]string{}, "plugin", tc.name); err == nil {
			t.Fatalf("expected %q to be rejected", tc.name)
		}
	}

	seen := map[string]string{"tool": "one"}
	if err := c.validatePluginCommandName(root, seen, "two", "tool"); err == nil {
		t.Fatal("expected duplicate plugin command to be rejected")
	}
	if err := c.validatePluginCommandName(root, map[string]string{}, "plugin", "valid-tool"); err != nil {
		t.Fatalf("expected valid command name, got %v", err)
	}
}

func TestHandlePluginAPISpecInvalidatesChangedSpecFiles(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "openapi.yaml")
	if err := os.WriteFile(specPath, []byte(`openapi: "3.1.0"
info:
  title: Old
  version: "1.0.0"
paths: {}`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	c := New()
	c.Hooks().SpecCachePath = t.TempDir()
	c.cfg = &config.Config{APIs: map[string]*config.APIConfig{
		"svc": {
			BaseURL:   "https://api.example.com",
			SpecFiles: []string{specPath},
		},
	}}
	if _, err := c.discoverSpec(context.Background(), "svc"); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	if err := os.WriteFile(specPath, []byte(`openapi: "3.1.0"
info:
  title: New
  version: "1.0.0"
paths: {}`), 0o644); err != nil {
		t.Fatalf("rewrite spec: %v", err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(specPath, future, future); err != nil {
		t.Fatalf("chtimes spec: %v", err)
	}

	reply := handleAPISpecForTest(t, c, context.Background(), "svc")
	if reply.Error != "" {
		t.Fatalf("APISpec returned error: %s", reply.Error)
	}
	if !strings.Contains(string(reply.Raw), "title: New") {
		t.Fatalf("APISpec returned stale raw spec:\n%s", string(reply.Raw))
	}
}

func TestPluginOperationsFromSpecUsesFallbackOperationName(t *testing.T) {
	ops := pluginOperationsFromSpec([]spec.Operation{{
		Method: "GET",
		Path:   "/pets/{petId}",
	}}, "")
	if len(ops) != 1 {
		t.Fatalf("len(ops) = %d, want 1", len(ops))
	}
	if got, want := ops[0].ID, "get-pets-petid"; got != want {
		t.Fatalf("operation ID = %q, want %q", got, want)
	}
}

func TestPluginOperationsFromSpecUsesOperationBaseFallbackName(t *testing.T) {
	ops := pluginOperationsFromSpec([]spec.Operation{{
		Method: "GET",
		Path:   "/api/rest/pets/{petId}",
	}}, "/api/rest")
	if len(ops) != 1 {
		t.Fatalf("len(ops) = %d, want 1", len(ops))
	}
	if got, want := ops[0].ID, "get-pets-petid"; got != want {
		t.Fatalf("operation ID = %q, want %q", got, want)
	}
}

func TestPluginOperationsFromSpecPreservesParameterContentSchema(t *testing.T) {
	ops := pluginOperationsFromSpec([]spec.Operation{{
		Method: "GET",
		Path:   "/items",
		Parameters: []spec.Param{{
			Name:              "filter",
			In:                "query",
			Type:              "object",
			ContentMediaType:  "application/json",
			JSONSchemaDialect: "https://json-schema.org/draft/2020-12/schema",
			JSONSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"active": map[string]any{"type": "boolean"},
				},
			},
		}},
	}}, "")
	if len(ops) != 1 || len(ops[0].Parameters) != 1 {
		t.Fatalf("operations = %#v, want one operation with one parameter", ops)
	}
	param := ops[0].Parameters[0]
	if got, want := param.ContentMediaType, "application/json"; got != want {
		t.Fatalf("ContentMediaType = %q, want %q", got, want)
	}
	if got, want := param.Type, "object"; got != want {
		t.Fatalf("Type = %q, want %q", got, want)
	}
	props, ok := param.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Schema properties = %#v, want map", param.Schema["properties"])
	}
	if _, ok := props["active"]; !ok {
		t.Fatalf("Schema properties = %#v, want active", props)
	}
	if got, want := param.SchemaDialect, "https://json-schema.org/draft/2020-12/schema"; got != want {
		t.Fatalf("SchemaDialect = %q, want %q", got, want)
	}
}

func TestPluginOperationsFromSpecPreservesRequestBodySchema(t *testing.T) {
	ops := pluginOperationsFromSpec([]spec.Operation{{
		Method:           "POST",
		Path:             "/items",
		HasBody:          true,
		BodyRequired:     true,
		RequestMediaType: "application/json",
		Help: spec.OperationHelp{
			Request: &spec.OperationBodyHelp{
				MediaType:         "application/json",
				JSONSchemaDialect: "https://json-schema.org/draft/2020-12/schema",
				JSONSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
				},
			},
		},
	}}, "")
	if len(ops) != 1 {
		t.Fatalf("len(ops) = %d, want 1", len(ops))
	}
	props, ok := ops[0].RequestSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("RequestSchema properties = %#v, want map", ops[0].RequestSchema["properties"])
	}
	if _, ok := props["name"]; !ok {
		t.Fatalf("RequestSchema properties = %#v, want name", props)
	}
	if got, want := ops[0].RequestSchemaDialect, "https://json-schema.org/draft/2020-12/schema"; got != want {
		t.Fatalf("RequestSchemaDialect = %q, want %q", got, want)
	}
}

func TestHandlePluginAPISpecUsesCommandContextForDiscovery(t *testing.T) {
	c := New()
	c.Hooks().SpecCachePath = t.TempDir()
	c.cfg = &config.Config{APIs: map[string]*config.APIConfig{
		"svc": {
			BaseURL: "https://api.example.com",
			SpecURL: "https://api.example.com/openapi.yaml",
		},
	}}
	c.Hooks().HTTPTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Context().Err() == nil {
			return nil, errors.New("request context was not canceled")
		}
		return nil, req.Context().Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reply := handleAPISpecForTest(t, c, ctx, "svc")
	if !strings.Contains(reply.Error, "context canceled") {
		t.Fatalf("APISpec error = %q, want context canceled", reply.Error)
	}
}

func handleAPISpecForTest(t *testing.T, c *CLI, ctx context.Context, apiName string) pluginwire.APISpecResponseMsg {
	t.Helper()
	var buf bytes.Buffer
	writer := &commandPluginWriter{w: &buf}
	if err := c.handlePluginAPISpec(ctx, nil, writer, pluginwire.APISpecMsg{Name: apiName}); err != nil {
		t.Fatalf("handlePluginAPISpec: %v", err)
	}
	var reply pluginwire.APISpecResponseMsg
	if err := pluginwire.ReadMessage(&buf, &reply); err != nil {
		t.Fatalf("decode APISpec reply: %v", err)
	}
	return reply
}
