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
	Callable       int
	Secured        int
	FallbackByID   map[string]int
	FallbackLabels map[string]string
}

type operationSecurityIssueKey struct {
	id   string
	kind string
	text string
}

func operationSecurityIssues(ops []spec.Operation) []string {
	counts := map[operationSecurityIssueKey]int{}
	tagsByIssue := map[operationSecurityIssueKey]map[string]bool{}
	operationsByIssue := map[operationSecurityIssueKey]map[string]bool{}
	for _, op := range ops {
		seen := map[operationSecurityIssueKey]bool{}
		for _, alternative := range op.CredentialAlternatives {
			for _, requirement := range alternative {
				text, ok := operationSecurityIssueText(requirement)
				if !ok {
					continue
				}
				key := operationSecurityIssueKey{id: requirement.ID, kind: requirement.Kind, text: text}
				seen[key] = true
			}
		}
		for key := range seen {
			counts[key]++
			if operationsByIssue[key] == nil {
				operationsByIssue[key] = map[string]bool{}
			}
			operationsByIssue[key][operationSecurityIssueOperationLabel(op)] = true
			for _, tag := range op.Tags {
				if tag == "" {
					continue
				}
				if tagsByIssue[key] == nil {
					tagsByIssue[key] = map[string]bool{}
				}
				tagsByIssue[key][tag] = true
			}
		}
	}
	return formatOperationSecurityIssues(counts, "operation", tagsByIssue, operationsByIssue)
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
	return formatOperationSecurityIssues(counts, "alternative", nil, nil)
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

func formatOperationSecurityIssues(counts map[operationSecurityIssueKey]int, unit string, tagsByIssue, operationsByIssue map[operationSecurityIssueKey]map[string]bool) []string {
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
		tagNames := make([]string, 0, len(tagsByIssue[key]))
		for tag := range tagsByIssue[key] {
			tagNames = append(tagNames, tag)
		}
		sort.Strings(tagNames)
		operationNames := sortedStringSet(operationsByIssue[key])
		action := fmt.Sprintf("fix the OpenAPI document or use --rsh-auth %s with configured credentials if you know what to send", key.id)
		if len(tagNames) == 0 && len(operationNames) == 0 {
			out = append(out, fmt.Sprintf("%s (%d %s); %s", key.text, counts[key], unitWord, action))
			continue
		}
		var details []string
		if len(tagNames) > 0 {
			details = append(details, "  tags: "+strings.Join(tagNames, ", "))
		}
		if len(operationNames) > 0 {
			details = append(details, "  operations:")
			for _, operationName := range operationNames {
				details = append(details, "    - "+operationName)
			}
		}
		details = append(details, "  action: "+action)
		out = append(out, fmt.Sprintf("%s (%d %s)\n%s", key.text, counts[key], unitWord, strings.Join(details, "\n")))
	}
	return out
}

func operationSecurityIssueOperationLabel(op spec.Operation) string {
	label := strings.ToUpper(op.Method) + " " + op.Path
	if op.ID != "" {
		label += " (" + op.ID + ")"
	}
	return label
}

func sortedStringSet(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c *CLI) operationAuthCoverage(apiName, profileName string, prof *config.ProfileConfig, ops []spec.Operation) operationAuthCoverage {
	coverage := operationAuthCoverage{
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
			continue
		}
		if c.operationHasUsableNamedAlternative(apiName, profileName, prof, op.CredentialAlternatives) {
			coverage.Callable++
			continue
		}
		policy := &operationAuthPolicy{OptionalAuth: op.OptionalAuth, CredentialAlternatives: op.CredentialAlternatives}
		if !canUseProfileAuthFallback(policy) {
			continue
		}
		resolved, ready, err := c.profileAuthReadiness(apiName, profileName, prof)
		if err != nil || !ready.Usable {
			continue
		}
		requirement := op.CredentialAlternatives[0][0]
		coverage.Callable++
		coverage.FallbackByID[requirement.ID]++
		coverage.FallbackLabels[requirement.ID] = profileFallbackLabel(requirement, resolved.Config)
	}
	return coverage
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
	case "http-basic":
		return ac.Type == "http-basic"
	case "api-key":
		return ac.Type == "api-key"
	case "oauth2":
		return strings.HasPrefix(ac.Type, "oauth-")
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
