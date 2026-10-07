// testplugin is a multipurpose Restish plugin helper used by internal/cli
// tests. The same binary is linked or copied under several restish-* names,
// and its behavior is selected from argv[0].
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rest-sh/restish/v2/plugin"
)

func main() {
	switch strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") {
	case "restish-testplugin":
		runManifestOnlyPlugin()
	case "restish-cmdplugin":
		runCommandPlugin()
	case "restish-hookplugin":
		runHookPlugin()
	case "restish-test-tls-signer":
		runTLSSignerPlugin()
	default:
		fmt.Fprintf(os.Stderr, "unknown test plugin name: %s\n", filepath.Base(os.Args[0]))
		os.Exit(2)
	}
}

func runManifestOnlyPlugin() {
	if plugin.HandleStartupFlags(os.Stdout, plugin.Manifest{
		Name:              "testplugin",
		Version:           "1.0.0",
		Description:       "Test plugin for unit tests",
		RestishAPIVersion: 2,
		Hooks:             []string{"command"},
	}, nil) {
		return
	}
	os.Exit(1)
}

var commandPluginCommands = []plugin.CommandDecl{
	{Name: "greet", Short: "Greet the user"},
	{Name: "fetch", Short: "Fetch a URL via Restish"},
	{Name: "choose", Short: "Choose a pet via Restish"},
	{Name: "abandon", Short: "Abandon a selection"},
	{Name: "pipe", Short: "Echo stdin via passthrough stdio", PassthroughStdio: true},
	{Name: "fail", Short: "Exit with code 1"},
	{Name: "die", Short: "Crash unexpectedly"},
	{Name: "hangdone", Short: "Send done and keep running"},
}

func runCommandPlugin() {
	if plugin.HandleStartupFlags(os.Stdout, plugin.Manifest{
		Name:              "cmdplugin",
		Version:           "1.0.0",
		Description:       "Test command plugin",
		RestishAPIVersion: 2,
		Hooks:             []string{"command"},
		RequiredFeatures:  []string{plugin.FeatureCommandSelect},
	}, commandPluginCommands) {
		return
	}

	dec := plugin.NewDecoder(os.Stdin)
	var initMsg plugin.InitMsg
	if err := dec.ReadMessage(&initMsg); err != nil {
		fmt.Fprintln(os.Stderr, "read init:", err)
		os.Exit(1)
	}

	switch initMsg.Command {
	case "greet":
		_ = plugin.WriteMessage(os.Stdout, plugin.WarnMsg{Type: plugin.MsgTypeWarn, Text: "Greeting in progress..."})
		_ = plugin.WriteMessage(os.Stdout, plugin.ResponseMsg{
			Type:   plugin.MsgTypeResponse,
			Status: 200,
			Body:   map[string]any{"greeting": "Hello from plugin!"},
		})
		_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone})
	case "fetch":
		runCommandPluginFetch(dec, initMsg.Args)
	case "choose":
		runCommandPluginSelect(dec)
	case "abandon":
		_ = plugin.WriteMessage(os.Stdout, plugin.SelectMsg{
			Type: plugin.MsgTypeSelect, RequestID: "abandoned", Message: "Choose",
			Options: []plugin.SelectOption{{Label: "One", Value: "1"}, {Label: "Two", Value: "2"}},
		})
		_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone})
	case "fail":
		_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone, ExitCode: 1})
	case "pipe":
		runCommandPluginPipe(dec)
	case "die":
		os.Exit(1)
	case "hangdone":
		_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone})
		select {}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", initMsg.Command)
		_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone, ExitCode: 1})
	}
}

func runCommandPluginSelect(dec *plugin.Decoder) {
	_ = plugin.WriteMessage(os.Stdout, plugin.SelectMsg{
		Type: plugin.MsgTypeSelect, RequestID: "select-1", Message: "Choose a pet",
		Options: []plugin.SelectOption{{Label: "Mochi", Value: "42"}, {Label: "Pixel", Value: "7"}},
	})
	var reply plugin.SelectResponseMsg
	if err := dec.ReadMessage(&reply); err != nil || reply.Error != "" {
		_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone, ExitCode: 1})
		return
	}
	_ = plugin.WriteMessage(os.Stdout, plugin.ResponseMsg{
		Type: plugin.MsgTypeResponse, Status: 200, Body: map[string]any{"selected": reply.Value},
	})
	_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone})
}

func runCommandPluginFetch(dec *plugin.Decoder, args []string) {
	var fetchURL string
	if len(args) > 0 {
		fetchURL = args[0]
	}
	if fetchURL == "" {
		_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone, ExitCode: 1})
		return
	}
	_ = plugin.WriteMessage(os.Stdout, plugin.HTTPRequestMsg{
		Type:   plugin.MsgTypeHTTPRequest,
		Method: "GET",
		URI:    fetchURL,
	})
	var httpResp plugin.HTTPResponseMsg
	if err := dec.ReadMessage(&httpResp); err != nil {
		fmt.Fprintln(os.Stderr, "read http-response:", err)
		os.Exit(1)
	}
	_ = plugin.WriteMessage(os.Stdout, plugin.ResponseMsg{
		Type:   plugin.MsgTypeResponse,
		Status: httpResp.Status,
		Body:   httpResp.Body,
	})
	_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone})
}

func runCommandPluginPipe(dec *plugin.Decoder) {
	for {
		raw, err := dec.ReadRaw()
		if err != nil {
			os.Exit(1)
		}
		switch plugin.MessageType(raw) {
		case plugin.MsgTypeStdinData:
			var msg plugin.StdinDataMsg
			_ = plugin.DecMode.Unmarshal(raw, &msg)
			_ = plugin.WriteMessage(os.Stdout, plugin.StdoutDataMsg{
				Type: plugin.MsgTypeStdoutData,
				Data: append([]byte("OUT:"), msg.Data...),
			})
			_ = plugin.WriteMessage(os.Stdout, plugin.StderrDataMsg{
				Type: plugin.MsgTypeStderrData,
				Data: append([]byte("ERR:"), msg.Data...),
			})
		case plugin.MsgTypeStdinClose:
			_ = plugin.WriteMessage(os.Stdout, plugin.DoneMsg{Type: plugin.MsgTypeDone})
			return
		}
	}
}

const minimalOpenAPI = `{
  "openapi": "3.0.0",
  "info": {"title": "Hook Plugin API", "version": "1.0.0"},
  "paths": {
    "/hook-items": {
      "get": {
        "operationId": "listHookItems",
        "summary": "List hook items",
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`

func runHookPlugin() {
	manifest := plugin.Manifest{
		Name:               "hookplugin",
		Version:            "1.0.0",
		Description:        "Test hook plugin",
		RestishAPIVersion:  2,
		Hooks:              []string{"auth", "request-middleware", "response-middleware", "formatter", "loader"},
		FormatterNames:     []string{"hookformat"},
		LoaderContentTypes: []string{"application/x-hook-api"},
	}
	if os.Getenv("RSH_HOOK_NEEDS_AUTH_SECRETS") == "1" {
		manifest.NeedsAuthSecrets = true
	}
	if plugin.HandleStartupFlags(os.Stdout, manifest, nil) {
		return
	}

	dec := plugin.NewDecoder(os.Stdin)
	raw, err := dec.ReadRaw()
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}

	switch plugin.MessageType(raw) {
	case "auth":
		var msg plugin.AuthHookInput
		_ = plugin.DecMode.Unmarshal(raw, &msg)
		checkSecretHeaders(msg.Type, msg.Request.Headers)
		checkSecretQuery(msg.Type, msg.Request.URI)
		_ = plugin.WriteMessage(os.Stdout, plugin.AuthHookOutput{
			Request: &plugin.HookRequestHeaderUpdate{
				Headers: map[string]any{"Authorization": []any{"Bearer hook-token"}},
			},
		})
	case "request-middleware":
		var msg plugin.RequestMiddlewareInput
		_ = plugin.DecMode.Unmarshal(raw, &msg)
		checkSecretHeaders(msg.Type, msg.Request.Headers)
		checkSecretQuery(msg.Type, msg.Request.URI)
		headers := map[string]any{"X-Trace-Id": []any{"hook-trace-123"}}
		if os.Getenv("RSH_HOOK_REQUEST_AUTH") == "1" {
			headers["Authorization"] = []any{"Bearer request-plugin"}
		}
		_ = plugin.WriteMessage(os.Stdout, plugin.RequestMiddlewareOutput{
			Request: &plugin.HookRequestHeaderUpdate{Headers: headers},
		})
	case "response-middleware":
		runResponseMiddleware(raw)
	case "formatter":
		runFormatter(dec, raw)
	case "loader":
		var msg plugin.LoaderRequest
		_ = plugin.DecMode.Unmarshal(raw, &msg)
		if os.Getenv("RSH_HOOK_EXPECT_LOADER_METADATA") == "1" {
			if msg.ContentType != "application/x-hook-api" || msg.SourceURL != "https://example.test/openapi.hook" || msg.LocalPath != "/tmp/openapi.hook" {
				fmt.Fprintf(os.Stderr, "loader metadata mismatch: content_type=%q source_url=%q local_path=%q\n", msg.ContentType, msg.SourceURL, msg.LocalPath)
				os.Exit(1)
			}
		}
		_ = plugin.WriteMessage(os.Stdout, plugin.LoaderResponse{
			ContentType: "application/openapi+json",
			Body:        minimalOpenAPI,
		})
	default:
		os.Exit(0)
	}
}

func runResponseMiddleware(raw []byte) {
	var msg plugin.ResponseMiddlewareInput
	_ = plugin.DecMode.Unmarshal(raw, &msg)
	checkSecretHeaders(msg.Type, msg.Request.Headers)
	checkSecretQuery(msg.Type, msg.Request.URI)

	behavior := os.Getenv("RSH_HOOK_RM_BEHAVIOR")
	switch {
	case behavior == "drop":
		_ = plugin.WriteMessage(os.Stdout, plugin.ResponseMiddlewareOutput{Drop: true})
	case strings.HasPrefix(behavior, "follow-body:"):
		uri := strings.TrimPrefix(behavior, "follow-body:")
		_ = plugin.WriteMessage(os.Stdout, plugin.ResponseMiddlewareOutput{
			Follow: &plugin.FollowRequest{
				Method: "POST",
				URI:    uri,
				Headers: map[string]string{
					"Content-Type":   "application/json",
					"X-Follow-Token": "hook-token",
				},
				Body: map[string]any{"from": "plugin"},
			},
		})
	case strings.HasPrefix(behavior, "follow:"):
		uri := strings.TrimPrefix(behavior, "follow:")
		_ = plugin.WriteMessage(os.Stdout, plugin.ResponseMiddlewareOutput{
			Follow: &plugin.FollowRequest{Method: "GET", URI: uri},
		})
	default:
		body, _ := msg.Response.Body.(map[string]any)
		if body == nil {
			body = map[string]any{}
		}
		body["plugin_added"] = true
		_ = plugin.WriteMessage(os.Stdout, plugin.ResponseMiddlewareOutput{
			Response: &plugin.HookResponseUpdate{Body: body},
		})
	}
}

func runFormatter(dec *plugin.Decoder, raw []byte) {
	var req plugin.FormatterRequest
	_ = plugin.DecMode.Unmarshal(raw, &req)
	for {
		if req.Event == "start" {
			fmt.Fprint(os.Stdout, "HOOK FORMATTED\n")
		}
		if req.Event == "end" {
			break
		}
		if err := dec.ReadMessage(&req); err != nil {
			fmt.Fprintln(os.Stderr, "read:", err)
			os.Exit(1)
		}
	}
}

func checkSecretHeaders(hook string, headers map[string][]string) {
	expect := os.Getenv("RSH_HOOK_EXPECT_SECRET_HEADERS")
	if expect == "" {
		return
	}
	for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key", "X-Auth-Token", "X-Secret"} {
		values := headers[name]
		if len(values) == 0 {
			fmt.Fprintf(os.Stderr, "%s: missing %s header\n", hook, name)
			os.Exit(2)
		}
		switch expect {
		case "redacted":
			if len(values) != 1 || values[0] != "<redacted>" {
				fmt.Fprintf(os.Stderr, "%s: %s = %q, want redacted\n", hook, name, values)
				os.Exit(2)
			}
		case "preserved":
			if len(values) == 1 && values[0] == "<redacted>" {
				fmt.Fprintf(os.Stderr, "%s: %s was redacted unexpectedly\n", hook, name)
				os.Exit(2)
			}
		}
	}
}

func checkSecretQuery(hook, uri string) {
	expect := os.Getenv("RSH_HOOK_EXPECT_SECRET_QUERY")
	if expect == "" {
		return
	}
	switch expect {
	case "redacted":
		if strings.Contains(uri, "api_key=secret") || strings.Contains(uri, "token=secret") {
			fmt.Fprintf(os.Stderr, "%s: secret query leaked in %q\n", hook, uri)
			os.Exit(2)
		}
		if !strings.Contains(uri, "api_key=%3Credacted%3E") || !strings.Contains(uri, "token=%3Credacted%3E") {
			fmt.Fprintf(os.Stderr, "%s: secret query not redacted in %q\n", hook, uri)
			os.Exit(2)
		}
	case "preserved":
		if !strings.Contains(uri, "api_key=secret") || !strings.Contains(uri, "token=secret") {
			fmt.Fprintf(os.Stderr, "%s: secret query not preserved in %q\n", hook, uri)
			os.Exit(2)
		}
	}
}

func runTLSSignerPlugin() {
	if plugin.HandleStartupFlags(os.Stdout, plugin.Manifest{
		Name:              "test-tls-signer",
		Version:           "1.0.0",
		Description:       "Test TLS signer plugin",
		RestishAPIVersion: 2,
		Hooks:             []string{"tls-signer"},
	}, nil) {
		return
	}

	dec := plugin.NewDecoder(os.Stdin)
	var init map[string]any
	if err := dec.ReadMessage(&init); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if init["type"] != "init" {
		fmt.Fprintln(os.Stderr, "expected init message")
		os.Exit(1)
	}
	params, _ := init["params"].(map[string]any)

	certPath := os.Getenv("RSH_TLS_SIGNER_CERT")
	keyPath := os.Getenv("RSH_TLS_SIGNER_KEY")
	if text, _ := params["cert_path"].(string); text != "" {
		certPath = text
	}
	if text, _ := params["key_path"].(string); text != "" {
		keyPath = text
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		fmt.Fprintln(os.Stderr, "invalid certificate")
		os.Exit(1)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		fmt.Fprintln(os.Stderr, "invalid private key")
		os.Exit(1)
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := plugin.WriteMessage(os.Stdout, map[string]any{
		"type":        "ready",
		"certificate": block.Bytes,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	runTLSSignerLoop(dec, key)
}

func runTLSSignerLoop(dec *plugin.Decoder, key *rsa.PrivateKey) {
	mode := os.Getenv("RSH_TLS_SIGNER_MODE")
	for {
		var msg map[string]any
		if err := dec.ReadMessage(&msg); err != nil {
			os.Exit(1)
		}
		if msg["type"] == plugin.MsgTypeTLSSignerShutdown {
			if shutdownFile := os.Getenv("RSH_TLS_SIGNER_SHUTDOWN_FILE"); shutdownFile != "" {
				_ = os.WriteFile(shutdownFile, []byte("shutdown"), 0o644)
			}
			return
		}
		if msg["type"] != "sign" {
			continue
		}
		if mode == "die" {
			os.Exit(1)
		}
		if mode == "error" {
			_ = plugin.WriteMessage(os.Stdout, map[string]any{"error": "device removed"})
			continue
		}
		if mode == "stderr-error" {
			fmt.Fprintln(os.Stderr, "pin incorrect")
			_ = plugin.WriteMessage(os.Stdout, map[string]any{"error": "sign failed"})
			continue
		}
		digest := msgBytes(msg["digest"])
		hash := crypto.Hash(msgInt(msg["hash"]))
		var sig []byte
		var err error
		if padding, _ := msg["padding"].(string); padding == "pss" {
			sig, err = rsa.SignPSS(rand.Reader, key, hash, digest, &rsa.PSSOptions{
				SaltLength: msgInt(msg["salt_length"]),
				Hash:       hash,
			})
		} else {
			sig, err = rsa.SignPKCS1v15(rand.Reader, key, hash, digest)
		}
		if err != nil {
			_ = plugin.WriteMessage(os.Stdout, map[string]any{"error": err.Error()})
			continue
		}
		_ = plugin.WriteMessage(os.Stdout, map[string]any{"signature": sig})
	}
}

func msgBytes(v any) []byte {
	switch data := v.(type) {
	case []byte:
		return data
	case []any:
		out := make([]byte, 0, len(data))
		for _, item := range data {
			switch n := item.(type) {
			case uint64:
				out = append(out, byte(n))
			case int64:
				out = append(out, byte(n))
			case int:
				out = append(out, byte(n))
			}
		}
		return out
	default:
		return nil
	}
}

func msgInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
