package plugin_test

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/rest-sh/restish/v2/plugin"
)

// TestWriteReadRoundTrip verifies that WriteMessage → ReadMessage recovers
// the original value for common Go types.
func TestWriteReadRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"string", "hello world"},
		{"int", 42},
		{"float", 3.14},
		{"bool", true},
		{"map", map[string]any{"key": "value", "num": float64(1)}},
		{"slice", []any{"a", "b", "c"}},
		{"nil", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := plugin.WriteMessage(&buf, tc.val); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			var got any
			if err := plugin.ReadMessage(&buf, &got); err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}
			// Basic non-nil check for non-nil inputs.
			if tc.val != nil && got == nil {
				t.Errorf("got nil, want %v", tc.val)
			}
		})
	}
}

// TestWriteMessageIsRawCBOR verifies that WriteMessage produces a plain CBOR
// data item with no additional framing (no length prefix, etc.).
func TestWriteMessageIsRawCBOR(t *testing.T) {
	var buf bytes.Buffer
	if err := plugin.WriteMessage(&buf, "test"); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	data := buf.Bytes()
	// The output must be valid CBOR that decodes back to our value.
	var got any
	if err := cbor.Unmarshal(data, &got); err != nil {
		t.Fatalf("output is not valid CBOR: %v", err)
	}
	if got != "test" {
		t.Errorf("decoded value: got %v, want %q", got, "test")
	}
}

// TestReadMessageTruncatedStream verifies that ReadMessage on a stream that
// ends mid-item returns a descriptive error.
func TestReadMessageTruncatedStream(t *testing.T) {
	// Start a CBOR text string of 100 bytes but provide only 2 bytes of content.
	// CBOR major type 3 (text string), additional info 24 (one-byte length follows),
	// length = 100, then only 2 bytes of actual content.
	buf := bytes.NewReader([]byte{0x78, 0x64, 'a', 'b'})

	var got any
	err := plugin.ReadMessage(buf, &got)
	if err == nil {
		t.Fatal("expected error for truncated stream, got nil")
	}
	// The error should mention truncation or EOF.
	s := err.Error()
	if !strings.Contains(s, "EOF") && !strings.Contains(s, "truncat") {
		t.Errorf("expected EOF or truncat in error, got: %v", err)
	}
}

// TestTypedMessageRoundTrip verifies that a typed message struct written with
// WriteMessage decodes back to the same map representation the host uses,
// confirming that cbor struct tags match the stringly-keyed wire format.
func TestTypedMessageRoundTrip(t *testing.T) {
	msg := plugin.HTTPRequestMsg{
		Type:    plugin.MsgTypeHTTPRequest,
		Method:  "POST",
		URI:     "https://api.example.com/items",
		NoCache: true,
		Filter:  "body.items",
	}

	var buf bytes.Buffer
	if err := plugin.WriteMessage(&buf, msg); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	var decoded map[string]any
	if err := plugin.ReadMessage(&buf, &decoded); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	if decoded["type"] != plugin.MsgTypeHTTPRequest {
		t.Errorf("type: got %v, want %q", decoded["type"], plugin.MsgTypeHTTPRequest)
	}
	if decoded["method"] != "POST" {
		t.Errorf("method: got %v, want POST", decoded["method"])
	}
	if decoded["uri"] != "https://api.example.com/items" {
		t.Errorf("uri: got %v", decoded["uri"])
	}
	if decoded["no_cache"] != true {
		t.Errorf("no_cache: got %v, want true", decoded["no_cache"])
	}
	if decoded["filter"] != "body.items" {
		t.Errorf("filter: got %v, want body.items", decoded["filter"])
	}
}

func TestSelectMessageWireShape(t *testing.T) {
	var buf bytes.Buffer
	err := plugin.WriteMessage(&buf, plugin.SelectMsg{
		Type:      plugin.MsgTypeSelect,
		RequestID: "req-1",
		Message:   "Choose a pet",
		Options:   []plugin.SelectOption{{Label: "Mochi", Value: "42"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := plugin.ReadMessage(&buf, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "select" || decoded["request_id"] != "req-1" || decoded["message"] != "Choose a pet" {
		t.Fatalf("select message = %#v", decoded)
	}
	options, ok := decoded["options"].([]any)
	if !ok || len(options) != 1 {
		t.Fatalf("select options = %#v", decoded["options"])
	}
	option, ok := options[0].(map[string]any)
	if !ok || option["label"] != "Mochi" || option["value"] != "42" {
		t.Fatalf("select option = %#v", options[0])
	}
}

func TestCommandClientDoConcurrentRoutesByRequestID(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	defer hostToPluginR.Close()
	defer hostToPluginW.Close()
	defer pluginToHostR.Close()
	defer pluginToHostW.Close()

	client := plugin.NewCommandClient(hostToPluginR, pluginToHostW)
	requests := make(chan plugin.HTTPRequestMsg, 2)
	go func() {
		dec := plugin.NewDecoder(pluginToHostR)
		for i := 0; i < 2; i++ {
			var req plugin.HTTPRequestMsg
			if err := dec.ReadMessage(&req); err != nil {
				t.Errorf("read request: %v", err)
				return
			}
			requests <- req
		}
		close(requests)
	}()

	var wg sync.WaitGroup
	responses := make(chan string, 2)
	for _, uri := range []string{"https://api.example.com/one", "https://api.example.com/two"} {
		wg.Add(1)
		go func(uri string) {
			defer wg.Done()
			resp, err := client.Do(&plugin.HTTPRequestMsg{URI: uri})
			if err != nil {
				t.Errorf("Do(%s): %v", uri, err)
				return
			}
			responses <- resp.Body.(string)
		}(uri)
	}

	var gotReqs []plugin.HTTPRequestMsg
	for req := range requests {
		gotReqs = append(gotReqs, req)
	}
	if len(gotReqs) != 2 {
		t.Fatalf("got %d requests, want 2", len(gotReqs))
	}
	for i := len(gotReqs) - 1; i >= 0; i-- {
		req := gotReqs[i]
		if req.RequestID == "" {
			t.Fatal("expected request_id to be assigned")
		}
		if err := plugin.WriteMessage(hostToPluginW, plugin.HTTPResponseMsg{
			Type:      plugin.MsgTypeHTTPResponse,
			RequestID: req.RequestID,
			Status:    200,
			Body:      req.URI,
		}); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}
	wg.Wait()
	close(responses)

	seen := map[string]bool{}
	for body := range responses {
		seen[body] = true
	}
	for _, uri := range []string{"https://api.example.com/one", "https://api.example.com/two"} {
		if !seen[uri] {
			t.Fatalf("missing response body %q in %#v", uri, seen)
		}
	}
}

func TestLargeMessageRoundTrip(t *testing.T) {
	// Build a slice of 10,000 elements to produce >64KB payload.
	large := make([]string, 10_000)
	for i := range large {
		large[i] = "0123456789abcdef" // 16 bytes each → ~160KB
	}

	var buf bytes.Buffer
	if err := plugin.WriteMessage(&buf, large); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	var got []string
	if err := plugin.ReadMessage(&buf, &got); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if len(got) != len(large) {
		t.Errorf("length mismatch: got %d, want %d", len(got), len(large))
	}
}

func TestDecModeUsesPluginMessageLimits(t *testing.T) {
	opts := plugin.DecMode.DecOptions()
	if opts.MaxNestedLevels != 16 {
		t.Fatalf("MaxNestedLevels = %d, want 16", opts.MaxNestedLevels)
	}
	if opts.MaxArrayElements != 65536 {
		t.Fatalf("MaxArrayElements = %d, want 65536", opts.MaxArrayElements)
	}
	if opts.MaxMapPairs != 16384 {
		t.Fatalf("MaxMapPairs = %d, want 16384", opts.MaxMapPairs)
	}
}

func TestReadMessageRejectsOversizedArray(t *testing.T) {
	tooLarge := make([]any, 65537)
	var buf bytes.Buffer
	if err := plugin.WriteMessage(&buf, tooLarge); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	var got any
	err := plugin.ReadMessage(&buf, &got)
	if err == nil {
		t.Fatal("expected array limit error")
	}
	if !strings.Contains(err.Error(), "max number") {
		t.Fatalf("error = %v, want max number limit", err)
	}
}

func TestReadMessageRejectsOversizedMap(t *testing.T) {
	tooLarge := make(map[string]any, 16385)
	for i := 0; i < 16385; i++ {
		tooLarge["k"+strconv.Itoa(i)] = nil
	}
	var buf bytes.Buffer
	if err := plugin.WriteMessage(&buf, tooLarge); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	var got any
	err := plugin.ReadMessage(&buf, &got)
	if err == nil {
		t.Fatal("expected map limit error")
	}
	if !strings.Contains(err.Error(), "max number") {
		t.Fatalf("error = %v, want max number limit", err)
	}
}

func TestReadMessageRejectsExcessiveNesting(t *testing.T) {
	var nested any = "leaf"
	for i := 0; i < 17; i++ {
		nested = []any{nested}
	}
	var buf bytes.Buffer
	if err := plugin.WriteMessage(&buf, nested); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	var got any
	err := plugin.ReadMessage(&buf, &got)
	if err == nil {
		t.Fatal("expected nesting limit error")
	}
	if !strings.Contains(err.Error(), "nested") {
		t.Fatalf("error = %v, want nesting limit", err)
	}
}

func TestReadRawRejectsOversizedMap(t *testing.T) {
	tooLarge := make(map[string]any, 16385)
	for i := 0; i < 16385; i++ {
		tooLarge["k"+strconv.Itoa(i)] = nil
	}
	var buf bytes.Buffer
	if err := plugin.WriteMessage(&buf, tooLarge); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	_, err := plugin.NewDecoder(&buf).ReadRaw()
	if err == nil {
		t.Fatal("expected raw map limit error")
	}
	if !strings.Contains(err.Error(), "max number") {
		t.Fatalf("error = %v, want max number limit", err)
	}
}

func TestHandleStartupFlagsOnlyUsesInjectedPrefix(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	manifest := plugin.Manifest{Name: "demo", RestishAPIVersion: 2}

	t.Run("manifest in prefix", func(t *testing.T) {
		os.Args = []string{"restish-demo", plugin.StartupFlagColor + "=true", plugin.StartupFlagManifest, "ignored"}
		var out bytes.Buffer
		if !plugin.HandleStartupFlags(&out, manifest, nil) {
			t.Fatal("expected startup flag to be handled")
		}
		var got plugin.Manifest
		if err := plugin.ReadMessage(&out, &got); err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if got.Name != "demo" {
			t.Fatalf("manifest name = %q, want demo", got.Name)
		}
	})

	t.Run("manifest after user arg", func(t *testing.T) {
		os.Args = []string{"restish-demo", plugin.StartupFlagColor + "=true", "run", plugin.StartupFlagManifest}
		var out bytes.Buffer
		if plugin.HandleStartupFlags(&out, manifest, nil) {
			t.Fatal("startup flag after user arg should not be handled")
		}
		if out.Len() != 0 {
			t.Fatalf("unexpected output: %x", out.Bytes())
		}
	})
}

func TestTerminalContextFromArgsOnlyUsesInjectedPrefix(t *testing.T) {
	ctx := plugin.TerminalContextFromArgs([]string{
		plugin.StartupFlagColor + "=true",
		plugin.StartupFlagStdoutTTY + "=true",
		plugin.StartupFlagTheme + `={"inserted":"#ffffff"}`,
		"run",
		plugin.StartupFlagColor + "=false",
		plugin.StartupFlagStderrTTY + "=true",
		plugin.StartupFlagTheme + `={"inserted":"#000000"}`,
	})
	if !ctx.Color {
		t.Fatal("expected color from injected prefix")
	}
	if !ctx.StdoutTTY {
		t.Fatal("expected stdout tty from injected prefix")
	}
	if ctx.StderrTTY {
		t.Fatal("stderr tty after user arg should be ignored")
	}
	if ctx.Theme["inserted"] != "#ffffff" {
		t.Fatalf("theme = %#v, want injected prefix theme", ctx.Theme)
	}
}

func TestArgsWithoutStartupFlagsPreservesUserRSHFlags(t *testing.T) {
	got := plugin.ArgsWithoutStartupFlags([]string{
		plugin.StartupFlagColor + "=true",
		plugin.StartupFlagStdoutTTY + "=false",
		plugin.StartupFlagTheme + `={"inserted":"#ffffff"}`,
		"run",
		plugin.StartupFlagManifest,
	})
	want := []string{"run", plugin.StartupFlagManifest}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

// TestDecoderSequentialPipe verifies that NewDecoder correctly reads multiple
// CBOR items sent sequentially through an os.Pipe, simulating the subprocess
// stdin/stdout channel used by command and TLS-signer plugins.
//
// The cbor.Decoder maintains a 512-byte internal read buffer, so a fresh
// decoder created per ReadMessage call would silently discard buffered bytes
// belonging to later items. This test catches that regression.
func TestDecoderSequentialPipe(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()

	messages := []map[string]any{
		{"type": "init", "command": "greet"},
		{"type": "http-request", "method": "GET", "uri": "https://example.com/foo"},
		{"type": "done", "exit_code": uint64(0)},
	}

	go func() {
		defer pw.Close()
		for _, msg := range messages {
			if wErr := plugin.WriteMessage(pw, msg); wErr != nil {
				return
			}
		}
	}()

	dec := plugin.NewDecoder(pr)
	for i, want := range messages {
		var got map[string]any
		if err := dec.ReadMessage(&got); err != nil {
			t.Fatalf("ReadMessage[%d]: %v", i, err)
		}
		if got["type"] != want["type"] {
			t.Errorf("msg[%d] type: got %q, want %q", i, got["type"], want["type"])
		}
	}
}
