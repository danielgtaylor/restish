package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/cache"
	internalplugin "github.com/rest-sh/restish/v2/internal/plugin"
	"github.com/rest-sh/restish/v2/internal/request"
	"github.com/rest-sh/restish/v2/internal/secrets"
	"github.com/rest-sh/restish/v2/internal/spec"
	"github.com/spf13/cobra"
)

func (c *CLI) addDoctorCommand(root *cobra.Command) {
	doctorCmd := &cobra.Command{
		Use:     "doctor",
		Short:   "Diagnose Restish configuration and runtime paths",
		Long:    doctorLong,
		GroupID: rootGroupUtility,
		Example: fmt.Sprintf(`  %s doctor
  %s doctor -o json
  %s doctor api demo --check-network`, c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: usageNoArgs,
		RunE: c.runDoctor,
	}
	doctorCmd.AddCommand(&cobra.Command{
		Use:   "api <name>",
		Short: "Diagnose a registered API",
		Long:  doctorAPILong,
		Example: fmt.Sprintf(`  %s doctor api demo
  %s doctor api demo --check-network`, c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: usageExactArgs(1),
		RunE: c.runDoctorAPI,
	})
	doctorAPI := doctorCmd.Commands()[0]
	doctorAPI.Flags().Bool("check-network", false, "Make a bounded network request to check API reachability")
	doctorCmd.AddCommand(&cobra.Command{
		Use:     "plugin <name>",
		Short:   "Diagnose a Restish plugin executable",
		Long:    doctorPluginLong,
		Example: fmt.Sprintf("  %s doctor plugin mcp", c.commandNameOrDefault()),
		Args:    usageExactArgs(1),
		RunE:    c.runDoctorPlugin,
	})
	root.AddCommand(doctorCmd)
}

type doctorConfigParseReport struct {
	Status        string                     `json:"status"`
	APICount      int                        `json:"api_count,omitempty"`
	Error         string                     `json:"error,omitempty"`
	UnknownFields []doctorUnknownFieldReport `json:"unknown_fields,omitempty"`
}

type doctorUnknownFieldReport struct {
	Path       string `json:"path"`
	Field      string `json:"field,omitempty"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

type doctorPermissionReport struct {
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

type doctorShellSetupReport struct {
	Status string `json:"status"`
	Shell  string `json:"shell,omitempty"`
	Source string `json:"source,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

type doctorContentTypeReport struct {
	Name      string   `json:"name"`
	MIMETypes []string `json:"mime_types"`
	Suffixes  []string `json:"suffixes,omitempty"`
	Quality   float32  `json:"quality"`
}

type doctorInstalledPluginReport struct {
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	Path         string   `json:"path"`
	Capabilities []string `json:"capabilities,omitempty"`
	Commands     []string `json:"commands,omitempty"`
	Formatters   []string `json:"formatters,omitempty"`
	Loaders      []string `json:"loaders,omitempty"`
}

type doctorRuntimeReport struct {
	Version        string `json:"version"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	StdoutTerminal bool   `json:"stdout_terminal"`
	ColorEnabled   bool   `json:"color_enabled"`
}

type doctorCacheSummaryReport struct {
	Directory   string `json:"directory"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	Size        string `json:"size,omitempty"`
	Entries     int    `json:"entries,omitempty"`
	OldestEntry string `json:"oldest_entry,omitempty"`
	Error       string `json:"error,omitempty"`
}

type doctorThemeReport struct {
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
}

type doctorAPIInventoryReport struct {
	Count int      `json:"count"`
	Names []string `json:"names,omitempty"`
}

type doctorRootReport struct {
	ConfigFile            string                        `json:"config_file"`
	Runtime               doctorRuntimeReport           `json:"runtime"`
	ConfigParse           doctorConfigParseReport       `json:"config_parse"`
	ConfigPermissions     doctorPermissionReport        `json:"config_permissions"`
	HTTPCache             string                        `json:"http_cache"`
	HTTPCacheSummary      doctorCacheSummaryReport      `json:"http_cache_summary"`
	SpecCache             string                        `json:"spec_cache"`
	TokenCache            string                        `json:"token_cache"`
	TokenCachePermissions doctorPermissionReport        `json:"token_cache_permissions"`
	Theme                 doctorThemeReport             `json:"theme"`
	APIs                  doctorAPIInventoryReport      `json:"apis"`
	PluginDirectory       string                        `json:"plugin_directory"`
	InstalledPlugins      []doctorInstalledPluginReport `json:"installed_plugins"`
	ContentTypes          []doctorContentTypeReport     `json:"content_types"`
	ShellSetup            doctorShellSetupReport        `json:"shell_setup"`
}

type doctorStatusReport struct {
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	FetchedAt string `json:"fetched_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type doctorGeneratedOperationsReport struct {
	Status    string   `json:"status"`
	Count     int      `json:"count,omitempty"`
	Stale     bool     `json:"stale,omitempty"`
	FetchedAt string   `json:"fetched_at,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	Issues    []string `json:"issues,omitempty"`
}

type doctorAuthReport struct {
	Status  string   `json:"status"`
	Sources []string `json:"sources,omitempty"`
	Issues  []string `json:"issues,omitempty"`
	Hint    string   `json:"hint,omitempty"`
}

type doctorReachabilityReport struct {
	Status     string `json:"status"`
	Checked    bool   `json:"checked"`
	Reachable  bool   `json:"reachable,omitempty"`
	Method     string `json:"method,omitempty"`
	HTTPStatus string `json:"http_status,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
	Note       string `json:"note,omitempty"`
}

type doctorAPIReport struct {
	API                 string                          `json:"api"`
	Registered          bool                            `json:"registered"`
	BaseURL             string                          `json:"base_url,omitempty"`
	SpecURL             string                          `json:"spec_url,omitempty"`
	SpecFiles           []string                        `json:"spec_files,omitempty"`
	SpecCache           doctorStatusReport              `json:"spec_cache"`
	GeneratedOperations doctorGeneratedOperationsReport `json:"generated_operations"`
	OpenAPIXCLI         *spec.XCLIExtensionReport       `json:"openapi_x_cli_extensions,omitempty"`
	Auth                doctorAuthReport                `json:"auth"`
	Reachability        doctorReachabilityReport        `json:"reachability"`
}

type doctorOperationInfo struct {
	Set         spec.OperationSet
	CacheStatus spec.OperationCacheStatus
	Available   bool
	Cached      bool
}

type doctorManifestReport struct {
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	Name         string `json:"name,omitempty"`
	Version      string `json:"version,omitempty"`
	Capabilities string `json:"capabilities,omitempty"`
	Commands     string `json:"commands,omitempty"`
	APIVersion   int    `json:"api_version,omitempty"`
}

type doctorPluginReport struct {
	Plugin     string               `json:"plugin"`
	Path       string               `json:"path"`
	Found      bool                 `json:"found"`
	Executable bool                 `json:"executable,omitempty"`
	Error      string               `json:"error,omitempty"`
	Manifest   doctorManifestReport `json:"manifest"`
}

func (c *CLI) runDoctor(cmd *cobra.Command, args []string) error {
	jsonOutput, err := doctorJSON(cmd)
	if err != nil {
		return err
	}
	if jsonOutput {
		return c.writeDoctorJSON(c.doctorRootReport())
	}
	out := c.doctorTextOutput()
	style := humanTextStyleFor(out)
	runtimeReport := c.doctorRuntimeReport()
	fmt.Fprintf(out, "Restish version: %s\n", runtimeReport.Version)
	fmt.Fprintf(out, "Platform: %s/%s\n", runtimeReport.GOOS, runtimeReport.GOARCH)
	fmt.Fprintf(out, "Terminal: stdout=%t color=%t\n", runtimeReport.StdoutTerminal, runtimeReport.ColorEnabled)
	cfgPath := c.configFilePath()
	fmt.Fprintf(out, "Config file: %s\n", cfgPath)
	var loadedCfg *config.Config
	if cfg, err := c.loadConfig(); err != nil {
		fmt.Fprintf(out, "Config parse: %s\n  %v\n", style.error("invalid"), err)
		c.printConfigDiagnostics(out, cfgPath)
	} else if err := c.validateConfigRuntime(cfg); err != nil {
		fmt.Fprintf(out, "Config parse: %s\n  %v\n", style.error("invalid"), err)
		c.printConfigDiagnostics(out, cfgPath)
	} else {
		loadedCfg = cfg
		apiCount := 0
		if cfg.APIs != nil {
			apiCount = len(cfg.APIs)
		}
		fmt.Fprintf(out, "Config parse: %s (%d APIs)\n", style.ok("ok"), apiCount)
		c.printConfigDiagnostics(out, cfgPath)
	}
	c.printDoctorTheme(out, style, loadedCfg)
	c.printDoctorAPIInventory(out, style, loadedCfg)
	if insecure, err := config.ConfigFileHasInsecurePermissions(cfgPath); err != nil {
		fmt.Fprintf(out, "Config permissions: %s (%v)\n", style.warn("unknown"), err)
	} else if insecure {
		fmt.Fprintf(out, "Config permissions: %s (%s)\n", style.error("insecure"), style.hint("run chmod 600 "+cfgPath))
	} else {
		fmt.Fprintf(out, "Config permissions: %s\n", style.ok("ok"))
	}
	cacheSummary := c.doctorCacheSummaryReport()
	fmt.Fprintf(out, "HTTP cache: %s\n", cacheSummary.Directory)
	if cacheSummary.Error != "" {
		fmt.Fprintf(out, "HTTP cache summary: %s (%s)\n", style.warn("unavailable"), cacheSummary.Error)
	} else {
		fmt.Fprintf(out, "HTTP cache summary: %s, %d entries\n", cacheSummary.Size, cacheSummary.Entries)
	}
	fmt.Fprintf(out, "Spec cache: %s\n", c.specCacheDir())
	tokenCachePath := c.tokenCachePath()
	fmt.Fprintf(out, "Token cache: %s\n", tokenCachePath)
	if insecure, err := config.ConfigFileHasInsecurePermissions(tokenCachePath); err != nil {
		fmt.Fprintf(out, "Token cache permissions: %s (%v)\n", style.warn("unknown"), err)
	} else if insecure {
		fmt.Fprintf(out, "Token cache permissions: %s (%s)\n", style.error("insecure"), style.hint("run chmod 600 "+tokenCachePath+" before the next OAuth request"))
	} else {
		fmt.Fprintf(out, "Token cache permissions: %s\n", style.ok("ok"))
	}
	fmt.Fprintf(out, "Plugin directory: %s\n", c.pluginDir())
	c.printInstalledPlugins(out, style)
	fmt.Fprintf(out, "Content types: %s\n", strings.Join(c.doctorContentTypeNames(), ", "))
	c.printShellSetupDiagnostic(out, style)
	return nil
}

func (c *CLI) runDoctorAPI(cmd *cobra.Command, args []string) error {
	jsonOutput, err := doctorJSON(cmd)
	if err != nil {
		return err
	}
	if jsonOutput {
		report := c.doctorAPIReport(cmd, args[0])
		if err := c.writeDoctorJSON(report); err != nil {
			return err
		}
		if !report.Registered {
			return &ExitCodeError{Code: 2}
		}
		return nil
	}
	out := c.doctorTextOutput()
	style := humanTextStyleFor(out)
	cfg, err := c.loadConfig()
	if err != nil {
		fmt.Fprintf(out, "Config parse: %s\n  %v\n", style.error("invalid"), err)
		return nil
	}
	name := args[0]
	api := cfg.APIs[name]
	if api == nil {
		fmt.Fprintf(out, "API %q: %s\n", name, style.error("not registered"))
		return &ExitCodeError{Code: 2}
	}
	fmt.Fprintf(out, "API %q: %s\n", name, style.ok("registered"))
	fmt.Fprintf(out, "Base URL: %s\n", api.BaseURL)
	if api.SpecURL != "" {
		fmt.Fprintf(out, "Spec URL: %s\n", api.SpecURL)
	}
	if len(api.SpecFiles) > 0 {
		fmt.Fprintf(out, "Spec files: %v\n", api.SpecFiles)
	}
	profileName := c.profileFromCmd(cmd)
	opInfo := c.doctorOperationSetStatus(requestContext(cmd), name, api, profileName)
	if _, ok := configFileExists(filepath.Join(c.specCacheDir(), c.apiStateName(name)+".cbor")); ok {
		if opInfo.Cached && opInfo.CacheStatus.Stale {
			fmt.Fprintf(out, "Spec cache: %s (last synced %s, expired %s)\n", style.warn("stale"), formatCacheTime(opInfo.CacheStatus.FetchedAt), formatCacheTime(opInfo.CacheStatus.ExpiresAt))
		} else if opInfo.Cached {
			fmt.Fprintf(out, "Spec cache: %s (last synced %s, expires %s)\n", style.ok("fresh"), formatCacheTime(opInfo.CacheStatus.FetchedAt), formatCacheTime(opInfo.CacheStatus.ExpiresAt))
		} else {
			fmt.Fprintf(out, "Spec cache: %s\n", style.ok("present"))
		}
	} else {
		fmt.Fprintf(out, "Spec cache: %s (%s)\n", style.warn("missing"), style.hint("run \"restish api sync "+name+"\""))
	}
	if opInfo.Available {
		if opInfo.Cached && opInfo.CacheStatus.Stale {
			fmt.Fprintf(out, "Generated operations: %d %s (%s; %s)\n", len(opInfo.Set.Operations), style.ok("available"), style.warn("stale"), style.hint("refresh with \"restish api sync "+name+"\""))
		} else {
			fmt.Fprintf(out, "Generated operations: %d %s\n", len(opInfo.Set.Operations), style.ok("available"))
		}
		for _, issue := range operationSecurityIssues(opInfo.Set.Operations) {
			fmt.Fprintf(out, "  %s: %s\n", style.error("Issue"), strings.ReplaceAll(issue, "\n", "\n    "))
		}
		printXCLIExtensionDoctorDetails(out, style, opInfo.Set.XCLIExtensions)
	} else {
		fmt.Fprintf(out, "Generated operations: %s (%s)\n", style.warn("unavailable"), style.hint("run \"restish api sync "+name+"\""))
	}
	if auth := c.doctorAuthForProfile(name, profileName, profileForName(api, profileName)); auth.Status == "configured" {
		if len(auth.Sources) > 0 {
			fmt.Fprintf(out, "Auth: %s (%s)\n", style.ok("configured"), strings.Join(auth.Sources, ", "))
		} else {
			fmt.Fprintf(out, "Auth: %s\n", style.ok("configured"))
		}
	} else if auth.Status == "configured-but-unresolved" {
		fmt.Fprintf(out, "Auth: %s%s", style.error("configured but unresolved"), formatAuthIssues(auth.Issues))
		if len(auth.Sources) > 0 {
			fmt.Fprintf(out, " (%s)", strings.Join(auth.Sources, ", "))
		}
		fmt.Fprintln(out)
	} else {
		fmt.Fprintf(out, "Auth: %s\n", style.warn("no profile auth configured"))
	}
	fmt.Fprintf(out, "Auth details: restish api auth inspect %s\n", name)
	checkNetwork, _ := cmd.Flags().GetBool("check-network")
	if checkNetwork {
		c.printAPIReachability(out, style, c.checkAPIReachability(requestContext(cmd), name, effectiveProfileBaseURL(api, profileName), api, profileName))
	} else {
		fmt.Fprintf(out, "Reachability: %s (%s)\n", style.warn("skipped"), style.hint("use --check-network"))
	}
	return nil
}

func (c *CLI) runDoctorPlugin(cmd *cobra.Command, args []string) error {
	jsonOutput, err := doctorJSON(cmd)
	if err != nil {
		return err
	}
	if jsonOutput {
		report := c.doctorPluginReport(args[0])
		if err := c.writeDoctorJSON(report); err != nil {
			return err
		}
		if !report.Found {
			return &ExitCodeError{Code: 2}
		}
		return nil
	}
	out := c.doctorTextOutput()
	style := humanTextStyleFor(out)
	name := args[0]
	path := c.resolveDoctorPluginPath(name)
	info, err := os.Stat(path)
	if runtime.GOOS == "windows" && errors.Is(err, os.ErrNotExist) && filepath.Ext(path) == "" {
		if exeInfo, exeErr := os.Stat(path + ".exe"); exeErr == nil {
			path += ".exe"
			info = exeInfo
			err = nil
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(out, "Plugin %q: %s at %s\n", name, style.error("not found"), path)
		return &ExitCodeError{Code: 2}
	}
	if err != nil {
		fmt.Fprintf(out, "Plugin %q: %s: %v\n", name, style.error("stat failed"), err)
		return nil
	}
	fmt.Fprintf(out, "Plugin %q: %s at %s\n", name, style.ok("found"), path)
	if info.Mode()&0o111 == 0 {
		fmt.Fprintf(out, "Executable: %s\n", style.error("no"))
	} else {
		fmt.Fprintf(out, "Executable: %s\n", style.ok("yes"))
	}
	manifest, err := internalplugin.LoadManifest(path, diagnosticPrefixWriter(c.Stderr))
	if err != nil {
		fmt.Fprintf(out, "Manifest: %s (%v)\n", style.error("invalid"), err)
		return nil
	}
	fmt.Fprintf(out, "Manifest: %s %s\n", manifest.Name, manifest.Version)
	fmt.Fprintf(out, "Declared capabilities: %s\n", pluginCapabilitySummary(*manifest))
	if pluginDeclaresHook(*manifest, "command") {
		commands := c.doctorCommandPluginNames(path)
		if len(commands) > 0 {
			fmt.Fprintf(out, "Commands: %s\n", strings.Join(commands, ", "))
		}
	}
	fmt.Fprintf(out, "Protocol startup: %s (API v%d)\n", style.ok("ok"), manifest.RestishAPIVersion)
	return nil
}

func (c *CLI) doctorTextOutput() io.Writer {
	if c.doctorStdoutIsTerminal() {
		return c.Stderr
	}
	fmt.Fprintln(c.Stderr, "Tip: use -o json for machine-readable output.")
	return c.Stdout
}

func (c *CLI) doctorStdoutIsTerminal() bool {
	return c.stdoutIsTerminal()
}

func doctorJSON(cmd *cobra.Command) (bool, error) {
	return commandJSONOutputRequested(cmd)
}

func (c *CLI) writeDoctorJSON(report any) error {
	return c.writePrettyJSON(report)
}

func (c *CLI) doctorRootReport() doctorRootReport {
	cfgPath := c.configFilePath()
	return doctorRootReport{
		ConfigFile:            cfgPath,
		Runtime:               c.doctorRuntimeReport(),
		ConfigParse:           c.doctorConfigParseReport(cfgPath),
		ConfigPermissions:     doctorFilePermissionReport(cfgPath, "run chmod 600 "+cfgPath),
		HTTPCache:             c.cacheDir(),
		HTTPCacheSummary:      c.doctorCacheSummaryReport(),
		SpecCache:             c.specCacheDir(),
		TokenCache:            c.tokenCachePath(),
		TokenCachePermissions: doctorFilePermissionReport(c.tokenCachePath(), "run chmod 600 "+c.tokenCachePath()+" before the next OAuth request"),
		Theme:                 c.doctorThemeReport(),
		APIs:                  c.doctorAPIInventoryReport(),
		PluginDirectory:       c.pluginDir(),
		InstalledPlugins:      c.doctorInstalledPluginsReport(),
		ContentTypes:          c.doctorContentTypesReport(),
		ShellSetup:            doctorShellSetupReportValue(),
	}
}

func (c *CLI) printInstalledPlugins(out io.Writer, style humanTextStyle) {
	plugins := c.doctorInstalledPluginsReport()
	if len(plugins) == 0 {
		fmt.Fprintf(out, "Installed plugins: %s\n", style.warn("none"))
		return
	}
	fmt.Fprintln(out, "Installed plugins:")
	for _, p := range plugins {
		parts := []string{style.key(p.Name)}
		if p.Version != "" {
			parts = append(parts, p.Version)
		}
		if summary := installedPluginCapabilitySummary(p); summary != "" {
			parts = append(parts, "capabilities: "+summary)
		}
		fmt.Fprintf(out, "  %s\n", strings.Join(parts, " "))
		if len(p.Commands) > 0 {
			fmt.Fprintf(out, "    %s %s\n", style.key("commands:"), strings.Join(p.Commands, ", "))
		}
	}
}

func (c *CLI) doctorRuntimeReport() doctorRuntimeReport {
	return doctorRuntimeReport{
		Version:        c.currentVersion(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		StdoutTerminal: c.stdoutIsTerminal(),
		ColorEnabled:   humanTextStyleFor(c.Stdout).color,
	}
}

func (c *CLI) doctorCacheSummaryReport() doctorCacheSummaryReport {
	dir := c.cacheDir()
	report := doctorCacheSummaryReport{Directory: dir}
	dc, err := cache.New(dir, cache.DefaultMaxBytes, "")
	if err != nil {
		report.Error = err.Error()
		return report
	}
	info, err := dc.Info()
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.SizeBytes = info.SizeBytes
	report.Size = formatBytes(info.SizeBytes)
	report.Entries = info.EntryCount
	if !info.OldestEntry.IsZero() {
		report.OldestEntry = info.OldestEntry.Format(time.RFC3339)
	}
	return report
}

func (c *CLI) doctorThemeReport() doctorThemeReport {
	cfg := c.cfg
	if cfg == nil {
		return doctorThemeReport{Status: "default"}
	}
	if cfg.ThemeSource != "" {
		return doctorThemeReport{Status: "configured", Source: cfg.ThemeSource}
	}
	if len(cfg.Theme) > 0 {
		return doctorThemeReport{Status: "configured"}
	}
	return doctorThemeReport{Status: "default"}
}

func (c *CLI) printDoctorTheme(out io.Writer, style humanTextStyle, cfg *config.Config) {
	report := doctorThemeReport{Status: "default"}
	if cfg != nil {
		if cfg.ThemeSource != "" {
			report = doctorThemeReport{Status: "configured", Source: cfg.ThemeSource}
		} else if len(cfg.Theme) > 0 {
			report = doctorThemeReport{Status: "configured"}
		}
	}
	if report.Source != "" {
		fmt.Fprintf(out, "Theme: %s (%s)\n", style.ok(report.Status), report.Source)
		return
	}
	if report.Status == "configured" {
		fmt.Fprintf(out, "Theme: %s\n", style.ok(report.Status))
		return
	}
	fmt.Fprintf(out, "Theme: %s\n", style.ok(report.Status))
}

func (c *CLI) doctorAPIInventoryReport() doctorAPIInventoryReport {
	cfg := c.cfg
	if cfg == nil {
		return doctorAPIInventoryReport{}
	}
	names := make([]string, 0, len(cfg.APIs))
	for name := range cfg.APIs {
		names = append(names, name)
	}
	sort.Strings(names)
	return doctorAPIInventoryReport{Count: len(names), Names: names}
}

func (c *CLI) printDoctorAPIInventory(out io.Writer, style humanTextStyle, cfg *config.Config) {
	if cfg == nil || len(cfg.APIs) == 0 {
		fmt.Fprintf(out, "APIs: %s\n", style.warn("none"))
		return
	}
	names := make([]string, 0, len(cfg.APIs))
	for name := range cfg.APIs {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintf(out, "APIs: %d (%s)\n", len(names), strings.Join(names, ", "))
}

func (c *CLI) doctorInstalledPluginsReport() []doctorInstalledPluginReport {
	out := make([]doctorInstalledPluginReport, 0, len(c.plugins))
	for _, p := range c.plugins {
		commands := []string(nil)
		if pluginDeclaresHook(p.Manifest, "command") {
			commands = c.doctorCommandPluginNames(p.Path)
		}
		out = append(out, doctorInstalledPluginReport{
			Name:         p.Manifest.Name,
			Version:      p.Manifest.Version,
			Path:         p.Path,
			Capabilities: append([]string(nil), p.Manifest.Hooks...),
			Commands:     commands,
			Formatters:   append([]string(nil), p.Manifest.FormatterNames...),
			Loaders:      append([]string(nil), p.Manifest.LoaderContentTypes...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Path < out[j].Path
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func installedPluginCapabilitySummary(p doctorInstalledPluginReport) string {
	m := internalplugin.Manifest{
		Name:               p.Name,
		Version:            p.Version,
		Hooks:              p.Capabilities,
		FormatterNames:     p.Formatters,
		LoaderContentTypes: p.Loaders,
	}
	return pluginCapabilitySummary(m)
}

func (c *CLI) doctorCommandPluginNames(path string) []string {
	ctx := c.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	decls, err := loadCommandPluginCommands(ctx, path)
	if err != nil {
		c.warnf("plugin %s: %v", filepath.Base(path), err)
		return nil
	}
	names := make([]string, 0, len(decls))
	for _, decl := range decls {
		if decl.Name != "" {
			names = append(names, decl.Name)
		}
	}
	sort.Strings(names)
	return names
}

func (c *CLI) doctorContentTypesReport() []doctorContentTypeReport {
	if c.content == nil {
		return nil
	}
	contentTypes := c.content.ContentTypes()
	out := make([]doctorContentTypeReport, 0, len(contentTypes))
	for _, ct := range contentTypes {
		out = append(out, doctorContentTypeReport{
			Name:      ct.Name,
			MIMETypes: append([]string(nil), ct.MIMETypes...),
			Suffixes:  append([]string(nil), ct.Suffixes...),
			Quality:   ct.Quality,
		})
	}
	return out
}

func (c *CLI) doctorContentTypeNames() []string {
	report := c.doctorContentTypesReport()
	if len(report) == 0 {
		return []string{"none"}
	}
	names := make([]string, 0, len(report))
	for _, ct := range report {
		if ct.Name != "" {
			names = append(names, ct.Name)
		}
	}
	if len(names) == 0 {
		return []string{"none"}
	}
	return names
}

func (c *CLI) doctorConfigParseReport(path string) doctorConfigParseReport {
	report := doctorConfigParseReport{Status: "ok"}
	if cfg, err := c.loadConfig(); err != nil {
		report.Status = "invalid"
		report.Error = err.Error()
	} else if cfg != nil {
		if cfg.APIs != nil {
			report.APICount = len(cfg.APIs)
		}
		if err := c.validateConfigRuntime(cfg); err != nil {
			report.Status = "invalid"
			report.Error = err.Error()
		}
	}
	if diags, err := config.DiagnoseConfig(path); err == nil {
		for _, diag := range diags.UnknownFields {
			report.UnknownFields = append(report.UnknownFields, doctorUnknownFieldReport{
				Path:       diag.Path,
				Field:      diag.Field,
				Line:       diag.Line,
				Column:     diag.Column,
				Suggestion: diag.Suggestion,
				Hint:       diag.Hint,
			})
		}
	}
	return report
}

func doctorFilePermissionReport(path, remediation string) doctorPermissionReport {
	if insecure, err := config.ConfigFileHasInsecurePermissions(path); err != nil {
		return doctorPermissionReport{Status: "unknown", Error: err.Error()}
	} else if insecure {
		return doctorPermissionReport{Status: "insecure", Remediation: remediation}
	}
	return doctorPermissionReport{Status: "ok"}
}

func doctorShellSetupReportValue() doctorShellSetupReport {
	shell, source := detectRunningShell()
	if shell == "" {
		return doctorShellSetupReport{Status: "unknown"}
	}
	if _, ok := shellSetups[shell]; !ok {
		return doctorShellSetupReport{Status: "unsupported", Shell: shell, Source: source}
	}
	hint := fmt.Sprintf("run `restish shell setup %s` if glob expansion causes trouble", shell)
	if source == "$SHELL" {
		hint += " (detected via $SHELL)"
	}
	return doctorShellSetupReport{Status: "recommended", Shell: shell, Source: source, Hint: hint}
}

func (c *CLI) doctorAPIReport(cmd *cobra.Command, name string) doctorAPIReport {
	report := doctorAPIReport{
		API:                 name,
		SpecCache:           doctorStatusReport{Status: "missing"},
		GeneratedOperations: doctorGeneratedOperationsReport{Status: "unavailable"},
		Auth:                doctorAuthReport{Status: "unknown"},
		Reachability:        doctorReachabilityReport{Status: "skipped", Checked: false, Note: "use --check-network"},
	}
	cfg, err := c.loadConfig()
	if err != nil {
		report.SpecCache = doctorStatusReport{Status: "unknown", Error: err.Error()}
		report.GeneratedOperations = doctorGeneratedOperationsReport{Status: "unknown"}
		report.Auth = doctorAuthReport{Status: "unknown"}
		return report
	}
	api := cfg.APIs[name]
	if api == nil {
		report.Auth = doctorAuthReport{Status: "none"}
		return report
	}
	report.Registered = true
	report.BaseURL = api.BaseURL
	report.SpecURL = api.SpecURL
	report.SpecFiles = append([]string(nil), api.SpecFiles...)
	profileName := c.profileFromCmd(cmd)
	opInfo := c.doctorOperationSetStatus(requestContext(cmd), name, api, profileName)
	if _, ok := configFileExists(filepath.Join(c.specCacheDir(), c.apiStateName(name)+".cbor")); ok {
		report.SpecCache = doctorStatusReport{Status: "present"}
		if opInfo.Cached {
			report.SpecCache.FetchedAt = opInfo.CacheStatus.FetchedAt.Format(time.RFC3339)
			report.SpecCache.ExpiresAt = opInfo.CacheStatus.ExpiresAt.Format(time.RFC3339)
			if opInfo.CacheStatus.Stale {
				report.SpecCache.Status = "stale"
			} else {
				report.SpecCache.Status = "fresh"
			}
		}
	}
	if opInfo.Available {
		report.GeneratedOperations = doctorGeneratedOperationsReport{
			Status: "available",
			Count:  len(opInfo.Set.Operations),
			Issues: operationSecurityIssues(opInfo.Set.Operations),
		}
		if !opInfo.Set.XCLIExtensions.Empty() {
			report.OpenAPIXCLI = &opInfo.Set.XCLIExtensions
		}
		if opInfo.Cached {
			report.GeneratedOperations.Stale = opInfo.CacheStatus.Stale
			report.GeneratedOperations.FetchedAt = opInfo.CacheStatus.FetchedAt.Format(time.RFC3339)
			report.GeneratedOperations.ExpiresAt = opInfo.CacheStatus.ExpiresAt.Format(time.RFC3339)
		}
	}
	report.Auth = c.doctorAuthForProfile(name, profileName, profileForName(api, profileName))
	report.Auth.Hint = fmt.Sprintf("run `restish api auth inspect %s` for credential coverage and auth material", name)
	checkNetwork, _ := cmd.Flags().GetBool("check-network")
	if checkNetwork {
		report.Reachability = c.checkAPIReachability(requestContext(cmd), name, effectiveProfileBaseURL(api, profileName), api, profileName)
	}
	return report
}

func (c *CLI) doctorOperationSetStatus(ctx context.Context, apiName string, apiCfg *config.APIConfig, profileName string) doctorOperationInfo {
	if set, status, ok := c.cachedOperationSetStatusForAPI(apiName, apiCfg, profileName); ok {
		return doctorOperationInfo{
			Set:         set,
			CacheStatus: status,
			Available:   true,
			Cached:      true,
		}
	}
	if apiCfg == nil || !spec.HasLocalSpecFiles(apiCfg.SpecFiles) {
		return doctorOperationInfo{}
	}
	set, ok, err := c.operationSetForAPI(ctx, apiName, apiCfg, profileName, false)
	if err != nil || !ok {
		return doctorOperationInfo{}
	}
	return doctorOperationInfo{
		Set:       set,
		Available: true,
	}
}

func (c *CLI) doctorAuthForProfile(apiName, profileName string, prof *config.ProfileConfig) doctorAuthReport {
	if prof == nil {
		return doctorAuthReport{Status: "none"}
	}
	var sources []string
	var readiness []authReadiness
	if prof.Auth != nil || prof.AuthRef != "" {
		sources = append(sources, "profile_auth")
		_, ready, _ := c.profileAuthReadiness(apiName, profileName, prof)
		readiness = append(readiness, ready)
	}
	if len(prof.Credentials) > 0 {
		sources = append(sources, "credentials")
		for id, credential := range prof.Credentials {
			_, ready, _ := c.credentialReadiness(apiName, profileName, id, credential)
			readiness = append(readiness, ready)
		}
	}
	sources = append(sources, profileCredentialSettingSources(prof)...)
	if len(sources) == 0 {
		return doctorAuthReport{Status: "none"}
	}
	issues := authReadinessIssues(readiness...)
	if len(issues) > 0 {
		return doctorAuthReport{Status: "configured-but-unresolved", Sources: sources, Issues: issues}
	}
	return doctorAuthReport{Status: "configured", Sources: sources}
}

func (c *CLI) doctorPluginReport(name string) doctorPluginReport {
	path := c.resolveDoctorPluginPath(name)
	report := doctorPluginReport{
		Plugin:   name,
		Path:     path,
		Manifest: doctorManifestReport{Status: "not_checked"},
	}
	info, err := os.Stat(path)
	if runtime.GOOS == "windows" && errors.Is(err, os.ErrNotExist) && filepath.Ext(path) == "" {
		if exeInfo, exeErr := os.Stat(path + ".exe"); exeErr == nil {
			path += ".exe"
			report.Path = path
			info = exeInfo
			err = nil
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		report.Error = "not found"
		return report
	}
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.Found = true
	report.Executable = info.Mode()&0o111 != 0
	manifest, err := internalplugin.LoadManifest(path, diagnosticPrefixWriter(c.Stderr))
	if err != nil {
		report.Manifest = doctorManifestReport{Status: "invalid", Error: err.Error()}
		return report
	}
	report.Manifest = doctorManifestReport{
		Status:       "ok",
		Name:         manifest.Name,
		Version:      manifest.Version,
		Capabilities: pluginCapabilitySummary(*manifest),
		APIVersion:   manifest.RestishAPIVersion,
	}
	if pluginDeclaresHook(*manifest, "command") {
		report.Manifest.Commands = strings.Join(c.doctorCommandPluginNames(path), ", ")
	}
	return report
}

func (c *CLI) resolveDoctorPluginPath(name string) string {
	if filepath.IsAbs(name) || filepath.Base(name) != name {
		return name
	}
	executableName := name
	if !strings.HasPrefix(executableName, "restish-") {
		executableName = "restish-" + executableName
	}
	return filepath.Join(c.pluginDir(), executableName)
}

func configFileExists(path string) (os.FileInfo, bool) {
	info, err := os.Stat(path)
	return info, err == nil
}

func (c *CLI) printConfigDiagnostics(out io.Writer, path string) {
	diags, err := config.DiagnoseConfig(path)
	if err != nil {
		return
	}
	for _, diag := range diags.UnknownFields {
		if diag.Line > 0 {
			fmt.Fprintf(out, "Unknown field: %s at %d:%d\n", diag.Path, diag.Line, diag.Column)
		} else {
			fmt.Fprintf(out, "Unknown field: %s\n", diag.Path)
		}
		if diag.Suggestion != "" {
			fmt.Fprintf(out, "  Did you mean %q?\n", diag.Suggestion)
		}
		if diag.Hint != "" {
			fmt.Fprintf(out, "  %s\n", diag.Hint)
		}
	}
}

func (c *CLI) printShellSetupDiagnostic(out io.Writer, style humanTextStyle) {
	shell, source := detectRunningShell()
	if shell == "" {
		fmt.Fprintf(out, "Shell setup: %s\n", style.warn("unknown"))
		return
	}
	if _, ok := shellSetups[shell]; !ok {
		fmt.Fprintf(out, "Shell setup: %s %s\n", style.warn("unsupported shell"), shell)
		return
	}
	if source == "$SHELL" {
		fmt.Fprintf(out, "Shell setup: %s if glob expansion causes trouble (detected via $SHELL)\n", style.hint("run `restish shell setup "+shell+"`"))
		return
	}
	fmt.Fprintf(out, "Shell setup: %s if glob expansion causes trouble\n", style.hint("run `restish shell setup "+shell+"`"))
}

func (c *CLI) checkAPIReachability(ctx context.Context, apiName, baseURL string, apiCfg *config.APIConfig, profileName string) doctorReachabilityReport {
	if baseURL == "" {
		return doctorReachabilityReport{Status: "skipped", Checked: false, Note: "no base URL"}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	normalizedBaseURL, err := request.Normalize(baseURL, "")
	if err != nil {
		return doctorReachabilityReport{Status: "invalid_url", Checked: false, Method: http.MethodHead, Error: secrets.RedactDiagnosticURLText(err.Error())}
	}
	transport, closer, err := c.discoveryTransport(ctx, apiCfg, profileName)
	if err != nil {
		return doctorReachabilityReport{Status: "tls_config_error", Checked: false, Method: http.MethodHead, Error: err.Error(), Note: "profile TLS settings could not be resolved"}
	}
	if closer != nil {
		defer closer.Close()
	}
	baseOpts, authOpts := c.discoveryRequestOptions(ctx, apiName, apiCfg, profileName, transport)
	authOrigins := discoveryAuthOrigins(apiCfg, profileName)
	opts := baseOpts
	if discoveryAuthAllowed(authOrigins, normalizedBaseURL) {
		opts = authOpts
	}
	resp, err := c.doDiscoveryRequest(ctx, http.MethodHead, normalizedBaseURL, opts)
	if err != nil {
		return doctorReachabilityReport{Status: "failed", Checked: true, Method: http.MethodHead, Error: secrets.RedactDiagnosticURLText(err.Error())}
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	report := doctorReachabilityReport{
		Status:     "ok",
		Checked:    true,
		Method:     http.MethodHead,
		HTTPStatus: resp.Status,
		StatusCode: resp.StatusCode,
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 400:
		report.Reachable = true
	case resp.StatusCode == http.StatusMethodNotAllowed:
		report.Reachable = true
		report.Note = "HEAD not allowed, but the server responded"
	default:
		report.Status = "warn"
		if resp.StatusCode >= 500 {
			report.Note = "server error; base URL may be wrong"
		} else {
			report.Note = "HTTP error; base URL may require authentication or may be wrong"
		}
	}
	return report
}

func (c *CLI) printAPIReachability(out io.Writer, style humanTextStyle, report doctorReachabilityReport) {
	switch report.Status {
	case "skipped":
		if report.Note == "no base URL" {
			fmt.Fprintf(out, "Reachability: %s (no base URL)\n", style.warn("skipped"))
		} else {
			fmt.Fprintf(out, "Reachability: %s (%s)\n", style.warn("skipped"), style.hint("use --check-network"))
		}
	case "invalid_url":
		fmt.Fprintf(out, "Reachability: %s (%v)\n", style.error("invalid base URL"), report.Error)
	case "failed":
		fmt.Fprintf(out, "Reachability: %s (%v)\n", style.error("failed"), report.Error)
	case "ok":
		if report.StatusCode == http.StatusMethodNotAllowed {
			fmt.Fprintf(out, "Reachability: HTTP %s (%s; HEAD not allowed)\n", style.ok(report.HTTPStatus), style.ok("network ok"))
			return
		}
		fmt.Fprintf(out, "Reachability: HTTP %s\n", style.ok(report.HTTPStatus))
	case "warn":
		if report.Note != "" {
			fmt.Fprintf(out, "Reachability: HTTP %s (%s)\n", style.warn(report.HTTPStatus), report.Note)
			return
		}
		fmt.Fprintf(out, "Reachability: HTTP %s (%s)\n", style.warn(report.HTTPStatus), style.warn("warning"))
	}
}
