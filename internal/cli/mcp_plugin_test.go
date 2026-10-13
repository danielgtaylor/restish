//go:build integration

package cli_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func installMCPPlugin(t *testing.T) string {
	t.Helper()
	skipNoMCPPlugin(t)

	data, err := os.ReadFile(testMCPPluginBin)
	if err != nil {
		t.Fatalf("read mcp plugin: %v", err)
	}

	pluginsParent := t.TempDir()
	pluginDir := filepath.Join(pluginsParent, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(pluginDir, "restish-mcp")
	if runtime.GOOS == "windows" {
		dest += ".exe"
	}
	if err := os.WriteFile(dest, data, 0o755); err != nil {
		t.Fatalf("write mcp plugin: %v", err)
	}
	t.Setenv("RSH_CONFIG_DIR", pluginsParent)
	t.Setenv("PATH", "")
	return pluginsParent
}

func TestMCPRequiresAPIName(t *testing.T) {
	pluginsParent := installMCPPlugin(t)
	cfgPath := filepath.Join(pluginsParent, "restish.json")
	if err := os.WriteFile(cfgPath, []byte(`{"apis":{"demo":{"base_url":"https://example.com"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgPath
	if err := c.Run([]string{"restish", "mcp", "serve"}); err == nil {
		t.Fatal("expected error when no API names are provided")
	}
}

func TestMCPPluginHelp(t *testing.T) {
	pluginsParent := installMCPPlugin(t)
	cfgPath := filepath.Join(pluginsParent, "restish.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = cfgPath
	if err := c.Run([]string{"restish", "mcp", "--help"}); err != nil {
		t.Fatalf("mcp --help: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "restish mcp serve <api...>") {
		t.Fatalf("unexpected mcp help:\n%s", got)
	} else if !strings.Contains(got, "Available Commands:") ||
		!strings.Contains(got, "Examples:") ||
		!strings.Contains(got, "--allow-write-tools") {
		t.Fatalf("mcp root help missing Cobra-style sections:\n%s", got)
	}

	c, out, _ = newTestCLI(t)
	c.Hooks().ConfigPath = cfgPath
	if err := c.Run([]string{"restish", "mcp", "serve", "--help"}); err != nil {
		t.Fatalf("mcp serve --help: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "--allow-write-tools") {
		t.Fatalf("unexpected mcp serve help:\n%s", got)
	} else if !strings.Contains(got, "Examples:") ||
		!strings.Contains(got, "restish mcp serve github") {
		t.Fatalf("mcp serve help missing examples:\n%s", got)
	}
}

func TestMCPServeToolCall(t *testing.T) {
	pluginsParent := installMCPPlugin(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, `openapi: "3.1.0"
info:
  title: Demo
  version: "1.0.0"
servers:
  - url: /{version}
    variables:
      version:
        default: v1
        enum: [v1, v2]
paths:
  /items/{id}:
    get:
      operationId: getItem
      summary: Get an item
      parameters:
        - $ref: "./params.yaml#/components/parameters/ID"
  /hidden:
    get:
      operationId: hiddenCli
      x-cli-ignore: true
  /mcp-hidden:
    get:
      operationId: hiddenMcp
      x-mcp-ignore: true
`)
	})
	mux.HandleFunc("/params.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, `components:
  parameters:
    ID:
      name: id
      in: path
      required: true
      schema:
        type: string
`)
	})
	mux.HandleFunc("/v2/items/42", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"42","name":"example"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(pluginsParent, "restish.json")
	cfg := fmt.Sprintf(`{"apis":{"demo":{"base_url":%q,"spec_url":%q,"server_variables":{"version":"v2"}}}}`, srv.URL, srv.URL+"/openapi.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	stdin := bytes.NewBuffer(nil)
	writeMCPFrame(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})
	writeMCPFrame(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	writeMCPFrame(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "get-item",
			"arguments": map[string]any{"id": "42"},
		},
	})

	c2, out, errOut := newTestCLI(t)
	c2.Hooks().ConfigPath = cfgPath
	c2.Hooks().SpecCachePath = filepath.Join(pluginsParent, "specs")
	c2.Stdin = stdin
	if err := c2.Run([]string{"restish", "mcp", "serve", "demo"}); err != nil {
		t.Fatalf("mcp: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}

	responses := readMCPResponses(t, out.Bytes())
	if len(responses) != 3 {
		t.Fatalf("expected 3 MCP responses, got %d", len(responses))
	}

	listResult := responses[1]["result"].(map[string]any)
	tools := listResult["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 visible tool, got %d", len(tools))
	}
	if got := tools[0].(map[string]any)["name"].(string); got != "get-item" {
		t.Fatalf("expected get-item tool, got %q", got)
	}

	callResult := responses[2]["result"].(map[string]any)
	content := callResult["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"name": "example"`) {
		t.Fatalf("expected item body in tool result, got:\n%s", text)
	}
}

func writeMCPFrame(t *testing.T, buf *bytes.Buffer, msg map[string]any) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	buf.Write(data)
	buf.WriteByte('\n')
}

func readMCPResponses(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	reader := bufio.NewReader(bytes.NewReader(data))
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			break
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			t.Fatalf("unmarshal MCP response: %v", err)
		}
		out = append(out, msg)
	}
	return out
}
