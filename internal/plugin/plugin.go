// Package plugin handles discovery, manifest loading, and caching of Restish
// out-of-process plugins. Plugins are executables named restish-<name> found
// in the plugin directory.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	configpkg "github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/fileutil"
	"github.com/rest-sh/restish/v2/internal/procutil"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
)

// CurrentPluginAPIVersion is the highest plugin protocol version this build of
// Restish requires plugins to ask for. The manifest restish_api_version field is
// a minimum required version, so a plugin built against a future Restish can
// still load when it only needs features this host supports.
const CurrentPluginAPIVersion = 2

const (
	maxManifestOutputBytes = 1 << 20
	maxManifestStderrBytes = 8 << 10
)

var knownHooks = map[string]bool{
	"auth":                true,
	"request-middleware":  true,
	"response-middleware": true,
	"loader":              true,
	"formatter":           true,
	"command":             true,
	"tls-signer":          true,
}

var supportedRequiredFeatures = map[string]bool{
	pluginwire.FeatureManifestRequiredFeatures: true,
	pluginwire.FeatureLoaderSourceMetadata:     true,
	pluginwire.FeatureRequestFinalBody:         true,
	pluginwire.FeatureCommandSelect:            true,
}

var renameManifestCacheFile = os.Rename

// Manifest is an alias for the canonical plugin.Manifest defined in the public
// plugin package. Using the same type eliminates the dual-maintenance risk that
// arose when the two structs diverged.
type Manifest = pluginwire.Manifest

// Plugin is a discovered plugin executable together with its manifest.
type Plugin struct {
	Path     string
	Manifest Manifest
}

// Discover finds all executable restish-* plugins in pluginDir.
// Errors loading individual plugin manifests are reported via errFn but do not
// abort discovery. Pass nil for errFn to silently skip broken plugins.
// manifestCacheFile, when non-empty, enables a CBOR on-disk manifest cache
// keyed by plugin path + mtime. This avoids subprocess spawns on every
// invocation when the plugin binary has not changed. When duplicate plugin
// identities are found, the first plugin in directory order is loaded and later
// duplicates are reported through errFn.
func Discover(pluginDir string, errFn func(path string, err error), manifestCacheFile string, stderr io.Writer) []Plugin {
	seenPaths := map[string]bool{}
	seenNames := map[string]string{}
	var plugins []Plugin

	cache := loadManifestCache(manifestCacheFile)
	cacheUpdated := false

	resolveManifest := func(path string) (*Manifest, error) {
		info, statErr := os.Stat(path)
		if statErr == nil && manifestCacheFile != "" {
			mtime := info.ModTime().UnixNano()
			size := info.Size()
			if entry, ok := cache[path]; ok && entry.Mtime == mtime && entry.Size == size {
				if len(entry.Manifest.RequiredFeatures) == 0 {
					m := entry.Manifest
					return &m, nil
				}
				delete(cache, path)
				cacheUpdated = true
			}
			m, err := loadManifest(path, stderr)
			if err != nil {
				return nil, err
			}
			if len(m.RequiredFeatures) == 0 {
				cache[path] = manifestCacheEntry{Mtime: mtime, Size: size, Manifest: *m}
				cacheUpdated = true
			}
			return m, nil
		}
		return loadManifest(path, stderr)
	}

	add := func(path string) {
		if seenPaths[path] {
			return
		}
		seenPaths[path] = true
		m, err := resolveManifest(path)
		if err != nil {
			if errFn != nil {
				errFn(path, err)
			}
			return
		}
		if firstPath, ok := seenNames[m.Name]; ok {
			if errFn != nil {
				errFn(path, fmt.Errorf("plugin %s: duplicate manifest name %q already declared by %s", filepath.Base(path), m.Name, filepath.Base(firstPath)))
			}
			return
		}
		seenNames[m.Name] = path
		plugins = append(plugins, Plugin{Path: path, Manifest: *m})
	}

	if pluginDir != "" {
		entries, err := os.ReadDir(pluginDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				full := filepath.Join(pluginDir, e.Name())
				if isPluginExecutableName(e.Name()) && isExecutable(full) {
					add(full)
				}
			}
		}
	}

	if cacheUpdated && manifestCacheFile != "" {
		saveManifestCache(manifestCacheFile, cache, stderr)
	}

	return plugins
}

// LoadManifest calls path with plugin.StartupFlagManifest and parses the CBOR
// (or JSON fallback) manifest from stdout. Compatibility warnings are sent to
// warningWriter; pass nil to suppress warnings.
func LoadManifest(path string, warningWriter io.Writer) (*Manifest, error) {
	return loadManifest(path, warningWriter)
}

func loadManifest(path string, warningWriter io.Writer) (*Manifest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, pluginwire.StartupFlagManifest)
	procutil.ConfigureCommandTreeKill(ctx, cmd)
	stdout := &limitedBuffer{limit: maxManifestOutputBytes + 1}
	stderr := &limitedBuffer{limit: maxManifestStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		return nil, manifestExecError(filepath.Base(path), err, stderr)
	}
	if stdout.Len() > maxManifestOutputBytes || stdout.Truncated() {
		return nil, fmt.Errorf("plugin %s: manifest output exceeded %d bytes", filepath.Base(path), maxManifestOutputBytes)
	}
	out := stdout.Bytes()
	if len(out) == 0 {
		return nil, fmt.Errorf("plugin %s: empty manifest", filepath.Base(path))
	}

	var m Manifest
	// Try CBOR first, then JSON.
	if err := pluginwire.DecMode.Unmarshal(out, &m); err != nil {
		if err2 := json.Unmarshal(out, &m); err2 != nil {
			return nil, fmt.Errorf("plugin %s: invalid manifest (cbor: %v; json: %v)", filepath.Base(path), err, err2)
		}
	}

	if m.Name == "" {
		return nil, fmt.Errorf("plugin %s: manifest missing name", filepath.Base(path))
	}
	if m.RestishAPIVersion < 1 {
		return nil, fmt.Errorf("plugin %s: manifest missing or invalid restish_api_version", filepath.Base(path))
	}
	if m.RestishAPIVersion > CurrentPluginAPIVersion {
		return nil, fmt.Errorf("plugin %s: requires restish_api_version %d, but this host supports %d", filepath.Base(path), m.RestishAPIVersion, CurrentPluginAPIVersion)
	}
	if err := validateManifest(m); err != nil {
		return nil, fmt.Errorf("plugin %s: %w", filepath.Base(path), err)
	}

	return &m, nil
}

func manifestExecError(name string, err error, stderr *limitedBuffer) error {
	excerpt := stderrExcerpt(stderr)
	if excerpt == "" {
		return fmt.Errorf("plugin %s: manifest exec: %w", name, err)
	}
	return fmt.Errorf("plugin %s: manifest exec: %w: stderr: %s", name, err, excerpt)
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
			return len(p), nil
		}
		return b.buf.Write(p)
	}
	if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	if b == nil {
		return nil
	}
	return b.buf.Bytes()
}

func (b *limitedBuffer) Len() int {
	if b == nil {
		return 0
	}
	return b.buf.Len()
}

func (b *limitedBuffer) String() string {
	if b == nil {
		return ""
	}
	return b.buf.String()
}

func (b *limitedBuffer) Truncated() bool {
	return b != nil && b.truncated
}

func stderrExcerpt(stderr *limitedBuffer) string {
	if stderr == nil {
		return ""
	}
	excerpt := strings.TrimSpace(stderr.String())
	if excerpt == "" {
		return ""
	}
	if stderr.truncated {
		excerpt += "..."
	}
	return excerpt
}

func validateManifest(m Manifest) error {
	declaredHooks := make(map[string]bool, len(m.Hooks))
	for _, hook := range m.Hooks {
		if !knownHooks[hook] {
			return fmt.Errorf("manifest declares unknown hook %q", hook)
		}
		declaredHooks[hook] = true
	}
	for _, feature := range m.RequiredFeatures {
		if !supportedRequiredFeatures[feature] {
			return fmt.Errorf("manifest requires unsupported feature %q", feature)
		}
	}
	if declaredHooks["formatter"] && len(m.FormatterNames) == 0 {
		return fmt.Errorf("manifest declares formatter hook but omits formatter_names")
	}
	if !declaredHooks["formatter"] && len(m.FormatterNames) > 0 {
		return fmt.Errorf("manifest sets formatter_names without declaring formatter hook")
	}
	if declaredHooks["loader"] && len(m.LoaderContentTypes) == 0 {
		return fmt.Errorf("manifest declares loader hook but omits loader_content_types")
	}
	if !declaredHooks["loader"] && len(m.LoaderContentTypes) > 0 {
		return fmt.Errorf("manifest sets loader_content_types without declaring loader hook")
	}
	if len(declaredHooks) == 0 {
		return fmt.Errorf("manifest declares no capabilities")
	}
	return nil
}

// manifestCacheEntry stores one plugin's cached manifest along with the
// modification time of the executable at the time it was cached.
type manifestCacheEntry struct {
	Mtime    int64    `cbor:"mtime"`
	Size     int64    `cbor:"size,omitempty"`
	Manifest Manifest `cbor:"manifest"`
}

// manifestCache maps plugin executable path to its cached entry.
type manifestCache map[string]manifestCacheEntry

// loadManifestCache reads the manifest cache CBOR file at cachePath.
// Returns an empty map on any error (missing file, corrupt data, etc.).
func loadManifestCache(cachePath string) manifestCache {
	if cachePath == "" {
		return make(manifestCache)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return make(manifestCache)
	}
	var cache manifestCache
	if err := cbor.Unmarshal(data, &cache); err != nil {
		return make(manifestCache)
	}
	return cache
}

// saveManifestCache writes cache to cachePath as CBOR. If the write fails a
// one-line warning is printed to w (a missing or stale cache is not fatal).
// Pass nil for w to suppress the warning.
func saveManifestCache(cachePath string, cache manifestCache, w io.Writer) {
	if cachePath == "" {
		return
	}
	data, err := cbor.Marshal(cache)
	if err != nil {
		if w != nil {
			fmt.Fprintf(w, "warning: manifest cache: marshal: %v\n", err)
		}
		return
	}
	if err := atomicWriteManifestCache(cachePath, data); err != nil {
		if w != nil {
			fmt.Fprintf(w, "warning: manifest cache: write: %v\n", err)
		}
	}
}

func atomicWriteManifestCache(cachePath string, data []byte) error {
	return fileutil.AtomicWriteFile(cachePath, data, fileutil.AtomicWriteOptions{
		FileMode:    0o600,
		DirMode:     0o700,
		TempPattern: "." + filepath.Base(cachePath) + "-*.tmp",
		Rename:      renameManifestCacheFile,
		SyncDir:     true,
	})
}

// DefaultPluginDir returns the default directory for installed plugins.
func DefaultPluginDir() string {
	return filepath.Join(configpkg.NewPaths().Config(), "plugins")
}

// DefaultManifestCachePath returns the path to the on-disk plugin manifest
// cache file. Stored next to the config file to be automatically cleaned up
// when users wipe their config directory.
func DefaultManifestCachePath() string {
	return configpkg.NewPaths().PluginManifestCache()
}

func isPluginExecutableName(name string) bool {
	return strings.HasPrefix(strings.TrimSuffix(name, ".exe"), "restish-")
}

// isExecutable reports whether path is a regular executable file.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		ext := strings.ToLower(filepath.Ext(path))
		return ext == ".exe" || ext == ".cmd" || ext == ".bat"
	}
	return info.Mode()&0o111 != 0
}
