package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pluginwire "github.com/rest-sh/restish/v2/plugin"
)

// writeScript writes an executable shell script (or .bat on Windows) to dir
// and returns its full path. On Unix the script is made executable via chmod.
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".bat"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("writeScript %s: %v", name, err)
	}
	return p
}

// jsonManifest returns a JSON-encoded Manifest for a plugin.
func jsonManifest(m Manifest) string {
	b, _ := json.Marshal(m)
	return string(b)
}

// ---- isExecutable ---------------------------------------------------------

func TestIsExecutable_Executable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping chmod-based test on Windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "restish-test")
	os.WriteFile(p, []byte("#!/bin/sh\necho hi"), 0o755)
	if !isExecutable(p) {
		t.Error("expected executable")
	}
}

func TestIsExecutable_NotExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping chmod-based test on Windows")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "not-exec")
	os.WriteFile(p, []byte("data"), 0o644)
	if isExecutable(p) {
		t.Error("expected non-executable")
	}
}

func TestIsExecutable_Directory(t *testing.T) {
	dir := t.TempDir()
	if isExecutable(dir) {
		t.Error("directory should not be reported as executable")
	}
}

func TestIsExecutable_Nonexistent(t *testing.T) {
	if isExecutable("/nonexistent/restish-plugin") {
		t.Error("nonexistent file should not be executable")
	}
}

// ---- DefaultPluginDir -----------------------------------------------------

func TestDefaultPluginDir_RSHConfigDir(t *testing.T) {
	t.Setenv("RSH_CONFIG_DIR", "/custom/config")
	dir := DefaultPluginDir()
	if dir != filepath.Join("/custom/config", "plugins") {
		t.Errorf("got %q, want %q", dir, "/custom/config/plugins")
	}
}

func TestDefaultPluginDir_HomeDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix home-dir test on Windows")
	}
	t.Setenv("RSH_CONFIG_DIR", "")
	dir := DefaultPluginDir()
	if dir == "" {
		t.Error("expected non-empty plugin dir")
	}
}

func TestDefaultPluginDir_XDGConfigHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix XDG test on Windows")
	}
	xdg := t.TempDir()
	t.Setenv("RSH_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := DefaultPluginDir()
	want := filepath.Join(xdg, "restish", "plugins")
	if dir != want {
		t.Fatalf("DefaultPluginDir() = %q, want %q", dir, want)
	}
}

// ---- LoadManifest ---------------------------------------------------------

func TestLoadManifest_JSONOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	m := Manifest{
		Name:              "myplugin",
		Version:           "1.0.0",
		Description:       "A test plugin",
		RestishAPIVersion: CurrentPluginAPIVersion,
		Hooks:             []string{"auth"},
	}
	script := fmt.Sprintf("#!/bin/sh\necho '%s'", jsonManifest(m))
	p := writeScript(t, dir, "restish-myplugin", script)

	got, err := LoadManifest(p, nil)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got.Name != "myplugin" {
		t.Errorf("Name: got %q, want %q", got.Name, "myplugin")
	}
	if got.RestishAPIVersion != CurrentPluginAPIVersion {
		t.Errorf("RestishAPIVersion: got %d, want %d", got.RestishAPIVersion, CurrentPluginAPIVersion)
	}
}

func TestLoadManifest_MissingName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	m := Manifest{RestishAPIVersion: 2} // no name
	script := fmt.Sprintf("#!/bin/sh\necho '%s'", jsonManifest(m))
	p := writeScript(t, dir, "restish-noname", script)

	_, err := LoadManifest(p, nil)
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestLoadManifest_MissingAPIVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	m := Manifest{Name: "test"} // no api version
	script := fmt.Sprintf("#!/bin/sh\necho '%s'", jsonManifest(m))
	p := writeScript(t, dir, "restish-nover", script)

	_, err := LoadManifest(p, nil)
	if err == nil {
		t.Error("expected error for missing restish_api_version")
	}
}

func TestLoadManifest_EmptyOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	p := writeScript(t, dir, "restish-empty", "#!/bin/sh\n# no output")

	_, err := LoadManifest(p, nil)
	if err == nil {
		t.Error("expected error for empty manifest output")
	}
}

func TestLoadManifest_OutputTooLarge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	p := writeScript(t, dir, "restish-huge-manifest", fmt.Sprintf("#!/bin/sh\ndd if=/dev/zero bs=%d count=1 2>/dev/null\n", maxManifestOutputBytes+1))

	_, err := LoadManifest(p, nil)
	if err == nil {
		t.Fatal("expected oversized manifest output to fail")
	}
	if !strings.Contains(err.Error(), "manifest output exceeded") {
		t.Fatalf("expected output cap error, got %v", err)
	}
}

func TestLoadManifest_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	p := writeScript(t, dir, "restish-fail", "#!/bin/sh\nexit 1")

	_, err := LoadManifest(p, nil)
	if err == nil {
		t.Error("expected error for non-zero exit")
	}
}

func TestLoadManifest_NonZeroExitIncludesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	p := writeScript(t, dir, "restish-fail-stderr", "#!/bin/sh\necho manifest exploded >&2\nexit 1")

	_, err := LoadManifest(p, nil)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "stderr: manifest exploded") {
		t.Fatalf("expected stderr excerpt, got %v", err)
	}
}

func TestLoadManifest_FutureRequiredVersionFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	m := Manifest{
		Name:              "future",
		RestishAPIVersion: CurrentPluginAPIVersion + 1,
	}
	script := fmt.Sprintf("#!/bin/sh\necho '%s'", jsonManifest(m))
	p := writeScript(t, dir, "restish-future", script)

	var warnBuf bytes.Buffer

	_, err := loadManifest(p, &warnBuf)
	if err == nil {
		t.Fatal("expected future minimum API version to fail")
	}
	if !strings.Contains(err.Error(), "requires restish_api_version") {
		t.Fatalf("expected minimum-version error, got %v", err)
	}
	if warnBuf.Len() != 0 {
		t.Fatalf("future required version should fail without warning, got %q", warnBuf.String())
	}
}

func TestLoadManifest_CompatibilityMatrix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name: "older minimum version",
			json: `{"name":"older","restish_api_version":1,"hooks":["auth"]}`,
		},
		{
			name: "current minimum version",
			json: `{"name":"current","restish_api_version":2,"hooks":["auth"]}`,
		},
		{
			name: "newer compatible optional field",
			json: `{"name":"newer-compatible","restish_api_version":2,"hooks":["auth"],"future_optional":true}`,
		},
		{
			name:    "unsupported required feature",
			json:    `{"name":"unsupported","restish_api_version":2,"hooks":["auth"],"required_features":["future.magic"]}`,
			wantErr: `unsupported feature "future.magic"`,
		},
		{
			name: "supported required feature",
			json: `{"name":"supported","restish_api_version":2,"hooks":["loader"],"loader_content_types":["application/x-test"],"required_features":["loader.source_metadata"]}`,
		},
		{
			name: "select required feature",
			json: `{"name":"select","restish_api_version":2,"hooks":["command"],"required_features":["command.select"]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeScript(t, dir, "restish-test", fmt.Sprintf("#!/bin/sh\necho '%s'", tt.json))
			_, err := LoadManifest(p, nil)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadManifest error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadManifest: %v", err)
			}
		})
	}
}

// ---- Discover -------------------------------------------------------------

func TestDiscover_FindsPluginsInDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	m := Manifest{
		Name:              "myplugin",
		RestishAPIVersion: CurrentPluginAPIVersion,
		Hooks:             []string{"auth"},
	}
	script := fmt.Sprintf("#!/bin/sh\necho '%s'", jsonManifest(m))
	writeScript(t, dir, "restish-myplugin", script)

	t.Setenv("PATH", "")
	plugins := Discover(dir, nil, "", nil)
	if len(plugins) == 0 {
		t.Fatal("expected at least one plugin to be discovered")
	}
	if plugins[0].Manifest.Name != "myplugin" {
		t.Errorf("Name: got %q, want %q", plugins[0].Manifest.Name, "myplugin")
	}
}

func TestDiscover_SkipsBrokenPlugins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	// Broken plugin: exits non-zero.
	writeScript(t, dir, "restish-broken", "#!/bin/sh\nexit 1")

	var errs []string
	plugins := Discover(dir, func(p string, err error) {
		errs = append(errs, err.Error())
	}, "", nil)
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(plugins))
	}
	if len(errs) == 0 {
		t.Error("expected errFn to be called for broken plugin")
	}
}

func TestDiscover_IgnoresNonRestishExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked")
	writeScript(t, dir, "not-a-plugin", fmt.Sprintf("#!/bin/sh\necho invoked > %q\nexit 1", marker))

	var errs []string
	plugins := Discover(dir, func(p string, err error) {
		errs = append(errs, err.Error())
	}, "", nil)
	if len(plugins) != 0 {
		t.Fatalf("expected 0 plugins, got %d", len(plugins))
	}
	if len(errs) != 0 {
		t.Fatalf("expected non-restish executable not to be probed, got errors: %v", errs)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected non-restish executable not to run, stat err = %v", err)
	}
}

func TestDiscover_FutureRequiredVersionReportsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	m := Manifest{
		Name:              "future",
		RestishAPIVersion: CurrentPluginAPIVersion + 1,
	}
	script := fmt.Sprintf("#!/bin/sh\necho '%s'", jsonManifest(m))
	writeScript(t, dir, "restish-future", script)

	var warnings bytes.Buffer
	var errs []string
	plugins := Discover(dir, func(_ string, err error) {
		errs = append(errs, err.Error())
	}, "", &warnings)
	if len(plugins) != 0 {
		t.Fatalf("expected future-version plugin to be skipped, got %d plugins", len(plugins))
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "requires restish_api_version") {
		t.Fatalf("expected future-version error through Discover errFn, got %v", errs)
	}
}

func TestDiscover_DeduplicatesPlugins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	m := Manifest{Name: "myplugin", RestishAPIVersion: CurrentPluginAPIVersion, Hooks: []string{"auth"}}
	script := fmt.Sprintf("#!/bin/sh\necho '%s'", jsonManifest(m))
	writeScript(t, dir, "restish-myplugin", script)
	writeScript(t, dir, "restish-myplugin-copy", script)

	var errs []string
	plugins := Discover(dir, func(_ string, err error) {
		errs = append(errs, err.Error())
	}, "", nil)
	if len(plugins) != 1 {
		t.Errorf("expected 1 unique plugin, got %d", len(plugins))
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "duplicate manifest name") {
		t.Fatalf("expected duplicate-name error, got %v", errs)
	}
}

func TestLoadManifest_ValidatesHooksAndHookSpecificFields(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	tests := []struct {
		name    string
		m       Manifest
		wantErr string
	}{
		{
			name:    "unknown hook",
			m:       Manifest{Name: "bad", RestishAPIVersion: CurrentPluginAPIVersion, Hooks: []string{"request"}},
			wantErr: `unknown hook "request"`,
		},
		{
			name:    "no capabilities",
			m:       Manifest{Name: "bad", RestishAPIVersion: CurrentPluginAPIVersion},
			wantErr: "declares no capabilities",
		},
		{
			name:    "formatter hook requires names",
			m:       Manifest{Name: "bad", RestishAPIVersion: CurrentPluginAPIVersion, Hooks: []string{"formatter"}},
			wantErr: "omits formatter_names",
		},
		{
			name:    "formatter names require hook",
			m:       Manifest{Name: "bad", RestishAPIVersion: CurrentPluginAPIVersion, FormatterNames: []string{"bad"}},
			wantErr: "formatter_names without declaring formatter hook",
		},
		{
			name:    "loader hook requires content types",
			m:       Manifest{Name: "bad", RestishAPIVersion: CurrentPluginAPIVersion, Hooks: []string{"loader"}},
			wantErr: "omits loader_content_types",
		},
		{
			name:    "loader content types require hook",
			m:       Manifest{Name: "bad", RestishAPIVersion: CurrentPluginAPIVersion, LoaderContentTypes: []string{"application/x-bad"}},
			wantErr: "loader_content_types without declaring loader hook",
		},
		{
			name: "valid formatter",
			m: Manifest{
				Name:              "good",
				RestishAPIVersion: CurrentPluginAPIVersion,
				Hooks:             []string{"formatter"},
				FormatterNames:    []string{"good"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeScript(t, dir, "restish-test", fmt.Sprintf("#!/bin/sh\necho '%s'", jsonManifest(tt.m)))
			_, err := LoadManifest(p, nil)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadManifest error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadManifest: %v", err)
			}
		})
	}
}

func TestDiscover_IgnoresPathPlugins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	pathDir := t.TempDir()
	pluginDir := t.TempDir()

	writeScript(t, pathDir, "restish-dupe", fmt.Sprintf("#!/bin/sh\necho '%s'", jsonManifest(Manifest{
		Name:              "dupe",
		Version:           "path",
		RestishAPIVersion: CurrentPluginAPIVersion,
		Hooks:             []string{"auth"},
	})))

	t.Setenv("PATH", pathDir)
	plugins := Discover(pluginDir, nil, "", nil)
	if len(plugins) != 0 {
		t.Fatalf("expected PATH plugin to be ignored, got %d plugins", len(plugins))
	}
}

func TestDiscover_EmptyPluginDir_NoPlugins(t *testing.T) {
	t.Setenv("PATH", "")
	plugins := Discover("", nil, "", nil)
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins without a plugin dir, got %d", len(plugins))
	}
}

func TestDiscover_UsesManifestCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	cacheFile := filepath.Join(t.TempDir(), "nested", "plugin-manifest-cache.cbor")
	counterFile := filepath.Join(dir, "manifest-count")
	m := Manifest{Name: "cached", RestishAPIVersion: CurrentPluginAPIVersion, Hooks: []string{"auth"}}
	script := fmt.Sprintf(`#!/bin/sh
count=0
if [ -f %q ]; then
	count=$(cat %q)
fi
count=$((count + 1))
echo "$count" > %q
echo '%s'
	`, counterFile, counterFile, counterFile, jsonManifest(m))
	writeScript(t, dir, "restish-cached", script)

	plugins := Discover(dir, nil, cacheFile, nil)
	if len(plugins) != 1 {
		t.Fatalf("first discover: got %d plugins, want 1", len(plugins))
	}
	count, err := os.ReadFile(counterFile)
	if err != nil {
		t.Fatalf("reading counter: %v", err)
	}
	if string(bytes.TrimSpace(count)) != "1" {
		t.Fatalf("first discover counter = %q, want 1", bytes.TrimSpace(count))
	}

	plugins = Discover(dir, nil, cacheFile, nil)
	if len(plugins) != 1 {
		t.Fatalf("second discover: got %d plugins, want 1", len(plugins))
	}
	count, err = os.ReadFile(counterFile)
	if err != nil {
		t.Fatalf("reading counter after cache hit: %v", err)
	}
	if string(bytes.TrimSpace(count)) != "1" {
		t.Fatalf("cached discover counter = %q, want 1", bytes.TrimSpace(count))
	}
}

func TestDiscover_DoesNotCacheRequiredFeatures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	cacheFile := filepath.Join(t.TempDir(), "plugin-manifest-cache.cbor")
	counterFile := filepath.Join(dir, "manifest-count")
	m := Manifest{
		Name: "select", RestishAPIVersion: CurrentPluginAPIVersion,
		Hooks: []string{"command"}, RequiredFeatures: []string{pluginwire.FeatureCommandSelect},
	}
	script := fmt.Sprintf(`#!/bin/sh
count=0
if [ -f %q ]; then count=$(cat %q); fi
count=$((count + 1))
echo "$count" > %q
echo '%s'
`, counterFile, counterFile, counterFile, jsonManifest(m))
	writeScript(t, dir, "restish-select", script)

	for range 2 {
		if plugins := Discover(dir, nil, cacheFile, nil); len(plugins) != 1 {
			t.Fatalf("discover: got %d plugins, want 1", len(plugins))
		}
	}
	count, err := os.ReadFile(counterFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes.TrimSpace(count)) != "2" {
		t.Fatalf("manifest executions = %q, want 2", bytes.TrimSpace(count))
	}
}

func TestDiscover_InvalidatesManifestCacheOnMtimeChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	cacheFile := filepath.Join(t.TempDir(), "plugin-manifest-cache.cbor")
	counterFile := filepath.Join(dir, "manifest-count")
	scriptPath := writeScript(t, dir, "restish-refresh", fmt.Sprintf(`#!/bin/sh
count=0
if [ -f %q ]; then
	count=$(cat %q)
fi
count=$((count + 1))
echo "$count" > %q
echo '%s'
`, counterFile, counterFile, counterFile, jsonManifest(Manifest{Name: "refresh", RestishAPIVersion: CurrentPluginAPIVersion, Hooks: []string{"auth"}})))

	if plugins := Discover(dir, nil, cacheFile, nil); len(plugins) != 1 {
		t.Fatalf("first discover: got %d plugins, want 1", len(plugins))
	}

	updated := fmt.Sprintf(`#!/bin/sh
count=0
if [ -f %q ]; then
	count=$(cat %q)
fi
count=$((count + 1))
echo "$count" > %q
echo '%s'
`, counterFile, counterFile, counterFile, jsonManifest(Manifest{Name: "refresh-v2", RestishAPIVersion: CurrentPluginAPIVersion, Hooks: []string{"auth"}}))
	if err := os.WriteFile(scriptPath, []byte(updated), 0o755); err != nil {
		t.Fatalf("updating plugin script: %v", err)
	}
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(scriptPath, now, now); err != nil {
		t.Fatalf("updating plugin mtime: %v", err)
	}

	plugins := Discover(dir, nil, cacheFile, nil)
	if len(plugins) != 1 {
		t.Fatalf("second discover: got %d plugins, want 1", len(plugins))
	}
	if plugins[0].Manifest.Name != "refresh-v2" {
		t.Fatalf("refreshed manifest name = %q, want refresh-v2", plugins[0].Manifest.Name)
	}
}

func TestDiscover_InvalidatesManifestCacheOnSizeChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	cacheFile := filepath.Join(t.TempDir(), "plugin-manifest-cache.cbor")
	scriptPath := writeScript(t, dir, "restish-refresh", fmt.Sprintf("#!/bin/sh\necho '%s'\n",
		jsonManifest(Manifest{Name: "refresh", Version: "v1", RestishAPIVersion: CurrentPluginAPIVersion, Hooks: []string{"auth"}})))
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}

	plugins := Discover(dir, nil, cacheFile, nil)
	if len(plugins) != 1 {
		t.Fatalf("first discover: got %d plugins, want 1", len(plugins))
	}

	updated := fmt.Sprintf("#!/bin/sh\n# size change\n# another line\n echo '%s'\n",
		jsonManifest(Manifest{Name: "refresh", Version: "v2", RestishAPIVersion: CurrentPluginAPIVersion, Hooks: []string{"auth"}}))
	if err := os.WriteFile(scriptPath, []byte(updated), 0o755); err != nil {
		t.Fatalf("updating plugin script: %v", err)
	}
	if err := os.Chtimes(scriptPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("restoring plugin mtime: %v", err)
	}

	plugins = Discover(dir, nil, cacheFile, nil)
	if len(plugins) != 1 {
		t.Fatalf("second discover: got %d plugins, want 1", len(plugins))
	}
	if got := plugins[0].Manifest.Version; got != "v2" {
		t.Fatalf("manifest version = %q, want v2", got)
	}
}

func TestSaveManifestCacheUsesAtomicRename(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "plugin-manifest-cache.cbor")
	oldRename := renameManifestCacheFile
	var sawTemp bool
	renameManifestCacheFile = func(oldpath, newpath string) error {
		if filepath.Dir(oldpath) != dir || newpath != cacheFile {
			t.Fatalf("rename %q -> %q does not use cache temp path", oldpath, newpath)
		}
		if !strings.Contains(filepath.Base(oldpath), ".plugin-manifest-cache.cbor-") || !strings.HasSuffix(oldpath, ".tmp") {
			t.Fatalf("temp name = %q, want cache temp", oldpath)
		}
		sawTemp = true
		return oldRename(oldpath, newpath)
	}
	t.Cleanup(func() { renameManifestCacheFile = oldRename })

	saveManifestCache(cacheFile, manifestCache{
		"/tmp/restish-demo": {
			Mtime:    1,
			Size:     2,
			Manifest: Manifest{Name: "demo", RestishAPIVersion: CurrentPluginAPIVersion},
		},
	}, nil)
	if !sawTemp {
		t.Fatal("rename hook was not called")
	}
	cache := loadManifestCache(cacheFile)
	if got := cache["/tmp/restish-demo"].Size; got != 2 {
		t.Fatalf("cached size = %d, want 2", got)
	}
}

func TestLoadManifestCacheBadCBORFallsBackToEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin-manifest-cache.cbor")
	if err := os.WriteFile(path, []byte("not cbor"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := loadManifestCache(path)
	if len(cache) != 0 {
		t.Fatalf("cache = %#v, want empty fallback", cache)
	}
}

// TestHookTimeout verifies default and override timeout logic.
func TestHookTimeout(t *testing.T) {
	tests := []struct {
		name     string
		manifest Manifest
		hook     string
		want     time.Duration
	}{
		{"auth default", Manifest{}, "auth", 5 * time.Minute},
		{"non-auth default", Manifest{}, "request-middleware", 30 * time.Second},
		{"auth override", Manifest{HookTimeouts: map[string]time.Duration{"auth": 10 * time.Second}}, "auth", 10 * time.Second},
		{"other override", Manifest{HookTimeouts: map[string]time.Duration{"request-middleware": 10 * time.Second}}, "request-middleware", 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HookTimeout(tt.manifest, tt.hook)
			if got != tt.want {
				t.Errorf("HookTimeout(%q) = %v, want %v", tt.hook, got, tt.want)
			}
		})
	}
}

func TestCallHookWithTimeoutContextCancellationKillsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	path := writeScript(t, dir, "restish-hook-block", "#!/bin/sh\nsleep 30\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	var out pluginwire.AuthHookOutput
	err := CallHookWithTimeoutContext(ctx, path, 30*time.Second, pluginwire.AuthHookInput{Type: "auth"}, &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("hook cancellation waited too long: %v", elapsed)
	}
}

func TestCallHookOutputTooLarge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	path := writeScript(t, dir, "restish-hook-huge", fmt.Sprintf("#!/bin/sh\ndd if=/dev/zero bs=%d count=1 2>/dev/null\n", maxHookOutputBytes+1))

	var out pluginwire.AuthHookOutput
	err := CallHook(path, pluginwire.AuthHookInput{Type: "auth"}, &out)
	if err == nil {
		t.Fatal("expected oversized hook output to fail")
	}
	if !strings.Contains(err.Error(), "output exceeded") {
		t.Fatalf("expected output cap error, got %v", err)
	}
}

func TestCallHookRedactsSensitiveStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	path := writeScript(t, dir, "restish-hook-secret", "#!/bin/sh\necho 'Authorization: Bearer super-secret-token' >&2\nexit 1\n")

	var out pluginwire.AuthHookOutput
	err := CallHook(path, pluginwire.AuthHookInput{Type: "auth"}, &out)
	if err == nil {
		t.Fatal("expected hook error")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("hook stderr leaked secret: %v", err)
	}
	if !strings.Contains(err.Error(), "Authorization: ***") {
		t.Fatalf("hook stderr was not redacted clearly: %v", err)
	}
}

func TestStartFormatterStreamContextCancellationKillsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tests not supported on Windows")
	}
	dir := t.TempDir()
	path := writeScript(t, dir, "restish-format-block", "#!/bin/sh\nsleep 30\n")

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := StartFormatterStream(ctx, path, io.Discard, pluginwire.FormatterRequest{
		Type:   "formatter",
		Format: "block",
		Event:  "start",
	})
	if err != nil {
		t.Fatalf("StartFormatterStream: %v", err)
	}
	cancel()

	start := time.Now()
	if err := stream.Close(); err == nil {
		t.Fatal("expected killed formatter to return an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("formatter close waited too long after context cancellation: %v", elapsed)
	}
}
