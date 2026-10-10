package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/spec"
	"github.com/spf13/cobra"
)

// CommandSurface controls the command tree exposed by embedded custom CLIs.
// The zero value preserves the stock Restish command surface.
type CommandSurface struct {
	// PromotedAPI promotes generated operations for this configured API to the
	// root command.
	PromotedAPI string

	// SupportCommandNamespace moves support commands under this root command.
	SupportCommandNamespace string
	// HideSupportCommands removes support commands from the custom CLI surface.
	HideSupportCommands bool

	// HTTPMethods keeps only the named generic HTTP method commands. Method
	// names are case-insensitive and exposed in lowercase.
	HTTPMethods []string

	// RegisteredAPIs keeps generated command groups for configured APIs.
	RegisteredAPIs bool

	// MetadataRefreshTimeout bounds live OpenAPI discovery for a curated
	// registered API. Zero uses Restish's short interactive refresh default.
	MetadataRefreshTimeout time.Duration

	// IgnoreUserConfig makes the embedder's default config authoritative.
	IgnoreUserConfig bool

	// DisablePlugins prevents discovery and execution of external Restish
	// subprocess plugins in a fully in-process embedded product.
	DisablePlugins bool
}

// SetCommandSurface changes the command tree exposed by an embedded CLI.
// Invalid combinations panic so mistakes fail during host application setup.
func (c *CLI) SetCommandSurface(surface CommandSurface) {
	surface.PromotedAPI = strings.TrimSpace(surface.PromotedAPI)
	surface.SupportCommandNamespace = strings.TrimSpace(surface.SupportCommandNamespace)
	for index, method := range surface.HTTPMethods {
		surface.HTTPMethods[index] = strings.ToLower(strings.TrimSpace(method))
	}
	if surface.HideSupportCommands && surface.SupportCommandNamespace != "" {
		panic("restish: CommandSurface SupportCommandNamespace and HideSupportCommands are mutually exclusive")
	}
	curated := surface.RegisteredAPIs || len(surface.HTTPMethods) > 0
	if surface.PromotedAPI == "" && !curated && (surface.SupportCommandNamespace != "" || surface.HideSupportCommands) {
		panic("restish: CommandSurface support command layout requires PromotedAPI")
	}
	if surface.PromotedAPI != "" && curated {
		panic("restish: promoted and curated command surfaces are mutually exclusive")
	}
	for _, method := range surface.HTTPMethods {
		switch method {
		case "get", "head", "post", "put", "patch", "delete":
		default:
			panic(fmt.Sprintf("restish: unsupported command-surface HTTP method %q", method))
		}
	}
	c.commandSurface = surface
}

func (c *CLI) hasCuratedCommandSurface() bool {
	return c.commandSurface.RegisteredAPIs || len(c.commandSurface.HTTPMethods) > 0
}

func (c *CLI) promotedAPIName() string {
	return c.commandSurface.PromotedAPI
}

func (c *CLI) hasPromotedAPI() bool {
	return c.promotedAPIName() != ""
}

func (c *CLI) isPromotedAPI(apiName string) bool {
	return apiName != "" && apiName == c.promotedAPIName()
}

func (c *CLI) ensurePromotedAPICommandMetadata(ctx context.Context, scan cliArgScan, cfg *config.Config) error {
	apiName := c.promotedAPIName()
	if apiName == "" || !c.promotedAPICommandMetadataNeeded(scan) {
		return nil
	}
	apiCfg := cfg.APIs[apiName]
	if apiCfg == nil {
		return fmt.Errorf("command surface: promoted API %q is not configured", apiName)
	}
	return c.ensurePromotedAPIMetadata(ctx, apiName, scan.ProfileName, apiCfg)
}

func (c *CLI) ensureCuratedAPICommandMetadata(ctx context.Context, scan cliArgScan, cfg *config.Config) error {
	if !c.commandSurface.RegisteredAPIs || scan.FirstCommand == "" {
		return nil
	}
	api := cfg.APIs[scan.FirstCommand]
	if api == nil {
		return nil
	}
	timeout := c.commandSurface.MetadataRefreshTimeout
	if timeout <= 0 {
		timeout = staleGeneratedOperationRefreshTimeout
	}
	return c.ensureAPIMetadata(ctx, scan.FirstCommand, scan.ProfileName, api, timeout)
}

func (c *CLI) ensurePromotedAPIMetadata(ctx context.Context, apiName, profileName string, apiCfg *config.APIConfig) error {
	return c.ensureAPIMetadata(ctx, apiName, profileName, apiCfg, staleGeneratedOperationRefreshTimeout)
}

func (c *CLI) ensureAPIMetadata(ctx context.Context, apiName, profileName string, apiCfg *config.APIConfig, timeout time.Duration) error {
	opts := spec.OperationOptions{
		BaseURL:         effectiveProfileBaseURL(apiCfg, profileName),
		OperationBase:   effectiveOperationBase(apiCfg, profileName),
		ServerVariables: effectiveServerVariables(apiCfg, profileName),
	}
	stateName := c.apiStateName(apiName)
	if _, status, ok := spec.LoadOperationSetFromCacheStatus(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles, opts, false); ok && !status.Stale {
		return nil
	}

	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := c.discoverSpecForProfile(refreshCtx, apiName, profileName, false, timeout); err != nil {
		if status, ok := c.promotedAPIStaleMetadataStatus(apiName, apiCfg, opts); ok {
			c.warnf("could not refresh API metadata for %q; using last synced metadata from %s: %v", apiName, formatCacheTime(status.FetchedAt), err)
			return nil
		}
		return fmt.Errorf("generated commands for promoted API %q are unavailable: %w", apiName, err)
	}
	return nil
}

func (c *CLI) promotedAPIStaleMetadataStatus(apiName string, apiCfg *config.APIConfig, opts spec.OperationOptions) (spec.OperationCacheStatus, bool) {
	stateName := c.apiStateName(apiName)
	if _, status, ok := spec.LoadOperationSetFromCacheStatus(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles, opts, true); ok {
		return status, true
	}
	return spec.RawSpecCacheStatus(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles)
}

func (c *CLI) promotedAPICommandMetadataNeeded(scan cliArgScan) bool {
	if scan.VersionFlag {
		return false
	}
	if scan.FirstCommand == "" {
		return true
	}
	if scan.FirstCommand == "help" {
		return scan.SecondCommand == "" || !c.isSupportCommandToken(scan.SecondCommand)
	}
	if scan.FirstCommand == "__complete" || scan.FirstCommand == "__completeNoDesc" {
		return true
	}
	return !c.isSupportCommandToken(scan.FirstCommand)
}

func (c *CLI) isSupportCommandToken(token string) bool {
	if token == "" || c.commandSurface.HideSupportCommands {
		return false
	}
	if ns := c.commandSurface.SupportCommandNamespace; ns != "" {
		return token == ns
	}
	return promotedSupportCommandNames()[token]
}

func promotedSupportCommandNames() map[string]bool {
	return map[string]bool{
		"auth":       true,
		"cache":      true,
		"completion": true,
		"config":     true,
		"doctor":     true,
		"version":    true,
	}
}

func (c *CLI) applyCommandSurface(root, promotedAPICmd *cobra.Command, scan cliArgScan, cfg *config.Config, originalArgs []string) error {
	apiName := c.promotedAPIName()
	if apiName == "" {
		if c.hasCuratedCommandSurface() {
			c.applyCuratedCommandSurface(root, cfg)
		}
		return nil
	}
	if promotedAPICmd == nil && c.promotedAPICommandMetadataNeeded(scan) {
		return fmt.Errorf("generated commands for promoted API %q are unavailable", apiName)
	}

	support := c.promotedSupportCommands(root, apiName)
	for _, cmd := range root.Commands() {
		root.RemoveCommand(cmd)
	}

	var promoted []*cobra.Command
	if promotedAPICmd != nil {
		root.Example = promotedAPICmd.Example
		for _, group := range promotedAPICmd.Groups() {
			if !rootCommandHasGroup(root, group.ID) {
				root.AddGroup(group)
			}
		}
		promoted = append(promoted, promotedAPICmd.Commands()...)
	}

	if err := c.checkPromotedCommandCollisions(promoted, support); err != nil {
		return err
	}

	c.addSupportCommandsForSurface(root, support)
	for _, cmd := range promoted {
		root.AddCommand(cmd)
	}
	c.installPromotedRootFallback(root, cfg, originalArgs)
	return nil
}

func (c *CLI) applyCuratedCommandSurface(root *cobra.Command, cfg *config.Config) {
	_ = root.PersistentFlags().MarkHidden("rsh-config")
	allowed := make(map[string]bool, len(c.commandSurface.HTTPMethods))
	for _, method := range c.commandSurface.HTTPMethods {
		allowed[method] = true
	}
	for _, command := range root.Commands() {
		keep := allowed[command.Name()]
		if !keep && c.commandSurface.RegisteredAPIs && cfg.APIs[command.Name()] != nil {
			keep = true
		}
		if !keep {
			root.RemoveCommand(command)
		}
	}
}

func (c *CLI) promotedSupportCommands(root *cobra.Command, apiName string) map[string]*cobra.Command {
	support := map[string]*cobra.Command{}
	if !c.commandSurface.HideSupportCommands {
		for _, cmd := range root.Commands() {
			if promotedSupportCommandNames()[cmd.Name()] {
				support[cmd.Name()] = cmd
			}
		}
		if support["completion"] != nil {
			support["completion"].Hidden = false
		}
		if support["auth"] == nil {
			support["auth"] = c.newPromotedAPIAuthCommand(apiName)
		}
		c.brandPromotedSupportCommands(support)
	}
	return support
}

func (c *CLI) brandPromotedSupportCommands(support map[string]*cobra.Command) {
	name := c.commandNameOrDefault()
	if cmd := support["config"]; cmd != nil {
		cmd.Short = fmt.Sprintf("Manage local %s configuration", name)
	}
	if cmd := support["doctor"]; cmd != nil {
		cmd.Short = fmt.Sprintf("Diagnose %s configuration and runtime paths", name)
	}
	if cmd := support["version"]; cmd != nil {
		cmd.Short = fmt.Sprintf("Print the %s version", name)
	}
}

func (c *CLI) checkPromotedCommandCollisions(promoted []*cobra.Command, support map[string]*cobra.Command) error {
	ns := c.commandSurface.SupportCommandNamespace
	for _, cmd := range promoted {
		name := cmd.Name()
		if name == "" {
			continue
		}
		if ns != "" && name == ns {
			return fmt.Errorf("command surface: promoted operation %q collides with support command namespace %q; choose another SupportCommandNamespace or hide support commands", name, ns)
		}
		if ns == "" && !c.commandSurface.HideSupportCommands && support[name] != nil {
			return fmt.Errorf("command surface: promoted operation %q collides with support command %q; set SupportCommandNamespace or HideSupportCommands", name, name)
		}
	}
	return nil
}

func (c *CLI) addSupportCommandsForSurface(root *cobra.Command, support map[string]*cobra.Command) {
	if c.commandSurface.HideSupportCommands {
		return
	}
	if ns := c.commandSurface.SupportCommandNamespace; ns != "" {
		nsCmd := &cobra.Command{
			Use:     ns,
			Short:   "CLI support commands",
			GroupID: rootGroupConfig,
			Args:    cobra.ArbitraryArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) > 0 {
					return unknownCommandError(cmd, args[0], "run "+strconvQuote(cmd.CommandPath()+" --help")+" to see support commands")
				}
				return cmd.Help()
			},
		}
		for _, name := range sortedSupportCommandNames(support) {
			cmd := support[name]
			cmd.GroupID = ""
			nsCmd.AddCommand(cmd)
		}
		root.AddCommand(nsCmd)
		return
	}
	for _, name := range sortedSupportCommandNames(support) {
		root.AddCommand(support[name])
	}
}

func sortedSupportCommandNames(commands map[string]*cobra.Command) []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *CLI) installPromotedRootFallback(root *cobra.Command, cfg *config.Config, originalArgs []string) {
	originalArgs = append([]string(nil), originalArgs...)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		if c.shouldSyncPromotedRootCommand(cmd, cfg, args) {
			return c.runSyncedPromotedAPICommand(cmd, cfg, args, originalArgs)
		}
		return unknownCommandError(cmd, args[0], "run "+strconvQuote(cmd.CommandPath()+" --help")+" to see available commands")
	}
}

func (c *CLI) shouldSyncPromotedRootCommand(cmd *cobra.Command, cfg *config.Config, args []string) bool {
	if len(args) == 0 || !looksLikeGeneratedCommandToken(args[0]) {
		return false
	}
	apiCfg := cfg.APIs[c.promotedAPIName()]
	return promotedAPIHasRefreshSource(apiCfg, c.profileFromCmd(cmd))
}

func promotedAPIHasRefreshSource(apiCfg *config.APIConfig, profileName string) bool {
	if apiCfg == nil {
		return false
	}
	return apiHasSpecSource(apiCfg) || effectiveProfileBaseURL(apiCfg, profileName) != ""
}

func (c *CLI) ensurePromotedAPIAuthOperationMetadata(cmd *cobra.Command, apiName string, args []string) error {
	operation, _ := cmd.Flags().GetString("operation")
	operation = strings.TrimSpace(operation)
	if operation == "" || promotedAPIAuthOperationValidationDeferred(cmd, args) {
		return nil
	}
	apiCfg, err := c.requireAPI(apiName)
	if err != nil {
		return err
	}
	ctx := requestContext(cmd)
	profileName := c.profileFromCmd(cmd)
	if _, ok, err := c.cachedOperationForAPI(ctx, apiName, apiCfg, profileName, operation); err != nil {
		return err
	} else if ok || !promotedAPIHasRefreshSource(apiCfg, profileName) {
		return nil
	}

	refreshCtx, cancel := context.WithTimeout(ctx, staleGeneratedOperationRefreshTimeout)
	defer cancel()
	if _, ok, err := c.operationSetForAPI(refreshCtx, apiName, apiCfg, profileName, true); err != nil {
		return fmt.Errorf("generated commands for promoted API %q are not available: %w", apiName, err)
	} else if !ok {
		return fmt.Errorf("generated commands for promoted API %q are not available", apiName)
	}
	return nil
}

func promotedAPIAuthOperationValidationDeferred(cmd *cobra.Command, args []string) bool {
	if len(args) > 0 {
		return true
	}
	credential, _ := cmd.Flags().GetString("credential")
	if strings.TrimSpace(credential) != "" {
		return true
	}
	return false
}

func (c *CLI) runSyncedPromotedAPICommand(cmd *cobra.Command, cfg *config.Config, args, originalArgs []string) error {
	apiName := c.promotedAPIName()
	apiCfg := cfg.APIs[apiName]
	profileName := c.profileFromCmd(cmd)
	refreshCtx, cancel := context.WithTimeout(cmd.Context(), staleGeneratedOperationRefreshTimeout)
	defer cancel()
	set, ok, err := c.operationSetForAPI(refreshCtx, apiName, apiCfg, profileName, true)
	if err != nil {
		return fmt.Errorf("generated commands for promoted API %q are not available: %w", apiName, err)
	}
	if !ok {
		return fmt.Errorf("generated commands for promoted API %q are not available", apiName)
	}
	apiCmd := c.buildAPICommandFromOperationSet(apiName, apiCfg, set, effectiveOperationBase(apiCfg, profileName))
	if apiCmd == nil {
		return fmt.Errorf("generated commands for promoted API %q are unavailable after sync", apiName)
	}
	root := c.newRootCmd()
	scan := cliArgScan{FirstCommand: args[0], ProfileName: profileName}
	if err := c.applyCommandSurface(root, apiCmd, scan, cfg, originalArgs); err != nil {
		return err
	}
	if !commandPathExists(root, args) {
		return unknownCommandError(root, args[0], "run "+strconvQuote(cmd.CommandPath()+" --help")+" to see generated operations")
	}
	if len(originalArgs) == 0 {
		originalArgs = append([]string{c.commandNameOrDefault()}, args...)
	}
	return c.executeRoot(cmd.Context(), root, originalArgs)
}

func (c *CLI) newPromotedAPIAuthCommand(apiName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "auth",
		Short:   "Inspect API auth credentials",
		Long:    apiAuthLong,
		GroupID: rootGroupConfig,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	getCmd := &cobra.Command{
		Use:   "get [credential-id]",
		Short: "Print curl-friendly auth material for the API profile",
		Long:  apiAuthGetLong,
		Args:  usageMaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runAPIAuthGet(cmd, append([]string{apiName}, args...))
		},
	}
	getCmd.Flags().String("operation", "", "Operation ID or command name to inspect")
	getCmd.Flags().Bool("print-header", false, "Print the single resolved header as 'Name: value' on stdout and exit non-zero for any non-header auth")
	getCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		return c.ensurePromotedAPIAuthOperationMetadata(cmd, apiName, args)
	}
	cmd.AddCommand(getCmd)

	headerCmd := &cobra.Command{
		Use:   "header [credential-id]",
		Short: "Print one HTTP auth header for the API profile",
		Long:  apiAuthGetLong,
		Args:  usageMaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fragment, err := c.apiAuthGetHeaderFragment(cmd, append([]string{apiName}, args...))
			if err != nil {
				return err
			}
			fmt.Fprintln(c.Stdout, fragment)
			return nil
		},
	}
	headerCmd.Flags().String("operation", "", "Operation ID or command name to inspect")
	headerCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		return c.ensurePromotedAPIAuthOperationMetadata(cmd, apiName, args)
	}
	cmd.AddCommand(headerCmd)

	inspectCmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect the auth material applied for the API profile",
		Long:  apiAuthInspectLong,
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runAPIAuthInspect(cmd, []string{apiName})
		},
	}
	inspectCmd.Flags().String("credential", "", "Credential ID to inspect instead of profile-level auth")
	inspectCmd.Flags().String("operation", "", "Operation ID or command name to inspect")
	inspectCmd.Flags().Bool("redact", false, "Redact sensitive auth values for shareable output")
	inspectCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if err := rejectResponseTransformFlags(cmd); err != nil {
			return err
		}
		return c.ensurePromotedAPIAuthOperationMetadata(cmd, apiName, args)
	}
	cmd.AddCommand(inspectCmd)

	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Delete cached API auth tokens",
		Long:  apiAuthLogoutLong,
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if authProfile, _ := cmd.Flags().GetString("auth-profile"); authProfile != "" {
				return c.runAPIAuthLogout(cmd, nil)
			}
			return c.runAPIAuthLogout(cmd, []string{apiName})
		},
	}
	addAPIAuthLogoutFlags(logoutCmd)
	cmd.AddCommand(logoutCmd)
	return cmd
}
