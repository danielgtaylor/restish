package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rest-sh/restish/v2/internal/content"
	"github.com/rest-sh/restish/v2/internal/output"
)

func TestRawDownloadReportsKnownLengthWithoutChangingBody(t *testing.T) {
	body := []byte("download body")
	formatter := &recordingProgressFormatter{}
	cli, stderr := downloadProgressTestCLI(formatter, true)
	resp := &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}

	progress := cli.newDownloadProgress(t.Context(), resp)
	raw, err := cli.rawResponseBodyBytes(resp, 1024, progress)
	progress.finish(err)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, body) {
		t.Fatalf("raw body = %q", raw)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if formatter.starts != 1 || len(formatter.stream.values) != 2 || !formatter.stream.closed {
		t.Fatalf("formatter starts=%d values=%d closed=%t", formatter.starts, len(formatter.stream.values), formatter.stream.closed)
	}
	initial := formatter.stream.values[0].(map[string]any)
	final := formatter.stream.values[1].(map[string]any)
	if initial["current"] != int64(0) || initial["total"] != int64(len(body)) || initial["unit"] != "bytes" || initial["state"] != "running" {
		t.Fatalf("initial record = %#v", initial)
	}
	if final["current"] != int64(len(body)) || final["total"] != int64(len(body)) || final["state"] != "success" {
		t.Fatalf("final record = %#v", final)
	}
}

func TestRawDownloadCountsCompressedWireBytes(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(bytes.Repeat([]byte("content"), 100)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	formatter := &recordingProgressFormatter{}
	cli, _ := downloadProgressTestCLI(formatter, true)
	resp := &http.Response{
		StatusCode:    200,
		ContentLength: int64(compressed.Len()),
		Body:          io.NopCloser(bytes.NewReader(compressed.Bytes())),
		Header:        http.Header{"Content-Encoding": {"gzip"}},
	}

	progress := cli.newDownloadProgress(t.Context(), resp)
	raw, err := cli.rawResponseBodyBytes(resp, 2048, progress)
	progress.finish(err)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 700 {
		t.Fatalf("decompressed length = %d", len(raw))
	}
	final := formatter.stream.values[len(formatter.stream.values)-1].(map[string]any)
	if final["current"] != int64(compressed.Len()) || final["total"] != int64(compressed.Len()) {
		t.Fatalf("final record = %#v, compressed bytes = %d", final, compressed.Len())
	}
}

func TestRawDownloadReportsUnknownLength(t *testing.T) {
	formatter := &recordingProgressFormatter{}
	cli, _ := downloadProgressTestCLI(formatter, true)
	resp := &http.Response{StatusCode: 200, ContentLength: -1, Body: io.NopCloser(bytes.NewReader([]byte("body"))), Header: http.Header{}}

	progress := cli.newDownloadProgress(t.Context(), resp)
	_, err := cli.rawResponseBodyBytes(resp, 1024, progress)
	progress.finish(err)
	if err != nil {
		t.Fatal(err)
	}
	final := formatter.stream.values[len(formatter.stream.values)-1].(map[string]any)
	if _, ok := final["total"]; ok || final["message"] != "4 bytes" || final["state"] != "success" {
		t.Fatalf("final record = %#v", final)
	}
}

func TestRawDownloadThrottlesUpdates(t *testing.T) {
	formatter := &recordingProgressFormatter{}
	cli, _ := downloadProgressTestCLI(formatter, true)
	resp := &http.Response{StatusCode: 200, ContentLength: 4, Body: io.NopCloser(bytes.NewReader(nil)), Header: http.Header{}}
	progress := cli.newDownloadProgress(t.Context(), resp)
	now := time.Unix(0, 0)
	progress.now = func() time.Time { return now }
	progress.last = now

	progress.observe(1)
	now = now.Add(50 * time.Millisecond)
	progress.observe(1)
	now = now.Add(50 * time.Millisecond)
	progress.observe(1)
	progress.observe(1)
	progress.finish(nil)

	if len(formatter.stream.values) != 3 {
		t.Fatalf("records = %d, want initial, throttled update, final", len(formatter.stream.values))
	}
	update := formatter.stream.values[1].(map[string]any)
	if update["current"] != int64(3) || update["state"] != "running" {
		t.Fatalf("update = %#v", update)
	}
}

func TestRawDownloadFailureClosesProgress(t *testing.T) {
	formatter := &recordingProgressFormatter{}
	cli, _ := downloadProgressTestCLI(formatter, true)
	resp := &http.Response{StatusCode: 200, ContentLength: 4, Body: io.NopCloser(io.MultiReader(bytes.NewReader([]byte("body")), errorReader{})), Header: http.Header{}}

	progress := cli.newDownloadProgress(t.Context(), resp)
	_, err := cli.rawResponseBodyBytes(resp, 1024, progress)
	progress.finish(err)
	if err == nil {
		t.Fatal("rawResponseBodyBytes returned nil error")
	}
	final := formatter.stream.values[len(formatter.stream.values)-1].(map[string]any)
	if final["state"] != "failed" || !formatter.stream.closed {
		t.Fatalf("final record = %#v, closed=%t", final, formatter.stream.closed)
	}
}

func TestRawDownloadProgressRequiresTTYAndFormatter(t *testing.T) {
	formatter := &recordingProgressFormatter{}
	cli, _ := downloadProgressTestCLI(formatter, false)
	resp := &http.Response{StatusCode: 200, ContentLength: 4, Body: io.NopCloser(bytes.NewReader([]byte("body"))), Header: http.Header{}}
	if progress := cli.newDownloadProgress(t.Context(), resp); progress != nil || formatter.starts != 0 {
		t.Fatalf("progress = %#v, formatter starts = %d", progress, formatter.starts)
	}

	cli.formatters = output.DefaultFormatters()
	cli.hooks.StderrIsTerminal = func(io.Writer) bool { return true }
	if progress := cli.newDownloadProgress(t.Context(), resp); progress != nil {
		t.Fatalf("progress without formatter = %#v", progress)
	}
}

func TestRawDownloadFormatterFailureIsAdvisory(t *testing.T) {
	formatter := &recordingProgressFormatter{startErr: errors.New("boom")}
	cli, stderr := downloadProgressTestCLI(formatter, true)
	resp := &http.Response{StatusCode: 200, ContentLength: 4, Body: io.NopCloser(bytes.NewReader([]byte("body"))), Header: http.Header{}}
	if progress := cli.newDownloadProgress(t.Context(), resp); progress != nil {
		t.Fatalf("progress = %#v", progress)
	}
	if got := stderr.String(); got == "" || !bytes.Contains(stderr.Bytes(), []byte("download progress unavailable")) {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRawDownloadCancellationSuppressesFormatterWarning(t *testing.T) {
	formatter := &recordingProgressFormatter{}
	formatter.stream.closeErr = errors.New("killed")
	cli, stderr := downloadProgressTestCLI(formatter, true)
	resp := &http.Response{StatusCode: 200, ContentLength: 4, Body: io.NopCloser(bytes.NewReader([]byte("body"))), Header: http.Header{}}
	ctx, cancel := context.WithCancel(context.Background())
	progress := cli.newDownloadProgress(ctx, resp)
	cancel()
	progress.finish(context.Canceled)
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func downloadProgressTestCLI(formatter output.Formatter, tty bool) (*CLI, *bytes.Buffer) {
	stderr := &bytes.Buffer{}
	return &CLI{
		Stderr:     stderr,
		content:    content.Default(),
		formatters: map[string]output.Formatter{"progress": formatter},
		hooks:      testHooks{StderrIsTerminal: func(io.Writer) bool { return tty }},
	}, stderr
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
