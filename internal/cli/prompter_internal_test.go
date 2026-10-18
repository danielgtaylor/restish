package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptSourceClosesOpenedTTY(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tty")
	if err := os.WriteFile(path, []byte("y\n"), 0o600); err != nil {
		t.Fatalf("write tty fixture: %v", err)
	}

	oldOpenTTY := promptOpenTTY
	t.Cleanup(func() {
		promptOpenTTY = oldOpenTTY
	})
	promptOpenTTY = func() (*os.File, error) {
		return os.Open(path)
	}

	c := &CLI{
		Stdin:  io.NopCloser(strings.NewReader("")),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
	src, cleanup := c.promptSource()
	f, ok := src.(*os.File)
	if !ok {
		t.Fatalf("promptSource returned %T, want *os.File", src)
	}
	cleanup()
	if _, err := f.Stat(); err == nil {
		t.Fatal("expected cleanup to close opened prompt file")
	}
}

func TestVisiblePromptHookDoesNotEnableSecretPrompt(t *testing.T) {
	oldOpenTTY := promptOpenTTY
	t.Cleanup(func() { promptOpenTTY = oldOpenTTY })
	promptOpenTTY = func() (*os.File, error) { return nil, os.ErrNotExist }

	c := &CLI{Stdin: io.NopCloser(strings.NewReader(""))}
	c.hooks.PromptFunc = func(context.Context, string) (string, error) { return "", nil }
	if c.canPromptSecretInteractively() {
		t.Fatal("visible prompt hook unexpectedly enabled secret prompting")
	}
}
