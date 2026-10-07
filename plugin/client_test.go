package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommandClientProgressWritesProgressMessage(t *testing.T) {
	var out bytes.Buffer
	client := NewCommandClient(bytes.NewReader(nil), &out)
	if err := client.Progress("working"); err != nil {
		t.Fatalf("Progress: %v", err)
	}

	var msg ProgressMsg
	if err := NewDecoder(bytes.NewReader(out.Bytes())).ReadMessage(&msg); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msg.Type != MsgTypeProgress {
		t.Fatalf("Type = %q, want %q", msg.Type, MsgTypeProgress)
	}
	if msg.Text != "working" {
		t.Fatalf("Text = %q, want %q", msg.Text, "working")
	}
}

func TestCommandClientProgressUpdateWritesStructuredProgress(t *testing.T) {
	var out bytes.Buffer
	client := NewCommandClient(bytes.NewReader(nil), &out)
	current, total := int64(2), int64(4)
	if err := client.ProgressUpdate(ProgressMsg{
		Text:    "Running step 2 of 4",
		ID:      "workflow",
		Label:   "Run workflow",
		State:   "running",
		Current: &current,
		Total:   &total,
		Unit:    "steps",
		Message: "fetch owner",
	}); err != nil {
		t.Fatalf("ProgressUpdate: %v", err)
	}

	var msg ProgressMsg
	if err := NewDecoder(bytes.NewReader(out.Bytes())).ReadMessage(&msg); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msg.Type != MsgTypeProgress || msg.Text != "Running step 2 of 4" || msg.ID != "workflow" || msg.Label != "Run workflow" || msg.State != "running" || msg.Current == nil || *msg.Current != 2 || msg.Total == nil || *msg.Total != 4 || msg.Unit != "steps" || msg.Message != "fetch owner" {
		t.Fatalf("message = %#v", msg)
	}
}

func TestStructuredProgressRetainsLegacyTextFallback(t *testing.T) {
	var out bytes.Buffer
	if err := WriteMessage(&out, ProgressMsg{Type: MsgTypeProgress, Text: "working", ID: "workflow"}); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	var legacy struct {
		Type string `cbor:"type"`
		Text string `cbor:"text"`
	}
	if err := NewDecoder(bytes.NewReader(out.Bytes())).ReadMessage(&legacy); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if legacy.Type != MsgTypeProgress || legacy.Text != "working" {
		t.Fatalf("legacy message = %#v", legacy)
	}
}

func TestCommandClientWriteStdoutWritesStdoutDataMessage(t *testing.T) {
	var out bytes.Buffer
	client := NewCommandClient(bytes.NewReader(nil), &out)
	if err := client.WriteStdout([]byte("hello")); err != nil {
		t.Fatalf("WriteStdout: %v", err)
	}

	var msg StdoutDataMsg
	if err := NewDecoder(bytes.NewReader(out.Bytes())).ReadMessage(&msg); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msg.Type != MsgTypeStdoutData || string(msg.Data) != "hello" {
		t.Fatalf("message = %#v", msg)
	}
}

func TestCommandClientWriteStderrWritesStderrDataMessage(t *testing.T) {
	var out bytes.Buffer
	client := NewCommandClient(bytes.NewReader(nil), &out)
	if err := client.WriteStderr([]byte("oops")); err != nil {
		t.Fatalf("WriteStderr: %v", err)
	}

	var msg StderrDataMsg
	if err := NewDecoder(bytes.NewReader(out.Bytes())).ReadMessage(&msg); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msg.Type != MsgTypeStderrData || string(msg.Data) != "oops" {
		t.Fatalf("message = %#v", msg)
	}
}

func TestCommandClientDoSkipsUnknownMessages(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	defer hostToPluginR.Close()
	defer hostToPluginW.Close()
	defer pluginToHostR.Close()
	defer pluginToHostW.Close()

	client := NewCommandClient(hostToPluginR, pluginToHostW)
	errCh := make(chan error, 1)
	go func() {
		resp, err := client.Do(&HTTPRequestMsg{URI: "https://api.example.com/items"})
		if err != nil {
			errCh <- err
			return
		}
		if resp.Status != 200 || resp.Body != "ok" {
			t.Errorf("unexpected response: %#v", resp)
		}
		errCh <- nil
	}()

	var req HTTPRequestMsg
	if err := NewDecoder(pluginToHostR).ReadMessage(&req); err != nil {
		t.Fatalf("read request: %v", err)
	}
	if err := WriteMessage(hostToPluginW, map[string]any{"type": "future-message"}); err != nil {
		t.Fatalf("write unknown message: %v", err)
	}
	if err := WriteMessage(hostToPluginW, HTTPResponseMsg{
		Type:      MsgTypeHTTPResponse,
		RequestID: req.RequestID,
		Status:    200,
		Body:      "ok",
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestCommandClientStdinHandlerDoesNotBlockReplies(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	defer hostToPluginR.Close()
	defer hostToPluginW.Close()
	defer pluginToHostR.Close()
	defer pluginToHostW.Close()

	client := NewCommandClient(hostToPluginR, pluginToHostW)
	blockHandler := make(chan struct{})
	client.StdinDataHandler = func(data []byte) {
		<-blockHandler
	}
	defer close(blockHandler)

	errCh := make(chan error, 1)
	go func() {
		resp, err := client.Do(&HTTPRequestMsg{URI: "https://api.example.com/items"})
		if err != nil {
			errCh <- err
			return
		}
		if resp.Status != 200 {
			errCh <- fmt.Errorf("status = %d, want 200", resp.Status)
			return
		}
		errCh <- nil
	}()

	var req HTTPRequestMsg
	if err := NewDecoder(pluginToHostR).ReadMessage(&req); err != nil {
		t.Fatalf("read request: %v", err)
	}
	if err := WriteMessage(hostToPluginW, StdinDataMsg{Type: MsgTypeStdinData, Data: []byte("blocked")}); err != nil {
		t.Fatalf("write stdin data: %v", err)
	}
	if err := WriteMessage(hostToPluginW, HTTPResponseMsg{
		Type:      MsgTypeHTTPResponse,
		RequestID: req.RequestID,
		Status:    200,
		Body:      "ok",
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response while stdin handler was blocked")
	}
}

func TestCommandClientDuplicateHTTPResponseDoesNotStopReadLoop(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	defer hostToPluginR.Close()
	defer hostToPluginW.Close()
	defer pluginToHostR.Close()
	defer pluginToHostW.Close()

	client := NewCommandClient(hostToPluginR, pluginToHostW)
	errCh := make(chan error, 1)
	go func() {
		resp, err := client.Do(&HTTPRequestMsg{URI: "https://api.example.com/items"})
		if err != nil {
			errCh <- err
			return
		}
		if resp.Status != 200 {
			errCh <- fmt.Errorf("status = %d, want 200", resp.Status)
			return
		}
		errCh <- nil
	}()

	dec := NewDecoder(pluginToHostR)
	var req HTTPRequestMsg
	if err := dec.ReadMessage(&req); err != nil {
		t.Fatalf("read request: %v", err)
	}
	reply := HTTPResponseMsg{Type: MsgTypeHTTPResponse, RequestID: req.RequestID, Status: 200, Body: "ok"}
	if err := WriteMessage(hostToPluginW, reply); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Do: %v", err)
	}
	if err := WriteMessage(hostToPluginW, reply); err != nil {
		t.Fatalf("write duplicate response: %v", err)
	}

	listErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		resp, err := client.ListAPIsContext(ctx)
		if err != nil {
			listErr <- err
			return
		}
		if len(resp.APIs) != 1 || resp.APIs[0] != "demo" {
			listErr <- fmt.Errorf("APIs = %v, want [demo]", resp.APIs)
			return
		}
		listErr <- nil
	}()
	var listReq ListAPIsMsg
	if err := dec.ReadMessage(&listReq); err != nil {
		t.Fatalf("read list request: %v", err)
	}
	if err := WriteMessage(hostToPluginW, ListAPIsResponseMsg{Type: MsgTypeListAPIsResponse, RequestID: listReq.RequestID, APIs: []string{"demo"}}); err != nil {
		t.Fatalf("write list response: %v", err)
	}
	if err := <-listErr; err != nil {
		t.Fatalf("ListAPIs: %v", err)
	}
}

func TestCommandClientFetchAPISpecContextTimesOut(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	defer hostToPluginR.Close()
	defer hostToPluginW.Close()
	defer pluginToHostR.Close()
	defer pluginToHostW.Close()

	client := NewCommandClient(hostToPluginR, pluginToHostW)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := client.FetchAPISpecContext(ctx, "example", "staging")
		errCh <- err
	}()

	var req APISpecMsg
	if err := NewDecoder(pluginToHostR).ReadMessage(&req); err != nil {
		t.Fatalf("read API spec request: %v", err)
	}
	if req.Name != "example" || req.Profile != "staging" {
		t.Fatalf("request = %#v", req)
	}
	if req.RequestID == "" {
		t.Fatalf("request_id was not set: %#v", req)
	}

	err := <-errCh
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out or was canceled") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, loaded := client.replies.Load(replyRequestKey(MsgTypeAPISpecResponse, req.RequestID)); loaded {
		t.Fatal("timed-out API spec request was not removed from pending replies")
	}
}

func TestCommandClientFetchAPISpecConcurrentSameAPIUsesRequestID(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	defer hostToPluginR.Close()
	defer hostToPluginW.Close()
	defer pluginToHostR.Close()
	defer pluginToHostW.Close()

	client := NewCommandClient(hostToPluginR, pluginToHostW)
	hostErr := make(chan error, 1)
	go func() {
		dec := NewDecoder(pluginToHostR)
		var reqs []APISpecMsg
		for len(reqs) < 2 {
			var req APISpecMsg
			if err := dec.ReadMessage(&req); err != nil {
				hostErr <- err
				return
			}
			if req.Type != MsgTypeAPISpec || req.Name != "example" || req.Profile != "staging" || req.RequestID == "" {
				hostErr <- fmt.Errorf("unexpected API spec request: %#v", req)
				return
			}
			reqs = append(reqs, req)
		}
		if reqs[0].RequestID == reqs[1].RequestID {
			hostErr <- fmt.Errorf("duplicate request_id %q", reqs[0].RequestID)
			return
		}
		for i := len(reqs) - 1; i >= 0; i-- {
			req := reqs[i]
			if err := WriteMessage(hostToPluginW, APISpecResponseMsg{
				Type:      MsgTypeAPISpecResponse,
				RequestID: req.RequestID,
				Name:      req.Name,
				Profile:   req.Profile,
				Raw:       []byte(req.RequestID),
			}); err != nil {
				hostErr <- err
				return
			}
		}
		hostErr <- nil
	}()

	var wg sync.WaitGroup
	results := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.FetchAPISpecContext(context.Background(), "example", "staging")
			if err != nil {
				t.Errorf("FetchAPISpecContext: %v", err)
				return
			}
			results <- string(resp.Raw)
		}()
	}
	wg.Wait()
	close(results)
	if err := <-hostErr; err != nil {
		t.Fatalf("host: %v", err)
	}

	seen := map[string]bool{}
	for result := range results {
		seen[result] = true
	}
	if len(seen) != 2 {
		t.Fatalf("expected two distinct request_id responses, got %#v", seen)
	}
}

func TestCommandClientFetchAPISpecIgnoresLateTimedOutReply(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	defer hostToPluginR.Close()
	defer hostToPluginW.Close()
	defer pluginToHostR.Close()
	defer pluginToHostW.Close()

	client := NewCommandClient(hostToPluginR, pluginToHostW)
	dec := NewDecoder(pluginToHostR)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	firstErr := make(chan error, 1)
	go func() {
		_, err := client.FetchAPISpecContext(ctx, "example", "staging")
		firstErr <- err
	}()
	var firstReq APISpecMsg
	if err := dec.ReadMessage(&firstReq); err != nil {
		t.Fatalf("read first API spec request: %v", err)
	}
	cancel()
	if err := <-firstErr; err == nil {
		t.Fatal("expected first API spec request to time out")
	}

	secondErr := make(chan error, 1)
	go func() {
		resp, err := client.FetchAPISpecContext(context.Background(), "example", "staging")
		if err != nil {
			secondErr <- err
			return
		}
		if string(resp.Raw) != "second" {
			secondErr <- fmt.Errorf("Raw = %q, want second", resp.Raw)
			return
		}
		secondErr <- nil
	}()
	var secondReq APISpecMsg
	if err := dec.ReadMessage(&secondReq); err != nil {
		t.Fatalf("read second API spec request: %v", err)
	}
	if firstReq.RequestID == secondReq.RequestID {
		t.Fatalf("request_id reused: %q", firstReq.RequestID)
	}
	if err := WriteMessage(hostToPluginW, APISpecResponseMsg{
		Type:      MsgTypeAPISpecResponse,
		RequestID: firstReq.RequestID,
		Name:      "example",
		Profile:   "staging",
		Raw:       []byte("late"),
	}); err != nil {
		t.Fatalf("write late response: %v", err)
	}
	if err := WriteMessage(hostToPluginW, APISpecResponseMsg{
		Type:      MsgTypeAPISpecResponse,
		RequestID: secondReq.RequestID,
		Name:      "example",
		Profile:   "staging",
		Raw:       []byte("second"),
	}); err != nil {
		t.Fatalf("write second response: %v", err)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second FetchAPISpecContext: %v", err)
	}
}

func TestCommandClientHelpersRouteConcurrentRepliesByRequestID(t *testing.T) {
	hostToPluginR, hostToPluginW := io.Pipe()
	pluginToHostR, pluginToHostW := io.Pipe()
	defer hostToPluginR.Close()
	defer hostToPluginW.Close()
	defer pluginToHostR.Close()
	defer pluginToHostW.Close()

	client := NewCommandClient(hostToPluginR, pluginToHostW)
	hostErr := make(chan error, 1)
	go func() {
		dec := NewDecoder(pluginToHostR)
		var prompts []PromptMsg
		for len(prompts) < 2 {
			msgRaw, err := dec.ReadRaw()
			if err != nil {
				hostErr <- err
				return
			}
			if MessageType(msgRaw) != MsgTypePrompt {
				hostErr <- nil
				return
			}
			var msg PromptMsg
			if err := DecMode.Unmarshal(msgRaw, &msg); err != nil {
				hostErr <- err
				return
			}
			if msg.RequestID == "" {
				hostErr <- nil
				return
			}
			prompts = append(prompts, msg)
		}
		for i := len(prompts) - 1; i >= 0; i-- {
			msg := prompts[i]
			if err := WriteMessage(hostToPluginW, PromptResponseMsg{
				Type:      MsgTypePromptResponse,
				RequestID: msg.RequestID,
				Value:     msg.Message + "-reply",
			}); err != nil {
				hostErr <- err
				return
			}
		}
		hostErr <- nil
	}()

	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, prompt := range []string{"first", "second"} {
		wg.Add(1)
		go func(prompt string) {
			defer wg.Done()
			resp, err := client.PromptContext(context.Background(), prompt, false)
			if err != nil {
				t.Errorf("PromptContext(%s): %v", prompt, err)
				return
			}
			results <- resp.Value
		}(prompt)
	}
	wg.Wait()
	close(results)
	if err := <-hostErr; err != nil {
		t.Fatalf("host: %v", err)
	}

	seen := map[string]bool{}
	for result := range results {
		seen[result] = true
	}
	for _, want := range []string{"first-reply", "second-reply"} {
		if !seen[want] {
			t.Fatalf("missing %q in %#v", want, seen)
		}
	}
}

func TestCommandClientHelpersWriteExpectedMessages(t *testing.T) {
	tests := []struct {
		name     string
		call     func(*CommandClient) error
		wantType string
	}{
		{
			name: "list apis",
			call: func(c *CommandClient) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := c.ListAPIsContext(ctx)
				return err
			},
			wantType: MsgTypeListAPIs,
		},
		{
			name: "list profiles",
			call: func(c *CommandClient) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := c.ListProfilesContext(ctx, "api")
				return err
			},
			wantType: MsgTypeListProfiles,
		},
		{
			name: "config read",
			call: func(c *CommandClient) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := c.ConfigReadContext(ctx, "api", "default", "plug")
				return err
			},
			wantType: MsgTypeConfigRead,
		},
		{
			name: "confirm",
			call: func(c *CommandClient) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := c.ConfirmContext(ctx, "continue?")
				return err
			},
			wantType: MsgTypeConfirm,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostToPluginR, hostToPluginW := io.Pipe()
			pluginToHostR, pluginToHostW := io.Pipe()
			defer hostToPluginR.Close()
			defer hostToPluginW.Close()
			defer pluginToHostR.Close()
			defer pluginToHostW.Close()

			client := NewCommandClient(hostToPluginR, pluginToHostW)
			errCh := make(chan error, 1)
			go func() { errCh <- tt.call(client) }()

			raw, err := NewDecoder(pluginToHostR).ReadRaw()
			if err != nil {
				t.Fatalf("read helper message: %v", err)
			}
			if got := MessageType(raw); got != tt.wantType {
				t.Fatalf("message type = %q, want %q", got, tt.wantType)
			}
			if err := <-errCh; err == nil || !strings.Contains(err.Error(), "timed out or was canceled") {
				t.Fatalf("helper error = %v, want cancellation", err)
			}
		})
	}

	var out bytes.Buffer
	client := NewCommandClient(bytes.NewReader(nil), &out)
	if err := client.Response(201, map[string][]string{"X-Test": []string{"yes"}}, map[string]any{"ok": true}); err != nil {
		t.Fatalf("Response: %v", err)
	}
	var msg ResponseMsg
	if err := NewDecoder(bytes.NewReader(out.Bytes())).ReadMessage(&msg); err != nil {
		t.Fatalf("read response message: %v", err)
	}
	if msg.Type != MsgTypeResponse || msg.Status != 201 {
		t.Fatalf("response message = %#v", msg)
	}
}

func TestCommandPluginDocsJSONExamplesDecode(t *testing.T) {
	data, err := os.ReadFile("../site/content/en/docs/plugins/command-plugins.md")
	if err != nil {
		t.Fatalf("read docs: %v", err)
	}
	re := regexp.MustCompile("(?s)```json\\n(.*?)\\n```")
	matches := re.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		t.Fatal("expected JSON examples in command plugin docs")
	}
	for _, match := range matches {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(match[1], &envelope); err != nil {
			t.Fatalf("decode envelope %s: %v", match[1], err)
		}
		switch envelope.Type {
		case MsgTypeHTTPRequest:
			var msg HTTPRequestMsg
			if err := json.Unmarshal(match[1], &msg); err != nil {
				t.Fatalf("decode HTTPRequestMsg %s: %v", match[1], err)
			}
			if msg.Method == "" || msg.URI == "" {
				t.Fatalf("incomplete HTTP request example: %#v", msg)
			}
		case MsgTypeAPISpec:
			var msg APISpecMsg
			if err := json.Unmarshal(match[1], &msg); err != nil {
				t.Fatalf("decode APISpecMsg %s: %v", match[1], err)
			}
			if msg.Name == "" || msg.Profile == "" {
				t.Fatalf("incomplete API spec example: %#v", msg)
			}
		default:
			t.Fatalf("unexpected command plugin docs message type %q", envelope.Type)
		}
	}
}
