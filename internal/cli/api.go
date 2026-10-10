package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
	authpkg "github.com/rest-sh/restish/v2/internal/auth"
	"github.com/rest-sh/restish/v2/internal/cache"
	internalconfig "github.com/rest-sh/restish/v2/internal/config"
	"github.com/rest-sh/restish/v2/internal/output"
	"github.com/rest-sh/restish/v2/internal/request"
	"github.com/rest-sh/restish/v2/internal/spec"
	"github.com/spf13/cobra"
)

// addAPICommand registers the "api" subcommand tree on root.
func (c *CLI) addAPICommand(root *cobra.Command) {
	apiCmd := &cobra.Command{
		Use:     "api",
		Short:   "Manage registered API configurations",
		Long:    apiLong,
		GroupID: rootGroupConfig,
		Example: fmt.Sprintf(`  %s api connect demo https://api.example.com
  %s api list
  %s api set demo 'profiles.staging.base_url: https://staging.example.com'`, c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault()),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownNamedSubcommandError(cmd, "api", args[0], "")
			}
			return cmd.Help()
		},
	}
	apiCmd.AddCommand(c.newAPIAuthCommand())
	apiCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all configured APIs",
		Long:  apiListLong,
		Example: fmt.Sprintf(`  %s api list
  %s api list -o json`, c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: usageNoArgs,
		RunE: c.runAPIList,
	})
	apiCmd.AddCommand(&cobra.Command{
		Use:     "remove <name>",
		Short:   "Remove a configured API",
		Long:    apiRemoveLong,
		Example: fmt.Sprintf("  %s api remove demo", c.commandNameOrDefault()),
		Args:    usageExactArgs(1),
		RunE:    c.runAPIRemove,
	})
	syncCmd := &cobra.Command{
		Use:   "sync <name> [name...]",
		Short: "Force re-fetch of the cached OpenAPI spec for one or more named APIs",
		Long:  apiSyncLong,
		Example: fmt.Sprintf(`  %s api sync demo
  %s api sync demo --yes
  %s api sync demo --allow-cross-origin-spec
  %s api sync api-one api-two api-three`, c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: usageMinimumNArgs(1),
		RunE: c.runAPISync,
	}
	syncCmd.Flags().Bool("allow-cross-origin-spec", false, "Allow safe Link-header spec discovery from another host for this sync run")
	syncCmd.Flags().Bool("yes", false, "Accept safe api sync prompts without asking")
	apiCmd.AddCommand(syncCmd)
	connectCmd := &cobra.Command{
		Use:   "connect <name> <url> [setup-expression ...]",
		Short: "Connect Restish to an API and discover generated commands",
		Long:  apiConnectLong,
		Example: fmt.Sprintf(`  %s api connect demo https://api.example.com
  %s api connect demo https://api.example.com 'prompt.api_key: env:DEMO_API_KEY'
  %s api connect demo https://api.example.com --spec ./openapi.yaml`, c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: usageMinimumNArgs(2),
		RunE: c.runAPIConnect,
	}
	connectCmd.Flags().Bool("allow-cross-origin-spec", false, "Allow safe Link-header spec discovery from another host; private/local follow targets are still rejected")
	connectCmd.Flags().Bool("no-discover", false, "Register the API locally without network spec discovery")
	connectCmd.Flags().String("spec", "", "OpenAPI spec URL or local file to use instead of discovery")
	connectCmd.Flags().Bool("replace", false, "Replace existing profiles and reapply discovered x-cli-config defaults")
	connectCmd.Flags().Bool("yes", false, "Accept safe api connect prompts without asking")
	apiCmd.AddCommand(connectCmd)
	apiCmd.AddCommand(&cobra.Command{
		Use:                "configure <name> [url]",
		Short:              "Explain the v1 api configure replacement",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return newUsageError(c.apiConfigureMigrationError(args))
		},
	})
	apiCmd.AddCommand(&cobra.Command{
		Use:     "inspect <name>",
		Short:   "Print the config for a registered API as JSON",
		Long:    apiInspectLong,
		Example: fmt.Sprintf("  %s api inspect demo", c.commandNameOrDefault()),
		Args:    usageExactArgs(1),
		RunE:    c.runAPIInspect,
	})
	apiCmd.AddCommand(&cobra.Command{
		Use:   "set <name> <patch> [patch...]",
		Short: "Patch API config using shorthand syntax",
		Long:  apiSetLong,
		Example: fmt.Sprintf(`  %s api set demo 'profiles.default.headers[]: X-Trace-Id: abc'
  %s api set demo 'base_url: https://staging.example.com'
  %s api set demo 'profiles.prod.auth.type: oauth-client-credentials'`, c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: usageMinimumNArgs(2),
		RunE: c.runAPISet,
	})
	root.AddCommand(apiCmd)
}

func (c *CLI) apiConfigureMigrationError(args []string) error {
	commandName := c.commandNameOrDefault()
	replacement := fmt.Sprintf("  %s api connect <name> <url>", commandName)
	if len(args) == 1 && !strings.HasPrefix(args[0], "-") {
		replacement = fmt.Sprintf("  %s api connect %s <url>", commandName, args[0])
	} else if len(args) >= 2 && !strings.HasPrefix(args[0], "-") && !strings.HasPrefix(args[1], "-") {
		replacement = fmt.Sprintf("  %s api connect %s %s", commandName, args[0], args[1])
	}
	return fmt.Errorf(`api configure was a Restish v1 command and is not available in v2.

Use api connect to register an API explicitly:

%s

In many cases replacing "configure" with "connect" is enough. If the v1
command prompted for auth, profiles, or other defaults, connect first and then
adjust the API with "restish api set".

Upgrade guide: https://rest.sh/docs/getting-started/upgrade-from-v1/
Archived v1 docs: https://rest.sh/v1/`, replacement)
}

// runAPIAuthLogout deletes the token cache entry for the named API+profile.
func (c *CLI) runAPIAuthLogout(cmd *cobra.Command, args []string) error {
	authProfile, _ := cmd.Flags().GetString("auth-profile")
	tc := auth.NewTokenCache(c.tokenCachePath())
	if authProfile != "" {
		if len(args) > 0 {
			return fmt.Errorf("--auth-profile cannot be used with an API argument")
		}
		if c.cfg == nil || c.cfg.AuthProfiles == nil || c.cfg.AuthProfiles[authProfile] == nil {
			return fmt.Errorf("unknown auth profile %q", authProfile)
		}
		if err := tc.DeletePrefix("auth_profile:" + authProfile + ":"); err != nil {
			return fmt.Errorf("auth logout: %w", err)
		}
		fmt.Fprintf(c.Stdout, "Cleared auth cache for auth profile %q\n", authProfile)
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("api auth logout requires an API name or --auth-profile <name>")
	}
	apiName := args[0]
	apiCfg, err := c.requireAPI(apiName)
	if err != nil {
		return err
	}

	profileName := c.profileFromCmd(cmd)
	allProfiles, _ := cmd.Flags().GetBool("all-profiles")

	if allProfiles {
		if err := tc.DeletePrefix(c.apiStateName(apiName) + ":"); err != nil {
			return fmt.Errorf("auth logout: %w", err)
		}
		for _, prof := range apiCfg.Profiles {
			resolved, err := c.resolveProfileAuth(apiName, "", prof)
			if err != nil {
				return err
			}
			if resolved.Ref != "" {
				if err := tc.DeletePrefix("auth_profile:" + resolved.Ref + ":"); err != nil {
					return fmt.Errorf("auth logout: %w", err)
				}
			}
		}
		fmt.Fprintf(c.Stdout, "Cleared auth cache for %q (all profiles)\n", apiName)
		return nil
	}
	key := c.apiCacheNamespace(apiName, profileName)
	if err := tc.Delete(key); err != nil {
		return fmt.Errorf("auth logout: %w", err)
	}
	if prof := apiCfg.Profiles[profileName]; prof != nil {
		resolved, err := c.resolveProfileAuth(apiName, profileName, prof)
		if err != nil {
			return err
		}
		if resolved.CacheKey != "" {
			if err := tc.Delete(resolved.CacheKey); err != nil {
				return fmt.Errorf("auth logout: %w", err)
			}
		}
	}
	fmt.Fprintf(c.Stdout, "Cleared auth cache for %q (profile %q)\n", apiName, profileName)
	return nil
}

// runAPISync force-invalidates the cached spec for one or more APIs, fetches a
// fresh one for each, and persists non-profile metadata that can be discovered
// safely from it. Accepting multiple names in a single invocation allows the
// process (and any shared PKCS#11 session) to be reused across all of them,
// which means a hardware-token PIN is only requested once.
func (c *CLI) runAPISync(cmd *cobra.Command, args []string) error {
	// When all APIs share the same TLS signer (e.g. all yubikey APIs), build
	// one transport up front so the plugin subprocess — and the PKCS#11 session
	// it holds — is shared across every sync. This means the user is prompted
	// for the hardware-token PIN exactly once instead of once per API.
	var shared http.RoundTripper
	if len(args) > 1 {
		tr, closer, err := c.sharedDiscoveryTransport(cmd, args)
		if err != nil {
			return err
		}
		if tr != nil {
			shared = tr
			if closer != nil {
				defer closer.Close()
			}
		}
	}

	var errs []error
	for _, apiName := range args {
		if err := c.syncOneAPI(cmd, apiName, shared); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sharedDiscoveryTransport returns a single transport to reuse across all
// named APIs when every API in the list has identical TLS transport settings
// (signer path, signer params, client cert/key, CA cert, insecure flag, and
// TLS min version). Returns (nil, nil, nil) when configs differ or no signer
// is configured; each syncOneAPI call then builds its own transport.
func (c *CLI) sharedDiscoveryTransport(cmd *cobra.Command, apiNames []string) (http.RoundTripper, interface{ Close() error }, error) {
	ctx := requestContext(cmd)
	profileName := c.profileFromCmd(cmd)
	if profileName == "" {
		profileName = "default"
	}

	var commonKey *discoveryTransportShareKey
	var representativeOpts request.Options
	for _, apiName := range apiNames {
		apiCfg, err := c.requireAPI(apiName)
		if err != nil {
			return nil, nil, err
		}
		opts, err := c.discoveryTransportOptions(ctx, apiCfg, profileName)
		if err != nil {
			return nil, nil, err
		}
		if opts.TLSSignerPath == "" {
			return nil, nil, nil // this API uses no signer; can't share
		}
		key := discoveryTransportShareKeyFromOptions(opts)
		if commonKey == nil {
			commonKey = &key
			representativeOpts = opts
		} else if key != *commonKey {
			return nil, nil, nil // different TLS inputs; can't share
		}
	}
	if commonKey == nil {
		return nil, nil, nil
	}
	c.warnInsecureDiscoveryTransport(ctx)
	tr := request.BuildTransport(representativeOpts)
	closer, _ := tr.(interface{ Close() error })
	return tr, closer, nil
}

func (c *CLI) syncOneAPI(cmd *cobra.Command, apiName string, sharedTransport http.RoundTripper) error {
	apiCfg, err := c.requireAPI(apiName)
	if err != nil {
		return err
	}

	allowCrossOrigin, _ := cmd.Flags().GetBool("allow-cross-origin-spec")
	yes, _ := cmd.Flags().GetBool("yes")
	profileName := c.profileFromCmd(cmd)
	var transport http.RoundTripper
	if sharedTransport != nil {
		transport = sharedTransport
	} else {
		tr, closer, err := c.discoveryTransport(requestContext(cmd), apiCfg, profileName)
		if err != nil {
			return err
		}
		if closer != nil {
			defer closer.Close()
		}
		transport = tr
	}
	fetch := c.discoveryFetcher(requestContext(cmd), apiName, apiCfg, profileName)
	discCfg := spec.DiscoverConfig{
		APIName:          c.apiStateName(apiName),
		BaseURL:          effectiveProfileBaseURL(apiCfg, profileName),
		SpecURL:          apiCfg.SpecURL,
		SpecFiles:        apiCfg.SpecFiles,
		CacheDir:         c.specCacheDir(),
		OperationBase:    effectiveOperationBase(apiCfg, profileName),
		ServerVariables:  effectiveServerVariables(apiCfg, profileName),
		Version:          Version,
		Transport:        transport,
		Fetch:            fetch,
		AllowCrossOrigin: apiCfg.AllowCrossOriginSpec || allowCrossOrigin,
		ForceRefresh:     true,
		Trace:            c.discoveryTrace(cmd),
	}
	apiSpec, err := spec.Discover(requestContext(cmd), discCfg, c.loaders)
	if err != nil {
		return fmt.Errorf("api sync %q failed: %w\nhint: API registration and existing spec cache were left unchanged; check the network and spec_url, then retry api sync", apiName, err)
	}

	if apiSpec != nil {
		syncedCfg, err := cloneAPIConfig(apiCfg)
		if err != nil {
			return err
		}
		if syncedCfg.SpecURL == "" && len(syncedCfg.SpecFiles) == 0 && apiSpec.SourceURL != "" {
			syncedCfg.SpecURL = apiSpec.SourceURL
		}
		if err := c.configureAllowedOperationOrigins(cmd, apiName, syncedCfg, apiSpec, profileName, yes); err != nil {
			return err
		}
		if !reflect.DeepEqual(apiCfg, syncedCfg) && !c.projectAPI(apiName) {
			if err := c.saveAPIConfig("api sync", apiName, c.cfg, syncedCfg); err != nil {
				return err
			}
			c.printConfigWrittenPath()
			apiCfg = syncedCfg
		}
		c.emitGeneratedCommandWarnings(apiName, apiCfg, apiSpec, profileName)
		opOpts := spec.OperationOptions{
			BaseURL:         effectiveProfileBaseURL(apiCfg, profileName),
			OperationBase:   effectiveOperationBase(apiCfg, profileName),
			ServerVariables: effectiveServerVariables(apiCfg, profileName),
		}
		if err := spec.StoreSpecInCache(c.specCacheDir(), c.apiStateName(apiName), Version, apiSpec, apiCfg.SpecFiles, opOpts, 0); err != nil {
			c.warnf("could not cache generated commands for API %q: %v; run 'restish api sync %s' before using generated help", apiName, err, apiName)
		}
		style := humanTextStyleFor(c.Stdout)
		if report, err := apiSpec.XCLIExtensionReport(); err == nil {
			c.printXCLIExtensionSummary(report)
		}
		fmt.Fprintf(c.Stdout, "%s spec for %q.\n", style.ok("Synced"), apiName)
	} else {
		style := humanTextStyleFor(c.Stdout)
		fmt.Fprintf(c.Stdout, "%s for %q.\n", style.warn("No spec found"), apiName)
	}
	return nil
}

// runAPIConnect creates or updates the config entry for an API, pre-populating
// it from the API's OpenAPI spec x-cli-config extension if available.
func (c *CLI) runAPIConnect(cmd *cobra.Command, args []string) error {
	apiName := args[0]
	baseURL, err := normalizeAPIBaseURL(args[1])
	if err != nil {
		return err
	}
	allowCrossOrigin, _ := cmd.Flags().GetBool("allow-cross-origin-spec")
	noDiscover, _ := cmd.Flags().GetBool("no-discover")
	explicitSpec, _ := cmd.Flags().GetString("spec")
	replaceDefaults, _ := cmd.Flags().GetBool("replace")
	yes, _ := cmd.Flags().GetBool("yes")
	promptAnswers, setupExprs, err := parseAPIConfigureSetupExpressions(args[2:])
	if err != nil {
		return err
	}
	if noDiscover && explicitSpec != "" {
		return fmt.Errorf("--no-discover cannot be used with --spec")
	}

	if isBuiltinCommandName(apiName) {
		return fmt.Errorf("API name %q conflicts with a built-in command; choose a different name", apiName)
	}
	if pluginName := c.pluginCommandNames[apiName]; pluginName != "" {
		return fmt.Errorf("API name %q conflicts with command plugin %s; choose a different name", apiName, pluginName)
	}
	if err := config.ValidateAPIName(apiName); err != nil {
		return fmt.Errorf("invalid API name %q: %w", apiName, err)
	}

	cfg, err := c.loadConfig()
	if err != nil {
		return err
	}
	var existingAPI *config.APIConfig
	if cfg.APIs != nil {
		existingAPI = cfg.APIs[apiName]
	}

	// Build the API config entry.
	apiCfg := &config.APIConfig{
		BaseURL:              baseURL,
		AllowCrossOriginSpec: allowCrossOrigin,
	}
	applyExplicitSpec(apiCfg, explicitSpec)

	var apiSpec *spec.APISpec
	if !noDiscover {
		transport, closer, err := c.discoveryTransport(requestContext(cmd), apiCfg, "default")
		if err != nil {
			return err
		}
		if closer != nil {
			defer closer.Close()
		}
		fetch := c.discoveryFetcher(requestContext(cmd), apiName, apiCfg, "default")
		discCfg := spec.DiscoverConfig{
			APIName:          apiName,
			BaseURL:          baseURL,
			SpecURL:          apiCfg.SpecURL,
			SpecFiles:        apiCfg.SpecFiles,
			CacheDir:         c.specCacheDir(),
			ServerVariables:  nil,
			Version:          Version,
			Transport:        transport,
			Fetch:            fetch,
			AllowCrossOrigin: allowCrossOrigin,
			ForceRefresh:     true,
			Trace:            c.discoveryTrace(cmd),
		}
		discovered, discoverErr := spec.Discover(requestContext(cmd), discCfg, c.loaders)
		if discoverErr != nil && !errors.Is(discoverErr, spec.ErrNoSpecFound) {
			return fmt.Errorf("discovering API spec for %q: %w", apiName, discoverErr)
		}
		apiSpec = discovered
	}
	if apiSpec != nil {
		discovery := newConfigureAuthDiscovery(apiSpec, baseURL)
		c.printAPIDiscovery(apiName, baseURL, discovery)
		xcli, _ := spec.ReadXCLIConfig(apiSpec)
		fallbackXCLI := false
		if xcli == nil {
			// No x-cli-config extension — try to derive auth from the spec's
			// declared security schemes.
			xcli = spec.FallbackXCLIConfig(apiSpec)
			fallbackXCLI = true
		}
		if xcli != nil {
			xcli = xcli.Normalize()
			if !replaceDefaults {
				removeExistingXCLIProfiles(xcli, existingAPI)
			}
			if !fallbackXCLI {
				if err := c.promptXCLIConfig(requestContext(cmd), xcli, promptAnswers); err != nil {
					return err
				}
			}
			if err := c.applyXCLIConfig(apiCfg, xcli.Resolve(apiSpec)); err != nil {
				return err
			}
			if fallbackXCLI && len(xcli.Profiles) > 0 {
				if err := c.configureFallbackAuth(requestContext(cmd), apiCfg, discovery, promptAnswers); err != nil {
					return err
				}
			}
		}
	}
	if apiSpec != nil && apiCfg.SpecURL == "" && len(apiCfg.SpecFiles) == 0 && apiSpec.SourceURL != "" {
		apiCfg.SpecURL = apiSpec.SourceURL
	}
	if apiSpec != nil {
		if err := c.configureAllowedOperationOrigins(cmd, apiName, apiCfg, apiSpec, "default", yes); err != nil {
			return err
		}
	}
	if !replaceDefaults {
		if err := preserveExistingProfiles(apiCfg, existingAPI); err != nil {
			return err
		}
		if existingAPI != nil && existingAPI.CommandLayout != "" {
			apiCfg.CommandLayout = existingAPI.CommandLayout
		}
	}
	preservedProfiles := preservedProfileNames(existingAPI, replaceDefaults)
	if len(setupExprs) > 0 {
		patched, err := c.applyAPIShorthandConfig(apiName, apiCfg, setupExprs)
		if err != nil {
			return err
		}
		apiCfg = patched
	}
	if unused := promptAnswers.unusedCredentialAnswerPaths(); len(unused) > 0 {
		return fmt.Errorf("unused auth setup value(s): %s; check credential IDs and field names", strings.Join(unused, ", "))
	}
	if apiSpec != nil {
		discovery := newConfigureAuthDiscovery(apiSpec, baseURL)
		c.printAuthCoverage(apiName, "default", apiCfg, discovery)
	}

	if err := c.saveAPIConfig("api connect", apiName, cfg, apiCfg); err != nil {
		return err
	}
	if apiSpec != nil {
		c.emitGeneratedCommandWarnings(apiName, apiCfg, apiSpec, "default")
		opOpts := spec.OperationOptions{
			BaseURL:         effectiveProfileBaseURL(apiCfg, "default"),
			OperationBase:   effectiveOperationBase(apiCfg, "default"),
			ServerVariables: effectiveServerVariables(apiCfg, "default"),
		}
		if err := spec.StoreSpecInCache(c.specCacheDir(), c.apiStateName(apiName), Version, apiSpec, apiCfg.SpecFiles, opOpts, 0); err != nil {
			c.warnf("could not cache generated commands for API %q: %v; run 'restish api sync %s' before using generated help", apiName, err, apiName)
		}
	}
	c.printConfigWrittenPath()
	style := humanTextStyleFor(c.Stdout)
	if apiSpec != nil {
		if report, err := apiSpec.XCLIExtensionReport(); err == nil {
			c.printXCLIExtensionSummary(report)
		}
	}
	if len(preservedProfiles) > 0 {
		fmt.Fprintf(c.Stdout, "%s existing profile(s): %s (%s)\n", style.info("Preserved"), strings.Join(preservedProfiles, ", "), style.hint("use --replace to recreate from discovered defaults"))
	}
	if apiSpec != nil {
		opCount := connectedOperationCount(apiSpec, apiCfg)
		fmt.Fprintf(c.Stdout, "%s API %q with base URL %s (%d operations discovered — %s)\n", style.ok("Connected"), apiName, baseURL, opCount, style.hint("run 'restish "+apiName+" --help'"))
	} else if noDiscover {
		fmt.Fprintf(c.Stdout, "%s API %q with base URL %s (%s — %s)\n", style.ok("Connected"), apiName, baseURL, style.warn("discovery skipped"), style.hint("run 'restish api sync "+apiName+"' later"))
	} else {
		fmt.Fprintf(c.Stdout, "%s API %q with base URL %s (%s — %s)\n", style.ok("Connected"), apiName, baseURL, style.warn("no spec found"), style.hint("run 'restish api sync "+apiName+"' after connecting"))
	}
	return nil
}

func applyExplicitSpec(apiCfg *config.APIConfig, raw string) {
	if raw == "" {
		return
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		apiCfg.SpecURL = raw
		return
	}
	apiCfg.SpecFiles = []string{raw}
}

func connectedOperationCount(apiSpec *spec.APISpec, apiCfg *config.APIConfig) int {
	if apiSpec == nil {
		return 0
	}
	ops, err := apiSpec.Operations(spec.OperationOptions{
		BaseURL:       effectiveProfileBaseURL(apiCfg, "default"),
		OperationBase: effectiveOperationBase(apiCfg, "default"),
	})
	if err != nil {
		return 0
	}
	return len(ops)
}

func (c *CLI) configureAllowedOperationOrigins(cmd *cobra.Command, apiName string, apiCfg *config.APIConfig, apiSpec *spec.APISpec, profileName string, yes bool) error {
	origins := discoveredCrossOriginOperationServers(apiSpec, apiCfg, profileName)
	if len(origins) == 0 {
		return nil
	}
	suggestions := suggestedAllowedOperationOrigins(origins, apiCfg.AllowedOperationOrigins)
	if len(suggestions) == 0 {
		return nil
	}
	if c.projectAPI(apiName) {
		c.warnf("cross-origin operation servers ignored for read-only project API %q: %s; edit %s and add allowed_operation_origins[]: %s, or run with --rsh-config %s to make it the selected config", apiName, strings.Join(origins, ", "), c.projectConfig.Path, suggestions[0], c.projectConfig.Path)
		return nil
	}
	if yes {
		apiCfg.AllowedOperationOrigins = append(apiCfg.AllowedOperationOrigins, suggestions...)
		return nil
	}
	if !output.IsTerminalReader(c.Stdin) {
		c.warnf("cross-origin operation servers ignored: %s; allow with: restish api set %s 'allowed_operation_origins[]: %s'", strings.Join(origins, ", "), apiName, suggestions[0])
		return nil
	}
	label := fmt.Sprintf("Allow generated operations to call %s? This writes allowed_operation_origins: %s [Y/n] ", strings.Join(origins, ", "), strings.Join(suggestions, ", "))
	ok, err := c.Confirm(requestContext(cmd), label)
	if err != nil {
		return err
	}
	if ok {
		apiCfg.AllowedOperationOrigins = append(apiCfg.AllowedOperationOrigins, suggestions...)
	}
	return nil
}

func discoveredCrossOriginOperationServers(apiSpec *spec.APISpec, apiCfg *config.APIConfig, profileName string) []string {
	if apiSpec == nil || apiCfg == nil {
		return nil
	}
	ops, err := apiSpec.Operations(spec.OperationOptions{
		BaseURL:         effectiveProfileBaseURL(apiCfg, profileName),
		OperationBase:   effectiveOperationBase(apiCfg, profileName),
		ServerVariables: effectiveServerVariables(apiCfg, profileName),
	})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var origins []string
	for _, op := range ops {
		if op.OperationServer == "" {
			continue
		}
		if operationServerURLOverridden(op, apiCfg, profileName) {
			continue
		}
		origin := operationServerOrigin(op.OperationServer)
		if !seen[origin] {
			seen[origin] = true
			origins = append(origins, origin)
		}
	}
	sort.Strings(origins)
	return origins
}

func operationServerURLOverridden(op spec.Operation, apiCfg *config.APIConfig, profileName string) bool {
	rawURL := strings.TrimRight(op.OperationServer, "/") + op.Path
	_, rewritten, err := config.ApplyURLOverrides(rawURL, effectiveURLOverrides(apiCfg, profileName))
	return err == nil && rewritten
}

func suggestedAllowedOperationOrigins(origins, existing []string) []string {
	seen := map[string]bool{}
	for _, origin := range existing {
		seen[origin] = true
	}
	var suggestions []string
	for _, origin := range origins {
		suggestion := suggestedOperationOrigin(origin)
		if seen[suggestion] || config.OperationOriginAllowed(origin, existing) {
			continue
		}
		seen[suggestion] = true
		suggestions = append(suggestions, suggestion)
	}
	sort.Strings(suggestions)
	return suggestions
}

func preservedProfileNames(existingAPI *config.APIConfig, replace bool) []string {
	if replace || existingAPI == nil || len(existingAPI.Profiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(existingAPI.Profiles))
	for name := range existingAPI.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func removeExistingXCLIProfiles(xcli *spec.XCLIConfig, existingAPI *config.APIConfig) {
	if xcli == nil || len(xcli.Profiles) == 0 || existingAPI == nil || len(existingAPI.Profiles) == 0 {
		return
	}
	for name := range existingAPI.Profiles {
		delete(xcli.Profiles, name)
	}
}

func (c *CLI) emitGeneratedCommandWarnings(apiName string, apiCfg *config.APIConfig, apiSpec *spec.APISpec, profileName string) {
	if apiCfg == nil || apiSpec == nil {
		return
	}
	opOpts := spec.OperationOptions{
		BaseURL:         effectiveProfileBaseURL(apiCfg, profileName),
		OperationBase:   effectiveOperationBase(apiCfg, profileName),
		ServerVariables: effectiveServerVariables(apiCfg, profileName),
	}
	set, err := apiSpec.OperationSet(opOpts)
	if err == nil {
		for _, issue := range operationSecurityIssuesWithResolver(set.Operations, c.authResolverAvailable(apiName)) {
			c.warnf("OpenAPI security: %s", issue)
		}
	}
	quiet := c.quietGeneratedWarnings
	c.quietGeneratedWarnings = false
	defer func() {
		c.quietGeneratedWarnings = quiet
	}()
	_ = c.buildAPICommandFromOperationResult(apiName, apiCfg, set, opOpts.OperationBase, err)
}

func preserveExistingProfiles(apiCfg, existingAPI *config.APIConfig) error {
	if apiCfg == nil || existingAPI == nil || len(existingAPI.Profiles) == 0 {
		return nil
	}
	existing, err := cloneAPIConfig(existingAPI)
	if err != nil {
		return err
	}
	if apiCfg.Profiles == nil {
		apiCfg.Profiles = map[string]*config.ProfileConfig{}
	}
	for name, profile := range existing.Profiles {
		apiCfg.Profiles[name] = profile
	}
	return nil
}

func cloneAPIConfig(src *config.APIConfig) (*config.APIConfig, error) {
	if src == nil {
		return nil, nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	var dst config.APIConfig
	if err := json.Unmarshal(data, &dst); err != nil {
		return nil, err
	}
	return &dst, nil
}

func normalizeAPIBaseURL(raw string) (string, error) {
	normalized, err := request.Normalize(raw, "")
	if err != nil {
		return "", err
	}
	return normalized, nil
}

type configurePromptAnswers struct {
	profiles       map[string]map[string]string
	credentials    map[string]map[string]map[string]string
	usedCredential map[string]map[string]map[string]bool
}

func parseAPIConfigureSetupExpressions(args []string) (configurePromptAnswers, []string, error) {
	if len(args) == 0 {
		return configurePromptAnswers{}, nil, nil
	}
	answers := configurePromptAnswers{
		profiles:       map[string]map[string]string{},
		credentials:    map[string]map[string]map[string]string{},
		usedCredential: map[string]map[string]map[string]bool{},
	}
	var patchArgs []string
	for _, arg := range args {
		key, raw, appendOp, err := parseShorthandAssignment(arg)
		if err != nil {
			if strings.Contains(arg, "^") {
				patchArgs = append(patchArgs, arg)
				continue
			}
			return configurePromptAnswers{}, nil, err
		}
		if !strings.HasPrefix(key, "prompt.") {
			patchArgs = append(patchArgs, arg)
			continue
		}
		if appendOp {
			return configurePromptAnswers{}, nil, fmt.Errorf("invalid shorthand %q: prompt answers cannot use append", arg)
		}
		trimmed := strings.TrimPrefix(key, "prompt.")
		if trimmed == "" {
			return configurePromptAnswers{}, nil, fmt.Errorf("invalid prompt answer %q: expected prompt.<name> or prompt.<profile>.<name>", arg)
		}
		value, err := parseConfigCLIValue(raw)
		if err != nil {
			return configurePromptAnswers{}, nil, err
		}
		valueString, ok := value.(string)
		if !ok {
			return configurePromptAnswers{}, nil, fmt.Errorf("invalid prompt answer %q: value must be a string", arg)
		}
		if parseCredentialPromptAnswer(answers, trimmed, valueString) {
			continue
		}
		profileName := "default"
		promptName := trimmed
		if first, rest, ok := strings.Cut(trimmed, "."); ok {
			profileName = first
			promptName = rest
		}
		if profileName == "" || promptName == "" {
			return configurePromptAnswers{}, nil, fmt.Errorf("invalid prompt answer %q: expected prompt.<name> or prompt.<profile>.<name>", arg)
		}
		if answers.profiles[profileName] == nil {
			answers.profiles[profileName] = map[string]string{}
		}
		answers.profiles[profileName][promptName] = valueString
	}
	return answers, patchArgs, nil
}

func parseCredentialPromptAnswer(answers configurePromptAnswers, trimmed, value string) bool {
	if rest, ok := strings.CutPrefix(trimmed, "credentials."); ok {
		credentialID, promptName, ok := strings.Cut(rest, ".")
		if !ok || credentialID == "" || promptName == "" {
			return false
		}
		setCredentialPromptAnswer(answers, "default", credentialID, promptName, value)
		return true
	}
	profileName, rest, ok := strings.Cut(trimmed, ".credentials.")
	if !ok || profileName == "" {
		return false
	}
	credentialID, promptName, ok := strings.Cut(rest, ".")
	if !ok || credentialID == "" || promptName == "" {
		return false
	}
	setCredentialPromptAnswer(answers, profileName, credentialID, promptName, value)
	return true
}

func setCredentialPromptAnswer(answers configurePromptAnswers, profileName, credentialID, promptName, value string) {
	if answers.credentials[profileName] == nil {
		answers.credentials[profileName] = map[string]map[string]string{}
	}
	if answers.credentials[profileName][credentialID] == nil {
		answers.credentials[profileName][credentialID] = map[string]string{}
	}
	answers.credentials[profileName][credentialID][promptName] = value
}

func (a configurePromptAnswers) answer(profileName, name string) (string, bool) {
	if len(a.profiles) == 0 {
		return "", false
	}
	if profileAnswers := a.profiles[profileName]; profileAnswers != nil {
		if value, ok := profileAnswers[name]; ok {
			return value, true
		}
	}
	if profileName != "default" {
		if profileAnswers := a.profiles["default"]; profileAnswers != nil {
			if value, ok := profileAnswers[name]; ok {
				return value, true
			}
		}
	}
	return "", false
}

func (a configurePromptAnswers) answerCredential(profileName, credentialID, name string) (string, bool) {
	if profileAnswers := a.credentials[profileName]; profileAnswers != nil {
		if credentialAnswers := profileAnswers[credentialID]; credentialAnswers != nil {
			if value, ok := credentialAnswers[name]; ok {
				a.markCredentialAnswerUsed(profileName, credentialID, name)
				return value, true
			}
		}
	}
	if profileName != "default" {
		if profileAnswers := a.credentials["default"]; profileAnswers != nil {
			if credentialAnswers := profileAnswers[credentialID]; credentialAnswers != nil {
				if value, ok := credentialAnswers[name]; ok {
					a.markCredentialAnswerUsed("default", credentialID, name)
					return value, true
				}
			}
		}
	}
	return a.answer(profileName, name)
}

func (a configurePromptAnswers) markCredentialAnswerUsed(profileName, credentialID, name string) {
	if a.usedCredential == nil {
		return
	}
	if a.usedCredential[profileName] == nil {
		a.usedCredential[profileName] = map[string]map[string]bool{}
	}
	if a.usedCredential[profileName][credentialID] == nil {
		a.usedCredential[profileName][credentialID] = map[string]bool{}
	}
	a.usedCredential[profileName][credentialID][name] = true
}

func (a configurePromptAnswers) hasCredentialAnswer(profileName, credentialID string) bool {
	if profileAnswers := a.credentials[profileName]; profileAnswers != nil && len(profileAnswers[credentialID]) > 0 {
		return true
	}
	if profileName != "default" {
		if profileAnswers := a.credentials["default"]; profileAnswers != nil && len(profileAnswers[credentialID]) > 0 {
			return true
		}
	}
	return false
}

func (a configurePromptAnswers) unusedCredentialAnswerPaths() []string {
	var paths []string
	for profileName, profileAnswers := range a.credentials {
		for credentialID, credentialAnswers := range profileAnswers {
			for name := range credentialAnswers {
				if a.usedCredential[profileName] != nil &&
					a.usedCredential[profileName][credentialID] != nil &&
					a.usedCredential[profileName][credentialID][name] {
					continue
				}
				paths = append(paths, credentialAnswerPath(profileName, credentialID, name))
			}
		}
	}
	sort.Strings(paths)
	return paths
}

func credentialAnswerPath(profileName, credentialID, name string) string {
	if profileName == "" || profileName == "default" {
		return "prompt.credentials." + credentialID + "." + name
	}
	return "prompt." + profileName + ".credentials." + credentialID + "." + name
}

func (c *CLI) promptXCLIConfig(ctx context.Context, xcli *spec.XCLIConfig, answers configurePromptAnswers) error {
	if xcli == nil || len(xcli.Profiles) == 0 {
		return nil
	}
	profileNames := make([]string, 0, len(xcli.Profiles))
	for name := range xcli.Profiles {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)

	for _, profileName := range profileNames {
		profile := xcli.Profiles[profileName]
		if profile == nil {
			continue
		}
		if err := c.promptXCLIParams(ctx, profileName, profile.Prompt, &profile.Params, &profile.PromptValues, &profile.PromptedParams, answers); err != nil {
			return err
		}
		var credentialIDs []string
		for id := range profile.Credentials {
			credentialIDs = append(credentialIDs, id)
		}
		sort.Strings(credentialIDs)
		for _, id := range credentialIDs {
			credential := profile.Credentials[id]
			if credential == nil {
				continue
			}
			if err := c.promptXCLIParams(ctx, profileName, credential.Prompt, &credential.Params, &credential.PromptValues, &credential.PromptedParams, answers); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *CLI) promptXCLIParams(ctx context.Context, profileName string, prompts map[string]spec.XCLIPromptVar, params *map[string]string, promptValues *map[string]string, promptedParams *map[string]bool, answers configurePromptAnswers) error {
	if len(prompts) == 0 {
		return nil
	}
	if *params == nil {
		*params = map[string]string{}
	}
	if *promptValues == nil {
		*promptValues = map[string]string{}
	}
	if *promptedParams == nil {
		*promptedParams = map[string]bool{}
	}
	promptNames := make([]string, 0, len(prompts))
	for name := range prompts {
		promptNames = append(promptNames, name)
	}
	sort.Strings(promptNames)

	for _, name := range promptNames {
		var value string
		if answer, ok := answers.answer(profileName, name); ok {
			var err error
			value, err = validateXCLIPromptValue(name, strings.TrimSpace(answer), prompts[name])
			if err != nil {
				return fmt.Errorf("x-cli-config prompt %q: %w", name, err)
			}
		} else {
			var err error
			value, err = c.readXCLIPrompt(ctx, profileName, name, prompts[name])
			if err != nil {
				return fmt.Errorf("x-cli-config prompt %q: %w", name, err)
			}
		}
		(*promptValues)[name] = value
		if !prompts[name].Exclude {
			(*params)[name] = value
			(*promptedParams)[name] = true
		}
	}
	return nil
}

func (c *CLI) readXCLIPrompt(ctx context.Context, profileName, name string, prompt spec.XCLIPromptVar) (string, error) {
	label := xcliPromptLabel(profileName, name, prompt)
	for {
		var (
			value string
			err   error
		)
		if xcliPromptLooksSecret(name) {
			value, err = c.Secret(ctx, label)
		} else {
			value, err = c.Prompt(ctx, label)
		}
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value == "" && prompt.Default != nil {
			value = fmt.Sprint(prompt.Default)
		}
		validated, validateErr := validateXCLIPromptValue(name, value, prompt)
		if validateErr != nil {
			fmt.Fprintf(c.Stderr, "%v.\n", validateErr)
			continue
		}
		return validated, nil
	}
}

func validateXCLIPromptValue(name, value string, prompt spec.XCLIPromptVar) (string, error) {
	if value == "" && prompt.Default != nil {
		value = fmt.Sprint(prompt.Default)
	}
	if value == "" {
		return "", fmt.Errorf("%s is required; please enter a non-empty value", name)
	}
	if len(prompt.Enum) > 0 && !xcliPromptValueAllowed(value, prompt.Enum) {
		return "", fmt.Errorf("%s must be one of: %s", name, xcliPromptEnumList(prompt.Enum))
	}
	return value, nil
}

func xcliPromptLabel(profileName, name string, prompt spec.XCLIPromptVar) string {
	label := name
	if prompt.Description != "" {
		label = prompt.Description
	}
	if profileName != "" && profileName != "default" {
		label = profileName + " " + label
	}
	if len(prompt.Enum) > 0 {
		label += " (" + xcliPromptEnumList(prompt.Enum) + ")"
	} else if prompt.Example != "" {
		label += " (example: " + prompt.Example + ")"
	}
	if prompt.Default != nil {
		label += " [" + fmt.Sprint(prompt.Default) + "]"
	}
	return label + ": "
}

func xcliPromptValueAllowed(value string, enum []any) bool {
	for _, candidate := range enum {
		if value == fmt.Sprint(candidate) {
			return true
		}
	}
	return false
}

func xcliPromptEnumList(enum []any) string {
	values := make([]string, 0, len(enum))
	for _, candidate := range enum {
		values = append(values, fmt.Sprint(candidate))
	}
	return strings.Join(values, ", ")
}

func xcliPromptLooksSecret(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "password") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "auth_token") ||
		strings.Contains(lower, "access_key") ||
		strings.Contains(lower, "credential") ||
		strings.Contains(lower, "credentials") ||
		strings.Contains(lower, "passphrase") ||
		strings.Contains(lower, "bearer") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "private_key")
}

// applyXCLIConfig merges x-cli-config fields into apiCfg.
// Auth type "external-tool" is rejected: a server-provided x-cli-config
// could otherwise pre-seed arbitrary shell-command execution on the next
// authenticated request.
func (c *CLI) applyXCLIConfig(apiCfg *config.APIConfig, xcli *spec.XCLIConfig) error {
	// command_layout is a server-supplied hint, so an invalid value is
	// ignored with a warning instead of failing the whole connect.
	if err := config.ValidateCommandLayout(xcli.CommandLayout); err != nil {
		c.warnf("ignoring x-cli-config.command_layout: %v", err)
	} else if xcli.CommandLayout != "" {
		apiCfg.CommandLayout = xcli.CommandLayout
	}
	if len(xcli.Profiles) == 0 {
		return nil
	}
	if apiCfg.Profiles == nil {
		apiCfg.Profiles = make(map[string]*config.ProfileConfig)
	}
	for name, xp := range xcli.Profiles {
		prof := &config.ProfileConfig{
			Headers: xp.Headers,
			Query:   xp.Query,
		}
		if xp.Auth != nil {
			if xp.Auth.Type == "external-tool" {
				c.warnf("x-cli-config: auth type %q is not permitted; skipping profile %q", xp.Auth.Type, name)
				continue
			}
			prof.Auth = &config.AuthConfig{
				Type:   xp.Auth.Type,
				Params: xp.Auth.Params,
			}
		}
		if len(xp.Credentials) > 0 {
			prof.Credentials = make(map[string]*config.CredentialConfig, len(xp.Credentials))
			for credentialID, xc := range xp.Credentials {
				if xc == nil {
					continue
				}
				credential := &config.CredentialConfig{
					AuthRef:   xc.AuthRef,
					Satisfies: append([]string(nil), xc.Satisfies...),
				}
				if xc.Auth != nil {
					if xc.Auth.Type == "external-tool" {
						c.warnf("x-cli-config: auth type %q is not permitted; skipping credential %q in profile %q", xc.Auth.Type, credentialID, name)
						continue
					}
					credential.Auth = &config.AuthConfig{
						Type:   xc.Auth.Type,
						Params: xc.Auth.Params,
					}
				}
				prof.Credentials[credentialID] = credential
			}
			if len(prof.Credentials) == 0 {
				prof.Credentials = nil
			}
		}
		apiCfg.Profiles[name] = prof
	}
	return nil
}

func (c *CLI) discoveryTransport(ctx context.Context, apiCfg *config.APIConfig, profileName string) (http.RoundTripper, interface{ Close() error }, error) {
	c.warnInsecureDiscoveryTransport(ctx)
	opts, err := c.discoveryTransportOptions(ctx, apiCfg, profileName)
	if err != nil {
		return nil, nil, err
	}
	transport := request.BuildTransport(opts)
	closer, _ := transport.(interface{ Close() error })
	return transport, closer, nil
}

func (c *CLI) warnInsecureDiscoveryTransport(ctx context.Context) {
	if globalFlagsFromContext(ctx).Insecure {
		c.warnf("TLS certificate verification is disabled (--rsh-insecure); connections are not secure")
	}
}

func (c *CLI) discoveryTransportOptions(ctx context.Context, apiCfg *config.APIConfig, profileName string) (request.Options, error) {
	gf := globalFlagsFromContext(ctx)
	tlsMinVersion, err := request.TLSVersionFromString(gf.TLSMinVersion)
	if err != nil {
		return request.Options{}, err
	}
	tlsSignerParams, err := parseKVStrings(gf.TLSSignerParams)
	if err != nil {
		return request.Options{}, fmt.Errorf("invalid tls signer param: %w", err)
	}
	opts := request.Options{
		Transport:       c.baseHTTPTransport(),
		Insecure:        gf.Insecure,
		ClientCertPath:  gf.ClientCert,
		ClientKeyPath:   gf.ClientKey,
		TLSSignerName:   gf.TLSSigner,
		TLSSignerParams: tlsSignerParams,
		CACertPath:      gf.CACert,
		TLSMinVersion:   tlsMinVersion,
		UserAgent:       "restish/" + Version,
		Logger:          diagnosticPrefixWriter(c.Stderr),
	}
	if apiCfg != nil {
		if profileName == "" {
			profileName = "default"
		}
		if prof := profileForName(apiCfg, profileName); prof != nil {
			if opts.TLSSignerName == "" {
				opts.TLSSignerName = prof.TLSSigner
			}
			opts.TLSSignerParams = mergeTLSSignerParams(opts.TLSSignerParams, prof.TLSSignerParams)
			if opts.CACertPath == "" {
				opts.CACertPath = prof.CACertPath
			}
			if opts.ClientCertPath == "" {
				opts.ClientCertPath = prof.ClientCertPath
			}
			if opts.ClientKeyPath == "" {
				opts.ClientKeyPath = prof.ClientKeyPath
			}
		}
	}
	opts, err = c.resolveTLSSigner(opts)
	if err != nil {
		return request.Options{}, err
	}
	return opts, nil
}

type discoveryTransportShareKey struct {
	Insecure        bool
	ClientCertPath  string
	ClientKeyPath   string
	TLSSignerPath   string
	TLSSignerParams string
	CACertPath      string
	TLSMinVersion   uint16
}

func discoveryTransportShareKeyFromOptions(opts request.Options) discoveryTransportShareKey {
	return discoveryTransportShareKey{
		Insecure:        opts.Insecure,
		ClientCertPath:  opts.ClientCertPath,
		ClientKeyPath:   opts.ClientKeyPath,
		TLSSignerPath:   opts.TLSSignerPath,
		TLSSignerParams: tlsSignerParamsKey(opts.TLSSignerParams),
		CACertPath:      opts.CACertPath,
		TLSMinVersion:   opts.TLSMinVersion,
	}
}

func tlsSignerParamsKey(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(params[key])
		b.WriteByte('\n')
	}
	return b.String()
}

func (c *CLI) discoveryFetcher(ctx context.Context, apiName string, apiCfg *config.APIConfig, profileName string) spec.HTTPFetcher {
	authOrigins := discoveryAuthOrigins(apiCfg, profileName)
	return func(ctx context.Context, rawURL string, transport http.RoundTripper) (*http.Response, error) {
		baseOpts, authOpts := c.discoveryRequestOptions(ctx, apiName, apiCfg, profileName, transport)
		opts := baseOpts
		if discoveryAuthAllowed(authOrigins, rawURL) {
			opts = authOpts
		}
		return c.doDiscoveryRequest(ctx, http.MethodGet, rawURL, opts)
	}
}

func (c *CLI) discoveryRequestOptions(ctx context.Context, apiName string, apiCfg *config.APIConfig, profileName string, transport http.RoundTripper) (request.Options, request.Options) {
	if profileName == "" {
		profileName = "default"
	}
	baseOpts := request.Options{
		Transport: transport,
		UserAgent: "restish/" + Version,
	}
	authOpts := baseOpts
	if apiCfg != nil {
		if apiCfg.PreserveHeaderCase {
			authOpts.PreserveHeaderCase = true
		}
		prof := profileForName(apiCfg, profileName)
		if prof != nil {
			authOpts.Headers = append([]string(nil), prof.Headers...)
			authOpts.Query = append([]string(nil), prof.Query...)
		}
		callbacks := c.authOnRequest(apiName, profileName, prof, authHandlerOptionsFromContext(ctx))
		authOpts.OnRequest = callbacks.OnRequest
		authOpts.OnUnauthorized = callbacks.OnUnauthorized
	}
	if authOpts.OnRequest != nil || len(c.pluginsByHook["request-middleware"]) > 0 {
		origOnRequest := authOpts.OnRequest
		authOpts.OnRequest = func(req *http.Request) error {
			if origOnRequest != nil {
				if err := origOnRequest(req); err != nil {
					return err
				}
			}
			return c.runRequestMiddlewarePlugins(req)
		}
	}
	return baseOpts, authOpts
}

func (c *CLI) doDiscoveryRequest(ctx context.Context, method, rawURL string, opts request.Options) (*http.Response, error) {
	resp, err := request.Do(ctx, method, rawURL, nil, opts)
	if err != nil || resp == nil || resp.StatusCode != http.StatusUnauthorized || opts.OnUnauthorized == nil {
		return resp, err
	}
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	retryOpts := opts
	onUnauthorized := retryOpts.OnUnauthorized
	nonce := resp.Header.Get("DPoP-Nonce")
	retryOpts.OnRequest = func(req *http.Request) error {
		authpkg.SetDPoPNonce(req, nonce)
		if err := onUnauthorized(req); err != nil {
			return err
		}
		return c.runRequestMiddlewarePlugins(req)
	}
	retryOpts.OnUnauthorized = nil
	return request.Do(ctx, method, rawURL, nil, retryOpts)
}

func authHandlerOptionsFromContext(ctx context.Context) authHandlerOptions {
	gf := globalFlagsFromContext(ctx)
	return authHandlerOptions{NoBrowser: gf.NoBrowser, Verbose: gf.Verbose > 0}
}

func discoveryAuthOrigins(apiCfg *config.APIConfig, profileName string) []*url.URL {
	if apiCfg == nil {
		return nil
	}
	if apiCfg.UnauthenticatedSpec {
		return nil
	}
	var origins []*url.URL
	addNormalized := func(raw string) {
		if raw == "" {
			return
		}
		if normalized, err := request.Normalize(raw, ""); err == nil {
			raw = normalized
		}
		u, err := url.Parse(raw)
		if err == nil && u.IsAbs() && (u.Scheme == "http" || u.Scheme == "https") {
			origins = append(origins, u)
		}
	}
	addExplicitURL := func(raw string) {
		u, err := url.Parse(raw)
		if err == nil && u.IsAbs() && (u.Scheme == "http" || u.Scheme == "https") {
			origins = append(origins, u)
		}
	}
	addNormalized(effectiveProfileBaseURL(apiCfg, profileName))
	addNormalized(apiCfg.SpecURL)
	for _, src := range apiCfg.SpecFiles {
		addExplicitURL(src)
	}
	return origins
}

func discoveryAuthAllowed(origins []*url.URL, rawURL string) bool {
	if normalized, err := request.Normalize(rawURL, ""); err == nil {
		rawURL = normalized
	}
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() {
		return false
	}
	for _, origin := range origins {
		if request.SameOrigin(origin, u) {
			return true
		}
	}
	return false
}

// runAPIInspect prints the config for a named API as indented JSON,
// with secret auth params replaced by "***".
func (c *CLI) runAPIInspect(cmd *cobra.Command, args []string) error {
	if _, err := commandJSONOutputRequested(cmd); err != nil {
		return err
	}
	apiName := args[0]
	apiCfg, err := c.requireAPI(apiName)
	if err != nil {
		return err
	}

	// Round-trip through JSON so we can redact secrets without modifying the live config.
	raw, err := json.Marshal(apiCfg)
	if err != nil {
		return err
	}
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		return err
	}
	redactSensitiveConfigValue(view)

	return c.writePrettyJSON(view)
}

// runConfigEdit opens the restish config file in $VISUAL or $EDITOR.
func (c *CLI) runConfigEdit(cmd *cobra.Command, args []string) error {
	cfgPath := c.configFilePath()
	oldCfg := c.cfg
	before, err := statConfigEditFile(cfgPath)
	if err != nil {
		return err
	}
	editorCmd, err := c.editorCommand(cfgPath)
	if err != nil {
		return err
	}
	editorCmd.Stdin = c.Stdin
	editorCmd.Stdout = c.Stdout
	editorCmd.Stderr = c.Stderr
	if err := editorCmd.Run(); err != nil {
		return err
	}
	if err := c.reloadConfigAfterMutation("config edit", oldCfg); err != nil {
		return err
	}
	after, err := statConfigEditFile(cfgPath)
	if err != nil {
		return err
	}
	if after.changedFrom(before) {
		c.printConfigWrittenPath()
	}
	return nil
}

type configEditFileState struct {
	exists  bool
	modTime time.Time
	size    int64
}

func statConfigEditFile(path string) (configEditFileState, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return configEditFileState{}, nil
	}
	if err != nil {
		return configEditFileState{}, err
	}
	return configEditFileState{
		exists:  true,
		modTime: info.ModTime(),
		size:    info.Size(),
	}, nil
}

func (s configEditFileState) changedFrom(old configEditFileState) bool {
	if s.exists != old.exists {
		return true
	}
	if !s.exists {
		return false
	}
	return !s.modTime.Equal(old.modTime) || s.size != old.size
}

func apiNamesWithSpecCacheRelevantChanges(oldCfg, newCfg *config.Config) []string {
	namesSeen := map[string]struct{}{}
	if oldCfg != nil {
		for name := range oldCfg.APIs {
			namesSeen[name] = struct{}{}
		}
	}
	if newCfg != nil {
		for name := range newCfg.APIs {
			namesSeen[name] = struct{}{}
		}
	}
	var changed []string
	for name := range namesSeen {
		var oldAPI, newAPI *config.APIConfig
		if oldCfg != nil {
			oldAPI = oldCfg.APIs[name]
		}
		if newCfg != nil {
			newAPI = newCfg.APIs[name]
		}
		if apiSpecCacheRelevantFieldsChanged(oldAPI, newAPI) {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

// runAPISet patches a named API config using shorthand patch expressions rooted
// at apis.<name>.
func (c *CLI) runAPISet(cmd *cobra.Command, args []string) error {
	apiName := args[0]

	if _, err := c.requireAPI(apiName); err != nil {
		return err
	}
	patchArgs := args[1:]
	if err := validateConfigPatchArgs("api set", patchArgs); err != nil {
		return err
	}
	if profiles := apiSetServerVariableValidationProfiles(patchArgs, c.cfg.APIs[apiName]); len(profiles) > 0 {
		patchedAPI, err := c.applyAPIShorthandConfig(apiName, c.cfg.APIs[apiName], patchArgs)
		if err != nil {
			return err
		}
		if err := c.validateAPISetServerVariables(requestContext(cmd), apiName, patchedAPI, profiles); err != nil {
			return err
		}
	}
	if err := c.saveConfigShorthand("api set", []string{"apis", apiName}, patchArgs); err != nil {
		return err
	}
	c.printConfigWrittenPath()
	return nil
}

func validateConfigPatchArgs(label string, args []string) error {
	for _, arg := range args {
		if !strings.Contains(arg, ":") && !strings.Contains(arg, "^") {
			return fmt.Errorf("%s: expected shorthand patch expression %q to contain ':' or '^'", label, arg)
		}
	}
	return nil
}

func apiSetServerVariableValidationProfiles(exprs []string, apiCfg *config.APIConfig) []string {
	if len(exprs) == 0 {
		return nil
	}
	apiLevel := false
	profiles := map[string]bool{}
	for _, expr := range exprs {
		path := configPatchExprPath(expr)
		if path == "server_variables" || strings.HasPrefix(path, "server_variables.") {
			apiLevel = true
			continue
		}
		const prefix = "profiles."
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		parts := strings.Split(rest, ".")
		if len(parts) >= 2 && parts[1] == "server_variables" && parts[0] != "" {
			profiles[parts[0]] = true
		}
	}
	if apiLevel {
		profiles["default"] = true
		if apiCfg != nil {
			for name := range apiCfg.Profiles {
				profiles[name] = true
			}
		}
	}
	return sortedMapKeys(profiles)
}

func configPatchExprPath(expr string) string {
	cut := len(expr)
	if idx := strings.IndexAny(expr, ":^"); idx >= 0 {
		cut = idx
	}
	return strings.TrimSpace(expr[:cut])
}

func sortedMapKeys(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c *CLI) validateAPISetServerVariables(ctx context.Context, apiName string, apiCfg *config.APIConfig, profiles []string) error {
	for _, profileName := range profiles {
		if _, _, err := c.operationSetForAPI(ctx, apiName, apiCfg, profileName, false); err != nil {
			if profileName == "default" {
				return fmt.Errorf("api set server_variables: %w", err)
			}
			return fmt.Errorf("api set profiles.%s.server_variables: %w", profileName, err)
		}
	}
	return nil
}

func (c *CLI) applyAPIShorthandConfig(apiName string, apiCfg *config.APIConfig, exprs []string) (*config.APIConfig, error) {
	cfg := &config.Config{APIs: map[string]*config.APIConfig{apiName: apiCfg}}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	_, patched, err := internalconfig.PatchConfigShorthandBytes(data, []string{"apis", apiName}, exprs)
	if err != nil {
		return nil, err
	}
	if err := c.validateConfigRuntime(patched); err != nil {
		return nil, err
	}
	return patched.APIs[apiName], nil
}

func apiSpecCacheRelevantFieldsChanged(oldAPI, newAPI *config.APIConfig) bool {
	if oldAPI == nil || newAPI == nil {
		return oldAPI != newAPI
	}
	return oldAPI.BaseURL != newAPI.BaseURL ||
		oldAPI.SpecURL != newAPI.SpecURL ||
		!reflect.DeepEqual(oldAPI.SpecFiles, newAPI.SpecFiles)
}

func parseShorthandAssignment(expr string) (string, string, bool, error) {
	key, raw, ok := strings.Cut(expr, ":")
	if !ok {
		return "", "", false, fmt.Errorf("invalid shorthand %q: expected path:value", expr)
	}
	key = strings.TrimSpace(key)
	raw = strings.TrimSpace(raw)
	if key == "" {
		return "", "", false, fmt.Errorf("invalid shorthand %q: empty path", expr)
	}
	if raw == "" {
		return "", "", false, fmt.Errorf("invalid shorthand %q: empty value", expr)
	}
	appendOp := false
	if strings.HasSuffix(key, "[]") {
		appendOp = true
		key = strings.TrimSuffix(key, "[]")
		key = strings.TrimSpace(key)
		if key == "" {
			return "", "", false, fmt.Errorf("invalid shorthand %q: empty path", expr)
		}
	}
	return key, raw, appendOp, nil
}

func parseConfigCLIValue(raw string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err == nil {
		return v, nil
	}
	return raw, nil
}

func cloneConfig(src *config.Config) (*config.Config, error) {
	if src == nil {
		return &config.Config{}, nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	var dst config.Config
	if err := json.Unmarshal(data, &dst); err != nil {
		return nil, err
	}
	return &dst, nil
}

// runAPIList prints all configured APIs with their base URL, generated
// operation count, and profile count.
func (c *CLI) runAPIList(cmd *cobra.Command, args []string) error {
	if c.cfg == nil || len(c.cfg.APIs) == 0 {
		if jsonOut, err := commandJSONOutputRequested(cmd); err != nil {
			return err
		} else if jsonOut {
			return c.writePrettyJSON([]any{})
		}
		fmt.Fprintf(c.Stdout, "No APIs configured. Run %q to add one.\n", c.commandNameOrDefault()+" api connect <name> <url>")
		return nil
	}
	names := make([]string, 0, len(c.cfg.APIs))
	for name := range c.cfg.APIs {
		names = append(names, name)
	}
	sort.Strings(names)
	if jsonOut, err := commandJSONOutputRequested(cmd); err != nil {
		return err
	} else if jsonOut {
		type apiListEntry struct {
			Name           string   `json:"name"`
			BaseURL        string   `json:"base_url"`
			OperationCount int      `json:"operation_count"`
			ProfileCount   int      `json:"profile_count"`
			Profiles       []string `json:"profiles,omitempty"`
		}
		entries := make([]apiListEntry, 0, len(names))
		for _, name := range names {
			api := c.cfg.APIs[name]
			profiles := make([]string, 0, len(api.Profiles))
			for profileName := range api.Profiles {
				profiles = append(profiles, profileName)
			}
			sort.Strings(profiles)
			entries = append(entries, apiListEntry{
				Name:           name,
				BaseURL:        api.BaseURL,
				OperationCount: c.apiListOperationCount(name, api),
				ProfileCount:   len(api.Profiles),
				Profiles:       profiles,
			})
		}
		return c.writePrettyJSON(entries)
	}
	type apiListRow struct {
		Name           string
		BaseURL        string
		OperationCount int
		OperationWord  string
		ProfileCount   int
		ProfileWord    string
	}
	rows := make([]apiListRow, 0, len(names))
	nameWidth, baseURLWidth, operationDigits, profileDigits := 0, 0, 1, 1
	for _, name := range names {
		api := c.cfg.APIs[name]
		operationCount := c.apiListOperationCount(name, api)
		profileCount := len(api.Profiles)
		row := apiListRow{
			Name:           name,
			BaseURL:        api.BaseURL,
			OperationCount: operationCount,
			OperationWord:  "operation",
			ProfileCount:   profileCount,
			ProfileWord:    "profile",
		}
		if operationCount != 1 {
			row.OperationWord = "operations"
		}
		if profileCount != 1 {
			row.ProfileWord = "profiles"
		}
		rows = append(rows, row)
		nameWidth = max(nameWidth, len(name))
		baseURLWidth = max(baseURLWidth, len(api.BaseURL))
		operationDigits = max(operationDigits, len(fmt.Sprint(operationCount)))
		profileDigits = max(profileDigits, len(fmt.Sprint(profileCount)))
	}
	for _, row := range rows {
		fmt.Fprintf(c.Stdout, "%-*s  %-*s  %*d %-10s  %*d %s\n",
			nameWidth, row.Name,
			baseURLWidth, row.BaseURL,
			operationDigits, row.OperationCount, row.OperationWord,
			profileDigits, row.ProfileCount, row.ProfileWord)
	}
	return nil
}

func (c *CLI) apiListOperationCount(apiName string, apiCfg *config.APIConfig) int {
	set, _, ok := c.cachedOperationSetStatusForAPI(apiName, apiCfg, "default")
	if !ok {
		return 0
	}
	return len(set.Operations)
}

// runAPIRemove removes a configured API and saves the updated config.
func (c *CLI) runAPIRemove(cmd *cobra.Command, args []string) error {
	apiName := args[0]
	if err := c.ensureMutableAPI(apiName); err != nil {
		return err
	}
	apiCfg, err := c.requireAPI(apiName)
	if err != nil {
		return err
	}
	unusedSharedAuthRefs := c.unusedSharedAuthRefsAfterAPIRemove(apiName, apiCfg)
	oldCfg, err := cloneConfig(c.cfg)
	if err != nil {
		return err
	}
	delete(c.cfg.APIs, apiName)
	if err := c.deleteAPIConfig("api remove", apiName, c.cfg, oldCfg); err != nil {
		return err
	}
	if err := c.removeAPILocalState(apiName, unusedSharedAuthRefs); err != nil {
		return err
	}
	c.printConfigWrittenPath()
	style := humanTextStyleFor(c.Stdout)
	fmt.Fprintf(c.Stdout, "%s API %q and cleared its local cache/auth state.\n", style.ok("Removed"), apiName)
	return nil
}

func (c *CLI) removeAPILocalState(apiName string, sharedAuthRefs []string) error {
	dc, err := cache.New(c.cacheDir(), cache.DefaultMaxBytes, "")
	if err != nil {
		return fmt.Errorf("api remove: clear HTTP cache: %w", err)
	}
	namespace := c.apiStateName(apiName)
	if _, err := dc.ClearNamespacePrefix(namespace + ":"); err != nil {
		return fmt.Errorf("api remove: clear HTTP cache for %q: %w", apiName, err)
	}

	tc := auth.NewTokenCache(c.tokenCachePath())
	if err := tc.DeletePrefix(namespace + ":"); err != nil {
		return fmt.Errorf("api remove: clear auth cache for %q: %w", apiName, err)
	}
	for _, ref := range sharedAuthRefs {
		if err := tc.DeletePrefix("auth_profile:" + ref + ":"); err != nil {
			return fmt.Errorf("api remove: clear auth cache for auth profile %q: %w", ref, err)
		}
	}
	return nil
}

func (c *CLI) unusedSharedAuthRefsAfterAPIRemove(apiName string, apiCfg *config.APIConfig) []string {
	if apiCfg == nil {
		return nil
	}
	refs := sharedAuthRefsForAPI(apiCfg)
	if len(refs) == 0 {
		return nil
	}
	var unused []string
	for _, ref := range refs {
		usedElsewhere := false
		for otherName, otherCfg := range c.cfg.APIs {
			if otherName == apiName || otherCfg == nil {
				continue
			}
			if apiUsesSharedAuthRef(otherCfg, ref) {
				usedElsewhere = true
				break
			}
		}
		if !usedElsewhere {
			unused = append(unused, ref)
		}
	}
	return unused
}

func sharedAuthRefsForAPI(apiCfg *config.APIConfig) []string {
	seen := map[string]bool{}
	var refs []string
	add := func(ref string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	for _, prof := range apiCfg.Profiles {
		if prof == nil {
			continue
		}
		add(prof.AuthRef)
		for _, credential := range prof.Credentials {
			if credential != nil {
				add(credential.AuthRef)
			}
		}
	}
	return refs
}

func apiUsesSharedAuthRef(apiCfg *config.APIConfig, ref string) bool {
	if ref == "" || apiCfg == nil {
		return false
	}
	for _, prof := range apiCfg.Profiles {
		if prof == nil {
			continue
		}
		if prof.AuthRef == ref {
			return true
		}
		for _, credential := range prof.Credentials {
			if credential != nil && credential.AuthRef == ref {
				return true
			}
		}
	}
	return false
}
