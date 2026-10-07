package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rest-sh/restish/v2/internal/output"
)

func TestProgressFormatterBoundsSubprocessStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	path := filepath.Join(t.TempDir(), "restish-progress")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := &CLI{formatters: map[string]output.Formatter{
		"progress": &output.PluginFormatter{PluginPath: path, FormatName: "progress"},
	}}
	session := cli.newProgressFormatterSession(context.Background(), 20*time.Millisecond)
	base := &output.Response{Headers: map[string][]string{"X-Large": {strings.Repeat("x", 2<<20)}}}

	start := time.Now()
	err := session.Write(io.Discard, base, false, map[string]any{"label": "Download"})
	if err == nil || !strings.Contains(err.Error(), "timed out after 20ms") {
		t.Fatalf("Write error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("blocked formatter returned after %v", elapsed)
	}
}
