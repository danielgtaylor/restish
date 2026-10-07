package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/rest-sh/restish/v2/internal/output"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
	"github.com/spf13/cobra"
)

type recordingProgressFormatter struct {
	startErr error
	starts   int
	color    bool
	stream   recordingProgressStream
}

func (f *recordingProgressFormatter) Format(io.Writer, *output.Response, bool) error {
	return nil
}

func (f *recordingProgressFormatter) StartValueStream(_ io.Writer, _ *output.Response, color bool) (output.ValueStream, error) {
	f.starts++
	f.color = color
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &f.stream, nil
}

type recordingProgressStream struct {
	values   []any
	closed   bool
	closeErr error
}

func (s *recordingProgressStream) WriteValue(value any) error {
	s.values = append(s.values, value)
	return nil
}

func (s *recordingProgressStream) Close() error {
	s.closed = true
	return s.closeErr
}

func TestStructuredCommandProgressUsesOneFormatterSession(t *testing.T) {
	var stderr bytes.Buffer
	formatter := &recordingProgressFormatter{}
	cli := &CLI{Stderr: &stderr, formatters: map[string]output.Formatter{"progress": formatter}}
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&stderr)
	renderer := cli.newCommandProgressRenderer(cmd)

	for current := int64(1); current <= 2; current++ {
		total := int64(2)
		msg := pluginwire.ProgressMsg{Type: pluginwire.MsgTypeProgress, Text: "fallback", ID: "workflow", State: "running", Current: &current, Total: &total}
		raw, err := cbor.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := cli.handleCommandPluginMessage(cmd, context.Background(), nil, nil, renderer, pluginwire.MsgTypeProgress, raw); err != nil {
			t.Fatalf("handleCommandPluginMessage: %v", err)
		}
	}
	if err := renderer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if formatter.starts != 1 || formatter.color || len(formatter.stream.values) != 2 || !formatter.stream.closed {
		t.Fatalf("formatter starts=%d color=%t values=%d closed=%t", formatter.starts, formatter.color, len(formatter.stream.values), formatter.stream.closed)
	}
	if stderr.Len() != 0 {
		t.Fatalf("fallback written despite formatter: %q", stderr.String())
	}
}

func TestStructuredCommandProgressFallsBackWithoutFormatter(t *testing.T) {
	var stderr bytes.Buffer
	cli := &CLI{Stderr: &stderr}
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&stderr)
	msg := pluginwire.ProgressMsg{Type: pluginwire.MsgTypeProgress, Text: "Running step 1 of 2", ID: "workflow"}
	raw, err := cbor.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := cli.handleCommandPluginMessage(cmd, context.Background(), nil, nil, cli.newCommandProgressRenderer(cmd), pluginwire.MsgTypeProgress, raw); err != nil {
		t.Fatalf("handleCommandPluginMessage: %v", err)
	}
	if got := stderr.String(); got != "Running step 1 of 2\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestInvalidStructuredCommandProgressFallsBack(t *testing.T) {
	var stderr bytes.Buffer
	formatter := &recordingProgressFormatter{}
	cli := &CLI{Stderr: &stderr, formatters: map[string]output.Formatter{"progress": formatter}}
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&stderr)
	renderer := cli.newCommandProgressRenderer(cmd)
	current := int64(1)
	msg := pluginwire.ProgressMsg{Type: pluginwire.MsgTypeProgress, Text: "fallback", ID: "workflow", Current: &current}
	raw, err := cbor.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := cli.handleCommandPluginMessage(cmd, context.Background(), nil, nil, renderer, pluginwire.MsgTypeProgress, raw); err != nil {
		t.Fatalf("handleCommandPluginMessage: %v", err)
	}
	if formatter.starts != 0 || stderr.String() != "fallback\n" {
		t.Fatalf("formatter starts=%d stderr=%q", formatter.starts, stderr.String())
	}
}

func TestStructuredCommandProgressFallsBackAfterFormatterFailure(t *testing.T) {
	var stderr bytes.Buffer
	formatter := &recordingProgressFormatter{startErr: errors.New("boom")}
	cli := &CLI{Stderr: &stderr, formatters: map[string]output.Formatter{"progress": formatter}}
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&stderr)
	renderer := cli.newCommandProgressRenderer(cmd)
	msg := pluginwire.ProgressMsg{Type: pluginwire.MsgTypeProgress, Text: "fallback", ID: "workflow"}
	raw, err := cbor.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for range 2 {
		if _, err := cli.handleCommandPluginMessage(cmd, context.Background(), nil, nil, renderer, pluginwire.MsgTypeProgress, raw); err != nil {
			t.Fatalf("handleCommandPluginMessage: %v", err)
		}
	}
	if formatter.starts != 1 {
		t.Fatalf("formatter starts = %d, want 1", formatter.starts)
	}
	if got := stderr.String(); strings.Count(got, "progress formatter unavailable") != 1 || strings.Count(got, "fallback\n") != 2 {
		t.Fatalf("stderr = %q", got)
	}
}

func TestStructuredCommandProgressReplaysFallbackAfterCloseFailure(t *testing.T) {
	var stderr bytes.Buffer
	formatter := &recordingProgressFormatter{}
	formatter.stream.closeErr = errors.New("boom")
	cli := &CLI{Stderr: &stderr, formatters: map[string]output.Formatter{"progress": formatter}}
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&stderr)
	renderer := cli.newCommandProgressRenderer(cmd)
	msg := pluginwire.ProgressMsg{Type: pluginwire.MsgTypeProgress, Text: "fallback", ID: "workflow"}
	raw, err := cbor.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := cli.handleCommandPluginMessage(cmd, context.Background(), nil, nil, renderer, pluginwire.MsgTypeProgress, raw); err != nil {
		t.Fatalf("handleCommandPluginMessage: %v", err)
	}
	if err := renderer.Close(); err == nil {
		t.Fatal("Close returned nil")
	}
	if got := stderr.String(); got != "fallback\n" {
		t.Fatalf("stderr = %q", got)
	}
}
