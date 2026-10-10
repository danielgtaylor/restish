package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/spec"
)

type authReadiness struct {
	Configured bool
	Usable     bool
	Issues     []string
}

func (r authReadiness) status(empty string) string {
	if !r.Configured {
		return empty
	}
	if len(r.Issues) == 0 {
		return "configured"
	}
	return "configured (" + strings.Join(r.Issues, "; ") + ")"
}

func (c *CLI) authConfigReadiness(ac *config.AuthConfig) authReadiness {
	if ac == nil {
		return authReadiness{}
	}
	var issues []string
	for _, value := range ac.Params {
		if !strings.HasPrefix(value, "env:") {
			continue
		}
		name := strings.TrimPrefix(value, "env:")
		if name == "" {
			issues = append(issues, "env missing: <empty>")
			continue
		}
		if _, ok := os.LookupEnv(name); !ok {
			issues = append(issues, "env missing: "+name)
		}
	}
	sort.Strings(issues)
	return authReadiness{Configured: true, Usable: len(issues) == 0, Issues: issues}
}

func (c *CLI) resolvedAuthReadiness(apiName, profileName string, resolved resolvedAuthConfig) authReadiness {
	readiness := c.authConfigReadiness(resolved.Config)
	if !readiness.Configured || !readiness.Usable || resolved.Config == nil {
		return readiness
	}
	if resolved.Config.Type == "oauth-authorization-code" &&
		!c.cachedOAuthAuthCodeUsable(resolved.Config.Type, resolved.CacheKey, apiName, profileName) {
		readiness.Usable = false
		readiness.Issues = append(readiness.Issues, "OAuth access token not cached")
	}
	return readiness
}

func (c *CLI) credentialReadiness(apiName, profileName, credentialID string, credential *config.CredentialConfig) (resolvedAuthConfig, authReadiness, error) {
	if credential == nil || (credential.Auth == nil && credential.AuthRef == "") {
		return resolvedAuthConfig{}, authReadiness{}, nil
	}
	resolved, err := c.resolveCredentialAuth(apiName, profileName, credentialID, credential)
	if err != nil {
		return resolvedAuthConfig{}, authReadiness{Configured: true, Issues: []string{err.Error()}}, err
	}
	return resolved, c.resolvedAuthReadiness(apiName, profileName, resolved), nil
}

func (c *CLI) profileAuthReadiness(apiName, profileName string, prof *config.ProfileConfig) (resolvedAuthConfig, authReadiness, error) {
	resolved, err := c.resolveProfileAuth(apiName, profileName, prof)
	if err != nil {
		return resolvedAuthConfig{}, authReadiness{Configured: true, Issues: []string{err.Error()}}, err
	}
	return resolved, c.resolvedAuthReadiness(apiName, profileName, resolved), nil
}

type operationAuthCoverage struct {
	Callable          int
	Secured           int
	SourceEvaluated   int
	ResolverEvaluated int
	ResolverByID      map[string]int
	FallbackByID      map[string]int
	FallbackLabels    map[string]string
}

type operationSecurityIssueKey struct {
	id   string
	kind string
	text string
}

func operationSecurityIssues(ops []spec.Operation) []string {
	return operationSecurityIssuesWithResolver(ops, false)
}

func operationSecurityIssuesWithResolver(ops []spec.Operation, resolverAvailable bool) []string {
	counts := map[operationSecurityIssueKey]int{}
	for _, op := range ops {
		seen := map[operationSecurityIssueKey]bool{}
		for _, alternative := range op.CredentialAlternatives {
			for _, requirement := range alternative {
				text, ok := operationSecurityIssueText(requirement)
				if !ok {
					continue
				}
				if resolverAvailable && !requirement.Undeclared && !authRequirementKindSupported(requirement.Kind) {
					continue
				}
				key := operationSecurityIssueKey{id: requirement.ID, kind: requirement.Kind, text: text}
				seen[key] = true
			}
		}
		for key := range seen {
			counts[key]++
		}
	}
	return formatOperationSecurityIssues(counts, "operation")
}

func operationSecurityIssuesFromAlternatives(alternatives []spec.CredentialAlternative) []string {
	counts := map[operationSecurityIssueKey]int{}
	seenByAlt := map[int]map[operationSecurityIssueKey]bool{}
	for altIndex, alternative := range alternatives {
		for _, requirement := range alternative {
			text, ok := operationSecurityIssueText(requirement)
			if !ok {
				continue
			}
			key := operationSecurityIssueKey{id: requirement.ID, kind: requirement.Kind, text: text}
			if seenByAlt[altIndex] == nil {
				seenByAlt[altIndex] = map[operationSecurityIssueKey]bool{}
			}
			if seenByAlt[altIndex][key] {
				continue
			}
			seenByAlt[altIndex][key] = true
			counts[key]++
		}
	}
	return formatOperationSecurityIssues(counts, "alternative")
}

func operationSecurityIssueText(requirement spec.CredentialRequirement) (string, bool) {
	switch {
	case requirement.Undeclared:
		return fmt.Sprintf("security scheme %q is referenced by operations but is not declared in components.securitySchemes", requirement.ID), true
	case !authRequirementKindSupported(requirement.Kind):
		return fmt.Sprintf("security scheme %q uses unsupported kind %q", requirement.ID, requirement.Kind), true
	default:
		return "", false
	}
}

func formatOperationSecurityIssues(counts map[operationSecurityIssueKey]int, unit string) []string {
	keys := make([]operationSecurityIssueKey, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].id != keys[j].id {
			return keys[i].id < keys[j].id
		}
		return keys[i].text < keys[j].text
	})
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		unitWord := unit + "s"
		if counts[key] == 1 {
			unitWord = unit
		}
		out = append(out, fmt.Sprintf("%s (%d %s); fix the OpenAPI document or use --rsh-auth %s with configured credentials if you know what to send", key.text, counts[key], unitWord, key.id))
	}
	return out
}

func (c *CLI) operationAuthCoverage(apiName, profileName string, prof *config.ProfileConfig, ops []spec.Operation) operationAuthCoverage {
	coverage := operationAuthCoverage{
		ResolverByID:   map[string]int{},
		FallbackByID:   map[string]int{},
		FallbackLabels: map[string]string{},
	}
	for _, op := range ops {
		if len(op.CredentialAlternatives) == 0 {
			continue
		}
		coverage.Secured++
		if op.OptionalAuth {
			coverage.Callable++
		}
		if c.operationHasUsableNamedAlternative(apiName, profileName, prof, op.CredentialAlternatives) {
			if !op.OptionalAuth {
				coverage.Callable++
			}
			continue
		}
		if c.operationHasDynamicDPoPAlternative(apiName, profileName, prof, op.CredentialAlternatives) {
			coverage.SourceEvaluated++
			continue
		}
		if c.authResolverAvailable(apiName) {
			coverage.ResolverEvaluated++
			for _, alternative := range op.CredentialAlternatives {
				for _, requirement := range alternative {
					coverage.ResolverByID[requirement.ID]++
				}
			}
		}
		if op.OptionalAuth {
			continue
		}
		policy := &operationAuthPolicy{OptionalAuth: op.OptionalAuth, CredentialAlternatives: op.CredentialAlternatives}
		resolved, ready, err := c.profileAuthReadiness(apiName, profileName, prof)
		if err != nil || !ready.Usable {
			continue
		}
		requirement, ok := profileAuthFallbackRequirement(policy, resolved.Config)
		if !ok {
			continue
		}
		coverage.Callable++
		coverage.FallbackByID[requirement.ID]++
		coverage.FallbackLabels[requirement.ID] = profileFallbackLabel(requirement, resolved.Config)
	}
	return coverage
}

func (c *CLI) operationHasDynamicDPoPAlternative(apiName, profileName string, prof *config.ProfileConfig, alternatives []spec.CredentialAlternative) bool {
	if prof == nil {
		return false
	}
	for _, alternative := range alternatives {
		if len(alternative) == 0 {
			continue
		}
		dynamic := true
		for _, requirement := range alternative {
			credential := prof.Credentials[requirement.ID]
			resolved, ready, err := c.credentialReadiness(apiName, profileName, requirement.ID, credential)
			if err != nil || !ready.Usable || resolved.Config == nil || resolved.Config.Type != "dpop" ||
				resolved.Config.Params["source"] == "" || resolved.Config.Params["reference"] == "" {
				dynamic = false
				break
			}
		}
		if dynamic {
			return true
		}
	}
	return false
}

func (c *CLI) authResolverAvailable(apiName string) bool {
	for _, resolver := range c.pluginsByHook["auth-resolver"] {
		if pluginAppliesToAPI(resolver, apiName) {
			return true
		}
	}
	return false
}

func (c *CLI) operationHasUsableNamedAlternative(apiName, profileName string, prof *config.ProfileConfig, alternatives []spec.CredentialAlternative) bool {
	if prof == nil {
		return false
	}
	for _, alternative := range alternatives {
		ok := true
		for _, requirement := range alternative {
			if requirement.Kind == "mtls" {
				if !operationMTLSSatisfiedByProfile(prof) {
					ok = false
					break
				}
				continue
			}
			credential := prof.Credentials[requirement.ID]
			resolved, ready, err := c.credentialReadiness(apiName, profileName, requirement.ID, credential)
			if err != nil || !ready.Usable || resolved.Config == nil {
				ok = false
				break
			}
			if resolved.Config.Type == "dpop" && resolved.Config.Params["source"] != "" && resolved.Config.Params["reference"] != "" {
				ok = false
				break
			}
			if err := credentialSatisfies(requirement, credential, resolved.Config); err != nil {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func profileFallbackLabel(requirement spec.CredentialRequirement, ac *config.AuthConfig) string {
	if profileFallbackObviouslyMatches(requirement, ac) {
		return "satisfied by profile auth fallback"
	}
	return "satisfied by profile auth fallback (unchecked auth kind)"
}

func profileFallbackObviouslyMatches(requirement spec.CredentialRequirement, ac *config.AuthConfig) bool {
	if ac == nil {
		return false
	}
	switch requirement.Kind {
	case "http-bearer":
		switch ac.Type {
		case "bearer", "oauth-client-credentials", "oauth-authorization-code", "oauth-device-code", "external-tool":
			return true
		}
	case "http-dpop":
		return ac.Type == "dpop"
	case "oauth2-dpop":
		return ac.Type == "dpop"
	case "http-basic":
		return ac.Type == "http-basic"
	case "api-key":
		return ac.Type == "api-key"
	case "oauth2":
		return strings.HasPrefix(ac.Type, "oauth-") || ac.Type == "dpop"
	}
	return false
}

func authReadinessIssues(values ...authReadiness) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, issue := range value.Issues {
			if issue == "" || seen[issue] {
				continue
			}
			seen[issue] = true
			out = append(out, issue)
		}
	}
	sort.Strings(out)
	return out
}

func formatAuthIssues(issues []string) string {
	if len(issues) == 0 {
		return ""
	}
	return fmt.Sprintf(" (%s)", strings.Join(issues, "; "))
}
