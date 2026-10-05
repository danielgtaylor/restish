//go:build integration

package cli_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/cli"
)

type captureWriter struct {
	mu sync.Mutex
	b  strings.Builder
}

type unreadableReader struct{}

func (unreadableReader) Read([]byte) (int, error) {
	return 0, errors.New("stdin should not be read")
}

func (w *captureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *captureWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func installCmdPlugin(t *testing.T) {
	t.Helper()
	skipNoCmdPlugin(t)

	pluginsParent, _ := installSharedPlugin(t, "cmd", testCmdPluginBin, "restish-cmdplugin")
	if err := os.WriteFile(filepath.Join(pluginsParent, "restish.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write plugin test config: %v", err)
	}
	t.Setenv("RSH_CONFIG_DIR", pluginsParent)
	t.Setenv("PATH", "")
}

func TestCommandPluginHelp(t *testing.T) {
	installCmdPlugin(t)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	_ = c.Run([]string{"restish", "--help"})

	if !strings.Contains(out.String(), "greet") {
		t.Errorf("expected 'greet' in help output, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "pipe") {
		t.Errorf("expected 'pipe' in help output, got:\n%s", out.String())
	}
}

func TestCommandPluginGreet(t *testing.T) {
	installCmdPlugin(t)

	c, out, _ := newTestCLI(t)
	var errOut captureWriter
	c.Stderr = &errOut
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	if err := c.Run([]string{"restish", "greet"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(errOut.String(), "Greeting in progress") {
		t.Errorf("expected progress on stderr, got:\n%s", errOut.String())
	}
	if !strings.Contains(out.String(), "Hello from plugin") {
		t.Errorf("expected greeting in stdout, got:\n%s", out.String())
	}
}

func TestCommandSurfacePreservesCommandPlugins(t *testing.T) {
	installCmdPlugin(t)

	c, out, _ := newTestCLI(t)
	var errOut captureWriter
	c.Stderr = &errOut
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	writeCommandSurfaceSpecConfig(t, c, "api", "getPing")
	c.SetCommandSurface(cli.CommandSurface{
		PromotedAPI: "api",
	})

	if err := c.Run([]string{"example", "greet"}); err != nil {
		t.Fatalf("promoted command plugin: %v", err)
	}
	if !strings.Contains(errOut.String(), "Greeting in progress") {
		t.Errorf("expected progress on stderr, got:\n%s", errOut.String())
	}
	if !strings.Contains(out.String(), "Hello from plugin") {
		t.Errorf("expected greeting in stdout, got:\n%s", out.String())
	}
}

func TestCommandSurfaceListsAllCommandPlugins(t *testing.T) {
	installCmdPlugin(t)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	writeCommandSurfaceSpecConfig(t, c, "api", "getPing")
	c.SetCommandSurface(cli.CommandSurface{
		PromotedAPI: "api",
	})

	if err := c.Run([]string{"example", "--help"}); err != nil {
		t.Fatalf("promoted help: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "greet") {
		t.Fatalf("selected command plugin missing from help:\n%s", got)
	}
	if !strings.Contains(got, "pipe") {
		t.Fatalf("command plugin missing from help:\n%s", got)
	}
}

func TestCommandSurfaceCommandPluginDoesNotRequirePromotedMetadata(t *testing.T) {
	installCmdPlugin(t)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	c.SetDefaultConfig(&config.Config{APIs: map[string]*config.APIConfig{
		"api": {BaseURL: "http://127.0.0.1:1"},
	}})
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})

	if err := c.Run([]string{"example", "greet", "--help"}); err != nil {
		t.Fatalf("command plugin help without promoted metadata: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "greet") {
		t.Fatalf("command plugin help missing command:\n%s", got)
	}
}

func TestCommandSurfaceCommandPluginOperationCollisionErrors(t *testing.T) {
	installCmdPlugin(t)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	writeCommandSurfaceSpecConfig(t, c, "api", "greet")
	c.SetCommandSurface(cli.CommandSurface{PromotedAPI: "api"})

	err := c.Run([]string{"example", "--help"})
	if err == nil || !strings.Contains(err.Error(), "command plugin") {
		t.Fatalf("collision error = %v, want command plugin collision", err)
	}
}

func TestCommandPluginFetch(t *testing.T) {
	installCmdPlugin(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"fetched":true}`)
	}))
	t.Cleanup(srv.Close)

	c, out, _ := newTestCLI(t)
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	if err := c.Run([]string{"restish", "fetch", srv.URL}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "fetched") {
		t.Errorf("expected fetched output, got:\n%s", out.String())
	}
}

func TestCommandPluginProgress(t *testing.T) {
	installCmdPlugin(t)

	c, _, _ := newTestCLI(t)
	var errOut captureWriter
	c.Stderr = &errOut
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	_ = c.Run([]string{"restish", "greet"})
	if !strings.Contains(errOut.String(), "Greeting in progress") {
		t.Errorf("expected progress on stderr, got:\n%s", errOut.String())
	}
}

func TestCommandPluginExitCode(t *testing.T) {
	installCmdPlugin(t)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	if err := c.Run([]string{"restish", "fail"}); err == nil {
		t.Fatal("expected error for exit_code=1, got nil")
	}
}

func TestCommandPluginDeath(t *testing.T) {
	installCmdPlugin(t)

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	err := c.Run([]string{"restish", "die"})
	if err == nil {
		t.Fatal("expected error for plugin crash, got nil")
	}
	if !strings.Contains(err.Error(), "died") && !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "truncated") {
		t.Errorf("expected process death error, got: %v", err)
	}
}

func TestCommandPluginPassthroughStdio(t *testing.T) {
	installCmdPlugin(t)

	c, out, _ := newTestCLI(t)
	var errOut captureWriter
	c.Stderr = &errOut
	c.Stdin = strings.NewReader("hello\n")
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	if err := c.Run([]string{"restish", "pipe"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := out.String(); got != "OUT:hello\n" {
		t.Fatalf("unexpected stdout passthrough: %q", got)
	}
	if got := errOut.String(); got != "ERR:hello\n" {
		t.Fatalf("unexpected stderr passthrough: %q", got)
	}
}

func TestCommandPluginDoneHangKilledAfterGrace(t *testing.T) {
	installCmdPlugin(t)
	t.Setenv("RSH_COMMAND_PLUGIN_SHUTDOWN_GRACE", "100ms")

	c, _, _ := newTestCLI(t)
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)

	start := time.Now()
	if err := c.Run([]string{"restish", "hangdone"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("expected hung plugin to be killed promptly, took %v", elapsed)
	}
}

func TestCommandPluginWithoutPassthroughDoesNotConsumeStdin(t *testing.T) {
	installCmdPlugin(t)

	c, _, _ := newTestCLI(t)
	c.Stdin = unreadableReader{}
	c.Hooks().ConfigPath = sharedPluginConfigPath(t)
	if err := c.Run([]string{"restish", "greet"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
