package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/spec"
	"github.com/spf13/cobra"
)

func (c *CLI) newAPIAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage API auth credentials",
		Long:  apiAuthLong,
		Example: fmt.Sprintf(`  %s api auth inspect demo
  %s api auth inspect demo --operation list-items
  %s api auth logout demo`, c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "add <api> <credential-id>",
		Short:   "Add an empty credential binding to an API profile",
		Long:    apiAuthAddLong,
		Example: fmt.Sprintf("  %s api auth add demo PartnerKey", c.commandNameOrDefault()),
		Args:    usageExactArgs(2),
		RunE:    c.runAPIAuthAdd,
	})
	cmd.AddCommand(&cobra.Command{
		Use:     "remove <api> <credential-id>",
		Short:   "Remove a credential binding from an API profile",
		Long:    apiAuthRemoveLong,
		Example: fmt.Sprintf("  %s api auth remove demo PartnerKey", c.commandNameOrDefault()),
		Args:    usageExactArgs(2),
		RunE:    c.runAPIAuthRemove,
	})
	logoutCmd := &cobra.Command{
		Use:   "logout [api]",
		Short: "Delete cached API auth tokens",
		Long:  apiAuthLogoutLong,
		Example: fmt.Sprintf(`  %s api auth logout demo
  %s api auth logout demo --all-profiles
  %s api auth logout --auth-profile shared-oauth`, c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: usageMaximumNArgs(1),
		RunE: c.runAPIAuthLogout,
	}
	addAPIAuthLogoutFlags(logoutCmd)
	cmd.AddCommand(logoutCmd)
	getCmd := &cobra.Command{
		Use:   "get <api> [credential-id]",
		Short: "Print curl-friendly auth material for an API profile",
		Long:  apiAuthGetLong,
		Example: fmt.Sprintf(`  %s api auth get demo UserBearer
  %s api auth get demo PartnerKey
  %s api auth get demo --operation list-items
  curl -H "$(%s api auth get demo UserBearer)" https://api.rest.sh/items
  export AUTH_HEADER="$("%s" api auth get demo --print-header)"`, c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: usageRangeArgs(1, 2),
		RunE: c.runAPIAuthGet,
	}
	getCmd.Flags().String("operation", "", "Operation ID or command name to inspect")
	getCmd.Flags().Bool("print-header", false, "Print the single resolved header as 'Name: value' on stdout and exit non-zero for any non-header auth")
	cmd.AddCommand(getCmd)
	inspectCmd := &cobra.Command{
		Use:   "inspect <api>",
		Short: "Inspect the auth material applied for an API profile",
		Long:  apiAuthInspectLong,
		Example: fmt.Sprintf(`  %s api auth inspect demo
  %s api auth inspect demo --operation list-items --redact`, c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: usageExactArgs(1),
		RunE: c.runAPIAuthInspect,
	}
	inspectCmd.Flags().String("credential", "", "Credential ID to inspect instead of profile-level auth")
	inspectCmd.Flags().String("operation", "", "Operation ID or command name to inspect")
	inspectCmd.Flags().Bool("redact", false, "Redact sensitive auth values for shareable output")
	cmd.AddCommand(inspectCmd)
	return cmd
}

func addAPIAuthLogoutFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("all-profiles", false, "Delete cached auth tokens for every profile of the named API")
	cmd.Flags().String("auth-profile", "", "Delete cached auth tokens for a shared auth profile instead of an API")
}

func (c *CLI) printAPIAuthOverview(cmd *cobra.Command, apiName, profileName string, apiCfg *config.APIConfig, prof *config.ProfileConfig) {
	style := humanTextStyleFor(c.Stdout)
	set, hasOps := c.cachedOperationSetForAPI(requestContext(cmd), apiName, apiCfg, profileName)
	_, profileReady, profileErr := c.profileAuthReadiness(apiName, profileName, prof)
	coverage := operationAuthCoverage{}
	if hasOps {
		coverage = c.operationAuthCoverage(apiName, profileName, prof, set.Operations)
	}
	var ids []string
	for id := range prof.Credentials {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Fprintf(c.Stdout, "API: %s\nProfile: %s\n", apiName, profileName)
	if profileErr != nil {
		fmt.Fprintf(c.Stdout, "Generic request auth: %s (%s)\n", style.warn("configured"), profileErr)
	} else if profileReady.Configured {
		fmt.Fprintf(c.Stdout, "Generic request auth: %s\n", style.authStatus(profileReady.status("none")))
	} else {
		fmt.Fprintf(c.Stdout, "Generic request auth: %s\n", style.warn("none"))
	}
	if hasOps {
		fmt.Fprintf(c.Stdout, "Callable secured operations: %d/%d\n", coverage.Callable, coverage.Secured)
	} else {
		fmt.Fprintf(c.Stdout, "Operation metadata: %s (%s)\n", style.warn("unavailable"), style.hint("run \"restish api sync "+apiName+"\" to refresh"))
	}
	if len(ids) == 0 {
		fmt.Fprintf(c.Stdout, "Credentials: %s\n", style.warn("none"))
	} else {
		fmt.Fprintln(c.Stdout, "Credentials:")
		for _, id := range ids {
			credential := prof.Credentials[id]
			_, ready, _ := c.credentialReadiness(apiName, profileName, id, credential)
			status := ready.status("empty")
			fmt.Fprintf(c.Stdout, "  %s: %s", id, style.authStatus(status))
			if len(credential.Satisfies) > 0 {
				fmt.Fprintf(c.Stdout, " (satisfies: %s)", strings.Join(credential.Satisfies, ", "))
			}
			fmt.Fprintln(c.Stdout)
		}
	}
	if hasOps {
		coverage := c.operationAuthCoverage(apiName, profileName, prof, set.Operations)
		c.printAPIAuthRequirementSummary(apiName, profileName, set.Operations, prof, coverage)
		if credentialID := nextMissingCredentialID(set.Operations, prof, coverage); credentialID != "" {
			fmt.Fprintf(c.Stdout, "%s run \"restish api auth add %s %s\".\n", style.hint("Next:"), apiName, credentialID)
		}
	}
}

func (c *CLI) runAPIAuthAdd(cmd *cobra.Command, args []string) error {
	apiName, credentialID := args[0], args[1]
	profileName := c.profileFromCmd(cmd)
	apiCfg, prof, err := c.apiProfileForAuth(apiName, profileName, true)
	if err != nil {
		return err
	}
	if prof.Credentials == nil {
		prof.Credentials = map[string]*config.CredentialConfig{}
	}
	if prof.Credentials[credentialID] == nil {
		prof.Credentials[credentialID] = &config.CredentialConfig{}
	}
	defaultNeeds := c.cachedCredentialDefaultNeeds(requestContext(cmd), apiName, apiCfg, profileName, credentialID)
	if prof.Credentials[credentialID].Auth == nil {
		if authCfg, ok, err := c.cachedAuthConfigForCredential(apiName, apiCfg, credentialID); err != nil {
			return err
		} else if ok {
			prof.Credentials[credentialID].Auth = authCfg
		}
	}
	if prof.Credentials[credentialID].Auth != nil {
		if err := c.promptAuthParams(requestContext(cmd), profileName, credentialID, prof.Credentials[credentialID].Auth, defaultNeeds, configurePromptAnswers{}); err != nil {
			return err
		}
		if len(defaultNeeds) > 0 {
			if prof.Credentials[credentialID].Auth.Params == nil {
				prof.Credentials[credentialID].Auth.Params = map[string]string{}
			}
			if prof.Credentials[credentialID].Auth.Params["scopes"] == "" {
				prof.Credentials[credentialID].Auth.Params["scopes"] = strings.Join(defaultNeeds, " ")
			}
			prof.Credentials[credentialID].Satisfies = authSatisfiesValues(defaultNeeds, prof.Credentials[credentialID].Auth)
		}
	}
	if err := c.saveAPIAuthCredentialConfig(apiName, profileName, credentialID, prof.Credentials[credentialID]); err != nil {
		return err
	}
	c.printConfigWrittenPath()
	style := humanTextStyleFor(c.Stdout)
	fmt.Fprintf(c.Stdout, "%s credential %q to API %q profile %q.\n", style.ok("Added"), credentialID, apiName, profileName)
	if prof.Credentials[credentialID].Auth == nil && prof.Credentials[credentialID].AuthRef == "" {
		fmt.Fprintf(c.Stdout, "%s run \"restish api auth inspect %s\" to review credential readiness.\n", style.hint("Next:"), apiName)
	}
	return nil
}

func (c *CLI) runAPIAuthRemove(cmd *cobra.Command, args []string) error {
	apiName, credentialID := args[0], args[1]
	profileName := c.profileFromCmd(cmd)
	_, prof, err := c.apiProfileForAuth(apiName, profileName, false)
	if err != nil {
		return err
	}
	if prof.Credentials == nil || prof.Credentials[credentialID] == nil {
		return fmt.Errorf("profile %q of API %q has no credential %q", profileName, apiName, credentialID)
	}
	if err := c.removeAPIAuthCredentialConfig(apiName, profileName, credentialID); err != nil {
		return err
	}
	c.printConfigWrittenPath()
	style := humanTextStyleFor(c.Stdout)
	fmt.Fprintf(c.Stdout, "%s credential %q from API %q profile %q.\n", style.ok("Removed"), credentialID, apiName, profileName)
	return nil
}

func (c *CLI) runAPIAuthInspect(cmd *cobra.Command, args []string) error {
	if err := rejectResponseTransformFlags(cmd); err != nil {
		return err
	}
	apiName := args[0]
	if looksLikeURLArgument(apiName) {
		return fmt.Errorf("api auth inspect expects an API name, not a URL\nv2 form: restish api auth get <api-name>")
	}
	profileName := c.profileFromCmd(cmd)
	apiCfg, prof, err := c.apiProfileForAuth(apiName, profileName, false)
	if err != nil {
		return err
	}
	credentialID, _ := cmd.Flags().GetString("credential")
	redact, _ := cmd.Flags().GetBool("redact")
	focused := credentialID != ""
	if operation, _ := cmd.Flags().GetString("operation"); operation != "" {
		if credentialID != "" {
			return fmt.Errorf("--operation and --credential are mutually exclusive")
		}
		return c.runAPIAuthInspectOperation(cmd, apiName, profileName, apiCfg, prof, operation, redact)
	}

	targets, err := c.authInspectionTargets(apiName, profileName, prof, credentialID)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		if focused {
			return fmt.Errorf("profile %q of API %q has no auth config", profileName, apiName)
		}
		c.printAPIAuthOverview(cmd, apiName, profileName, apiCfg, prof)
		return nil
	}
	if !focused {
		c.printAPIAuthOverview(cmd, apiName, profileName, apiCfg, prof)
		fmt.Fprintln(c.Stdout)
		fmt.Fprintln(c.Stdout, "Auth material:")
	}
	for i, target := range targets {
		if i > 0 {
			fmt.Fprintln(c.Stdout)
		}
		if target.Label != "" {
			fmt.Fprintf(c.Stdout, "%s\n", target.Label)
		}
		if target.Resolved.Config == nil {
			return c.missingAuthConfigError(apiName, profileName)
		}
		if !focused {
			ready := c.resolvedAuthReadiness(apiName, profileName, target.Resolved)
			if !ready.Usable {
				if target.Resolved.Config != nil {
					fmt.Fprintf(c.Stdout, "Auth type: %s\n", target.Resolved.Config.Type)
				}
				fmt.Fprintf(c.Stdout, "Auth material: unavailable (%s)\n", strings.Join(ready.Issues, "; "))
				continue
			}
		}
		req, err := c.authInspectionRequest(cmd, apiName, profileName, target.Resolved)
		if err != nil {
			return err
		}
		c.printAuthInspectionRequest(req, []*config.AuthConfig{target.Resolved.Config}, redact)
	}
	return nil
}

type authInspectionTarget struct {
	Label        string
	CredentialID string
	Resolved     resolvedAuthConfig
}

func (c *CLI) authInspectionTargets(apiName, profileName string, prof *config.ProfileConfig, credentialID string) ([]authInspectionTarget, error) {
	if credentialID != "" {
		resolved, _, err := c.resolveAuthInspectionConfig(apiName, profileName, prof, credentialID)
		return []authInspectionTarget{{Label: fmt.Sprintf("Credential: %s", credentialID), CredentialID: credentialID, Resolved: resolved}}, err
	}

	var targets []authInspectionTarget
	resolved, err := c.resolveProfileAuth(apiName, profileName, prof)
	if err != nil {
		return nil, err
	}
	if resolved.Config != nil {
		targets = append(targets, authInspectionTarget{Resolved: resolved})
	}

	ids := configuredCredentialIDs(prof)
	if len(ids) == 0 {
		return targets, nil
	}
	for _, id := range ids {
		resolved, err := c.resolveCredentialAuth(apiName, profileName, id, prof.Credentials[id])
		if err != nil {
			return nil, fmt.Errorf("credential %q: %w", id, err)
		}
		targets = append(targets, authInspectionTarget{
			Label:        fmt.Sprintf("Credential: %s", id),
			CredentialID: id,
			Resolved:     resolved,
		})
	}
	if len(targets) > 1 && targets[0].Label == "" {
		targets[0].Label = "Profile auth:"
	}
	return targets, nil
}

func (c *CLI) runAPIAuthInspectOperation(cmd *cobra.Command, apiName, profileName string, apiCfg *config.APIConfig, prof *config.ProfileConfig, operationName string, redact bool) error {
	op, ok, err := c.cachedOperationForAPI(requestContext(cmd), apiName, apiCfg, profileName, operationName)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("operation %q not found in cached metadata for API %q; run \"restish api sync %s\"", operationName, apiName, apiName)
	}
	if op.NoAuth {
		fmt.Fprintf(c.Stdout, "Operation: %s\nAuth: none (security: [])\n", op.ID)
		return nil
	}
	if op.OptionalAuth && len(op.CredentialAlternatives) == 0 {
		fmt.Fprintf(c.Stdout, "Operation: %s\nAuth: none (security: [{}])\n", op.ID)
		return nil
	}

	var selected []selectedOperationAuth
	if len(op.CredentialAlternatives) > 0 {
		var selectedOK bool
		selected, selectedOK, err = c.planOperationAuth(apiName, profileName, prof, &operationAuthPolicy{
			OptionalAuth:           op.OptionalAuth,
			CredentialAlternatives: op.CredentialAlternatives,
		})
		if err != nil {
			return err
		}
		if !selectedOK || len(selected) == 0 {
			fmt.Fprintf(c.Stdout, "Operation: %s\nAuth: none\n", op.ID)
			return nil
		}
	} else {
		resolved, selectedCredential, err := c.resolveAuthInspectionConfig(apiName, profileName, prof, "")
		if err != nil {
			return err
		}
		if resolved.Config == nil {
			return fmt.Errorf("profile %q of API %q has no auth config", profileName, apiName)
		}
		selected = []selectedOperationAuth{{requirement: spec.CredentialRequirement{ID: selectedCredential}, resolved: resolved, source: "profile auth"}}
	}

	req, err := c.operationAuthInspectionRequest(cmd, apiName, profileName, selected)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.Stdout, "Operation: %s\n", op.ID)
	fmt.Fprintf(c.Stdout, "Credentials: %s\n", strings.Join(selectedOperationCredentialIDs(selected), ", "))
	fmt.Fprintf(c.Stdout, "Source: %s\n", strings.Join(selectedOperationSources(selected), ", "))
	c.printAuthInspectionRequest(req, selectedOperationAuthConfigs(selected), redact)
	return nil
}

func (c *CLI) runAPIAuthGet(cmd *cobra.Command, args []string) error {
	fragment, err := c.apiAuthGetFragment(cmd, args)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Stdout, fragment)
	return nil
}

func (c *CLI) apiAuthGetFragment(cmd *cobra.Command, args []string) (string, error) {
	printHeader, _ := cmd.Flags().GetBool("print-header")
	return c.apiAuthGetFragmentMode(cmd, args, printHeader)
}

func (c *CLI) apiAuthGetHeaderFragment(cmd *cobra.Command, args []string) (string, error) {
	return c.apiAuthGetFragmentMode(cmd, args, true)
}

func (c *CLI) apiAuthGetFragmentMode(cmd *cobra.Command, args []string, printHeader bool) (string, error) {
	apiName := args[0]
	if looksLikeURLArgument(apiName) {
		return "", fmt.Errorf("api auth get expects an API name, not a URL")
	}
	credentialID := ""
	if len(args) == 2 {
		credentialID = args[1]
	}
	profileName := c.profileFromCmd(cmd)
	apiCfg, prof, err := c.apiProfileForAuth(apiName, profileName, false)
	if err != nil {
		return "", err
	}
	if operation, _ := cmd.Flags().GetString("operation"); operation != "" {
		if credentialID != "" {
			return "", fmt.Errorf("--operation and credential ID are mutually exclusive")
		}
		return c.apiAuthGetOperationFragment(cmd, apiName, profileName, apiCfg, prof, operation, printHeader)
	}
	if credentialID == "" {
		resolvedProfile, err := c.resolveProfileAuth(apiName, profileName, prof)
		if err != nil {
			return "", err
		}
		if resolvedProfile.Config == nil {
			ids := configuredCredentialIDs(prof)
			if len(ids) > 1 {
				return "", fmt.Errorf("profile %q of API %q has multiple configured credentials (%s); pass a credential ID: %s api auth get %s <credential-id>", profileName, apiName, strings.Join(ids, ", "), c.commandNameOrDefault(), apiName)
			}
		}
	}

	resolved, _, err := c.resolveAuthInspectionConfig(apiName, profileName, prof, credentialID)
	if err != nil {
		return "", err
	}
	if resolved.Config == nil {
		return "", c.missingAuthConfigError(apiName, profileName)
	}
	req, err := c.authInspectionRequest(cmd, apiName, profileName, resolved)
	if err != nil {
		return "", err
	}
	configs := []*config.AuthConfig{resolved.Config}
	if printHeader {
		return authHeaderFragment(req, configs, apiName, profileName)
	}
	fragment, err := authGetFragment(req, configs)
	if err != nil {
		return "", fmt.Errorf("%w; run %q for details", err, c.commandNameOrDefault()+" api auth inspect "+apiName)
	}
	return fragment, nil
}

func (c *CLI) runAPIAuthGetOperation(cmd *cobra.Command, apiName, profileName string, apiCfg *config.APIConfig, prof *config.ProfileConfig, operationName string, printHeader bool) error {
	fragment, err := c.apiAuthGetOperationFragment(cmd, apiName, profileName, apiCfg, prof, operationName, printHeader)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Stdout, fragment)
	return nil
}

// runAPIAuthGetPrintHeader prints the single resolved auth header as
// "Name: value" and returns an error if the resolved auth is not a single
// header (for example, query parameter auth or a multi-credential
// combination). This is the stable, parseable contract that shell scripts
// and external Go binaries use to obtain a bearer token without going
// through the restish CLI's full request flow.
func (c *CLI) runAPIAuthGetPrintHeader(req *http.Request, configs []*config.AuthConfig, apiName, profileName string) error {
	fragment, err := authHeaderFragment(req, configs, apiName, profileName)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Stdout, fragment)
	return nil
}

func authHeaderFragment(req *http.Request, configs []*config.AuthConfig, apiName, profileName string) (string, error) {
	if req.URL != nil && req.URL.RawQuery != "" {
		return "", fmt.Errorf("auth for %s:%s is applied as a query parameter, not a header; --print-header is not applicable", apiName, profileName)
	}
	if authInspectionAppliesCookie(configs) {
		return "", fmt.Errorf("auth for %s:%s is applied as a cookie, not a header; --print-header is not applicable", apiName, profileName)
	}
	headers := 0
	var name, value string
	for _, h := range sortedHeaderKeys(req.Header) {
		for _, v := range req.Header[h] {
			headers++
			if headers == 1 {
				name = authInspectionDisplayHeaderName(configs, h)
				value = v
			}
		}
	}
	switch headers {
	case 0:
		return "", fmt.Errorf("auth for %s:%s did not produce a header value", apiName, profileName)
	case 1:
		return name + ": " + value, nil
	default:
		return "", fmt.Errorf("auth for %s:%s produced multiple header values; --print-header expects a single header", apiName, profileName)
	}
}

func authInspectionAppliesCookie(configs []*config.AuthConfig) bool {
	for _, ac := range configs {
		if ac != nil && ac.Type == "api-key" && strings.EqualFold(strings.TrimSpace(ac.Params["in"]), "cookie") {
			return true
		}
	}
	return false
}

func (c *CLI) apiAuthGetOperationFragment(cmd *cobra.Command, apiName, profileName string, apiCfg *config.APIConfig, prof *config.ProfileConfig, operationName string, printHeader bool) (string, error) {
	op, ok, err := c.cachedOperationForAPI(requestContext(cmd), apiName, apiCfg, profileName, operationName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("operation %q not found in cached metadata for API %q; run \"restish api sync %s\"", operationName, apiName, apiName)
	}
	if op.NoAuth {
		return "", fmt.Errorf("operation %q has security: [] and does not send auth material", op.ID)
	}
	if op.OptionalAuth && len(op.CredentialAlternatives) == 0 {
		return "", fmt.Errorf("operation %q has anonymous-only security and does not send auth material", op.ID)
	}

	var selected []selectedOperationAuth
	if len(op.CredentialAlternatives) > 0 {
		var selectedOK bool
		selected, selectedOK, err = c.planOperationAuth(apiName, profileName, prof, &operationAuthPolicy{
			OptionalAuth:           op.OptionalAuth,
			CredentialAlternatives: op.CredentialAlternatives,
		})
		if err != nil {
			return "", err
		}
		if !selectedOK || len(selected) == 0 {
			return "", fmt.Errorf("operation %q did not select auth material", op.ID)
		}
	} else {
		resolved, selectedCredential, err := c.resolveAuthInspectionConfig(apiName, profileName, prof, "")
		if err != nil {
			return "", err
		}
		if resolved.Config == nil {
			return "", fmt.Errorf("profile %q of API %q has no auth config", profileName, apiName)
		}
		selected = []selectedOperationAuth{{requirement: spec.CredentialRequirement{ID: selectedCredential}, resolved: resolved, source: "profile auth"}}
	}

	req, err := c.operationAuthInspectionRequest(cmd, apiName, profileName, selected)
	if err != nil {
		return "", err
	}
	configs := selectedOperationAuthConfigs(selected)
	if printHeader {
		return authHeaderFragment(req, configs, apiName, profileName)
	}
	fragment, err := authGetFragment(req, configs)
	if err != nil {
		return "", fmt.Errorf("%w; run %q for details", err, c.commandNameOrDefault()+" api auth inspect "+apiName+" --operation "+operationName)
	}
	return fragment, nil
}

func authGetFragment(req *http.Request, configs []*config.AuthConfig) (string, error) {
	var fragments []string
	for _, name := range sortedHeaderKeys(req.Header) {
		displayName := authInspectionDisplayHeaderName(configs, name)
		for _, value := range req.Header[name] {
			fragments = append(fragments, displayName+": "+value)
		}
	}
	if req.URL != nil && req.URL.RawQuery != "" {
		fragments = append(fragments, "?"+req.URL.RawQuery)
	}
	switch len(fragments) {
	case 0:
		return "", fmt.Errorf("auth did not produce a curl-friendly header or query fragment")
	case 1:
		return fragments[0], nil
	default:
		return "", fmt.Errorf("auth produced multiple header/query fragments")
	}
}

func (c *CLI) resolveAuthInspectionConfig(apiName, profileName string, prof *config.ProfileConfig, credentialID string) (resolvedAuthConfig, string, error) {
	if credentialID != "" {
		credential := prof.Credentials[credentialID]
		if credential == nil {
			return resolvedAuthConfig{}, "", fmt.Errorf("profile %q of API %q has no credential %q", profileName, apiName, credentialID)
		}
		resolved, err := c.resolveCredentialAuth(apiName, profileName, credentialID, credential)
		return resolved, credentialID, err
	}

	resolved, err := c.resolveProfileAuth(apiName, profileName, prof)
	if err != nil || resolved.Config != nil {
		return resolved, "", err
	}

	ids := configuredCredentialIDs(prof)
	switch len(ids) {
	case 0:
		if len(prof.Credentials) > 0 {
			return resolvedAuthConfig{}, "", fmt.Errorf("profile %q of API %q has credentials but none have auth configured; run \"restish api auth inspect %s\"", profileName, apiName, apiName)
		}
		return resolvedAuthConfig{}, "", nil
	case 1:
		credentialID := ids[0]
		resolved, err := c.resolveCredentialAuth(apiName, profileName, credentialID, prof.Credentials[credentialID])
		return resolved, credentialID, err
	default:
		return resolvedAuthConfig{}, "", fmt.Errorf("profile %q of API %q has multiple configured credentials (%s); pass --credential <id>", profileName, apiName, strings.Join(ids, ", "))
	}
}

func (c *CLI) missingAuthConfigError(apiName, profileName string) error {
	return fmt.Errorf("profile %q of API %q has no auth config; run %q to inspect credential coverage or %q to configure a credential", profileName, apiName, c.commandNameOrDefault()+" api auth inspect "+apiName, c.commandNameOrDefault()+" api auth add "+apiName+" <credential-id>")
}

func looksLikeURLArgument(arg string) bool {
	return strings.Contains(arg, "://") || strings.HasPrefix(arg, "/") || strings.ContainsAny(arg, "?#")
}

func configuredCredentialIDs(prof *config.ProfileConfig) []string {
	if prof == nil || len(prof.Credentials) == 0 {
		return nil
	}
	ids := make([]string, 0, len(prof.Credentials))
	for id, credential := range prof.Credentials {
		if credential != nil && (credential.Auth != nil || credential.AuthRef != "") {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func isAuthInspectionSensitiveHeader(ac *config.AuthConfig, name string) bool {
	return ac != nil &&
		ac.Type == "api-key" &&
		strings.EqualFold(ac.Params["in"], "header") &&
		strings.EqualFold(ac.Params["name"], name)
}

func isAuthInspectionSensitiveQueryParam(ac *config.AuthConfig, name string) bool {
	return ac != nil &&
		ac.Type == "api-key" &&
		strings.EqualFold(ac.Params["in"], "query") &&
		strings.EqualFold(ac.Params["name"], name)
}

func (c *CLI) operationAuthInspectionRequest(cmd *cobra.Command, apiName, profileName string, selected []selectedOperationAuth) (*http.Request, error) {
	authOpts, err := c.authHandlerOptionsFromCmd(cmd)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(requestContext(cmd), "GET", "http://example.com", nil)
	for _, item := range selected {
		step, err := c.operationAuthStep(apiName, profileName, item, authOpts)
		if err != nil {
			return nil, err
		}
		if err := c.applyOperationAuthInspectionStep(req, step); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func (c *CLI) applyOperationAuthInspectionStep(req *http.Request, s operationAuthStep) error {
	params, err := c.buildAuthParams(s.rawParams)
	if err != nil {
		return err
	}
	if s.authType == "external-tool" {
		if err := c.ensureExternalToolApproved(req.Context(), s.apiName, s.profileName, params["commandline"]); err != nil {
			return err
		}
	}
	return s.handler.Authenticate(req.Context(), req, c.authContext(req.Context(), s.apiName, s.profileName, params, s.cacheKey, false, false))
}

func (c *CLI) authInspectionRequest(cmd *cobra.Command, apiName, profileName string, resolved resolvedAuthConfig) (*http.Request, error) {
	authOpts, err := c.authHandlerOptionsFromCmd(cmd)
	if err != nil {
		return nil, err
	}
	handler, err := c.authHandlerFor(resolved.Config, authOpts)
	if err != nil {
		return nil, err
	}
	params, err := c.buildAuthParams(resolved.Config.Params)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(requestContext(cmd), "GET", "http://example.com", nil)
	if err := handler.Authenticate(requestContext(cmd), req, c.authContext(requestContext(cmd), apiName, profileName, params, resolved.CacheKey, false, false)); err != nil {
		return nil, fmt.Errorf("building auth inspection: %w", err)
	}
	return req, nil
}

func (c *CLI) printAuthInspectionRequest(req *http.Request, configs []*config.AuthConfig, redact bool) {
	for _, ac := range configs {
		if ac != nil {
			fmt.Fprintf(c.Stdout, "Auth type: %s\n", ac.Type)
		}
	}
	for _, name := range sortedHeaderKeys(req.Header) {
		values := req.Header[name]
		displayName := authInspectionDisplayHeaderName(configs, name)
		for _, value := range values {
			if redact && (isSensitiveHeader(name) || authInspectionSensitiveHeader(configs, name)) {
				value = "<redacted>"
			}
			fmt.Fprintf(c.Stdout, "%s: %s\n", displayName, value)
		}
	}
	if req.URL.RawQuery != "" {
		query := req.URL.String()
		if redact {
			query = redactedAuthInspectionURL(req.URL, configs)
		}
		fmt.Fprintf(c.Stdout, "Query: %s\n", query)
	}
}

func authInspectionSensitiveHeader(configs []*config.AuthConfig, name string) bool {
	for _, ac := range configs {
		if isAuthInspectionSensitiveHeader(ac, name) {
			return true
		}
	}
	return false
}

func authInspectionDisplayHeaderName(configs []*config.AuthConfig, name string) string {
	for _, ac := range configs {
		if isAuthInspectionSensitiveHeader(ac, name) && ac.Params["name"] != "" {
			return ac.Params["name"]
		}
	}
	return name
}

func authInspectionSensitiveQueryParam(configs []*config.AuthConfig, name string) bool {
	for _, ac := range configs {
		if isAuthInspectionSensitiveQueryParam(ac, name) {
			return true
		}
	}
	return false
}

func redactedAuthInspectionURL(u *url.URL, configs []*config.AuthConfig) string {
	if u == nil {
		return ""
	}
	copyURL := *u
	q := copyURL.Query()
	for name := range q {
		if isSensitiveQueryParam(name) || authInspectionSensitiveQueryParam(configs, name) {
			q.Set(name, "<redacted>")
		}
	}
	copyURL.RawQuery = q.Encode()
	return copyURL.String()
}

func selectedOperationCredentialIDs(selected []selectedOperationAuth) []string {
	ids := make([]string, 0, len(selected))
	for _, item := range selected {
		if item.requirement.ID != "" {
			ids = append(ids, item.requirement.ID)
		}
	}
	return ids
}

func selectedOperationSources(selected []selectedOperationAuth) []string {
	sources := make([]string, 0, len(selected))
	for _, item := range selected {
		source := item.source
		if source == "" {
			source = selectedAuthSourceCredential(item.resolved)
		}
		sources = append(sources, source)
	}
	return sources
}

func selectedOperationAuthConfigs(selected []selectedOperationAuth) []*config.AuthConfig {
	configs := make([]*config.AuthConfig, 0, len(selected))
	for _, item := range selected {
		configs = append(configs, item.resolved.Config)
	}
	return configs
}

func (c *CLI) apiProfileForAuth(apiName, profileName string, create bool) (*config.APIConfig, *config.ProfileConfig, error) {
	apiCfg, err := c.requireAPI(apiName)
	if err != nil {
		return nil, nil, fmt.Errorf("%w; run \"restish api list\" to see configured APIs", err)
	}
	if apiCfg.Profiles != nil && apiCfg.Profiles[profileName] != nil {
		return apiCfg, apiCfg.Profiles[profileName], nil
	}
	if profileName == "default" {
		prof := &config.ProfileConfig{}
		if create {
			if apiCfg.Profiles == nil {
				apiCfg.Profiles = map[string]*config.ProfileConfig{}
			}
			apiCfg.Profiles[profileName] = prof
		}
		return apiCfg, prof, nil
	}
	if create {
		if apiCfg.Profiles == nil {
			apiCfg.Profiles = map[string]*config.ProfileConfig{}
		}
		prof := &config.ProfileConfig{}
		apiCfg.Profiles[profileName] = prof
		return apiCfg, prof, nil
	}
	if apiCfg.Profiles == nil || apiCfg.Profiles[profileName] == nil {
		return nil, nil, fmt.Errorf("API %q has no profile %q; configured profiles: %s", apiName, profileName, profileNames(apiCfg.Profiles))
	}
	return apiCfg, apiCfg.Profiles[profileName], nil
}

func (c *CLI) cachedOperationSetForAPI(ctx context.Context, apiName string, apiCfg *config.APIConfig, profileName string) (spec.OperationSet, bool) {
	if set, _, ok := c.cachedOperationSetStatusForAPI(apiName, apiCfg, profileName); ok {
		return set, true
	}
	set, ok, _ := c.operationSetForAPI(ctx, apiName, apiCfg, profileName, false)
	return set, ok
}

func (c *CLI) cachedOperationSetStatusForAPI(apiName string, apiCfg *config.APIConfig, profileName string) (spec.OperationSet, spec.OperationCacheStatus, bool) {
	if apiCfg == nil {
		return spec.OperationSet{}, spec.OperationCacheStatus{}, false
	}
	opts := spec.OperationOptions{
		BaseURL:         effectiveProfileBaseURL(apiCfg, profileName),
		OperationBase:   effectiveOperationBase(apiCfg, profileName),
		ServerVariables: effectiveServerVariables(apiCfg, profileName),
		Warnf:           c.warnf,
	}
	stateName := c.apiStateName(apiName)
	if set, status, ok := spec.LoadOperationSetFromCacheStatus(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles, opts, true); ok {
		return set, status, true
	}
	opts.BaseURL = apiCfg.BaseURL
	opts.OperationBase = apiCfg.OperationBase
	return spec.LoadOperationSetFromCacheStatus(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles, opts, true)
}

func (c *CLI) operationSetForAPI(ctx context.Context, apiName string, apiCfg *config.APIConfig, profileName string, forceRefresh bool) (spec.OperationSet, bool, error) {
	if apiCfg == nil {
		return spec.OperationSet{}, false, nil
	}
	opts := spec.OperationOptions{
		BaseURL:         effectiveProfileBaseURL(apiCfg, profileName),
		OperationBase:   effectiveOperationBase(apiCfg, profileName),
		ServerVariables: effectiveServerVariables(apiCfg, profileName),
		Warnf:           c.warnf,
	}
	if !forceRefresh {
		if set, _, ok := spec.LoadOperationSetFromCacheStatus(c.specCacheDir(), c.apiStateName(apiName), Version, apiCfg.SpecFiles, opts, true); ok {
			c.warnOperationSetWarnings(set)
			return set, true, nil
		}
	}
	var s *spec.APISpec
	var err error
	if forceRefresh {
		s, err = c.discoverSpecForProfile(ctx, apiName, profileName, true, 0)
	} else {
		s, err = spec.LoadStaleFromCache(c.specCacheDir(), c.apiStateName(apiName), Version, apiCfg.SpecFiles, c.loaders)
	}
	if err != nil || s == nil {
		if !spec.HasLocalSpecFiles(apiCfg.SpecFiles) {
			if err == nil && !forceRefresh && (apiCfg.BaseURL != "" || apiCfg.SpecURL != "" || len(apiCfg.SpecFiles) > 0) {
				c.hintf("spec cache for API %q is empty; run \"restish api sync %s\" to populate it", apiName, apiName)
			}
			return spec.OperationSet{}, false, err
		}
		s, err = spec.Discover(ctx, spec.DiscoverConfig{
			APIName:         c.apiStateName(apiName),
			BaseURL:         effectiveProfileBaseURL(apiCfg, profileName),
			SpecFiles:       apiCfg.SpecFiles,
			CacheDir:        c.specCacheDir(),
			OperationBase:   effectiveOperationBase(apiCfg, profileName),
			ServerVariables: effectiveServerVariables(apiCfg, profileName),
			Version:         Version,
			ForceRefresh:    true,
		}, c.loaders)
		if err != nil || s == nil {
			return spec.OperationSet{}, false, err
		}
	}
	set, err := s.OperationSet(opts)
	if err != nil {
		return spec.OperationSet{}, false, err
	}
	_ = spec.StoreOperationSetInCache(c.specCacheDir(), c.apiStateName(apiName), Version, opts, set)
	return set, true, nil
}

func (c *CLI) warnOperationSetWarnings(set spec.OperationSet) {
	for _, warning := range set.Warnings {
		c.warnf("%s", warning)
	}
}

func (c *CLI) cachedOperationForAPI(ctx context.Context, apiName string, apiCfg *config.APIConfig, profileName, value string) (spec.Operation, bool, error) {
	set, ok := c.cachedOperationSetForAPI(ctx, apiName, apiCfg, profileName)
	if !ok {
		return spec.Operation{}, false, nil
	}
	operationBase := effectiveOperationBase(apiCfg, profileName)
	for _, op := range set.Operations {
		if op.ID == value || operationCommandName(op, operationBase) == value {
			return op, true, nil
		}
	}
	return spec.Operation{}, false, nil
}

func operationCommandName(op spec.Operation, operationBase string) string {
	if op.XCLI.Name != "" {
		return op.XCLI.Name
	}
	if name := toKebabCase(op.ID); name != "" {
		return name
	}
	return fallbackOperationName(op.Method, operationNamePath(op.Path, operationBase))
}

func configuredCredentialsForProfile(prof *config.ProfileConfig) map[string]bool {
	out := map[string]bool{}
	if prof == nil {
		return out
	}
	for id, credential := range prof.Credentials {
		if credential != nil && (credential.Auth != nil || credential.AuthRef != "") {
			out[id] = true
		}
	}
	return out
}

type authRequirementSummary struct {
	id         string
	kind       string
	needs      []string
	opCount    int
	external   bool
	undeclared bool
	deprecated bool
}

func (c *CLI) printAPIAuthRequirementSummary(apiName, profileName string, ops []spec.Operation, prof *config.ProfileConfig, coverage operationAuthCoverage) {
	summaries := authRequirementSummaries(ops)
	if len(summaries) == 0 {
		return
	}
	style := humanTextStyleFor(c.Stdout)
	fmt.Fprintln(c.Stdout, "Declared credentials:")
	for _, summary := range summaries {
		status := "missing"
		var satisfies []string
		if summary.kind == "mtls" {
			if mtlsStatus, usable := operationMTLSStatusFromProfile(prof); mtlsStatus != "" {
				status = mtlsStatus
				if !usable {
					status = "configured (" + mtlsStatus + ")"
				}
			}
		} else if prof != nil && prof.Credentials != nil {
			if credential := prof.Credentials[summary.id]; credential != nil {
				_, ready, _ := c.credentialReadiness(apiName, profileName, summary.id, credential)
				status = ready.status("empty")
				satisfies = credential.Satisfies
			}
		}
		var parts []string
		parts = append(parts, style.authStatus(status))
		if status == "missing" && coverage.FallbackByID[summary.id] > 0 {
			parts = append(parts, coverage.FallbackLabels[summary.id])
		}
		if len(summary.needs) > 0 {
			parts = append(parts, "needs "+strings.Join(summary.needs, " "))
		}
		if len(satisfies) > 0 {
			parts = append(parts, "satisfies "+strings.Join(satisfies, " "))
		}
		if !authRequirementKindSupported(summary.kind) {
			parts = append(parts, "unsupported "+summary.kind)
		}
		if summary.undeclared {
			parts = append(parts, "undeclared security scheme")
		}
		if summary.deprecated {
			parts = append(parts, "deprecated")
		}
		if summary.external {
			parts = append(parts, "URI-backed")
		}
		operationWord := "operations"
		if summary.opCount == 1 {
			operationWord = "operation"
		}
		parts = append(parts, fmt.Sprintf("%d %s", summary.opCount, operationWord))
		fmt.Fprintf(c.Stdout, "  %s: %s\n", summary.id, strings.Join(parts, ", "))
	}
}

func nextMissingCredentialID(ops []spec.Operation, prof *config.ProfileConfig, coverage operationAuthCoverage) string {
	for _, summary := range authRequirementSummaries(ops) {
		if !authRequirementKindSupported(summary.kind) || summary.undeclared || summary.external {
			continue
		}
		if coverage.FallbackByID[summary.id] > 0 {
			continue
		}
		if prof != nil && prof.Credentials != nil {
			if credential := prof.Credentials[summary.id]; credential != nil && (credential.Auth != nil || credential.AuthRef != "") {
				continue
			}
		}
		return summary.id
	}
	return ""
}

func authRequirementSummaries(ops []spec.Operation) []authRequirementSummary {
	byID := map[string]*authRequirementSummary{}
	opSeen := map[string]map[int]bool{}
	for opIndex, op := range ops {
		for _, alternative := range op.CredentialAlternatives {
			for _, requirement := range alternative {
				summary := byID[requirement.ID]
				if summary == nil {
					summary = &authRequirementSummary{id: requirement.ID, kind: requirement.Kind}
					byID[requirement.ID] = summary
				}
				summary.needs = mergeStringSet(summary.needs, requirement.Needs)
				summary.external = summary.external || requirement.External
				summary.undeclared = summary.undeclared || requirement.Undeclared
				summary.deprecated = summary.deprecated || requirement.Deprecated
				if opSeen[requirement.ID] == nil {
					opSeen[requirement.ID] = map[int]bool{}
				}
				if !opSeen[requirement.ID][opIndex] {
					summary.opCount++
					opSeen[requirement.ID][opIndex] = true
				}
			}
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]authRequirementSummary, 0, len(ids))
	for _, id := range ids {
		sort.Strings(byID[id].needs)
		out = append(out, *byID[id])
	}
	return out
}

func mergeStringSet(existing, values []string) []string {
	seen := map[string]bool{}
	for _, v := range existing {
		seen[v] = true
	}
	for _, v := range values {
		if v != "" && !seen[v] {
			existing = append(existing, v)
			seen[v] = true
		}
	}
	return existing
}

func authRequirementKindSupported(kind string) bool {
	switch kind {
	case "api-key", "http-basic", "http-bearer", "oauth2", "mtls":
		return true
	default:
		return false
	}
}

func (c *CLI) cachedCredentialDefaultNeeds(ctx context.Context, apiName string, apiCfg *config.APIConfig, profileName, credentialID string) []string {
	set, ok := c.cachedOperationSetForAPI(ctx, apiName, apiCfg, profileName)
	if !ok {
		return nil
	}
	needs := credentialNeedDefaults(set.Operations)[credentialID]
	return append([]string(nil), needs...)
}

func (c *CLI) cachedAuthConfigForCredential(apiName string, apiCfg *config.APIConfig, credentialID string) (*config.AuthConfig, bool, error) {
	if apiCfg == nil {
		return nil, false, nil
	}
	apiSpec, err := spec.LoadFromCache(c.specCacheDir(), c.apiStateName(apiName), Version, apiCfg.SpecFiles, c.loaders)
	if err != nil || apiSpec == nil {
		return nil, false, err
	}
	xcli := (&spec.XCLIConfig{Profiles: map[string]*spec.XCLIProfile{
		"default": {
			Credentials: map[string]*spec.XCLICredential{
				credentialID: {},
			},
		},
	}}).Resolve(apiSpec)
	if xcli == nil || xcli.Profiles["default"] == nil || xcli.Profiles["default"].Credentials[credentialID] == nil {
		return nil, false, nil
	}
	auth := xcli.Profiles["default"].Credentials[credentialID].Auth
	if auth == nil {
		return nil, false, nil
	}
	return &config.AuthConfig{Type: auth.Type, Params: auth.Params}, true, nil
}

func (c *CLI) saveAPIAuthCredentialConfig(apiName, profileName, credentialID string, credential *config.CredentialConfig) error {
	if err := c.ensureMutableAPI(apiName); err != nil {
		return err
	}
	return c.saveConfigMutation("api auth", func(cfg *config.Config) error {
		apiCfg := cfg.APIs[apiName]
		if apiCfg == nil {
			return c.unknownAPIError(apiName)
		}
		if apiCfg.Profiles == nil {
			apiCfg.Profiles = map[string]*config.ProfileConfig{}
		}
		prof := apiCfg.Profiles[profileName]
		if prof == nil {
			prof = &config.ProfileConfig{}
			apiCfg.Profiles[profileName] = prof
		}
		if prof.Credentials == nil {
			prof.Credentials = map[string]*config.CredentialConfig{}
		}
		if credential == nil {
			credential = &config.CredentialConfig{}
		}
		prof.Credentials[credentialID] = credential
		return nil
	})
}

func (c *CLI) removeAPIAuthCredentialConfig(apiName, profileName, credentialID string) error {
	if err := c.ensureMutableAPI(apiName); err != nil {
		return err
	}
	return c.saveConfigMutation("api auth", func(cfg *config.Config) error {
		apiCfg := cfg.APIs[apiName]
		if apiCfg == nil {
			return c.unknownAPIError(apiName)
		}
		prof := profileForName(apiCfg, profileName)
		if prof == nil || prof.Credentials == nil || prof.Credentials[credentialID] == nil {
			return fmt.Errorf("profile %q of API %q has no credential %q", profileName, apiName, credentialID)
		}
		delete(prof.Credentials, credentialID)
		if len(prof.Credentials) == 0 {
			prof.Credentials = nil
		}
		return nil
	})
}
