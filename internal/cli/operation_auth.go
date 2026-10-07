package cli

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/rest-sh/restish/v2/auth"
	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/request"
	"github.com/rest-sh/restish/v2/internal/spec"
)

type operationAuthPolicy struct {
	OptionalAuth           bool
	NoAuth                 bool
	CredentialAlternatives []spec.CredentialAlternative
	Override               string
	Transport              request.Options
}

type selectedOperationAuth struct {
	requirement spec.CredentialRequirement
	resolved    resolvedAuthConfig
	source      string
}

func (c *CLI) planOperationAuth(apiName, profileName string, prof *config.ProfileConfig, policy *operationAuthPolicy) ([]selectedOperationAuth, bool, error) {
	if policy != nil && strings.TrimSpace(policy.Override) != "" {
		return c.planOperationAuthOverride(apiName, profileName, prof, policy)
	}
	if policy == nil {
		return nil, false, nil
	}
	if len(policy.CredentialAlternatives) == 0 {
		if policy.OptionalAuth {
			return nil, true, nil
		}
		return nil, false, nil
	}
	securityIssueSuffix := operationSecurityIssueErrorSuffix(policy.CredentialAlternatives)
	var missing []string
	var needErrors []string
	for _, alternative := range policy.CredentialAlternatives {
		selected := make([]selectedOperationAuth, 0, len(alternative))
		alternativeMissing := false
		alternativeNeedErrors := false
		for _, requirement := range alternative {
			if requirement.Kind == "mtls" {
				if operationMTLSSatisfiedByRequestOptions(policy.Transport) {
					continue
				}
				alternativeNeedErrors = true
				needErrors = append(needErrors, operationMTLSMissingRequirementMessage(requirement))
				continue
			}
			if prof == nil {
				alternativeMissing = true
				missing = append(missing, requirement.ID)
				continue
			}
			credential := prof.Credentials[requirement.ID]
			if credential == nil {
				alternativeMissing = true
				missing = append(missing, requirement.ID)
				continue
			}
			resolved, err := c.resolveCredentialAuth(apiName, profileName, requirement.ID, credential)
			if err != nil {
				return nil, false, err
			}
			if resolved.Config == nil {
				alternativeMissing = true
				missing = append(missing, requirement.ID)
				continue
			}
			if err := credentialSatisfies(requirement, credential, resolved.Config); err != nil {
				alternativeNeedErrors = true
				needErrors = append(needErrors, err.Error())
				continue
			}
			if err := c.resolvedAuthParamsReady(resolved, apiName, profileName); err != nil {
				alternativeNeedErrors = true
				needErrors = append(needErrors, fmt.Sprintf("%s has unresolved auth params: %v", requirement.ID, err))
				continue
			}
			selected = append(selected, selectedOperationAuth{requirement: requirement, resolved: resolved, source: selectedAuthSourceCredential(resolved)})
		}
		if !alternativeMissing && !alternativeNeedErrors {
			if err := rejectConflictingSelectedAuth(selected); err != nil {
				return nil, false, err
			}
			return selected, true, nil
		}
	}

	if policy.OptionalAuth {
		return nil, true, nil
	}

	if prof != nil && canUseProfileAuthFallback(policy) {
		resolved, err := c.resolveProfileAuth(apiName, profileName, prof)
		if err != nil {
			return nil, false, err
		}
		if resolved.Config != nil {
			return []selectedOperationAuth{{requirement: policy.CredentialAlternatives[0][0], resolved: resolved, source: "profile auth fallback"}}, true, nil
		}
	}

	if prof == nil {
		var details []string
		if len(missing) > 0 {
			sort.Strings(missing)
			details = append(details, "missing credential bindings: "+strings.Join(uniqueStrings(missing), ", "))
		}
		if len(needErrors) > 0 {
			sort.Strings(needErrors)
			details = append(details, strings.Join(uniqueStrings(needErrors), "; "))
		}
		details = append(details, operationAuthSetupHint(apiName, profileName))
		return nil, false, fmt.Errorf("operation requires credentials for API %q but profile %q is not configured%s; %s", apiName, profileName, securityIssueSuffix, strings.Join(details, "; "))
	}
	if len(needErrors) > 0 {
		sort.Strings(needErrors)
		return nil, false, fmt.Errorf("profile %q of API %q has credential bindings that do not satisfy this operation: %s%s%s", profileName, apiName, strings.Join(uniqueStrings(needErrors), "; "), securityIssueSuffix, operationAuthConfiguredOverrideHint(prof, policy.CredentialAlternatives))
	}
	sort.Strings(missing)
	return nil, false, fmt.Errorf("profile %q of API %q is missing credential bindings for this operation: %s%s%s; %s", profileName, apiName, strings.Join(uniqueStrings(missing), ", "), securityIssueSuffix, operationAuthConfiguredOverrideHint(prof, policy.CredentialAlternatives), operationAuthSetupHint(apiName, profileName))
}

func (c *CLI) planOperationAuthOverride(apiName, profileName string, prof *config.ProfileConfig, policy *operationAuthPolicy) ([]selectedOperationAuth, bool, error) {
	override := strings.TrimSpace(policy.Override)
	if strings.EqualFold(override, "anonymous") {
		if policy.OptionalAuth {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf(`auth override "anonymous" is not valid for this operation`)
	}
	requested, err := parseAuthOverride(override)
	if err != nil {
		return nil, false, err
	}
	alternative, ok := matchingSecurityAlternative(policy.CredentialAlternatives, requested)
	if !ok {
		alternative = configuredAuthOverrideAlternative(requested)
		if len(alternative) == 0 {
			return nil, false, fmt.Errorf("auth override %q does not match this operation; valid values: %s", override, strings.Join(authOverrideCandidates(policy.OptionalAuth, policy.CredentialAlternatives), ", "))
		}
		if policy.NoAuth {
			c.warnf("auth override %q is being sent for an operation with security: []", override)
		} else {
			c.warnf("auth override %q is not listed in this operation's OpenAPI security requirements; using configured credential override", override)
		}
	}
	selected, missing, needErrors, err := c.selectOperationAlternative(apiName, profileName, prof, alternative, policy.Transport)
	if err != nil {
		return nil, false, err
	}
	if len(needErrors) > 0 {
		sort.Strings(needErrors)
		return nil, false, fmt.Errorf("auth override %q is not satisfied by profile %q of API %q: %s", override, profileName, apiName, strings.Join(uniqueStrings(needErrors), "; "))
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		if prof == nil {
			return nil, false, fmt.Errorf("auth override %q requires profile %q of API %q to configure credential bindings: %s; %s", override, profileName, apiName, strings.Join(uniqueStrings(missing), ", "), operationAuthSetupHint(apiName, profileName))
		}
		return nil, false, fmt.Errorf("auth override %q requires missing credential bindings in profile %q of API %q: %s; %s", override, profileName, apiName, strings.Join(uniqueStrings(missing), ", "), operationAuthSetupHint(apiName, profileName))
	}
	if err := rejectConflictingSelectedAuth(selected); err != nil {
		return nil, false, err
	}
	return selected, true, nil
}

func operationAuthSetupHint(apiName, profileName string) string {
	prefix := "restish"
	if profileName != "" && profileName != "default" {
		prefix += " --rsh-profile " + profileName
	}
	return fmt.Sprintf("run %q to inspect coverage or %q to configure a credential", prefix+" api auth inspect "+apiName, prefix+" api auth add "+apiName+" <credential-id>")
}

func operationSecurityIssueErrorSuffix(alternatives []spec.CredentialAlternative) string {
	issues := operationSecurityIssuesFromAlternatives(alternatives)
	if len(issues) == 0 {
		return ""
	}
	return "; OpenAPI security issue: " + strings.Join(issues, "; ")
}

func operationAuthConfiguredOverrideHint(prof *config.ProfileConfig, alternatives []spec.CredentialAlternative) string {
	if prof == nil || len(prof.Credentials) == 0 {
		return ""
	}
	declared := map[string]bool{}
	for _, alternative := range alternatives {
		for _, requirement := range alternative {
			declared[requirement.ID] = true
		}
	}
	var extra []string
	for id, credential := range prof.Credentials {
		if id == "" || declared[id] || credential == nil {
			continue
		}
		if credential.Auth == nil && credential.AuthRef == "" {
			continue
		}
		extra = append(extra, id)
	}
	sort.Strings(extra)
	switch len(extra) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("; configured credential %q is not declared for this operation; if the provider accepts it, retry with --rsh-auth %s", extra[0], extra[0])
	default:
		return fmt.Sprintf("; configured credentials are not declared for this operation: %s; if the provider accepts one, retry with --rsh-auth <credential-id>", strings.Join(extra, ", "))
	}
}

func (c *CLI) selectOperationAlternative(apiName, profileName string, prof *config.ProfileConfig, alternative spec.CredentialAlternative, transport request.Options) ([]selectedOperationAuth, []string, []string, error) {
	selected := make([]selectedOperationAuth, 0, len(alternative))
	var missing []string
	var needErrors []string
	for _, requirement := range alternative {
		if requirement.Kind == "mtls" {
			if !operationMTLSSatisfiedByRequestOptions(transport) {
				needErrors = append(needErrors, operationMTLSMissingRequirementMessage(requirement))
			}
			continue
		}
		if prof == nil {
			missing = append(missing, requirement.ID)
			continue
		}
		credential := prof.Credentials[requirement.ID]
		if credential == nil {
			missing = append(missing, requirement.ID)
			continue
		}
		resolved, err := c.resolveCredentialAuth(apiName, profileName, requirement.ID, credential)
		if err != nil {
			return nil, nil, nil, err
		}
		if resolved.Config == nil {
			missing = append(missing, requirement.ID)
			continue
		}
		if err := credentialSatisfies(requirement, credential, resolved.Config); err != nil {
			needErrors = append(needErrors, err.Error())
			continue
		}
		if err := c.resolvedAuthParamsReady(resolved, apiName, profileName); err != nil {
			needErrors = append(needErrors, fmt.Sprintf("%s has unresolved auth params: %v", requirement.ID, err))
			continue
		}
		selected = append(selected, selectedOperationAuth{requirement: requirement, resolved: resolved, source: selectedAuthSourceCredential(resolved)})
	}
	return selected, missing, needErrors, nil
}

func configuredAuthOverrideAlternative(requested map[string]bool) spec.CredentialAlternative {
	ids := make([]string, 0, len(requested))
	for id := range requested {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	alternative := make(spec.CredentialAlternative, 0, len(ids))
	for _, id := range ids {
		alternative = append(alternative, spec.CredentialRequirement{
			ID:     id,
			Kind:   "configured",
			Source: "override",
		})
	}
	return alternative
}

func selectedAuthSourceCredential(resolved resolvedAuthConfig) string {
	if resolved.Ref != "" {
		return "auth profile reference"
	}
	return "named credential"
}

func (c *CLI) resolveCredentialAuth(apiName, profileName, credentialID string, credential *config.CredentialConfig) (resolvedAuthConfig, error) {
	if credential == nil {
		return resolvedAuthConfig{}, nil
	}
	if credential.Auth != nil && credential.AuthRef != "" {
		return resolvedAuthConfig{}, fmt.Errorf("credential %q in profile %q of API %q has both auth and auth_ref", credentialID, profileName, apiName)
	}
	if credential.AuthRef == "" {
		cacheKey := c.apiCacheNamespace(apiName, profileName) + ":credential:" + credentialID
		if relativeKey := inlineAuthCacheKey(cacheKey, credential.Auth, c.authBaseURL(apiName, profileName)); relativeKey != "" {
			cacheKey = relativeKey
		}
		return resolvedAuthConfig{
			Config:   credential.Auth,
			CacheKey: cacheKey,
		}, nil
	}
	if c.cfg == nil || c.cfg.AuthProfiles == nil || c.cfg.AuthProfiles[credential.AuthRef] == nil {
		return resolvedAuthConfig{}, fmt.Errorf("credential %q in profile %q of API %q references unknown auth profile %q", credentialID, profileName, apiName, credential.AuthRef)
	}
	ac := c.cfg.AuthProfiles[credential.AuthRef]
	return resolvedAuthConfig{
		Config:   ac,
		Ref:      credential.AuthRef,
		CacheKey: sharedAuthCacheKey(credential.AuthRef, ac, c.authBaseURL(apiName, profileName)),
	}, nil
}

func (c *CLI) operationAuthCallbacks(apiName, profileName string, selected []selectedOperationAuth, opts authHandlerOptions) (authCallbacks, error) {
	if len(selected) == 0 {
		return authCallbacks{}, nil
	}
	steps := make([]operationAuthStep, 0, len(selected))
	for _, item := range selected {
		step, err := c.operationAuthStep(apiName, profileName, item, opts)
		if err != nil {
			return authCallbacks{}, err
		}
		steps = append(steps, step)
	}
	callbacks := authCallbacks{
		OnRequest: func(req *http.Request) error {
			for _, step := range steps {
				if err := c.applyOperationAuthStep(req, step, false); err != nil {
					return err
				}
			}
			if opts.NonInteractive {
				return nil
			}
			return c.runOperationAuthHookPlugins(req, steps)
		},
	}
	for _, step := range steps {
		if step.forceCapable {
			callbacks.OnUnauthorized = func(req *http.Request) error {
				for _, step := range steps {
					if err := c.applyOperationAuthStep(req, step, step.forceCapable); err != nil {
						return err
					}
				}
				if opts.NonInteractive {
					return nil
				}
				return c.runOperationAuthHookPlugins(req, steps)
			}
			break
		}
	}
	return callbacks, nil
}

func (c *CLI) runOperationAuthHookPlugins(req *http.Request, steps []operationAuthStep) error {
	if len(steps) == 0 {
		return nil
	}
	rawParams, secretKeys := operationAuthHookContext(steps)
	return c.runAuthHookPlugins(steps[0].apiName, steps[0].profileName, rawParams, secretKeys, req)
}

func operationAuthHookContext(steps []operationAuthStep) (map[string]string, map[string]bool) {
	if len(steps) != 1 {
		return nil, nil
	}
	return steps[0].rawParams, steps[0].secretKeys
}

type operationAuthStep struct {
	handler        auth.Handler
	rawParams      map[string]string
	cacheKey       string
	forceCapable   bool
	secretKeys     map[string]bool
	apiName        string
	profileName    string
	authType       string
	nonInteractive bool
}

func (c *CLI) operationAuthStep(apiName, profileName string, selected selectedOperationAuth, opts authHandlerOptions) (operationAuthStep, error) {
	handler, err := c.authHandlerFor(selected.resolved.Config, opts)
	if err != nil {
		return operationAuthStep{}, err
	}
	secretKeys := make(map[string]bool)
	for _, p := range handler.Parameters() {
		if p.Secret {
			secretKeys[p.Name] = true
		}
	}
	_, forceCapable := handler.(auth.ForceCapable)
	return operationAuthStep{
		handler:        handler,
		rawParams:      selected.resolved.Config.Params,
		cacheKey:       selected.resolved.CacheKey,
		forceCapable:   forceCapable,
		secretKeys:     secretKeys,
		apiName:        apiName,
		profileName:    profileName,
		authType:       selected.resolved.Config.Type,
		nonInteractive: opts.NonInteractive,
	}, nil
}

func (c *CLI) applyOperationAuthStep(req *http.Request, s operationAuthStep, force bool) error {
	if handled, err := c.applyNonInteractiveOAuth(req, s.authType, s.cacheKey, s.apiName, s.profileName, s.nonInteractive); handled {
		return err
	}
	if c.applyCachedOAuthClientCredentials(req, s.authType, s.cacheKey, s.apiName, s.profileName, force) {
		return nil
	}
	if err := rejectNonInteractiveCommandParams(s.rawParams, s.nonInteractive); err != nil {
		return err
	}
	params, err := c.buildAuthParams(s.rawParams)
	if err != nil {
		return err
	}
	if s.authType == "external-tool" {
		if err := c.ensureExternalToolApproved(req.Context(), s.apiName, s.profileName, params["commandline"]); err != nil {
			return err
		}
	}
	if err := c.ensureOAuthAuthorizationCodeReady(s.authType, s.cacheKey, s.apiName, s.profileName); err != nil {
		return err
	}
	preserveInsertedHeader := c.apiPreservesHeaderCase(s.apiName) && !authHeaderPresent(req.Header, s.authType, params)
	if err := s.handler.Authenticate(req.Context(), req, c.authContext(req.Context(), s.apiName, s.profileName, params, s.cacheKey, force, s.nonInteractive)); err != nil {
		return err
	}
	if preserveInsertedHeader {
		preserveAuthHeaderCase(req, s.authType, params)
	}
	markAuthCredentialTargets(req, s.authType, params)
	return nil
}

func credentialSatisfies(requirement spec.CredentialRequirement, credential *config.CredentialConfig, authCfg *config.AuthConfig) error {
	if len(requirement.Needs) == 0 {
		return nil
	}
	satisfies := credential.Satisfies
	if len(satisfies) == 0 && authCfg != nil && authCfg.Params != nil {
		satisfies = strings.Fields(authCfg.Params["scopes"])
	}
	have := make(map[string]bool, len(satisfies))
	for _, value := range satisfies {
		have[value] = true
	}
	var missing []string
	for _, need := range requirement.Needs {
		if !have[need] {
			missing = append(missing, need)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s missing required values %s", requirement.ID, strings.Join(missing, ", "))
	}
	return nil
}

func operationMTLSSatisfiedByRequestOptions(opts request.Options) bool {
	return (opts.ClientCertPath != "" && opts.ClientKeyPath != "") ||
		opts.TLSSignerName != "" ||
		opts.TLSSignerPath != ""
}

func operationMTLSSatisfiedByProfile(prof *config.ProfileConfig) bool {
	if prof == nil {
		return false
	}
	return (prof.ClientCertPath != "" && prof.ClientKeyPath != "") ||
		prof.TLSSigner != ""
}

func operationMTLSStatusFromProfile(prof *config.ProfileConfig) (string, bool) {
	if prof == nil {
		return "", false
	}
	if prof.TLSSigner != "" {
		return "configured via profile TLS signer", true
	}
	if prof.ClientCertPath != "" && prof.ClientKeyPath != "" {
		return "configured via profile client certificate", true
	}
	if prof.ClientCertPath != "" || prof.ClientKeyPath != "" {
		return "client certificate and key are both required", false
	}
	return "", false
}

func operationMTLSMissingRequirementMessage(requirement spec.CredentialRequirement) string {
	id := requirement.ID
	if id == "" {
		id = "mutualTLS"
	}
	return fmt.Sprintf("%s requires mutual TLS; supply --rsh-client-cert and --rsh-client-key, or configure profile client_cert/client_key or tls_signer", id)
}

func canUseProfileAuthFallback(policy *operationAuthPolicy) bool {
	return policy != nil &&
		len(policy.CredentialAlternatives) == 1 &&
		len(policy.CredentialAlternatives[0]) == 1 &&
		policy.CredentialAlternatives[0][0].Kind != "mtls"
}

func parseAuthOverride(value string) (map[string]bool, error) {
	parts := strings.Split(value, "+")
	out := make(map[string]bool, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "" {
			return nil, fmt.Errorf("invalid auth override %q", value)
		}
		if out[id] {
			return nil, fmt.Errorf("invalid auth override %q: duplicate credential %q", value, id)
		}
		out[id] = true
	}
	return out, nil
}

func matchingSecurityAlternative(alternatives []spec.CredentialAlternative, requested map[string]bool) (spec.CredentialAlternative, bool) {
	for _, alternative := range alternatives {
		if len(alternative) != len(requested) {
			continue
		}
		matches := true
		for _, requirement := range alternative {
			if !requested[requirement.ID] {
				matches = false
				break
			}
		}
		if matches {
			return alternative, true
		}
	}
	return nil, false
}

func authOverrideCandidates(optional bool, alternatives []spec.CredentialAlternative) []string {
	candidates := make([]string, 0, len(alternatives)+1)
	for _, alternative := range alternatives {
		var ids []string
		for _, requirement := range alternative {
			ids = append(ids, requirement.ID)
		}
		if len(ids) > 0 {
			candidates = append(candidates, strings.Join(ids, "+"))
		}
	}
	if optional {
		candidates = append(candidates, "anonymous")
	}
	return candidates
}

func rejectConflictingSelectedAuth(selected []selectedOperationAuth) error {
	seen := map[string]string{}
	for _, item := range selected {
		key := authMutationKey(item.resolved.Config)
		if key == "" {
			continue
		}
		if prev := seen[key]; prev != "" {
			return fmt.Errorf("selected credentials %q and %q both write %s", prev, item.requirement.ID, key)
		}
		seen[key] = item.requirement.ID
	}
	return nil
}

func authMutationKey(ac *config.AuthConfig) string {
	if ac == nil {
		return ""
	}
	switch ac.Type {
	case "api-key":
		location := strings.ToLower(ac.Params["in"])
		name := strings.ToLower(ac.Params["name"])
		if location == "" || name == "" {
			return ""
		}
		switch location {
		case "header", "query", "cookie":
			return location + ":" + name
		default:
			return ""
		}
	case "bearer", "http-basic", "oauth-client-credentials", "oauth-authorization-code", "oauth-device-code":
		return "header:authorization"
	case "external-tool":
		// External tools may return complete header/query mutations, so the
		// mutation target is not knowable from config alone.
		return ""
	default:
		return ""
	}
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
