package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/spec"
)

type configureAuthDiscovery struct {
	info         spec.APIInfo
	sourceURL    string
	schemes      []spec.SecuritySchemeSummary
	operations   []spec.Operation
	opCounts     map[string]int
	needDefaults map[string][]string
}

func newConfigureAuthDiscovery(apiSpec *spec.APISpec, baseURL string) configureAuthDiscovery {
	var d configureAuthDiscovery
	if apiSpec == nil {
		return d
	}
	d.sourceURL = apiSpec.SourceURL
	if info, err := apiSpec.Info(); err == nil {
		d.info = info
	}
	if schemes, err := apiSpec.SecuritySchemeSummaries(); err == nil {
		d.schemes = schemes
	}
	if ops, err := apiSpec.Operations(spec.OperationOptions{BaseURL: baseURL}); err == nil {
		d.operations = ops
		d.opCounts = credentialOperationCounts(ops)
		d.needDefaults = credentialNeedDefaults(ops)
	}
	return d
}

func (c *CLI) printAPIDiscovery(apiName, baseURL string, d configureAuthDiscovery) {
	title := d.info.Title
	if title == "" {
		title = apiName
	}
	fmt.Fprintf(c.Stdout, "Discovered %s\n", title)
	fmt.Fprintf(c.Stdout, "Base URL: %s\n", baseURL)
	if d.sourceURL != "" {
		fmt.Fprintf(c.Stdout, "OpenAPI:  %s\n", d.sourceURL)
	}
	if len(d.schemes) == 0 {
		fmt.Fprintln(c.Stdout)
		return
	}
	fmt.Fprintln(c.Stdout)
	fmt.Fprintf(c.Stdout, "This API declares %d auth scheme(s):\n\n", len(d.schemes))
	for _, scheme := range d.schemes {
		count := d.opCounts[scheme.ID]
		var details []string
		if needs := d.needDefaults[scheme.ID]; len(needs) > 0 {
			details = append(details, "needs "+strings.Join(needs, " "))
		}
		if scheme.GlobalDefault {
			details = append(details, "global default")
		}
		if scheme.Kind == "mtls" {
			details = append(details, "use TLS client certificate or signer")
		} else if !scheme.Supported {
			if c.authResolverAvailable(apiName) {
				details = append(details, "resolver evaluated at request time")
			} else {
				details = append(details, "unsupported")
			}
		}
		if scheme.Deprecated {
			details = append(details, "deprecated")
		}
		suffix := ""
		if len(details) > 0 {
			suffix = ", " + strings.Join(details, ", ")
		}
		fmt.Fprintf(c.Stdout, "  %-14s %-32s %3d operations%s\n", scheme.ID, scheme.Detail, count, suffix)
	}
	fmt.Fprintln(c.Stdout)
}

func (c *CLI) configureFallbackAuth(ctx context.Context, apiCfg *config.APIConfig, d configureAuthDiscovery, answers configurePromptAnswers) error {
	if apiCfg == nil || apiCfg.Profiles == nil || apiCfg.Profiles["default"] == nil || len(d.schemes) == 0 {
		return nil
	}
	prof := apiCfg.Profiles["default"]
	if prof.Credentials == nil {
		prof.Credentials = map[string]*config.CredentialConfig{}
	}

	supported := supportedSchemeIDs(d.schemes)
	if len(supported) == 0 {
		return nil
	}
	if err := c.validateFallbackCredentialAnswers("default", prof, d, answers); err != nil {
		return err
	}

	fmt.Fprintf(c.Stdout, "Configure auth for profile %q.\n\n", "default")
	configured := map[string]bool{}
	for _, scheme := range d.schemes {
		credential := prof.Credentials[scheme.ID]
		if !scheme.Supported || credential == nil || credential.Auth == nil {
			delete(prof.Credentials, scheme.ID)
			continue
		}
		configure := true
		if len(supported) > 1 && !answers.hasCredentialAnswer("default", scheme.ID) {
			def := defaultConfigureSchemeID(d.schemes, d.opCounts) == scheme.ID
			ok, err := c.promptYesNoDefault(ctx, fmt.Sprintf("Configure %s? %s ", scheme.ID, yesNoDefaultSuffix(def)), def)
			if err != nil {
				return err
			}
			configure = ok
		}
		if !configure {
			delete(prof.Credentials, scheme.ID)
			if prof.Auth != nil && (scheme.GlobalDefault || len(supported) == 1) {
				prof.Auth = nil
			}
			continue
		}
		defaultNeeds := d.needDefaults[scheme.ID]
		if err := c.promptAuthParams(ctx, "default", scheme.ID, credential.Auth, defaultNeeds, answers); err != nil {
			return err
		}
		if credential.Auth.Type == "dpop" {
			delete(credential.Auth.Params, "scopes")
			credential.Satisfies = nil
		}
		if len(defaultNeeds) > 0 && credential.Auth.Type != "dpop" {
			if credential.Auth.Params == nil {
				credential.Auth.Params = map[string]string{}
			}
			if credential.Auth.Params["scopes"] == "" {
				credential.Auth.Params["scopes"] = strings.Join(defaultNeeds, " ")
			}
			credential.Satisfies = authSatisfiesValues(defaultNeeds, credential.Auth)
		}
		configured[scheme.ID] = true
		if prof.Auth != nil && (scheme.GlobalDefault || len(supported) == 1) {
			prof.Auth.Params = cloneAuthParams(credential.Auth.Params)
		}
	}
	if len(prof.Credentials) == 0 {
		prof.Credentials = nil
	}
	if len(supported) > 1 && !configuredGlobalScheme(d.schemes, configured) {
		prof.Auth = nil
	}
	return nil
}

func (c *CLI) validateFallbackCredentialAnswers(profileName string, prof *config.ProfileConfig, d configureAuthDiscovery, answers configurePromptAnswers) error {
	profileAnswers := answers.credentials[profileName]
	if len(profileAnswers) == 0 {
		return nil
	}
	valid := map[string]map[string]bool{}
	for _, scheme := range d.schemes {
		if !scheme.Supported {
			continue
		}
		credential := prof.Credentials[scheme.ID]
		if credential == nil || credential.Auth == nil {
			continue
		}
		handler, err := c.authHandlerFor(credential.Auth, authHandlerOptions{})
		if err != nil {
			return err
		}
		params := configureAuthPromptParams(handler, d.needDefaults[scheme.ID])
		if len(params) == 0 {
			continue
		}
		if valid[scheme.ID] == nil {
			valid[scheme.ID] = map[string]bool{}
		}
		for _, p := range params {
			valid[scheme.ID][p.Name] = true
		}
	}
	var invalid []string
	for credentialID, credentialAnswers := range profileAnswers {
		for name := range credentialAnswers {
			if valid[credentialID][name] {
				continue
			}
			invalid = append(invalid, credentialAnswerPath(profileName, credentialID, name))
		}
	}
	if len(invalid) == 0 {
		return nil
	}
	sort.Strings(invalid)
	return fmt.Errorf("unused auth setup value(s): %s; valid credential setup value(s): %s", strings.Join(invalid, ", "), strings.Join(validCredentialAnswerPaths(profileName, valid), ", "))
}

func validCredentialAnswerPaths(profileName string, valid map[string]map[string]bool) []string {
	var paths []string
	var credentialIDs []string
	for credentialID := range valid {
		credentialIDs = append(credentialIDs, credentialID)
	}
	sort.Strings(credentialIDs)
	for _, credentialID := range credentialIDs {
		var names []string
		for name := range valid[credentialID] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			paths = append(paths, credentialAnswerPath(profileName, credentialID, name))
		}
	}
	if len(paths) == 0 {
		return []string{"none"}
	}
	return paths
}

func configuredGlobalScheme(schemes []spec.SecuritySchemeSummary, configured map[string]bool) bool {
	for _, scheme := range schemes {
		if scheme.GlobalDefault && configured[scheme.ID] {
			return true
		}
	}
	return false
}

func defaultConfigureSchemeID(schemes []spec.SecuritySchemeSummary, opCounts map[string]int) string {
	var best string
	bestCount := -1
	for _, scheme := range schemes {
		if !scheme.Supported || scheme.Deprecated {
			continue
		}
		count := opCounts[scheme.ID]
		if best == "" ||
			(scheme.GlobalDefault && !schemeByID(schemes, best).GlobalDefault) ||
			(scheme.GlobalDefault == schemeByID(schemes, best).GlobalDefault && count > bestCount) {
			best = scheme.ID
			bestCount = count
		}
	}
	return best
}

func schemeByID(schemes []spec.SecuritySchemeSummary, id string) spec.SecuritySchemeSummary {
	for _, scheme := range schemes {
		if scheme.ID == id {
			return scheme
		}
	}
	return spec.SecuritySchemeSummary{}
}

func (c *CLI) promptAuthParams(ctx context.Context, profileName, credentialID string, ac *config.AuthConfig, defaultNeeds []string, answers configurePromptAnswers) error {
	if ac == nil {
		return nil
	}
	if ac.Params == nil {
		ac.Params = map[string]string{}
	}
	handler, err := c.authHandlerFor(ac, authHandlerOptions{})
	if err != nil {
		return err
	}
	promptParams := configureAuthPromptParams(handler, defaultNeeds)
	if !c.canPromptInteractively() {
		missing := missingAuthSetupExpressionKeys(profileName, credentialID, ac, promptParams, answers)
		if len(missing) > 0 {
			return fmt.Errorf("missing required auth setup value(s) for credential %s: provide %s", credentialID, strings.Join(missing, ", "))
		}
		applyNoninteractiveAuthParamValues(profileName, credentialID, ac, promptParams, defaultNeeds, answers)
		return nil
	}
	for _, p := range promptParams {
		if ac.Params[p.Name] != "" {
			continue
		}
		if answer, ok := answers.answerCredential(profileName, credentialID, p.Name); ok {
			ac.Params[p.Name] = strings.TrimSpace(answer)
			if ac.Params[p.Name] == "" && p.Required {
				return fmt.Errorf("%s is required for credential %s", p.Name, credentialID)
			}
			continue
		}
		value, err := c.readAuthParam(ctx, p, defaultNeeds)
		if err != nil {
			return fmt.Errorf("%s %s: %w", credentialID, p.Name, err)
		}
		if value != "" {
			ac.Params[p.Name] = value
		}
	}
	return nil
}

func applyNoninteractiveAuthParamValues(profileName, credentialID string, ac *config.AuthConfig, params []auth.Param, defaultNeeds []string, answers configurePromptAnswers) {
	for _, p := range params {
		if ac.Params[p.Name] != "" {
			continue
		}
		if answer, ok := answers.answerCredential(profileName, credentialID, p.Name); ok {
			ac.Params[p.Name] = strings.TrimSpace(answer)
			continue
		}
		if value := defaultAuthParamValue(p, defaultNeeds); value != "" {
			ac.Params[p.Name] = value
		}
	}
}

func defaultAuthParamValue(p auth.Param, defaultNeeds []string) string {
	if p.Name == "scopes" && len(defaultNeeds) > 0 {
		return strings.Join(defaultNeeds, " ")
	}
	return ""
}

func authSatisfiesValues(defaultNeeds []string, ac *config.AuthConfig) []string {
	if ac != nil && ac.Type == "dpop" {
		return nil
	}
	if ac != nil && ac.Params != nil {
		if scopes := uniqueStrings(strings.Fields(ac.Params["scopes"])); len(scopes) > 0 {
			return scopes
		}
	}
	return append([]string(nil), defaultNeeds...)
}

func missingAuthSetupExpressionKeys(profileName, credentialID string, ac *config.AuthConfig, params []auth.Param, answers configurePromptAnswers) []string {
	var missing []string
	for _, p := range params {
		if !p.Required || ac.Params[p.Name] != "" {
			continue
		}
		if answer, ok := answers.answerCredential(profileName, credentialID, p.Name); ok && strings.TrimSpace(answer) != "" {
			continue
		}
		missing = append(missing, authSetupExpressionKey(profileName, credentialID, p.Name))
	}
	return missing
}

func authSetupExpressionKey(profileName, credentialID, paramName string) string {
	if profileName == "" || profileName == "default" {
		return fmt.Sprintf("prompt.credentials.%s.%s:<value>", credentialID, paramName)
	}
	return fmt.Sprintf("prompt.%s.credentials.%s.%s:<value>", profileName, credentialID, paramName)
}

func configureAuthPromptParams(handler auth.Handler, defaultNeeds []string) []auth.Param {
	params := handler.Parameters()
	var out []auth.Param
	for _, p := range params {
		switch p.Name {
		case "in", "name", "token_url", "authorize_url", "issuer_url", "device_authorization_url", "redirect_port", "auth_method":
			continue
		case "password":
			p.Required = true
		case "scopes":
			if len(defaultNeeds) == 0 {
				continue
			}
		}
		if p.Required || p.Name == "scopes" {
			out = append(out, p)
		}
	}
	return out
}

func (c *CLI) readAuthParam(ctx context.Context, p auth.Param, defaultNeeds []string) (string, error) {
	label := authParamLabel(p, defaultNeeds)
	var (
		value string
		err   error
	)
	if p.Secret {
		value, err = c.Secret(ctx, label)
	} else {
		value, err = c.Prompt(ctx, label)
	}
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" && p.Name == "scopes" && len(defaultNeeds) > 0 {
		value = strings.Join(defaultNeeds, " ")
	}
	if value == "" && p.Required {
		return "", fmt.Errorf("required value was empty")
	}
	return value, nil
}

func authParamLabel(p auth.Param, defaultNeeds []string) string {
	switch p.Name {
	case "client_id":
		return "Client ID: "
	case "client_secret":
		return "Client Secret: "
	case "username":
		return "Username: "
	case "password":
		return "Password: "
	case "token":
		return "Token: "
	case "value":
		return "API key: "
	case "scopes":
		return fmt.Sprintf("Scopes [%s]: ", strings.Join(defaultNeeds, " "))
	default:
		if p.Description != "" {
			return p.Description + ": "
		}
		return p.Name + ": "
	}
}

func (c *CLI) promptYesNoDefault(ctx context.Context, label string, def bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	fmt.Fprint(c.Stderr, label)
	src, cleanup := c.promptSource()
	defer cleanup()
	line, err := readPromptLine(src)
	if errors.Is(err, io.EOF) && line == "" {
		return false, nil
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("confirm: %w", err)
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	if answer == "" {
		return def, nil
	}
	return answer == "y" || answer == "yes", nil
}

func yesNoDefaultSuffix(def bool) string {
	if def {
		return "[Y/n]"
	}
	return "[y/N]"
}

func supportedSchemeIDs(schemes []spec.SecuritySchemeSummary) []string {
	var ids []string
	for _, scheme := range schemes {
		if scheme.Supported {
			ids = append(ids, scheme.ID)
		}
	}
	return ids
}

func credentialOperationCounts(ops []spec.Operation) map[string]int {
	counts := map[string]int{}
	for _, op := range ops {
		seen := map[string]bool{}
		for _, alternative := range op.CredentialAlternatives {
			for _, requirement := range alternative {
				seen[requirement.ID] = true
			}
		}
		for id := range seen {
			counts[id]++
		}
	}
	return counts
}

func credentialNeedDefaults(ops []spec.Operation) map[string][]string {
	needs := map[string]map[string]bool{}
	for _, op := range ops {
		for _, alternative := range op.CredentialAlternatives {
			for _, requirement := range alternative {
				if len(requirement.Needs) == 0 {
					continue
				}
				if needs[requirement.ID] == nil {
					needs[requirement.ID] = map[string]bool{}
				}
				for _, need := range requirement.Needs {
					needs[requirement.ID][need] = true
				}
			}
		}
	}
	out := map[string][]string{}
	for id, values := range needs {
		for value := range values {
			out[id] = append(out[id], value)
		}
		sort.Strings(out[id])
	}
	return out
}

func (c *CLI) printAuthCoverage(apiName, profileName string, apiCfg *config.APIConfig, d configureAuthDiscovery) {
	if len(d.schemes) == 0 {
		return
	}
	configured := configuredCredentials(apiCfg, profileName)
	var configuredIDs, skippedIDs []string
	var unresolved []string
	for _, scheme := range d.schemes {
		if configured[scheme.ID] {
			configuredIDs = append(configuredIDs, scheme.ID)
			if apiCfg != nil {
				if status, ok := c.authCoverageCredentialStatus(apiName, profileName, apiCfg, scheme.ID); ok && status != "" {
					unresolved = append(unresolved, scheme.ID+" ("+status+")")
				}
			}
		} else {
			skippedIDs = append(skippedIDs, scheme.ID)
		}
	}
	var callable, secured, resolverEvaluated int
	if apiCfg != nil {
		prof := profileForName(apiCfg, profileName)
		coverage := c.operationAuthCoverage(apiName, profileName, prof, d.operations)
		callable, secured, resolverEvaluated = coverage.Callable, coverage.Secured, coverage.ResolverEvaluated
	}
	fmt.Fprintf(c.Stdout, "\nAuth coverage for profile %q:\n", profileName)
	fmt.Fprintf(c.Stdout, "  configured: %s\n", formatIDList(configuredIDs))
	if len(unresolved) > 0 {
		fmt.Fprintf(c.Stdout, "  unresolved: %s\n", formatIDList(unresolved))
	}
	fmt.Fprintf(c.Stdout, "  skipped:    %s\n", formatIDList(skippedIDs))
	fmt.Fprintf(c.Stdout, "  callable:   %d/%d secured operations\n", callable, secured)
	if resolverEvaluated > 0 {
		fmt.Fprintf(c.Stdout, "  resolver:   %d secured operations evaluated at request time\n", resolverEvaluated)
	}
	fmt.Fprintln(c.Stdout)
}

func (c *CLI) authCoverageCredentialStatus(apiName, profileName string, apiCfg *config.APIConfig, credentialID string) (string, bool) {
	prof := profileForName(apiCfg, profileName)
	if prof == nil || prof.Credentials == nil {
		return "", false
	}
	credential := prof.Credentials[credentialID]
	if credential == nil || (credential.Auth == nil && credential.AuthRef == "") {
		return "", false
	}
	_, ready, err := c.credentialReadiness(apiName, profileName, credentialID, credential)
	if err != nil || !ready.Configured || ready.Usable || len(ready.Issues) == 0 {
		return "", false
	}
	return strings.Join(ready.Issues, "; "), true
}

func configuredCredentials(apiCfg *config.APIConfig, profileName string) map[string]bool {
	out := map[string]bool{}
	if apiCfg == nil || apiCfg.Profiles == nil || apiCfg.Profiles[profileName] == nil {
		return out
	}
	for id, credential := range apiCfg.Profiles[profileName].Credentials {
		if credential != nil && (credential.Auth != nil || credential.AuthRef != "") {
			out[id] = true
		}
	}
	return out
}

func formatIDList(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

func cloneAuthParams(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
