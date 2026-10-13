package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pb33f/libopenapi"
	"github.com/rest-sh/restish/v2/plugin"
)

func loadTestSpec(t *testing.T, name, raw string) *APISpec {
	t.Helper()
	doc, err := libopenapi.NewDocument([]byte(raw))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	return &APISpec{Name: name, ContentType: "application/json", Raw: []byte(raw), Document: doc}
}

func TestToolsFromSpecFilteringAndNamespacing(t *testing.T) {
	s := loadTestSpec(t, "demo", `{
	  "openapi": "3.1.0",
	  "info": {"title": "Demo", "version": "1.0.0"},
	  "paths": {
	    "/items/{id}": {
	      "get": {
	        "operationId": "getItem",
	        "summary": "Get an item",
	        "parameters": [
	          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}
	        ]
	      }
	    },
	    "/items": {
	      "post": {
	        "operationId": "createItem",
	        "requestBody": {
	          "required": true,
	          "content": {
	            "application/json": {
	              "schema": {
	                "type": "object",
	                "properties": {"name": {"type": "string"}}
	              }
	            }
	          }
	        }
	      }
	    },
	    "/hidden": {
	      "get": {
	        "operationId": "hiddenCli",
	        "x-cli-ignore": true
	      }
	    },
	    "/mcp-hidden": {
	      "get": {
	        "operationId": "hiddenMcp",
	        "x-mcp-ignore": true
	      }
	    }
	  }
	}`)

	tools, err := toolsFromSpec("demo", true, s, Options{AllowWriteTools: true})
	if err != nil {
		t.Fatalf("toolsFromSpec: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 visible tools, got %d", len(tools))
	}

	var create *Tool
	for _, tool := range tools {
		if tool.Name == "demo__createItem" {
			create = tool
		}
	}
	if create == nil {
		t.Fatal("expected namespaced createItem tool")
	}
	if create.BodyContentType != "application/json" {
		t.Fatalf("expected json body content type, got %q", create.BodyContentType)
	}
	props := create.InputSchema["properties"].(map[string]any)
	if _, ok := props["body"]; !ok {
		t.Fatal("expected body property in input schema")
	}
}

func TestToolsFromSpecSkipsWriteOperationsByDefault(t *testing.T) {
	s := loadTestSpec(t, "demo", `{
	  "openapi": "3.1.0",
	  "info": {"title": "Demo", "version": "1.0.0"},
	  "paths": {
	    "/items": {
	      "get": {"operationId": "listItems"},
	      "post": {"operationId": "createItem"},
	      "put": {"operationId": "replaceItem"},
	      "patch": {"operationId": "patchItem"},
	      "delete": {"operationId": "deleteItem"}
	    },
	    "/options": {
	      "options": {"operationId": "optionsItems"}
	    }
	  }
	}`)

	tools, err := toolsFromSpec("demo", false, s, Options{})
	if err != nil {
		t.Fatalf("toolsFromSpec: %v", err)
	}
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "listItems,optionsItems" {
		t.Fatalf("tools = %v, want listItems and optionsItems", names)
	}

	_, stats, err := toolsFromSpecWithStats("demo", false, s, Options{})
	if err != nil {
		t.Fatalf("toolsFromSpecWithStats: %v", err)
	}
	if stats.HiddenWriteOperations != 4 {
		t.Fatalf("HiddenWriteOperations = %d, want 4", stats.HiddenWriteOperations)
	}
}

func TestToolsFromSpecAllowWriteToolsExposesWrites(t *testing.T) {
	s := &APISpec{
		Name: "demo",
		Operations: []plugin.APIOperation{
			{ID: "getItem", Method: "GET", Path: "/items"},
			{ID: "createItem", Method: "POST", Path: "/items"},
			{ID: "replaceItem", Method: "PUT", Path: "/items"},
			{ID: "patchItem", Method: "PATCH", Path: "/items"},
			{ID: "deleteItem", Method: "DELETE", Path: "/items"},
		},
	}

	tools, err := toolsFromSpec("demo", false, s, Options{AllowWriteTools: true})
	if err != nil {
		t.Fatalf("toolsFromSpec: %v", err)
	}
	if len(tools) != 5 {
		t.Fatalf("expected all write tools with --allow-write-tools, got %#v", tools)
	}
}

func TestToolsFromSpecIncludesPathLevelParameters(t *testing.T) {
	s := loadTestSpec(t, "demo", `{
	  "openapi": "3.1.0",
	  "info": {"title": "Demo", "version": "1.0.0"},
	  "paths": {
	    "/items/{id}": {
	      "parameters": [
	        {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}},
	        {"name": "verbose", "in": "query", "schema": {"type": "boolean"}}
	      ],
	      "get": {
	        "operationId": "getItem",
	        "parameters": [
	          {"name": "verbose", "in": "query", "schema": {"type": "string"}}
	        ]
	      }
	    }
	  }
	}`)

	tools, err := toolsFromSpec("demo", false, s, Options{})
	if err != nil {
		t.Fatalf("toolsFromSpec: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool := tools[0]
	props := tool.InputSchema["properties"].(map[string]any)
	if _, ok := props["id"]; !ok {
		t.Fatalf("expected path-level id parameter in schema: %#v", props)
	}
	required := tool.InputSchema["required"].([]string)
	if len(required) != 1 || required[0] != "id" {
		t.Fatalf("required = %#v, want [id]", required)
	}
	if got := props["verbose"].(map[string]any)["type"]; got != "string" {
		t.Fatalf("operation-level verbose parameter should override path-level schema, got %#v", got)
	}

	req, err := tool.Request(map[string]any{"id": "a/b", "verbose": "yes"})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if req.URI != "demo/items/a%2Fb?verbose=yes" {
		t.Fatalf("URI = %q, want demo/items/a%%2Fb?verbose=yes", req.URI)
	}
}

func TestToolsFromSpecPrefersHostResolvedOperations(t *testing.T) {
	s := &APISpec{
		Name: "demo",
		Operations: []plugin.APIOperation{
			{
				ID:      "getItem",
				Method:  "GET",
				Path:    "/v2/items/{id}",
				Summary: "Get an item",
				Parameters: []plugin.APIParam{
					{Name: "id", In: "path", Required: true, Type: "string"},
					{Name: "include", In: "query", Type: "boolean"},
				},
			},
			{ID: "hiddenMcp", Method: "GET", Path: "/hidden", MCPIgnore: true},
			{ID: "createItem", Method: "POST", Path: "/v2/items"},
		},
	}

	tools, err := toolsFromSpec("demo", false, s, Options{})
	if err != nil {
		t.Fatalf("toolsFromSpec: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "getItem" {
		t.Fatalf("expected only getItem, got %#v", tools)
	}
	req, err := tools[0].Request(map[string]any{"id": "a/b", "include": true})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if req.URI != "demo/v2/items/a%2Fb?include=true" {
		t.Fatalf("URI = %q, want demo/v2/items/a%%2Fb?include=true", req.URI)
	}
}

func TestToolsFromHostOperationsPreserveContentQueryParameters(t *testing.T) {
	s := &APISpec{
		Name: "demo",
		Operations: []plugin.APIOperation{
			{
				ID:     "listItems",
				Method: "GET",
				Path:   "/items",
				Parameters: []plugin.APIParam{{
					Name:             "filter",
					In:               "query",
					Required:         true,
					Type:             "object",
					ContentMediaType: "application/json",
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":   map[string]any{"type": "string"},
							"active": map[string]any{"type": "boolean"},
						},
					},
				}},
			},
		},
	}

	tools, err := toolsFromSpec("demo", false, s, Options{})
	if err != nil {
		t.Fatalf("toolsFromSpec: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", tools)
	}
	props := tools[0].InputSchema["properties"].(map[string]any)
	filter := props["filter"].(map[string]any)
	if got := filter["type"]; got != "object" {
		t.Fatalf("filter type = %#v, want object", got)
	}
	filterProps, ok := filter["properties"].(map[string]any)
	if !ok || filterProps["active"] == nil {
		t.Fatalf("filter properties = %#v, want active property", filter["properties"])
	}

	req, err := tools[0].Request(map[string]any{
		"filter": map[string]any{"name": "Ada", "active": true},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	want := `demo/items?filter=%7B%22active%22%3Atrue%2C%22name%22%3A%22Ada%22%7D`
	if req.URI != want {
		t.Fatalf("URI = %q, want %q", req.URI, want)
	}
}

func TestToolsFromHostOperationsPreserveRequestBodySchema(t *testing.T) {
	s := &APISpec{
		Name: "demo",
		Operations: []plugin.APIOperation{{
			ID:                   "createItem",
			Method:               "POST",
			Path:                 "/items",
			HasBody:              true,
			BodyRequired:         true,
			RequestMediaType:     "application/json",
			RequestSchemaDialect: "https://json-schema.org/draft/2020-12/schema",
			RequestSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
			},
		}},
	}

	tools, err := toolsFromSpec("demo", false, s, Options{AllowWriteTools: true})
	if err != nil {
		t.Fatalf("toolsFromSpec: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", tools)
	}
	props := tools[0].InputSchema["properties"].(map[string]any)
	body := props["body"].(map[string]any)
	if got := body["type"]; got != "object" {
		t.Fatalf("body type = %#v, want object", got)
	}
	bodyProps, ok := body["properties"].(map[string]any)
	if !ok || bodyProps["name"] == nil {
		t.Fatalf("body properties = %#v, want name property", body["properties"])
	}
	if got, want := tools[0].BodySchemaDialect, "https://json-schema.org/draft/2020-12/schema"; got != want {
		t.Fatalf("BodySchemaDialect = %q, want %q", got, want)
	}
}

func TestToolsFromRawSpecPreserveContentQueryParameters(t *testing.T) {
	s := loadTestSpec(t, "demo", `{
	  "openapi": "3.1.0",
	  "info": {"title": "Demo", "version": "1.0.0"},
	  "paths": {
	    "/items": {
	      "get": {
	        "operationId": "listItems",
	        "parameters": [
	          {
	            "name": "filter",
	            "in": "query",
	            "required": true,
	            "content": {
	              "application/json": {
	                "schema": {
	                  "type": "object",
	                  "properties": {
	                    "name": {"type": "string"},
	                    "active": {"type": "boolean"}
	                  }
	                }
	              }
	            }
	          }
	        ]
	      }
	    }
	  }
	}`)

	tools, err := toolsFromSpec("demo", false, s, Options{})
	if err != nil {
		t.Fatalf("toolsFromSpec: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", tools)
	}
	props := tools[0].InputSchema["properties"].(map[string]any)
	filter := props["filter"].(map[string]any)
	if got := filter["type"]; got != "object" {
		t.Fatalf("filter type = %#v, want object", got)
	}
	req, err := tools[0].Request(map[string]any{
		"filter": map[string]any{"name": "Ada", "active": true},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	want := `demo/items?filter=%7B%22active%22%3Atrue%2C%22name%22%3A%22Ada%22%7D`
	if req.URI != want {
		t.Fatalf("URI = %q, want %q", req.URI, want)
	}
}

func TestToolRequestSerializesArrayParameters(t *testing.T) {
	explode := true
	tool := &Tool{
		APIName: "demo",
		Method:  "GET",
		Path:    "/items",
		Params: []Param{
			{Name: "tag", In: "query", Type: "array", ItemType: "string", Style: "form", Explode: &explode},
			{Name: "X-Ids", In: "header", Type: "array", ItemType: "integer", Style: "simple"},
		},
	}
	req, err := tool.Request(map[string]any{
		"tag":   []any{"red", "blue"},
		"X-Ids": []any{float64(1), float64(2)},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if req.URI != "demo/items?tag=red&tag=blue" {
		t.Fatalf("URI = %q, want repeated query keys", req.URI)
	}
	if got := req.Headers["X-Ids"]; got != "1,2" {
		t.Fatalf("X-Ids = %q, want 1,2", got)
	}
}

func TestToolRequestSerializesCookieParameters(t *testing.T) {
	explode := true
	tool := &Tool{
		APIName: "demo",
		Method:  "GET",
		Path:    "/items",
		Params: []Param{
			{Name: "session", In: "cookie", Type: "string"},
			{Name: "tag", In: "cookie", Type: "array", Style: "form", Explode: &explode},
		},
	}
	req, err := tool.Request(map[string]any{
		"session": "a/b",
		"tag":     []any{"red", "blue"},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if got := req.Headers["Cookie"]; got != "session=a%2Fb; tag=red; tag=blue" {
		t.Fatalf("Cookie = %q, want encoded cookie pairs", got)
	}
}

func TestToolRequestSerializesDeepObjectQueryParameter(t *testing.T) {
	tool := &Tool{
		APIName: "demo",
		Method:  "GET",
		Path:    "/items",
		Params:  []Param{{Name: "filter", In: "query", Type: "object", Style: "deepObject"}},
	}
	req, err := tool.Request(map[string]any{"filter": map[string]any{"status": "open", "limit": float64(10)}})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if req.URI != "demo/items?filter%5Blimit%5D=10&filter%5Bstatus%5D=open" {
		t.Fatalf("URI = %q, want deepObject query", req.URI)
	}
}

func TestToolRequestPreservesAllowReservedQueryCharacters(t *testing.T) {
	tool := &Tool{
		APIName: "demo",
		Method:  "GET",
		Path:    "/items",
		Params:  []Param{{Name: "q", In: "query", Type: "string", AllowReserved: true}},
	}
	req, err := tool.Request(map[string]any{"q": "a/b?c=d,e"})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if req.URI != "demo/items?q=a/b?c=d,e" {
		t.Fatalf("URI = %q, want allowReserved query", req.URI)
	}
}

func TestToolRequestRejectsObjectPathParameter(t *testing.T) {
	tool := &Tool{
		APIName: "demo",
		Method:  "GET",
		Path:    "/items/{filter}",
		Params:  []Param{{Name: "filter", In: "path", Type: "object"}},
	}
	_, err := tool.Request(map[string]any{"filter": map[string]any{"status": "open"}})
	if err == nil {
		t.Fatal("expected object rejection")
	}
	if !strings.Contains(err.Error(), "object values are not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolRequestRejectsUnsupportedArrayStyle(t *testing.T) {
	tool := &Tool{
		APIName: "demo",
		Method:  "GET",
		Path:    "/items",
		Params:  []Param{{Name: "ids", In: "header", Type: "array", Style: "form"}},
	}
	_, err := tool.Request(map[string]any{"ids": []any{"a", "b"}})
	if err == nil {
		t.Fatal("expected unsupported style error")
	}
	if !strings.Contains(err.Error(), "unsupported header array style") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsRemovedHTTPFlag(t *testing.T) {
	if _, err := ParseArgs([]string{"serve", "--http", ":3000", "demo"}); err == nil {
		t.Fatal("expected removed --http flag to be rejected by flag parser")
	}
}

func TestParseArgsRequestTimeout(t *testing.T) {
	cfg, err := ParseArgs([]string{"serve", "--request-timeout", "7", "demo"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if got := cfg.Options.RequestTimeout; got != 7 {
		t.Fatalf("RequestTimeout = %d, want 7", got)
	}
}

func TestPluginClientSendsHTTPRequestTimeout(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	client := newPluginClient(plugin.NewDecoder(inR), outW)

	done := make(chan error, 1)
	go func() {
		_, err := client.do(&HTTPRequest{Method: "GET", URI: "demo/items", Timeout: 9})
		done <- err
	}()

	var msg plugin.HTTPRequestMsg
	if err := plugin.NewDecoder(outR).ReadMessage(&msg); err != nil {
		t.Fatalf("decode request message: %v", err)
	}
	if got := msg.Timeout; got != 9 {
		t.Fatalf("Timeout = %d, want 9", got)
	}
	if err := plugin.WriteMessage(inW, plugin.HTTPResponseMsg{Type: plugin.MsgTypeHTTPResponse, RequestID: msg.RequestID, Status: 200}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("do: %v", err)
	}
}

func TestPluginClientHTTPRequestTimesOutLocally(t *testing.T) {
	inR, _ := io.Pipe()
	var out bytes.Buffer
	client := newPluginClient(plugin.NewDecoder(inR), &out)

	start := time.Now()
	_, err := client.do(&HTTPRequest{Method: "GET", URI: "demo/items", Timeout: 1})
	if err == nil {
		t.Fatal("expected local timeout error")
	}
	if !strings.Contains(err.Error(), "timed out after 1s") {
		t.Fatalf("unexpected timeout error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("local timeout took too long: %v", elapsed)
	}
}

func TestRunServeToolCall(t *testing.T) {
	spec := loadTestSpec(t, "demo", `{
	  "openapi": "3.1.0",
	  "info": {"title": "Demo", "version": "1.0.0"},
	  "paths": {
	    "/items/{id}": {
	      "get": {
	        "operationId": "getItem",
	        "summary": "Get an item",
	        "parameters": [
	          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}
	        ]
	      }
	    }
	  }
	}`)

	var stdin bytes.Buffer
	writeFrame(&stdin, mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	}))
	writeFrame(&stdin, mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}))
	writeFrame(&stdin, mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "getItem",
			"arguments": map[string]any{"id": "42"},
		},
	}))

	var stdout bytes.Buffer
	err := Run(&stdin, &stdout, func(name string) (*APISpec, error) {
		if name != "demo" {
			t.Fatalf("unexpected api name: %s", name)
		}
		return spec, nil
	}, func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URI != "demo/items/42" {
			t.Fatalf("unexpected request URI: %s", req.URI)
		}
		if req.Timeout != 60 {
			t.Fatalf("request timeout = %d, want default 60", req.Timeout)
		}
		return &HTTPResponse{
			Status: 200,
			Body:   map[string]any{"id": "42", "name": "example"},
		}, nil
	}, []string{"serve", "demo"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	responses := readResponses(t, stdout.Bytes())
	if len(responses) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(responses))
	}
	listResult := responses[1]["result"].(map[string]any)
	tools := listResult["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"].(string) != "getItem" {
		t.Fatalf("unexpected tools list: %#v", tools)
	}
	last := responses[2]["result"].(map[string]any)
	content := last["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"name": "example"`) {
		t.Fatalf("expected tool body in result, got:\n%s", text)
	}
}

func TestServeStdioInvalidRequests(t *testing.T) {
	var stdin bytes.Buffer
	writeFrame(&stdin, mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "missing-method",
	}))
	writeFrame(&stdin, mustJSON(t, map[string]any{
		"jsonrpc": "1.0",
		"id":      "wrong-version",
		"method":  "ping",
	}))
	writeFrame(&stdin, []byte(`{"jsonrpc":"2.0","id":`))
	writeFrame(&stdin, mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "ping",
	}))
	writeFrame(&stdin, mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "ok",
		"method":  "ping",
	}))

	var stdout bytes.Buffer
	server := &Server{}
	if err := server.ServeStdio(&stdin, &stdout); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	responses := readResponses(t, stdout.Bytes())
	if len(responses) != 4 {
		t.Fatalf("responses = %d, want 4: %#v", len(responses), responses)
	}
	for i, resp := range responses[:2] {
		errObj := resp["error"].(map[string]any)
		if got := int(errObj["code"].(float64)); got != -32600 {
			t.Fatalf("response %d code = %d, want -32600", i, got)
		}
	}
	if got := responses[0]["id"]; got != "missing-method" {
		t.Fatalf("missing-method id = %#v", got)
	}
	if got := responses[1]["id"]; got != "wrong-version" {
		t.Fatalf("wrong-version id = %#v", got)
	}
	if got := int(responses[2]["error"].(map[string]any)["code"].(float64)); got != -32700 {
		t.Fatalf("parse error code = %d, want -32700", got)
	}
	if _, ok := responses[2]["id"]; !ok || responses[2]["id"] != nil {
		t.Fatalf("parse error id = %#v, want null", responses[2]["id"])
	}
	if _, ok := responses[3]["result"].(map[string]any); !ok {
		t.Fatalf("expected ping result, got %#v", responses[3])
	}
}

func TestReadFrameLineLimits(t *testing.T) {
	t.Run("oversized line", func(t *testing.T) {
		input := strings.Repeat("X", maxRPCPayloadBytes+1) + "\n"
		_, err := readFrame(bufio.NewReader(strings.NewReader(input)))
		if err == nil || !strings.Contains(err.Error(), "message exceeds") {
			t.Fatalf("err = %v, want message size limit", err)
		}
	})

	t.Run("payload accepted within line limits", func(t *testing.T) {
		got, err := readFrame(bufio.NewReader(strings.NewReader("{}\n")))
		if err != nil {
			t.Fatalf("readFrame: %v", err)
		}
		if string(got) != "{}" {
			t.Fatalf("payload = %q", got)
		}
	})

	t.Run("blank lines are skipped", func(t *testing.T) {
		got, err := readFrame(bufio.NewReader(strings.NewReader("\n\n{}\n")))
		if err != nil {
			t.Fatalf("readFrame: %v", err)
		}
		if string(got) != "{}" {
			t.Fatalf("payload = %q", got)
		}
	})

	t.Run("trailing message without newline", func(t *testing.T) {
		got, err := readFrame(bufio.NewReader(strings.NewReader("{}")))
		if err != nil {
			t.Fatalf("readFrame: %v", err)
		}
		if string(got) != "{}" {
			t.Fatalf("payload = %q", got)
		}
	})
}

func TestServeStdioWritesFramingError(t *testing.T) {
	input := strings.Repeat("X", maxRPCPayloadBytes+1) + "\n"
	var stdout bytes.Buffer
	err := (&Server{}).ServeStdio(strings.NewReader(input), &stdout)
	if err == nil {
		t.Fatal("expected framing error")
	}
	responses := readResponses(t, stdout.Bytes())
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	errObj := responses[0]["error"].(map[string]any)
	if got := int(errObj["code"].(float64)); got != -32700 {
		t.Fatalf("code = %d, want -32700", got)
	}
	if !strings.Contains(errObj["message"].(string), "framing error") {
		t.Fatalf("message = %q", errObj["message"])
	}
	if responses[0]["id"] != nil {
		t.Fatalf("id = %#v, want null", responses[0]["id"])
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func readResponses(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	reader := bufio.NewReader(bytes.NewReader(data))
	for {
		payload, err := readFrame(reader)
		if err != nil {
			break
		}
		var msg map[string]any
		if err := json.Unmarshal(payload, &msg); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		out = append(out, msg)
	}
	return out
}
