package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// maxRPCPayloadBytes caps the size of a single NDJSON message accepted from
// an MCP client to prevent memory exhaustion. Matches the CBOR plugin
// protocol limit.
const maxRPCPayloadBytes = 64 << 20 // 64 MiB

type Server struct {
	Tools          []*Tool
	ToolIndex      map[string]*Tool
	Exec           HTTPExecutor
	MaxResultBytes int
	RequestTimeout int
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	HasID   bool            `json:"-"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) ServeStdio(stdin io.Reader, stdout io.Writer) error {
	reader := bufio.NewReader(stdin)
	for {
		payload, err := readFrame(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			_ = writeRPC(stdout, rpcResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &rpcError{Code: -32700, Message: "framing error: " + err.Error()},
			})
			return err
		}
		req, err := parseRPCRequest(payload)
		if err != nil {
			if writeErr := writeRPC(stdout, rpcResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &rpcError{Code: -32700, Message: "parse error"},
			}); writeErr != nil {
				return writeErr
			}
			continue
		}
		if err := validateRPCRequest(req); err != nil {
			if writeErr := writeRPC(stdout, rpcResponse{
				JSONRPC: "2.0",
				ID:      responseID(req),
				Error:   &rpcError{Code: -32600, Message: "invalid request"},
			}); writeErr != nil {
				return writeErr
			}
			continue
		}
		resp := s.handleRequest(req)
		if !req.HasID || resp.JSONRPC == "" {
			continue
		}
		if err := writeRPC(stdout, resp); err != nil {
			return err
		}
	}
}

func parseRPCRequest(payload []byte) (rpcRequest, error) {
	var req rpcRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return req, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return req, err
	}
	if _, ok := fields["id"]; ok {
		req.HasID = true
	}
	return req, nil
}

func validateRPCRequest(req rpcRequest) error {
	if req.JSONRPC != "2.0" || req.Method == "" || !usableRPCID(req.ID) {
		return errors.New("invalid request")
	}
	return nil
}

func usableRPCID(id any) bool {
	switch id.(type) {
	case nil, string, float64:
		return true
	default:
		return false
	}
}

func responseID(req rpcRequest) any {
	if !req.HasID || !usableRPCID(req.ID) {
		return nil
	}
	return req.ID
}

func (s *Server) handleRequest(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2025-03-26",
			"serverInfo": map[string]any{
				"name":    "restish-mcp",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		}
	case "notifications/initialized":
		return rpcResponse{}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.Tools))
		for _, tool := range s.Tools {
			tools = append(tools, map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": tool.InputSchema,
			})
		}
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			return resp
		}
		tool := s.ToolIndex[params.Name]
		if tool == nil {
			resp.Error = &rpcError{Code: -32601, Message: "unknown tool"}
			return resp
		}
		result, err := s.callTool(tool, params.Arguments)
		if err != nil {
			resp.Error = &rpcError{Code: -32000, Message: err.Error()}
			return resp
		}
		resp.Result = result
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return resp
}

func (s *Server) callTool(tool *Tool, args map[string]any) (map[string]any, error) {
	if args == nil {
		args = map[string]any{}
	}
	req, err := tool.Request(args)
	if err != nil {
		return nil, err
	}
	if s.Exec == nil {
		return nil, errors.New("no HTTP executor configured")
	}
	if s.RequestTimeout > 0 {
		req.Timeout = s.RequestTimeout
	}
	resp, err := s.Exec(req)
	if err != nil {
		return nil, err
	}
	text, isError := formatToolResult(resp, s.MaxResultBytes)
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isError,
	}, nil
}

func formatToolResult(resp *HTTPResponse, maxBytes int) (string, bool) {
	isError := resp == nil || resp.Error != "" || resp.Status >= 400
	if resp == nil {
		return "request failed", true
	}

	bodyText := marshalPretty(resp.Body)
	if bodyText == "" {
		bodyText = "null"
	}
	if resp.Error != "" {
		bodyText = resp.Error
	}

	if resp.Status >= 400 {
		bodyText = fmt.Sprintf("HTTP %d\n%s", resp.Status, bodyText)
	} else if includeEnvelope(resp) {
		bodyText = marshalPretty(map[string]any{
			"status":  resp.Status,
			"headers": resp.Headers,
			"body":    resp.Body,
		})
	}

	if maxBytes > 0 && len(bodyText) > maxBytes {
		// Walk back to a UTF-8 code-point boundary so we don't split a multi-byte rune.
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(bodyText[cut]) {
			cut--
		}
		bodyText = bodyText[:cut] + "\n... truncated ..."
	}
	return bodyText, isError
}

func includeEnvelope(resp *HTTPResponse) bool {
	if resp == nil {
		return false
	}
	if resp.Status == 201 && resp.Headers != nil {
		if _, ok := resp.Headers["Location"]; ok {
			return true
		}
	}
	return false
}

func marshalPretty(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err == nil {
		return string(data)
	}
	return fmt.Sprint(v)
}

// readFrame reads one newline-delimited JSON-RPC message from the MCP stdio
// transport (spec revision 2025-06-18 and later: messages are single-line
// JSON objects separated by '\n'). Blank lines are skipped.
func readFrame(r *bufio.Reader) ([]byte, error) {
	for {
		var line []byte
		for {
			frag, err := r.ReadSlice('\n')
			line = append(line, frag...)
			if len(line) > maxRPCPayloadBytes {
				return nil, fmt.Errorf("MCP message exceeds %d bytes", maxRPCPayloadBytes)
			}
			if err == nil {
				break
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(err, io.EOF) {
				if len(bytes.TrimSpace(line)) == 0 {
					return nil, io.EOF
				}
				break
			}
			return nil, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		return line, nil
	}
}

func writeRPC(w io.Writer, resp rpcResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return writeFrame(w, data)
}

func writeFrame(w io.Writer, payload []byte) error {
	buf := make([]byte, 0, len(payload)+1)
	buf = append(buf, payload...)
	buf = append(buf, '\n')
	_, err := w.Write(buf)
	return err
}
