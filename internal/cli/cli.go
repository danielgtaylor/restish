package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
	internalconfig "github.com/rest-sh/restish/v2/internal/config"
	"github.com/rest-sh/restish/v2/internal/content"
	"github.com/rest-sh/restish/v2/internal/hypermedia"
	"github.com/rest-sh/restish/v2/internal/output"
	internalplugin "github.com/rest-sh/restish/v2/internal/plugin"
	"github.com/rest-sh/restish/v2/internal/spec"
	pluginwire "github.com/rest-sh/restish/v2/plugin"
	"github.com/spf13/cobra"
)

var errSignalCanceled = errors.New("signal canceled")

const staleGeneratedOperationRefreshTimeout = 3 * time.Second

type signalCancelError struct {
	signal os.Signal
}

func (e signalCancelError) Error() string {
	if e.signal == nil {
		return errSignalCanceled.Error()
	}
	return fmt.Sprintf("%s: %s", errSignalCanceled, e.signal)
}

func (e signalCancelError) Is(target error) bool {
	return target == errSignalCanceled
}

// Version is the current build version, set at build time via -ldflags.
var Version = "2.0.0-dev"

// testHooks holds test-only overrides for CLI internals. The zero value is
// safe: every field is treated as "not set" when empty/nil.
type testHooks struct {
	// ConfigPath overrides the default config file location.
	ConfigPath string
	// PassReader, if non-nil, is used as the source for secret prompts.
	PassReader io.Reader
	// PromptFunc overrides visible prompt reads in tests.
	PromptFunc func(context.Context, string) (string, error)
	// SecretFunc overrides secret prompt reads in tests.
	SecretFunc func(context.Context, string) (string, error)
	// TokenCachePath overrides the default token cache file location.
	TokenCachePath string
	// CachePath overrides the default HTTP response cache directory.
	CachePath string
	// SpecCachePath overrides the default API spec cache directory.
	SpecCachePath string
	// HTTPTransport overrides the base HTTP transport used for outbound requests.
	HTTPTransport http.RoundTripper
	// PluginManifestCachePath overrides the default plugin manifest cache file.
	PluginManifestCachePath string
	// RetryBaseDelay overrides the 1 s default backoff base for retries.
	RetryBaseDelay time.Duration
	// SignalAwareContext overrides signal-aware root context creation in tests.
	SignalAwareContext func() (context.Context, context.CancelFunc)
	// AuthHookFunc overrides auth hook plugin execution in tests.
	AuthHookFunc func(apiName, profileName string, rawParams map[string]string, secretKeys map[string]bool, req *http.Request) error
	// AuthResolverHookFunc overrides one auth-resolver plugin invocation in tests.
	AuthResolverHookFunc func(internalplugin.Plugin, pluginwire.AuthResolverInput) (bool, error)
	// StdoutIsTerminal overrides terminal detection in tests.
	StdoutIsTerminal func(io.Writer) bool
}

func (c *CLI) stdoutIsTerminal() bool {
	if c.hooks.StdoutIsTerminal != nil {
		return c.hooks.StdoutIsTerminal(c.Stdout)
	}
	return output.IsTerminal(c.Stdout)
}

// CLI holds all state for a Restish instance. Using a struct instead of
// package-level globals makes it safe to instantiate multiple independent
// instances and trivially testable with in-memory I/O.
type CLI struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	hooks testHooks

	// Paths holds computed config/cache locations. Tests may replace this with
	// a custom instance.
	Paths *config.Paths

	cfg                     *config.Config
	defaultConfig           *config.Config
	commandName             string
	commandShort            string
	commandLong             string
	commandVersion          string
	content                 *content.Registry
	loaders                 []spec.Loader
	linkParsers             []hypermedia.Parser
	formatters              map[string]output.Formatter
	plugins                 []internalplugin.Plugin
	pluginsByHook           map[string][]internalplugin.Plugin
	pluginCommandNames      map[string]string
	authPluginsByAPI        map[string][]internalplugin.Plugin
	globalAuthPlugins       []internalplugin.Plugin
	customAuthHandlers      map[string]auth.Handler
	explicitConfigFile      bool
	createExplicitConfig    bool
	retryUnsafeWarned       bool
	signalHandling          bool
	quietGeneratedWarnings  bool
	silentMode              bool
	requestExecutionStarted bool
	bodyPrefixHinted        bool
	commandSurface          CommandSurface
	runCtx                  context.Context
	projectConfig           *projectConfigState
}

// New returns a CLI wired to the real OS stdin/stdout/stderr.
func New() *CLI {
	return &CLI{
		Stdin:          os.Stdin,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		Paths:          config.NewPaths(),
		content:        content.Default(),
		loaders:        spec.DefaultLoaders(),
		linkParsers:    hypermedia.DefaultParsers(),
		formatters:     output.DefaultFormatters(),
		signalHandling: true,
	}
}

// AddLinkParser registers an additional hypermedia link parser. Parsers are
// called in registration order; later parsers can override earlier ones.
func (c *CLI) AddLinkParser(p hypermedia.Parser) {
	c.linkParsers = append(c.linkParsers, p)
}

// AddFormatter registers a named response formatter. Use the same name to
// override a built-in formatter (e.g. "json") or a new name to add a custom
// format selectable via -o <name>.
func (c *CLI) AddFormatter(name string, f output.Formatter) {
	if c.formatters == nil {
		c.formatters = output.DefaultFormatters()
	}
	c.formatters[name] = f
}

// AddContentType registers an additional content type with the CLI's registry.
func (c *CLI) AddContentType(ct *content.ContentType) {
	c.content.AddContentType(ct)
}

// AddEncoding registers an additional compression encoding with the CLI's registry.
func (c *CLI) AddEncoding(e *content.Encoding) {
	c.content.AddEncoding(e)
}

type flushWriter interface {
	Flush() error
}

type cascadingFlushWriter struct {
	*bufio.Writer
	downstream io.Writer
	mu         sync.Mutex
}

func (w *cascadingFlushWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Writer.Write(p)
}

func (w *cascadingFlushWriter) WriteString(s string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Writer.WriteString(s)
}

func (w *cascadingFlushWriter) ReadFrom(r io.Reader) (int64, error) {
	var written int64
	buf := make([]byte, 32*1024)
	for {
		nr, er := r.Read(buf)
		if nr > 0 {
			nw, ew := w.Write(buf[:nr])
			written += int64(nw)
			if ew != nil {
				return written, ew
			}
			if nw != nr {
				return written, io.ErrShortWrite
			}
		}
		if er == io.EOF {
			return written, nil
		}
		if er != nil {
			return written, er
		}
	}
}

func (w *cascadingFlushWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.Writer.Flush(); err != nil {
		return err
	}
	if f, ok := w.downstream.(flushWriter); ok {
		return f.Flush()
	}
	return nil
}

func (c *CLI) flushStdout() error {
	if f, ok := c.Stdout.(flushWriter); ok {
		return f.Flush()
	}
	return nil
}

// AddAuthHandler registers a custom auth handler under the given type name.
// The name is used in the profile's auth.type config field.
// Built-in names (http-basic, oauth-client-credentials,
// oauth-authorization-code, oauth-device-code, external-tool) can be overridden.
// Call this before CLI.Run.
//
// Use the restish.AuthHandler / restish.AuthParam aliases on the embedded API
// when implementing custom auth.
func (c *CLI) AddAuthHandler(name string, handler auth.Handler) {
	if c.customAuthHandlers == nil {
		c.customAuthHandlers = make(map[string]auth.Handler)
	}
	c.customAuthHandlers[name] = handler
}

// AddLoader registers an additional spec loader. Higher-priority loaders
// that detect the same content type take precedence over built-in loaders.
func (c *CLI) AddLoader(l spec.Loader) {
	c.loaders = append(c.loaders, l)
}

// SetCommandName changes the root command name shown in help and examples.
func (c *CLI) SetCommandName(name string) {
	c.commandName = strings.TrimSpace(name)
}

// SetCommandDescription changes the short and long root help text.
func (c *CLI) SetCommandDescription(short, long string) {
	c.commandShort = strings.TrimSpace(short)
	c.commandLong = strings.TrimSpace(long)
}

// SetVersion changes the version shown by this CLI instance.
func (c *CLI) SetVersion(version string) {
	c.commandVersion = strings.TrimSpace(version)
}

// SetSignalHandling controls whether Run installs process-level SIGINT/SIGTERM
// handling for this CLI instance. It is enabled by default for the stock CLI.
// Embedders that already own process signal handling can disable it so Restish
// does not register competing process signal handlers.
func (c *CLI) SetSignalHandling(enabled bool) {
	c.signalHandling = enabled
}

// SetDefaultConfig installs in-memory API/profile defaults that are merged
// under user config at load time. User config wins on key conflicts.
func (c *CLI) SetDefaultConfig(cfg *config.Config) {
	c.defaultConfig = cloneConfigForEmbedding(cfg)
}

// Config returns the loaded configuration after Run has been called, or nil
// if Run has not yet been called or configuration loading failed.
// Embedders can use this to inspect configured APIs and profiles.
func (c *CLI) Config() *config.Config {
	return c.cfg
}

// configFilePath returns the effective config file path.
func (c *CLI) configFilePath() string {
	if c.hooks.ConfigPath != "" {
		return c.hooks.ConfigPath
	}
	return c.paths().ConfigFile()
}

// profileFromCmd returns the active profile name from the --rsh-profile flag,
// falling back to the RSH_PROFILE environment variable (handled in
// parseGlobalFlags), then "default".
func (c *CLI) profileFromCmd(cmd *cobra.Command) string {
	gf := globalFlagsFromContext(requestContext(cmd))
	if gf.Profile != "" {
		return gf.Profile
	}
	return "default"
}

func (c *CLI) discoveryTrace(cmd *cobra.Command) func(format string, args ...any) {
	return c.contextDiscoveryTrace(requestContext(cmd))
}

func (c *CLI) contextDiscoveryTrace(ctx context.Context) func(format string, args ...any) {
	if globalFlagsFromContext(ctx).Verbose <= 0 {
		return nil
	}
	return func(format string, args ...any) {
		fmt.Fprintf(c.Stderr, "* "+format+"\n", args...)
	}
}

// specCacheDir returns the effective directory for API spec CBOR files.
func (c *CLI) specCacheDir() string {
	if c.hooks.SpecCachePath != "" {
		return c.hooks.SpecCachePath
	}
	return c.configScopedCacheDir(c.paths().SpecCache())
}

// pluginManifestCachePath returns the effective plugin manifest cache file path.
func (c *CLI) pluginManifestCachePath() string {
	if c.hooks.PluginManifestCachePath != "" {
		return c.hooks.PluginManifestCachePath
	}
	return c.paths().PluginManifestCache()
}

func (c *CLI) pluginDir() string {
	return filepath.Join(filepath.Dir(c.configFilePath()), "plugins")
}

func (c *CLI) paths() *config.Paths {
	if c.Paths != nil {
		return c.Paths
	}
	return config.NewPaths()
}

func (c *CLI) configScopedCacheDir(base string) string {
	if !c.explicitConfigFile {
		return base
	}
	sum := sha256.Sum256([]byte(c.configFilePath()))
	return filepath.Join(base, "configs", fmt.Sprintf("%x", sum[:8]))
}

func (c *CLI) loadConfig() (*config.Config, error) {
	cfg, err := c.loadBaseConfig()
	if err != nil {
		return nil, err
	}
	merged := mergeDefaultConfigForEmbedding(c.defaultConfig, cfg)
	if c.projectConfig != nil && c.projectConfig.Trusted {
		projectCfg, err := c.loadTrustedProjectConfig()
		if err != nil {
			return nil, err
		}
		merged = mergeProjectConfig(merged, projectCfg)
		if err := config.Validate(merged); err != nil {
			return nil, fmt.Errorf("project config %s: %w", c.projectConfig.Path, err)
		}
	}
	return merged, nil
}

func (c *CLI) loadBaseConfig() (*config.Config, error) {
	var migration *config.MigrationInfo
	if c.shouldRunLegacyMigration() {
		info, err := internalconfig.TryMigrate(c.configFilePath())
		if err != nil {
			return nil, fmt.Errorf("config migration: %w", err)
		}
		migration = info
	}
	var cfg *config.Config
	var err error
	if c.explicitConfigFile && c.createExplicitConfig {
		cfg, err = config.LoadExplicitOrEmpty(c.configFilePath())
	} else if c.explicitConfigFile {
		cfg, err = config.LoadExplicit(c.configFilePath())
	} else {
		cfg, err = config.Load(c.configFilePath())
	}
	if err != nil {
		return nil, err
	}
	if migration != nil && cfg.Migration == nil {
		cfg.Migration = migration
	}
	return cfg, nil
}

// shouldRunLegacyMigration is true when the restish CLI is being run with
// the default config path and the v2 config does not yet exist. This
// preserves the historical behaviour that a detected v1 apis.json is
// migrated on first v2 run. The explicitConfigFile flag does not gate
// this; an explicit --rsh-config to the default location should still
// trigger migration.
func (c *CLI) shouldRunLegacyMigration() bool {
	if os.Getenv("RSH_CONFIG_DIR") != "" {
		return false
	}
	defaultPath := legacyMigrationDefaultConfigPath()
	if defaultPath == "" || filepath.Clean(c.configFilePath()) != filepath.Clean(defaultPath) {
		return false
	}
	_, err := os.Stat(c.configFilePath())
	return errors.Is(err, os.ErrNotExist)
}

func legacyMigrationDefaultConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "restish", "restish.json")
	}
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "restish", "restish.json")
		}
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "restish", "restish.json")
	}
	return ""
}

func cloneConfigForEmbedding(src *config.Config) *config.Config {
	if src == nil {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var dst config.Config
	if err := json.Unmarshal(data, &dst); err != nil {
		return nil
	}
	return &dst
}

func mergeDefaultConfigForEmbedding(defaults, loaded *config.Config) *config.Config {
	if defaults == nil {
		return loaded
	}
	merged := cloneConfigForEmbedding(defaults)
	if merged == nil {
		merged = &config.Config{}
	}
	if loaded == nil {
		return merged
	}
	if len(loaded.APIs) > 0 {
		if merged.APIs == nil {
			merged.APIs = map[string]*config.APIConfig{}
		}
		for name, api := range loaded.APIs {
			merged.APIs[name] = api
		}
	}
	if len(loaded.AuthProfiles) > 0 {
		if merged.AuthProfiles == nil {
			merged.AuthProfiles = map[string]*config.AuthConfig{}
		}
		for name, auth := range loaded.AuthProfiles {
			merged.AuthProfiles[name] = auth
		}
	}
	if loaded.Cache != (config.CacheConfig{}) {
		merged.Cache = loaded.Cache
	}
	if len(loaded.Theme) > 0 {
		merged.Theme = loaded.Theme
	}
	if len(loaded.Plugins) > 0 {
		merged.Plugins = loaded.Plugins
	}
	merged.Migration = loaded.Migration
	return merged
}

// discoverSpec runs spec discovery for the named API using the registered loaders.
func (c *CLI) discoverSpec(ctx context.Context, apiName string) (*spec.APISpec, error) {
	return c.discoverSpecForProfile(ctx, apiName, "default", false, 0)
}

func (c *CLI) discoverSpecForProfile(ctx context.Context, apiName, profileName string, forceRefresh bool, timeout time.Duration) (*spec.APISpec, error) {
	api, err := c.requireAPI(apiName)
	if err != nil {
		return nil, nil
	}
	transport, closer, err := c.discoveryTransport(ctx, api, profileName)
	if err != nil {
		return nil, err
	}
	if closer != nil {
		defer closer.Close()
	}
	fetch := c.discoveryFetcher(ctx, apiName, api, profileName)
	cfg := spec.DiscoverConfig{
		APIName:          c.apiStateName(apiName),
		BaseURL:          effectiveProfileBaseURL(api, profileName),
		SpecURL:          api.SpecURL,
		SpecFiles:        api.SpecFiles,
		CacheDir:         c.specCacheDir(),
		OperationBase:    effectiveOperationBase(api, profileName),
		ServerVariables:  effectiveServerVariables(api, profileName),
		Version:          Version,
		Transport:        transport,
		Fetch:            fetch,
		AllowCrossOrigin: api.AllowCrossOriginSpec,
		ForceRefresh:     forceRefresh,
		Timeout:          timeout,
		Trace:            c.contextDiscoveryTrace(ctx),
		Warnf:            c.warnf,
	}
	return spec.Discover(ctx, cfg, c.loaders)
}

func (c *CLI) baseHTTPTransport() http.RoundTripper {
	if c.hooks.HTTPTransport != nil {
		return c.hooks.HTTPTransport
	}
	return http.DefaultTransport
}

// Run executes the CLI with the provided arguments (pass os.Args from main).
func (c *CLI) Run(args []string) error {
	// Build the root context once so requests, discovery, plugins, and
	// formatters all share the same cancellation source.
	ctx, cancel := c.rootContext()
	defer cancel()
	c.runCtx = ctx
	defer func() { c.runCtx = nil }()

	argScan := scanCLIArgs(args)
	c.retryUnsafeWarned = false
	c.silentMode = argScan.Silent
	c.requestExecutionStarted = false
	c.bodyPrefixHinted = false
	c.createExplicitConfig = false
	c.projectConfig = nil
	defer func() {
		c.silentMode = false
		c.requestExecutionStarted = false
		c.bodyPrefixHinted = false
		c.createExplicitConfig = false
		c.projectConfig = nil
	}()

	if c.hooks.ConfigPath == "" {
		if argScan.ExplicitConfigPath {
			c.Paths = config.NewPathsWithConfigFile(argScan.ConfigPath)
			c.explicitConfigFile = true
			c.createExplicitConfig = canCreateExplicitConfig(argScan)
		} else if os.Getenv("RSH_CONFIG") != "" {
			c.explicitConfigFile = true
		}
	}
	if err := c.prepareProjectConfig(ctx, argScan); err != nil {
		return err
	}
	if pathErr := c.paths().ConfigError(); pathErr != nil && c.hooks.ConfigPath == "" && !c.explicitConfigFile && !argScan.Bootstrap {
		return pathErr
	}

	if !output.IsTerminal(c.Stdout) {
		origStdout := c.Stdout
		buf := &cascadingFlushWriter{
			Writer:     bufio.NewWriterSize(origStdout, 64*1024),
			downstream: origStdout,
		}
		c.Stdout = buf
		defer func() {
			_ = buf.Flush()
			c.Stdout = origStdout
		}()
	}

	// On first run (no config file yet), suggest shell setup if on a supported
	// shell so users discover the noglob alias before hitting the foot-gun.
	if !c.commandSurface.IgnoreUserConfig {
		if cfgPath := c.configFilePath(); cfgPath != "" {
			if _, statErr := os.Stat(cfgPath); errors.Is(statErr, os.ErrNotExist) {
				if output.IsTerminal(c.Stderr) {
					c.hintShellSetup()
				}
			}
		}
	}

	var cfg *config.Config
	var err error
	if c.commandSurface.IgnoreUserConfig {
		cfg = cloneConfigForEmbedding(c.defaultConfig)
		if cfg == nil {
			cfg = &config.Config{}
		}
	} else {
		cfg, err = c.loadConfig()
	}
	if err != nil {
		if argScan.Bootstrap {
			c.cfg = &config.Config{}
			root := c.newRootCmd()
			return c.executeRoot(ctx, root, args)
		}
		return err
	}
	if !c.commandSurface.IgnoreUserConfig {
		if insecure, permErr := config.ConfigFileHasInsecurePermissions(c.configFilePath()); permErr == nil && insecure {
			return fmt.Errorf("%s is group/world-readable; credentials in config should be kept private (chmod 600)", c.configFilePath())
		}
	}
	c.cfg = cfg
	if cfg.Migration != nil {
		c.infof("Migrated config from v1 at %s; kept backup at %s", cfg.Migration.SourcePath, cfg.Migration.BackupPath)
		for _, warning := range cfg.Migration.Warnings {
			c.warnf("migration: %s", warning)
		}
	}
	if err := output.SetTheme(output.ThemeEntries(cfg.Theme)); err != nil {
		return fmt.Errorf("config theme: %w", err)
	}
	for apiName := range cfg.APIs {
		if isBuiltinCommandName(apiName) && !c.isPromotedAPI(apiName) {
			return fmt.Errorf("config: API name %q conflicts with a built-in command; rename it before continuing", apiName)
		}
		if err := config.ValidateAPIName(apiName); err != nil {
			return fmt.Errorf("config: API name %q is invalid: %w", apiName, err)
		}
	}

	// Discover hook plugins at startup; warn about broken plugins so users
	// know their plugin is not active rather than silently ignoring it.
	if !c.commandSurface.DisablePlugins {
		c.plugins = internalplugin.Discover(c.pluginDir(), func(path string, err error) {
			c.warnf("plugin %s: %v", filepath.Base(path), err)
		}, c.pluginManifestCachePath(), diagnosticPrefixWriter(c.Stderr))
	}
	c.pluginsByHook = indexPluginsByHook(c.plugins)
	c.globalAuthPlugins, c.authPluginsByAPI = indexAuthPluginsByAPI(c.pluginsByHook["auth"])

	// Register plugin-provided formatters and loaders.
	for _, p := range c.plugins {
		if pluginDeclaresHook(p.Manifest, "formatter") {
			for _, name := range p.Manifest.FormatterNames {
				c.formatters[name] = &output.PluginFormatter{
					PluginPath: p.Path,
					FormatName: name,
					Context:    ctx,
				}
			}
		}
		if pluginDeclaresHook(p.Manifest, "loader") && len(p.Manifest.LoaderContentTypes) > 0 {
			c.loaders = append(c.loaders, spec.PluginLoader{
				PluginPath:   p.Path,
				PluginName:   p.Manifest.Name,
				ContentTypes: p.Manifest.LoaderContentTypes,
			})
		}
	}

	root := c.newRootCmd()
	c.quietGeneratedWarnings = quietGeneratedWarningsForScan(argScan, cfg)
	if err := c.ensurePromotedAPICommandMetadata(ctx, argScan, cfg); err != nil {
		return err
	}
	if err := c.ensureCuratedAPICommandMetadata(ctx, argScan, cfg); err != nil {
		return err
	}
	if !c.hasPromotedAPI() {
		c.refreshStaleGeneratedMetadataForCommand(ctx, argScan, cfg)
	}

	// Register generated commands for APIs whose spec is already cached.
	// When the first positional arg names a configured API, only load that
	// API's cached spec to avoid eagerly parsing every registered API.
	// (Network discovery is not triggered here; use "restish api sync <name>"
	// to prime the cache for an API.)
	startupProfile := argScan.ProfileName
	var promotedAPICmd *cobra.Command
	for _, apiName := range c.generatedAPINamesForScan(argScan, cfg) {
		apiCfg := cfg.APIs[apiName]
		opOpts := spec.OperationOptions{
			BaseURL:         effectiveProfileBaseURL(apiCfg, startupProfile),
			OperationBase:   effectiveOperationBase(apiCfg, startupProfile),
			ServerVariables: effectiveServerVariables(apiCfg, startupProfile),
		}
		stateName := c.apiStateName(apiName)
		if set, _, ok := spec.LoadOperationSetFromCacheStatus(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles, opOpts, true); ok {
			if apiCmd := c.buildAPICommandFromOperationSet(apiName, apiCfg, set, opOpts.OperationBase); apiCmd != nil {
				if c.isPromotedAPI(apiName) {
					promotedAPICmd = apiCmd
					if !rootHasCommand(root, apiName) {
						root.AddCommand(apiCmd)
					}
				} else {
					root.AddCommand(apiCmd)
				}
			}
			continue
		}
		s, err := spec.LoadStaleFromCache(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles, c.loaders)
		if err != nil {
			continue
		}
		if s == nil && spec.HasLocalSpecFiles(apiCfg.SpecFiles) {
			s, err = c.discoverSpec(ctx, apiName)
		}
		if err != nil || s == nil {
			continue
		}
		set, opsErr := s.OperationSet(opOpts)
		if opsErr == nil {
			_ = spec.StoreOperationSetInCache(c.specCacheDir(), stateName, Version, opOpts, set)
		}
		if apiCmd := c.buildAPICommandFromOperationResult(apiName, apiCfg, set, opOpts.OperationBase, opsErr); apiCmd != nil {
			if c.isPromotedAPI(apiName) {
				promotedAPICmd = apiCmd
				if !rootHasCommand(root, apiName) {
					root.AddCommand(apiCmd)
				}
			} else {
				root.AddCommand(apiCmd)
			}
		}
	}
	c.addAPIShortNameCommands(root, cfg)
	if err := c.applyCommandSurface(root, promotedAPICmd, argScan, cfg, args); err != nil {
		return err
	}

	err = c.executeRoot(ctx, root, args)
	// When the context was cancelled by a signal (SIGINT/SIGTERM), return
	// ExitCodeError{130} so main exits with 130 without printing any extra message.
	if isSignalCancellation(err, ctx) {
		return &ExitCodeError{Code: 130}
	}
	if c.shouldSuppressFinalError(err) {
		return silentExitError(err)
	}
	return err
}

func (c *CLI) executeRoot(ctx context.Context, root *cobra.Command, args []string) error {
	if len(args) > 0 {
		if err := validateGeneratedFlagValueTokens(root, args); err != nil {
			return err
		}
		args = shieldGeneratedNegativeNumberArgs(root, args)
		if err := rejectUnknownSubcommandHelp(root, args[1:]); err != nil {
			return err
		}
		root.SetArgs(args[1:])
	}
	root.SetOut(c.Stdout)
	root.SetErr(c.Stderr)
	setupHelpAllExecution(root)
	return usageExitError(root.ExecuteContext(ctx))
}

const helpAllExecutionWrappedAnnotation = "restish.helpAllExecutionWrapped"

func setupHelpAllExecution(root *cobra.Command) {
	for _, cmd := range commandTree(root) {
		if cmd.Annotations != nil && cmd.Annotations[helpAllExecutionWrappedAnnotation] == "true" {
			continue
		}
		if cmd.Annotations == nil {
			cmd.Annotations = map[string]string{}
		}
		cmd.Annotations[helpAllExecutionWrappedAnnotation] = "true"

		origArgs := cmd.Args
		cmd.Args = func(cmd *cobra.Command, args []string) error {
			if commandHelpAllRequested(cmd) {
				return nil
			}
			if origArgs != nil {
				return origArgs(cmd, args)
			}
			return nil
		}

		if cmd.LocalFlags().Lookup("help-all") != nil {
			continue
		}
		if origRunE := cmd.RunE; origRunE != nil {
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if commandHelpAllRequested(cmd) {
					return cmd.Help()
				}
				return origRunE(cmd, args)
			}
			continue
		}
		if origRun := cmd.Run; origRun != nil {
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if commandHelpAllRequested(cmd) {
					return cmd.Help()
				}
				origRun(cmd, args)
				return nil
			}
		}
	}
}

func commandTree(root *cobra.Command) []*cobra.Command {
	var commands []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd == nil {
			return
		}
		commands = append(commands, cmd)
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	return commands
}

func commandHelpAllRequested(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	helpAll, err := cmd.Flags().GetBool("help-all")
	return err == nil && helpAll
}

func (c *CLI) rootContext() (context.Context, context.CancelFunc) {
	if !c.signalHandling {
		return context.WithCancel(context.Background())
	}
	if c.hooks.SignalAwareContext != nil {
		return c.hooks.SignalAwareContext()
	}
	return signalAwareContext()
}

type cliArgScan struct {
	FirstCommand            string
	SecondCommand           string
	ProfileName             string
	ConfigPath              string
	ExplicitConfigPath      bool
	VersionFlag             bool
	Silent                  bool
	Bootstrap               bool
	GeneratedAPICommandTree bool
}

func scanCLIArgs(args []string) cliArgScan {
	scan := cliArgScan{ProfileName: os.Getenv("RSH_PROFILE")}
	if scan.ProfileName == "" {
		scan.ProfileName = "default"
	}
	if len(args) <= 1 {
		scan.Bootstrap = true
		return scan
	}

	hasHelpFlag := false
	hasBootstrapFlag := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help", "-h":
			hasHelpFlag = true
			hasBootstrapFlag = true
		case "--version":
			scan.VersionFlag = true
			hasBootstrapFlag = true
		case "--rsh-silent", "-S":
			scan.Silent = true
		case "help", "__complete", "__completeNoDesc":
			scan.GeneratedAPICommandTree = true
		}

		if arg == "--" {
			if scan.FirstCommand == "" && i+1 < len(args) {
				scan.FirstCommand = args[i+1]
				if i+2 < len(args) {
					scan.SecondCommand = args[i+2]
				}
			}
			break
		}
		if arg == "help" || arg == "__complete" || arg == "__completeNoDesc" {
			scan.GeneratedAPICommandTree = true
		}

		consumesNext := false
		if strings.HasPrefix(arg, "--rsh-config=") {
			value := strings.TrimPrefix(arg, "--rsh-config=")
			scan.ConfigPath = value
			scan.ExplicitConfigPath = value != ""
		} else if strings.HasPrefix(arg, "--rsh-silent=") {
			value := strings.TrimPrefix(arg, "--rsh-silent=")
			scan.Silent = isTruthy(value)
		} else if arg == "--rsh-config" {
			consumesNext = true
			if i+1 < len(args) && args[i+1] != "" {
				scan.ConfigPath = args[i+1]
				scan.ExplicitConfigPath = true
			}
		} else if strings.HasPrefix(arg, "--rsh-profile=") {
			if value := strings.TrimPrefix(arg, "--rsh-profile="); value != "" {
				scan.ProfileName = value
			}
		} else if arg == "--rsh-profile" || arg == "-p" {
			consumesNext = true
			if i+1 < len(args) && args[i+1] != "" {
				scan.ProfileName = args[i+1]
			}
		} else if strings.HasPrefix(arg, "-p") && len(arg) > 2 {
			scan.ProfileName = strings.TrimPrefix(arg, "-p")
		} else if strings.HasPrefix(arg, "-") {
			consumesNext = flagConsumesNextArg(arg)
		}

		if !strings.HasPrefix(arg, "-") {
			if scan.FirstCommand == "" {
				scan.FirstCommand = arg
			} else if scan.SecondCommand == "" {
				scan.SecondCommand = arg
			}
		}
		if consumesNext && i+1 < len(args) {
			i++
		}
	}

	switch scan.FirstCommand {
	case "help", "completion", "version", "doctor", "__complete", "__completeNoDesc":
		scan.Bootstrap = true
	}
	if hasBootstrapFlag {
		scan.Bootstrap = true
	}
	if hasHelpFlag {
		scan.GeneratedAPICommandTree = true
	}
	return scan
}

func canCreateExplicitConfig(scan cliArgScan) bool {
	return scan.FirstCommand == "api" && scan.SecondCommand == "connect"
}

func (c *CLI) refreshStaleGeneratedMetadataForCommand(ctx context.Context, scan cliArgScan, cfg *config.Config) {
	if scan.Bootstrap || scan.GeneratedAPICommandTree || scan.FirstCommand == "" || scan.SecondCommand == "" {
		return
	}
	apiCfg := cfg.APIs[scan.FirstCommand]
	if apiCfg == nil {
		return
	}
	opts := spec.OperationOptions{
		BaseURL:         effectiveProfileBaseURL(apiCfg, scan.ProfileName),
		OperationBase:   effectiveOperationBase(apiCfg, scan.ProfileName),
		ServerVariables: effectiveServerVariables(apiCfg, scan.ProfileName),
	}
	stateName := c.apiStateName(scan.FirstCommand)
	_, status, ok := spec.LoadOperationSetFromCacheStatus(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles, opts, true)
	if !ok {
		status, ok = spec.RawSpecCacheStatus(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles)
	}
	if !ok || !status.Stale {
		return
	}
	refreshCtx, cancel := context.WithTimeout(ctx, staleGeneratedOperationRefreshTimeout)
	defer cancel()
	if _, err := c.discoverSpecForProfile(refreshCtx, scan.FirstCommand, scan.ProfileName, true, staleGeneratedOperationRefreshTimeout); err != nil {
		c.warnf("could not refresh stale API metadata for %q; using last synced metadata from %s: %v", scan.FirstCommand, formatCacheTime(status.FetchedAt), err)
	}
}

func formatCacheTime(t time.Time) string {
	if t.IsZero() {
		return "unknown time"
	}
	return t.Format("2006-01-02 15:04:05 MST")
}

func signalAwareContext() (context.Context, context.CancelFunc) {
	ctx, cancelCause := context.WithCancelCause(context.Background())
	sigCh := make(chan os.Signal, 1)
	done := make(chan struct{})
	var stopOnce sync.Once
	stopSignals := func() {
		stopOnce.Do(func() {
			signal.Stop(sigCh)
			close(done)
		})
	}
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			cancelCause(signalCancelError{signal: sig})
			stopSignals()
		case <-done:
		}
	}()
	cancel := func() {
		stopSignals()
		cancelCause(nil)
	}
	return ctx, cancel
}

func isSignalCancellation(err error, ctx context.Context) bool {
	return err != nil &&
		errors.Is(err, context.Canceled) &&
		errors.Is(context.Cause(ctx), errSignalCanceled)
}

func effectiveServerVariables(apiCfg *config.APIConfig, profileName string) map[string]string {
	if apiCfg == nil {
		return nil
	}
	var out map[string]string
	for key, value := range apiCfg.ServerVariables {
		if out == nil {
			out = map[string]string{}
		}
		out[key] = value
	}
	if profileName == "" {
		profileName = "default"
	}
	if prof := apiCfg.Profiles[profileName]; prof != nil {
		for key, value := range prof.ServerVariables {
			if out == nil {
				out = map[string]string{}
			}
			out[key] = value
		}
	}
	return out
}

func effectiveURLOverrides(apiCfg *config.APIConfig, profileName string) map[string]string {
	if apiCfg == nil {
		return nil
	}
	var out map[string]string
	for source, destination := range apiCfg.URLOverrides {
		if out == nil {
			out = map[string]string{}
		}
		out[source] = destination
	}
	if profileName == "" {
		profileName = "default"
	}
	if prof := apiCfg.Profiles[profileName]; prof != nil {
		for source, destination := range prof.URLOverrides {
			if out == nil {
				out = map[string]string{}
			}
			out[source] = destination
		}
	}
	return out
}

func (c *CLI) addAPIShortNameCommands(root *cobra.Command, cfg *config.Config) {
	if cfg == nil || len(cfg.APIs) == 0 {
		return
	}
	names := make([]string, 0, len(cfg.APIs))
	for name := range cfg.APIs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, apiName := range names {
		apiCfg := cfg.APIs[apiName]
		if apiCfg == nil || c.isPromotedAPI(apiName) || isBuiltinCommandName(apiName) || rootHasCommand(root, apiName) {
			continue
		}
		apiName := apiName
		short := "Generic requests using the registered API base URL"
		if apiCfg.BaseURL != "" {
			short = fmt.Sprintf("Generic requests using %s", apiCfg.BaseURL)
		}
		root.AddCommand(&cobra.Command{
			Use:     apiName,
			Short:   short,
			GroupID: rootGroupAPI,
			Args:    cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if shouldSyncGeneratedBeforeGeneric(apiCfg, args) {
					return c.runSyncedGeneratedAPICommand(cmd, apiName, apiCfg, args)
				}
				return c.runInferredHTTP(cmd, append([]string{apiName}, args...))
			},
		})
	}
}

func shouldSyncGeneratedBeforeGeneric(apiCfg *config.APIConfig, args []string) bool {
	if apiCfg == nil || !apiHasSpecSource(apiCfg) || len(args) == 0 {
		return false
	}
	return looksLikeGeneratedCommandToken(args[0])
}

func apiHasSpecSource(apiCfg *config.APIConfig) bool {
	return apiCfg.SpecURL != "" || len(apiCfg.SpecFiles) > 0
}

func looksLikeGeneratedCommandToken(token string) bool {
	if token == "" || strings.HasPrefix(token, "-") || strings.ContainsAny(token, "/?#:") {
		return false
	}
	return true
}

func (c *CLI) runSyncedGeneratedAPICommand(cmd *cobra.Command, apiName string, apiCfg *config.APIConfig, args []string) error {
	profileName := c.profileFromCmd(cmd)
	set, ok, err := c.operationSetForAPI(cmd.Context(), apiName, apiCfg, profileName, true)
	if err != nil {
		return fmt.Errorf("generated commands for API %q are not available: %w", apiName, err)
	}
	if !ok {
		return fmt.Errorf("generated commands for API %q are not available; run %q after fixing spec discovery", apiName, "api sync "+apiName)
	}
	apiCmd := c.buildAPICommandFromOperationSet(apiName, apiCfg, set, effectiveOperationBase(apiCfg, profileName))
	if apiCmd == nil {
		return fmt.Errorf("generated commands for API %q are unavailable after sync", apiName)
	}
	if !commandPathExists(apiCmd, args) {
		return unknownCommandError(apiCmd, args[0], "run "+strconvQuote(cmd.CommandPath()+" --help")+" to see generated operations")
	}
	root := c.newRootCmd()
	root.AddCommand(apiCmd)
	commandName := c.commandName
	if commandName == "" {
		commandName = "restish"
	}
	return c.executeRoot(cmd.Context(), root, append([]string{commandName, apiName}, args...))
}

func rootHasCommand(root *cobra.Command, name string) bool {
	for _, cmd := range root.Commands() {
		if cmd.Name() == name {
			return true
		}
	}
	return false
}

func commandPathExists(cmd *cobra.Command, args []string) bool {
	if len(args) == 0 {
		return false
	}
	current := cmd
	for _, arg := range args {
		if arg == "--" || strings.HasPrefix(arg, "-") {
			break
		}
		next := childCommandForToken(current, arg)
		if next == nil {
			break
		}
		current = next
		if current.RunE != nil || current.Run != nil {
			return true
		}
	}
	return false
}

// builtinCommands is the set of top-level public and hidden compatibility
// command names registered by the CLI itself. When the first non-flag argument
// is one of these, the fast-path skips API-name detection and loads all
// configured APIs.
var builtinCommands = map[string]bool{
	"api": true, "cache": true, "cert": true, "completion": true, "config": true,
	"delete": true, "doctor": true, "edit": true, "get": true, "head": true,
	"help": true, "links": true, "options": true, "patch": true, "plugin": true,
	"post": true, "put": true, "shell": true, "version": true,
}

// isBuiltinCommandName reports whether name collides with a top-level built-in
// command, which would prevent the generated API subcommand from being reachable.
func isBuiltinCommandName(name string) bool {
	return builtinCommands[name]
}

// generatedAPINames returns the APIs whose generated commands should be
// registered for this invocation. When the first non-flag positional argument
// names a configured API, only that API's spec is loaded; otherwise all
// configured APIs are registered. This prevents eagerly parsing every cached
// spec on every invocation.
//
// Flags (tokens starting with "-") are skipped when scanning for the first
// positional argument, so `restish -v myapi op` still fast-paths on myapi.
// Flags of the form --flag=value are consumed as a single token; all other
// value-taking flags consume the next token as their value unless it starts
// with "-"; bool/count flags do not consume the API name.
func (c *CLI) generatedAPINames(args []string, cfg *config.Config) []string {
	return c.generatedAPINamesForScan(scanCLIArgs(args), cfg)
}

func quietGeneratedWarningsForScan(scan cliArgScan, cfg *config.Config) bool {
	return true
}

func (c *CLI) generatedAPINamesForScan(scan cliArgScan, cfg *config.Config) []string {
	if scan.VersionFlag {
		return nil
	}
	if promoted := c.promotedAPIName(); promoted != "" {
		if !c.promotedAPICommandMetadataNeeded(scan) {
			return nil
		}
		return []string{promoted}
	}
	if scan.GeneratedAPICommandTree {
		if scan.FirstCommand != "" && !isBuiltinCommandName(scan.FirstCommand) {
			if _, ok := cfg.APIs[scan.FirstCommand]; ok {
				return []string{scan.FirstCommand}
			}
		}
		return sortedAPINames(cfg)
	}
	if scan.FirstCommand == "" {
		return sortedAPINames(cfg)
	}
	if _, ok := cfg.APIs[scan.FirstCommand]; ok {
		return []string{scan.FirstCommand}
	}
	return nil
}

func sortedAPINames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.APIs))
	for name := range cfg.APIs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func flagConsumesNextArg(token string) bool {
	if strings.Contains(token, "=") {
		return false
	}
	name := strings.TrimLeft(token, "-")
	if strings.HasPrefix(token, "--") {
		_, noValue := boolLikeLongFlags[name]
		return !noValue
	}
	for _, r := range name {
		if !boolLikeShortFlags[r] {
			return true
		}
	}
	return false
}

var boolLikeLongFlags = map[string]bool{
	"help": true, "version": true,
	"rsh-silent": true, "rsh-headers": true,
	"rsh-verbose": true, "rsh-insecure": true, "rsh-ignore-status-code": true,
	"rsh-no-cache": true, "rsh-no-browser": true, "rsh-no-paginate": true,
	"rsh-collect": true,
}

var boolLikeShortFlags = map[rune]bool{
	'S': true, 'v': true,
}
