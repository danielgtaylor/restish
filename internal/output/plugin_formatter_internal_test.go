package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	pluginwire "github.com/rest-sh/restish/v2/plugin"
)

type fakeFormatterStream struct {
	sendErr  error
	closeErr error
	closed   bool
}

type fakeFormatterOutputStream struct {
	outputs   chan pluginwire.FormatterOutput
	done      chan struct{}
	closeOnce sync.Once
	respond   func(pluginwire.FormatterRequest) []pluginwire.FormatterOutput
	sent      []pluginwire.FormatterRequest
	closed    bool
}

func (s *fakeFormatterOutputStream) SendWithTimeout(value any, _ time.Duration) error {
	req, ok := value.(pluginwire.FormatterRequest)
	if !ok {
		return errors.New("unexpected request type")
	}
	s.sent = append(s.sent, req)
	for _, output := range s.respond(req) {
		s.outputs <- output
	}
	return nil
}

func (s *fakeFormatterOutputStream) Receive(out *pluginwire.FormatterOutput) error {
	select {
	case output := <-s.outputs:
		*out = output
		return nil
	case <-s.done:
		return io.EOF
	}
}

func (s *fakeFormatterOutputStream) Abort() error {
	return s.Close()
}

func (s *fakeFormatterOutputStream) Close() error {
	s.closeOnce.Do(func() {
		s.closed = true
		close(s.done)
	})
	return nil
}

func (s *fakeFormatterStream) Send(any) error {
	return s.sendErr
}

func (s *fakeFormatterStream) Close() error {
	s.closed = true
	return s.closeErr
}

func TestPluginFormatterStreamCloseClosesAfterFinalSendFailure(t *testing.T) {
	sendErr := errors.New("send failed")
	closeErr := errors.New("close failed")
	fake := &fakeFormatterStream{sendErr: sendErr, closeErr: closeErr}
	stream := &pluginFormatterStream{formatName: "csv", stream: fake}

	err := stream.Close()
	if err == nil {
		t.Fatal("expected close error")
	}
	if !fake.closed {
		t.Fatal("underlying stream was not closed after send failure")
	}
	if !errors.Is(err, sendErr) {
		t.Fatalf("expected joined send error, got %v", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("expected joined close error, got %v", err)
	}
	if !strings.Contains(err.Error(), "formatter plugin csv") {
		t.Fatalf("expected formatter context, got %v", err)
	}
}

func TestPluginFormatterFormatJoinsFinalSendAndCloseErrors(t *testing.T) {
	sendErr := errors.New("send failed")
	closeErr := errors.New("close failed")
	fake := &fakeFormatterStream{sendErr: sendErr, closeErr: closeErr}
	oldStart := startPluginFormatterStream
	startPluginFormatterStream = func(ctx context.Context, path string, w io.Writer, in any) (formatterStream, error) {
		return fake, nil
	}
	t.Cleanup(func() { startPluginFormatterStream = oldStart })

	err := (&PluginFormatter{FormatName: "csv"}).Format(&bytes.Buffer{}, &Response{}, false)
	if err == nil {
		t.Fatal("expected format error")
	}
	if !fake.closed {
		t.Fatal("underlying stream was not closed")
	}
	if !errors.Is(err, sendErr) || !errors.Is(err, closeErr) {
		t.Fatalf("expected joined send and close errors, got %v", err)
	}
}

func TestPluginFormatterFormatValueJoinsItemSendAndCloseErrors(t *testing.T) {
	sendErr := errors.New("send failed")
	closeErr := errors.New("close failed")
	fake := &fakeFormatterStream{sendErr: sendErr, closeErr: closeErr}
	oldStart := startPluginFormatterStream
	startPluginFormatterStream = func(ctx context.Context, path string, w io.Writer, in any) (formatterStream, error) {
		return fake, nil
	}
	t.Cleanup(func() { startPluginFormatterStream = oldStart })

	err := (&PluginFormatter{FormatName: "csv"}).FormatValue(&bytes.Buffer{}, map[string]any{"id": 1}, false)
	if err == nil {
		t.Fatal("expected format value error")
	}
	if !fake.closed {
		t.Fatal("underlying stream was not closed")
	}
	if !errors.Is(err, sendErr) || !errors.Is(err, closeErr) {
		t.Fatalf("expected joined send and close errors, got %v", err)
	}
}

func TestPluginFormatterStreamFallsBackToAutoFormatter(t *testing.T) {
	fake := &fakeFormatterOutputStream{
		outputs: make(chan pluginwire.FormatterOutput),
		done:    make(chan struct{}),
		respond: func(req pluginwire.FormatterRequest) []pluginwire.FormatterOutput {
			if req.Event == "item" {
				return []pluginwire.FormatterOutput{
					{Type: pluginwire.MsgTypeFormatterOutput, Data: []byte("plugin output\n")},
					{Type: pluginwire.MsgTypeFormatterOutput, RequestID: req.RequestID, Fallback: true, Complete: true},
				}
			}
			return []pluginwire.FormatterOutput{{Type: pluginwire.MsgTypeFormatterOutput, RequestID: req.RequestID, Complete: true}}
		},
	}
	oldStart := startPluginFormatterOutputStream
	startPluginFormatterOutputStream = func(context.Context, string) (formatterOutputStream, error) {
		return fake, nil
	}
	t.Cleanup(func() { startPluginFormatterOutputStream = oldStart })

	var out bytes.Buffer
	formatter := &PluginFormatter{FormatName: "progress", HostFallback: true}
	stream, err := formatter.StartValueStream(&out, &Response{}, false)
	if err != nil {
		t.Fatalf("StartValueStream: %v", err)
	}
	if err := stream.WriteValue(map[string]any{"message": "cannot match command"}); err != nil {
		t.Fatalf("WriteValue: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := "plugin output\n{\n  \"message\": \"cannot match command\"\n}\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if !fake.closed || len(fake.sent) != 3 || fake.sent[0].RequestID != 1 || fake.sent[1].RequestID != 2 || fake.sent[2].RequestID != 3 {
		t.Fatalf("closed=%t requests=%#v", fake.closed, fake.sent)
	}
}

func TestPluginFormatterOutputTimeoutClosesSession(t *testing.T) {
	fake := &fakeFormatterOutputStream{
		outputs: make(chan pluginwire.FormatterOutput),
		done:    make(chan struct{}),
		respond: func(pluginwire.FormatterRequest) []pluginwire.FormatterOutput { return nil },
	}
	oldStart := startPluginFormatterOutputStream
	startPluginFormatterOutputStream = func(context.Context, string) (formatterOutputStream, error) {
		return fake, nil
	}
	t.Cleanup(func() { startPluginFormatterOutputStream = oldStart })

	err := (&PluginFormatter{FormatName: "progress", HostFallback: true, Timeout: time.Millisecond}).Format(&bytes.Buffer{}, &Response{}, false)
	if err == nil || !strings.Contains(err.Error(), "did not complete request") {
		t.Fatalf("error = %v", err)
	}
	if !fake.closed {
		t.Fatal("timed out formatter was not closed")
	}
}

func TestPluginFormatterRejectsDuplicateCompletion(t *testing.T) {
	fake := &fakeFormatterOutputStream{
		outputs: make(chan pluginwire.FormatterOutput),
		done:    make(chan struct{}),
		respond: func(req pluginwire.FormatterRequest) []pluginwire.FormatterOutput {
			result := pluginwire.FormatterOutput{Type: pluginwire.MsgTypeFormatterOutput, RequestID: req.RequestID, Complete: true}
			if req.Event == "end" {
				return []pluginwire.FormatterOutput{result, result}
			}
			return []pluginwire.FormatterOutput{result}
		},
	}
	oldStart := startPluginFormatterOutputStream
	startPluginFormatterOutputStream = func(context.Context, string) (formatterOutputStream, error) {
		return fake, nil
	}
	t.Cleanup(func() { startPluginFormatterOutputStream = oldStart })

	err := (&PluginFormatter{FormatName: "progress", HostFallback: true}).Format(&bytes.Buffer{}, &Response{}, false)
	if err == nil || !strings.Contains(err.Error(), "active request is 0") {
		t.Fatalf("error = %v", err)
	}
}
