package cli

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	urlpath "path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/shorthand/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/rest-sh/restish/v2/config"
	openapiparam "github.com/rest-sh/restish/v2/internal/openapi"
	"github.com/rest-sh/restish/v2/internal/spec"
)

const (
	generatedOperationAnnotation              = "restish.generated.operation"
	generatedOperationRequiredTypesAnnotation = "restish.generated.requiredTypes"
	generatedAPIHelpShortAnnotation           = "restish.generated.api.helpShort"
	generatedAPIHelpFullAnnotation            = "restish.generated.api.helpFull"
	generatedNegativeNumberArgPrefix          = "__restish_negative_number_arg__"

	generatedAPIHelpDescriptionMaxLines = 12
	generatedAPIHelpDescriptionMaxRunes = 1200
)

// buildAPICommand constructs a Cobra command group for a registered API and
// populates it with one subcommand per OpenAPI operation found in s.
// Returns nil when the spec cannot be built into a v3 model.
func (c *CLI) buildAPICommand(apiName string, apiCfg *config.APIConfig, s *spec.APISpec) *cobra.Command {
	operationBase := effectiveOperationBase(apiCfg, "default")
	set, err := s.OperationSet(spec.OperationOptions{
		BaseURL:         effectiveProfileBaseURL(apiCfg, "default"),
		OperationBase:   operationBase,
		ServerVariables: effectiveServerVariables(apiCfg, "default"),
	})
	return c.buildAPICommandFromOperationResult(apiName, apiCfg, set, operationBase, err)
}

func (c *CLI) buildAPICommandFromOperationResult(apiName string, apiCfg *config.APIConfig, set spec.OperationSet, operationBase string, err error) *cobra.Command {
	if err != nil {
		source := apiCfg.SpecURL
		if source == "" && len(apiCfg.SpecFiles) > 0 {
			source = apiCfg.SpecFiles[0]
		}
		if source == "" {
			source = apiCfg.BaseURL
		}
		c.warnf("skipping generated commands for API %q from %s: %v", apiName, source, err)
		return nil
	}
	return c.buildAPICommandFromOperationSet(apiName, apiCfg, set, operationBase)
}

func (c *CLI) buildAPICommandFromOperationSet(apiName string, apiCfg *config.APIConfig, set spec.OperationSet, operationBase string) *cobra.Command {
	ops := set.Operations
	// ops == nil means no V3 model or no paths section — nothing to generate.
	if ops == nil {
		return nil
	}
	for _, warning := range set.Warnings {
		c.generatedWarnf("API %q: %s", apiName, warning)
	}
	operationsByID := map[string]spec.Operation{}
	for _, op := range ops {
		if op.ID != "" {
			operationsByID[op.ID] = op
		}
	}

	long := strings.TrimSpace(set.Info.Description)
	if long == "" {
		long = strings.TrimSpace(set.Info.Summary)
	}
	isRootAPI := c.isPromotedAPI(apiName)
	long, fullLong := generatedAPIHelpDescription(apiName, long)
	if generatedAPIHasAuth(ops) && !isRootAPI {
		if long != "" {
			long += "\n\n"
		}
		long += fmt.Sprintf("Auth: run %q for credential coverage. Use --rsh-auth on generated operations when you need an explicit credential override.", c.commandNameOrDefault()+" api auth inspect "+apiName)
		if fullLong != "" {
			fullLong += "\n\n" + fmt.Sprintf("Auth: run %q for credential coverage. Use --rsh-auth on generated operations when you need an explicit credential override.", c.commandNameOrDefault()+" api auth inspect "+apiName)
		}
	}

	apiCmd := &cobra.Command{
		Use:     apiName,
		Short:   fmt.Sprintf("Commands generated from the %s API spec", apiName),
		Long:    long,
		GroupID: rootGroupAPI,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommandError(cmd, args[0], "run "+strconvQuote(cmd.CommandPath()+" --help")+" to see generated operations")
			}
			return cmd.Help()
		},
	}
	if fullLong != "" {
		apiCmd.Annotations = map[string]string{
			generatedAPIHelpShortAnnotation: long,
			generatedAPIHelpFullAnnotation:  fullLong,
		}
	}

	tagsSeen := map[string]bool{}
	commandNames := map[string]bool{}
	tagCommands := map[string]*cobra.Command{}
	tagCommandOrder := []string{}
	tagCommandNameByTag := map[string]string{}
	commandNamesByTag := map[string]map[string]bool{}
	useTagLayout := apiCfg.CommandLayout == "tags"
	rootExamples := make([]string, 0, 3)

	for _, op := range ops {
		tagCommandName := ""
		examplePrefix := apiName
		if isRootAPI {
			examplePrefix = ""
		}
		if useTagLayout && len(op.Tags) > 0 {
			tagCommandName = generatedTagCommandName(op.Tags[0], tagCommandNameByTag, tagCommands)
			if isRootAPI {
				examplePrefix = tagCommandName
			} else {
				examplePrefix = apiName + " " + tagCommandName
			}
		}
		cmd, err := c.buildOperationCommandWithProviders(apiName, examplePrefix, op, operationBase, operationsByID)
		if err != nil {
			c.generatedWarnf("skipping %s %s for API %q: %v", op.Method, op.Path, apiName, err)
			continue
		}
		if cmd == nil {
			continue
		}
		collisionScope := commandNames
		if useTagLayout && tagCommandName != "" {
			if tagCommands[tagCommandName] == nil {
				tagCommands[tagCommandName] = newGeneratedTagCommand(tagCommandName, op.Tags[0])
				tagCommandOrder = append(tagCommandOrder, tagCommandName)
				commandNamesByTag[tagCommandName] = map[string]bool{}
			}
			collisionScope = commandNamesByTag[tagCommandName]
		}
		if collisionScope[cmd.Name()] {
			originalName := cmd.Name()
			disambiguated := originalName + "-" + strings.ToLower(op.Method)
			if collisionScope[disambiguated] {
				suffix := 2
				for collisionScope[fmt.Sprintf("%s-%d", disambiguated, suffix)] {
					suffix++
				}
				disambiguated = fmt.Sprintf("%s-%d", disambiguated, suffix)
			}
			c.generatedWarnf("command name collision for API %q: %q; using %q for %s %s", apiName, originalName, disambiguated, op.Method, op.Path)
			cmd.Use = strings.Replace(cmd.Use, originalName, disambiguated, 1)
			cmd.Aliases = append(cmd.Aliases, originalName)
		}
		collisionScope[cmd.Name()] = true
		cmd.Aliases = c.filterGeneratedAliases(apiName, cmd, collisionScope)
		if useTagLayout && tagCommandName != "" {
			tagCommands[tagCommandName].AddCommand(cmd)
			if !cmd.Hidden && len(rootExamples) < 3 {
				rootExamples = append(rootExamples, generatedOperationExampleLine(c.commandNameOrDefault(), examplePrefix, cmd.Use, op.Help.Examples))
			}
			continue
		}
		commandNames[cmd.Name()] = true
		if !useTagLayout && len(op.Tags) > 0 {
			tag := op.Tags[0]
			if !tagsSeen[tag] {
				tagsSeen[tag] = true
				apiCmd.AddGroup(&cobra.Group{ID: tag, Title: tag})
			}
			cmd.GroupID = tag
		}
		apiCmd.AddCommand(cmd)
		if !cmd.Hidden && len(rootExamples) < 3 {
			rootExamples = append(rootExamples, generatedOperationExampleLine(c.commandNameOrDefault(), examplePrefix, cmd.Use, op.Help.Examples))
		}
	}
	if useTagLayout {
		for _, name := range tagCommandOrder {
			apiCmd.AddCommand(tagCommands[name])
		}
	}
	apiCmd.Example = strings.Join(rootExamples, "\n")

	return apiCmd
}

func generatedAPIHasAuth(ops []spec.Operation) bool {
	for _, op := range ops {
		if len(op.CredentialAlternatives) > 0 {
			return true
		}
	}
	return false
}

func generatedAPIHelpDescription(apiName, description string) (string, string) {
	description = strings.TrimSpace(description)
	if description == "" {
		return "", ""
	}
	short, truncated := truncateGeneratedAPIHelpDescription(description)
	if !truncated {
		return description, ""
	}
	short = strings.TrimSpace(short)
	if short != "" && !strings.HasSuffix(short, "...") {
		short += "\n..."
	}
	note := fmt.Sprintf("Description truncated; run \"restish %s --help-all\" to show the full API description.", apiName)
	if short == "" {
		return note, description
	}
	return short + "\n\n" + note, description
}

func truncateGeneratedAPIHelpDescription(description string) (string, bool) {
	lines := strings.Split(description, "\n")
	runeCount := utf8.RuneCountInString(description)
	if len(lines) <= generatedAPIHelpDescriptionMaxLines && runeCount <= generatedAPIHelpDescriptionMaxRunes {
		return description, false
	}

	var b strings.Builder
	remaining := generatedAPIHelpDescriptionMaxRunes
	for i, line := range lines {
		if i >= generatedAPIHelpDescriptionMaxLines || remaining <= 0 {
			break
		}
		if i > 0 {
			b.WriteByte('\n')
			remaining--
			if remaining <= 0 {
				break
			}
		}
		lineRunes := []rune(line)
		if len(lineRunes) > remaining {
			b.WriteString(string(lineRunes[:remaining]))
			break
		}
		b.WriteString(line)
		remaining -= len(lineRunes)
	}
	return strings.TrimSpace(b.String()), true
}

func (c *CLI) filterGeneratedAliases(apiName string, cmd *cobra.Command, collisionScope map[string]bool) []string {
	if len(cmd.Aliases) == 0 {
		return nil
	}
	aliases := make([]string, 0, len(cmd.Aliases))
	for _, alias := range cmd.Aliases {
		if alias == "" {
			continue
		}
		if collisionScope[alias] {
			c.generatedWarnf("command alias collision for API %q: dropping alias %q on %q", apiName, alias, cmd.Name())
			continue
		}
		collisionScope[alias] = true
		aliases = append(aliases, alias)
	}
	return aliases
}

func (c *CLI) generatedWarnf(format string, args ...any) {
	if c.quietGeneratedWarnings {
		return
	}
	c.warnf(format, args...)
}

func generatedTagCommandName(tag string, namesByTag map[string]string, existing map[string]*cobra.Command) string {
	if name := namesByTag[tag]; name != "" {
		return name
	}
	base := toKebabCase(tag)
	if base == "" {
		base = "operations"
	}
	name := base
	for suffix := 2; existing[name] != nil; suffix++ {
		name = fmt.Sprintf("%s-%d", base, suffix)
	}
	namesByTag[tag] = name
	return name
}

func newGeneratedTagCommand(name, tag string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   name,
		Short: fmt.Sprintf("Operations tagged %s", tag),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommandError(cmd, args[0], "run "+strconvQuote(cmd.CommandPath()+" --help")+" to see tagged operations")
			}
			return cmd.Help()
		},
	}
	return cmd
}

// paramInfo holds the information we need about a single parameter.
type paramInfo struct {
	name             string // original API parameter name
	flagName         string // kebab-case flag name
	in               string // "path", "query", "header", "cookie"
	required         bool
	hidden           bool
	desc             string
	schema           string
	typ              string
	itemType         string
	defaultValue     string
	defaultValues    []string
	hasDefault       bool
	style            string
	explode          *bool
	allowReserved    bool
	contentMediaType string
	enum             []string // allowed values from OpenAPI schema enum, if present
	completion       *spec.ParamCompletion
	objectProperties []spec.ParamObjectProperty
	parent           *paramInfo
	objectKey        string
}

// buildOperationCommand creates a Cobra command for one OpenAPI operation.
// Returns nil when the operation is excluded via x-cli-ignore.
// operationBase, when non-empty, is removed from fallback command names derived
// from paths. It does not affect explicit operationId or x-cli-name values.
func (c *CLI) buildOperationCommand(apiName, examplePrefix string, op spec.Operation, operationBase string) (*cobra.Command, error) {
	return c.buildOperationCommandWithProviders(apiName, examplePrefix, op, operationBase, nil)
}

func (c *CLI) buildOperationCommandWithProviders(apiName, examplePrefix string, op spec.Operation, operationBase string, operationsByID map[string]spec.Operation) (*cobra.Command, error) {
	cmdName := operationCommandName(op, operationBase)

	// Build param lists, honouring per-parameter x-cli-name / x-cli-description.
	pathParamOrder := extractPathParamNames(op.Path)
	seenPathParams := map[string]bool{}
	for _, name := range pathParamOrder {
		if seenPathParams[name] {
			opID := op.ID
			if opID == "" {
				opID = op.Method + " " + op.Path
			}
			return nil, fmt.Errorf("operation %q repeats path parameter %q", opID, name)
		}
		seenPathParams[name] = true
	}

	allParams := make(map[string]*paramInfo)
	for _, p := range op.Parameters {
		if p.XCLI.Ignore {
			continue
		}
		flagName := toKebabCase(p.Name)
		if p.XCLI.Name != "" {
			flagName = p.XCLI.Name
		}
		desc := p.Desc
		if p.XCLI.Description != "" {
			desc = p.XCLI.Description
		}
		if strings.TrimSpace(desc) == "" && p.In == "path" {
			desc = "path parameter"
		}
		desc = appendGeneratedParamSupportNote(desc, p)
		allParams[paramKey(p.In, p.Name)] = &paramInfo{
			name:             p.Name,
			flagName:         flagName,
			in:               p.In,
			required:         p.Required,
			hidden:           p.XCLI.Hidden,
			desc:             desc,
			schema:           p.Schema,
			typ:              p.Type,
			itemType:         p.ItemType,
			defaultValue:     p.Default,
			defaultValues:    append([]string(nil), p.DefaultValues...),
			hasDefault:       p.HasDefault,
			style:            p.Style,
			explode:          p.Explode,
			allowReserved:    p.AllowReserved,
			contentMediaType: p.ContentMediaType,
			enum:             p.Enum,
			completion:       p.XCLI.Completion,
			objectProperties: p.ObjectProperties,
		}
	}

	// Required positional args are path params in path-template order, followed
	// by required non-path params in spec order. Optional params become flags.
	var required []*paramInfo
	for _, name := range pathParamOrder {
		pi, ok := allParams[paramKey("path", name)]
		if !ok {
			opID := op.ID
			if opID == "" {
				opID = op.Method + " " + op.Path
			}
			return nil, fmt.Errorf("operation %q references missing path parameter %q", opID, name)
		}
		required = append(required, pi)
	}
	var optional []*paramInfo
	for _, p := range op.Parameters {
		if p.In == "path" {
			continue
		}
		pi := allParams[paramKey(p.In, p.Name)]
		if pi == nil {
			continue
		}
		if generatedParamSatisfiedByAPIKeySecurity(pi, op.CredentialAlternatives) {
			continue
		}
		if p.Required {
			required = append(required, pi)
			continue
		}
		optional = append(optional, pi)
	}
	optional = expandGeneratedObjectChildParams(optional)
	for _, warning := range disambiguateGeneratedFlagNames(optional) {
		c.generatedWarnf("operation %q parameter flag collision: %s", operationDisplayID(op), warning)
	}
	if err := validateGeneratedFlagNames(op, optional); err != nil {
		return nil, err
	}

	// Build Use string.
	use := cmdName
	for _, p := range required {
		use += " <" + p.flagName + ">"
	}
	if op.HasBody {
		if op.BodyRequired {
			use += " <body...>"
		} else {
			use += " [body...]"
		}
	}

	// Short and long descriptions, with x-cli-description override.
	short := op.Summary
	if op.XCLI.Description != "" {
		short = op.XCLI.Description
	}
	if short == "" {
		short = fmt.Sprintf("%s %s", op.Method, op.Path)
	}

	long := op.Description
	if op.XCLI.Description != "" {
		long = op.XCLI.Description
	}
	if len(required) > 0 {
		var argDocs strings.Builder
		if long != "" {
			argDocs.WriteString("\n\n")
		}
		argDocs.WriteString("Arguments:\n")
		for _, p := range required {
			argDocs.WriteString(fmt.Sprintf("  %-20s %s\n", p.flagName, p.desc))
		}
		long += argDocs.String()
	}
	long = appendGeneratedOperationHelp(long, required, optional, op.Help)

	cmd := &cobra.Command{
		Use:        use,
		Short:      short,
		Long:       long,
		Example:    generatedOperationExamples(c.commandName, examplePrefix, use, op.Help.Examples),
		Aliases:    op.XCLI.Aliases,
		Hidden:     op.XCLI.Hidden,
		Deprecated: deprecatedNotice(op.Deprecated),
		RunE: func(cmd *cobra.Command, args []string) error {
			args = restoreGeneratedNegativeNumberArgs(args)
			if helpAll, _ := cmd.Flags().GetBool("help-all"); helpAll {
				return showGeneratedOperationHelpAll(cmd)
			}
			if generateBody, _ := cmd.Flags().GetBool("rsh-generate-body"); generateBody {
				gf := globalFlagsFromContext(requestContext(cmd))
				return c.printGeneratedBodyExample(op.Help, gf.ContentType)
			}
			acceptOverride := c.generatedOperationAcceptHeader(op.ResponseMediaTypes, op.ResponseMediaType)
			rawBinaryBody := op.Help.Request != nil && op.Help.Request.RawBinary
			return c.runGeneratedOp(cmd, apiName, op.Path, op.OperationServer, op.Method, op.RequestMediaType, acceptOverride, op.RequestMultipartContentTypes, op.Help, op.BodyRequired, rawBinaryBody, op.NoAuth, op.OptionalAuth, op.CredentialAlternatives, required, optional, args)
		},
	}
	if candidates := authOverrideCandidates(op.OptionalAuth, op.CredentialAlternatives); len(candidates) > 0 {
		cmd.Annotations = map[string]string{
			securityCompletionAnnotation: strings.Join(candidates, "\n"),
		}
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[generatedOperationAnnotation] = "true"
	if len(required) > 0 {
		types := make([]string, 0, len(required))
		for _, p := range required {
			types = append(types, p.typ)
		}
		cmd.Annotations[generatedOperationRequiredTypesAnnotation] = strings.Join(types, "\n")
	}
	cmd.Flags().Bool("help-all", false, "Show all inherited Restish flags in help")
	cmd.SetUsageTemplate(generatedOperationUsageTemplate)
	if !op.HasBody {
		cmd.Args = generatedOperationArgs(required, false)
	} else {
		cmd.Args = generatedOperationArgs(required, true)
		cmd.Flags().Bool("rsh-generate-body", false, "Print an example request body and exit")
		cmd.Flags().Bool("rsh-validate", false, "Validate the JSON request body against the OpenAPI schema before sending")
	}

	for _, p := range optional {
		desc := generatedParamDescription(p)
		switch p.typ {
		case "boolean":
			cmd.Flags().Bool(p.flagName, false, desc)
		case "integer":
			cmd.Flags().Int(p.flagName, 0, desc)
		case "number":
			cmd.Flags().Float64(p.flagName, 0, desc)
		case "array":
			cmd.Flags().StringArray(p.flagName, nil, desc)
		default:
			cmd.Flags().String(p.flagName, "", desc)
		}
		if p.hidden {
			_ = cmd.Flags().MarkHidden(p.flagName)
		}
		if len(p.enum) > 0 {
			vals := p.enum
			_ = cmd.RegisterFlagCompletionFunc(p.flagName, func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
				return vals, cobra.ShellCompDirectiveNoFileComp
			})
		} else if p.completion != nil {
			param := p
			_ = cmd.RegisterFlagCompletionFunc(p.flagName, func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				return c.completeGeneratedParam(cmd, args, toComplete, apiName, param, required, optional, operationsByID)
			})
		}
	}

	// Register valid args for required positional params that have enum values.
	// Cobra uses ValidArgsFunction for the first positional arg completion.
	if len(required) > 0 {
		reqWithEnum := required // capture
		cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			idx := len(args)
			if idx < len(reqWithEnum) && len(reqWithEnum[idx].enum) > 0 {
				return reqWithEnum[idx].enum, cobra.ShellCompDirectiveNoFileComp
			}
			if idx < len(reqWithEnum) && reqWithEnum[idx].completion != nil {
				return c.completeGeneratedParam(cmd, args, toComplete, apiName, reqWithEnum[idx], required, optional, operationsByID)
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
	return cmd, nil
}

func generatedParamSatisfiedByAPIKeySecurity(p *paramInfo, alternatives []spec.CredentialAlternative) bool {
	if p == nil || p.in != "query" || len(alternatives) == 0 {
		return false
	}
	for _, alternative := range alternatives {
		if !credentialAlternativeHasAPIKeyParam(alternative, p.in, p.name) {
			return false
		}
	}
	return true
}

func credentialAlternativeHasAPIKeyParam(alternative spec.CredentialAlternative, in, name string) bool {
	for _, requirement := range alternative {
		if requirement.Kind == "api-key" &&
			strings.EqualFold(requirement.In, in) &&
			requirement.Name == name {
			return true
		}
	}
	return false
}

func generatedParamDescription(p *paramInfo) string {
	if p == nil || !p.hasDefault {
		if p == nil {
			return ""
		}
		return p.desc
	}
	defaultValue := p.defaultValue
	if p.typ == "array" && len(p.defaultValues) > 0 {
		defaultValue = strings.Join(p.defaultValues, ", ")
	}
	if defaultValue == "" {
		defaultValue = `""`
	}
	if p.desc == "" {
		return "Default: " + defaultValue
	}
	return p.desc + " (default: " + defaultValue + ")"
}

func validateGeneratedFlagNames(op spec.Operation, optional []*paramInfo) error {
	seen := map[string]*paramInfo{}
	for _, p := range optional {
		if p.flagName == "" {
			continue
		}
		if prev := seen[p.flagName]; prev != nil {
			opID := op.ID
			if opID == "" {
				opID = op.Method + " " + op.Path
			}
			return fmt.Errorf("operation %q has duplicate generated flag --%s for %s parameter %q and %s parameter %q", opID, p.flagName, prev.in, prev.name, p.in, p.name)
		}
		seen[p.flagName] = p
	}
	return nil
}

func expandGeneratedObjectChildParams(optional []*paramInfo) []*paramInfo {
	var expanded []*paramInfo
	for _, p := range optional {
		expanded = append(expanded, p)
		if p.in != "query" || len(p.objectProperties) == 0 {
			continue
		}
		for _, prop := range p.objectProperties {
			flagName := p.flagName + "-" + toKebabCase(prop.Name)
			if flagName == p.flagName+"-" {
				continue
			}
			expanded = append(expanded, &paramInfo{
				name:          p.name,
				flagName:      flagName,
				in:            p.in,
				hidden:        p.hidden,
				desc:          prop.Desc,
				typ:           prop.Type,
				style:         p.style,
				explode:       p.explode,
				allowReserved: p.allowReserved,
				enum:          prop.Enum,
				parent:        p,
				objectKey:     prop.Name,
			})
		}
	}
	return expanded
}

func validateGeneratedObjectChildFlagConflicts(cmd *cobra.Command, optional []*paramInfo) error {
	childrenChanged := map[*paramInfo][]string{}
	for _, p := range optional {
		if p.parent == nil || !cmd.Flags().Changed(p.flagName) {
			continue
		}
		childrenChanged[p.parent] = append(childrenChanged[p.parent], "--"+p.flagName)
	}
	for parent, childFlags := range childrenChanged {
		if cmd.Flags().Changed(parent.flagName) {
			sort.Strings(childFlags)
			return fmt.Errorf("--%s cannot be combined with generated child flag(s) %s", parent.flagName, strings.Join(childFlags, ", "))
		}
	}
	return nil
}

func disambiguateGeneratedFlagNames(optional []*paramInfo) []string {
	groups := map[string][]*paramInfo{}
	order := []string{}
	for _, p := range optional {
		if p.flagName == "" {
			continue
		}
		if _, ok := groups[p.flagName]; !ok {
			order = append(order, p.flagName)
		}
		groups[p.flagName] = append(groups[p.flagName], p)
	}

	used := map[string]int{}
	for _, p := range optional {
		if p.flagName != "" {
			used[p.flagName]++
		}
	}

	var warnings []string
	for _, originalFlag := range order {
		group := groups[originalFlag]
		if len(group) < 2 && !isReservedGeneratedFlagName(originalFlag) {
			continue
		}
		for _, p := range group {
			used[p.flagName]--
		}

		assigned := map[string]bool{}
		for i, p := range group {
			candidate := ""
			if isReservedGeneratedFlagName(originalFlag) {
				candidate = locationPrefixedGeneratedFlagName(p)
			} else if base, op, ok := comparisonOperatorSuffix(p.name); ok {
				candidate = toKebabCase(base) + "-" + op
			}
			if candidate == "" && !assigned[originalFlag] && used[originalFlag] == 0 {
				candidate = originalFlag
			}
			if candidate == "" || assigned[candidate] || used[candidate] > 0 || isReservedGeneratedFlagName(candidate) {
				candidate = fallbackDisambiguatedFlagName(originalFlag, p.name, i+1, assigned, used)
				warnings = append(warnings, fmt.Sprintf("using --%s for parameter %q", candidate, p.name))
			} else if isReservedGeneratedFlagName(originalFlag) {
				warnings = append(warnings, fmt.Sprintf("using --%s for parameter %q because --%s is reserved", candidate, p.name, originalFlag))
			}
			p.flagName = candidate
			assigned[candidate] = true
			used[candidate]++
		}
	}
	return warnings
}

func comparisonOperatorSuffix(name string) (base, op string, ok bool) {
	symbols := []struct {
		suffix string
		op     string
	}{
		{"<=", "lte"},
		{">=", "gte"},
		{"==", "eq"},
		{"!=", "ne"},
		{"<>", "ne"},
		{"<", "lt"},
		{">", "gt"},
		{"=", "eq"},
	}
	for _, symbol := range symbols {
		if strings.HasSuffix(name, symbol.suffix) {
			base := strings.TrimSpace(strings.TrimSuffix(name, symbol.suffix))
			if base != "" {
				return base, symbol.op, true
			}
		}
	}

	lower := strings.ToLower(name)
	words := []struct {
		suffix string
		op     string
	}{
		{"_lte", "lte"},
		{"-lte", "lte"},
		{"_gte", "gte"},
		{"-gte", "gte"},
		{"_lt", "lt"},
		{"-lt", "lt"},
		{"_gt", "gt"},
		{"-gt", "gt"},
		{"_eq", "eq"},
		{"-eq", "eq"},
		{"_ne", "ne"},
		{"-ne", "ne"},
	}
	for _, word := range words {
		if strings.HasSuffix(lower, word.suffix) {
			base := strings.TrimSpace(name[:len(name)-len(word.suffix)])
			if base != "" {
				return base, word.op, true
			}
		}
	}
	return "", "", false
}

func locationPrefixedGeneratedFlagName(p *paramInfo) string {
	prefix := toKebabCase(p.in)
	if prefix == "" {
		prefix = "param"
	}
	return prefix + "-" + p.flagName
}

func fallbackDisambiguatedFlagName(originalFlag, wireName string, ordinal int, assigned map[string]bool, used map[string]int) string {
	suffix := escapedFlagSuffix(wireName)
	candidates := make([]string, 0, 2)
	if suffix != "" && suffix != originalFlag {
		candidates = append(candidates, suffix)
	}
	if suffix == "" {
		suffix = fmt.Sprintf("param-%d", ordinal)
	}
	candidates = append(candidates, originalFlag+"-"+suffix)
	for _, candidate := range candidates {
		if !assigned[candidate] && used[candidate] == 0 && !isReservedGeneratedFlagName(candidate) {
			return candidate
		}
	}
	base := candidates[len(candidates)-1]
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !assigned[candidate] && used[candidate] == 0 && !isReservedGeneratedFlagName(candidate) {
			return candidate
		}
	}
}

func escapedFlagSuffix(name string) string {
	parts := make([]string, 0)
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		parts = append(parts, b.String())
		b.Reset()
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
		switch r {
		case '_':
			parts = append(parts, "underscore")
		case '-':
			parts = append(parts, "dash")
		case '<':
			parts = append(parts, "lt")
		case '>':
			parts = append(parts, "gt")
		case '=':
			parts = append(parts, "eq")
		case '!':
			parts = append(parts, "bang")
		default:
			parts = append(parts, fmt.Sprintf("u%x", r))
		}
	}
	flush()
	return strings.Join(parts, "-")
}

var reservedGeneratedFlagNames = map[string]bool{
	"help":              true,
	"help-all":          true,
	"rsh-generate-body": true,
}

func isReservedGeneratedFlagName(name string) bool {
	return reservedGeneratedFlagNames[name] || strings.HasPrefix(name, "rsh-")
}

func operationDisplayID(op spec.Operation) string {
	if op.ID != "" {
		return op.ID
	}
	return op.Method + " " + op.Path
}

func paramKey(in, name string) string {
	return in + "\x00" + name
}

func appendGeneratedParamSupportNote(desc string, p spec.Param) string {
	var note string
	if p.ContentMediaType != "" && !isJSONMediaType(p.ContentMediaType) {
		note = fmt.Sprintf("parameter content type %s is sent as raw text", p.ContentMediaType)
	} else if p.Style != "" && !supportedGeneratedParamStyle(p.In, p.Style) {
		note = fmt.Sprintf("OpenAPI style %q is not fully supported; using default serialization", p.Style)
	}
	if note == "" {
		return desc
	}
	if desc == "" {
		return note
	}
	return desc + " (" + note + ")"
}

func supportedGeneratedParamStyle(in, style string) bool {
	switch in {
	case "path":
		return style == "simple" || style == "label" || style == "matrix"
	case "query":
		return style == "form" || style == "spaceDelimited" || style == "pipeDelimited" || style == "deepObject"
	case "header":
		return style == "simple"
	case "cookie":
		return style == "form"
	default:
		return true
	}
}

func generatedOperationArgs(required []*paramInfo, hasBody bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if helpAll, _ := cmd.Flags().GetBool("help-all"); helpAll {
			return nil
		}
		if generateBody, _ := cmd.Flags().GetBool("rsh-generate-body"); generateBody {
			return nil
		}
		requiredCount := len(required)
		if len(args) < requiredCount {
			missing := make([]string, 0, requiredCount-len(args))
			for _, p := range required[len(args):] {
				missing = append(missing, p.flagName)
			}
			return fmt.Errorf("missing required argument(s): %s; run %q for usage", strings.Join(missing, ", "), cmd.CommandPath()+" --help")
		}
		if hasBody {
			return nil
		}
		if len(args) > requiredCount {
			return fmt.Errorf("too many arguments: expected %d, got %d; run %q for usage", requiredCount, len(args), cmd.CommandPath()+" --help")
		}
		return nil
	}
}

func shieldGeneratedNegativeNumberArgs(root *cobra.Command, args []string) []string {
	cmd, opStart := findGeneratedOperationCommand(root, args)
	if cmd == nil || opStart < 0 {
		return args
	}
	rawTypes := cmd.Annotations[generatedOperationRequiredTypesAnnotation]
	if rawTypes == "" {
		return args
	}
	requiredTypes := strings.Split(rawTypes, "\n")
	out := append([]string(nil), args...)
	pos := 0
	for i := opStart; i < len(out); i++ {
		token := out[i]
		if token == "--" {
			for j := i + 1; j < len(out); j++ {
				if shouldShieldGeneratedArg(requiredTypes, pos, out[j]) {
					out[j] = encodeGeneratedNegativeNumberArg(out[j])
				}
				pos++
			}
			break
		}
		if isFlagLikeToken(token) && !isNegativeNumberToken(token) {
			if generatedFlagConsumesNext(cmd, token) && i+1 < len(out) {
				i++
			}
			continue
		}
		if shouldShieldGeneratedArg(requiredTypes, pos, token) {
			out[i] = encodeGeneratedNegativeNumberArg(token)
		}
		pos++
	}
	return out
}

func validateGeneratedFlagValueTokens(root *cobra.Command, args []string) error {
	cmd, opStart := findGeneratedOperationCommand(root, args)
	if cmd == nil || opStart < 0 {
		return nil
	}
	for i := opStart; i < len(args); i++ {
		token := args[i]
		if token == "--" {
			return nil
		}
		if !isFlagLikeToken(token) || isNegativeNumberToken(token) {
			continue
		}
		if !generatedLocalFlagConsumesNext(cmd, token) {
			continue
		}
		if i+1 >= len(args) {
			continue
		}
		next := args[i+1]
		if isFlagLikeToken(next) && !isNegativeNumberToken(next) {
			return fmt.Errorf("%s requires a value; use %s=<value> when the value starts with '-'", token, token)
		}
		i++
	}
	return nil
}

func generatedLocalFlagConsumesNext(cmd *cobra.Command, token string) bool {
	if token == "--" || !isFlagLikeToken(token) || strings.Contains(token, "=") {
		return false
	}
	if strings.HasPrefix(token, "--") {
		name := strings.TrimPrefix(token, "--")
		flag := cmd.LocalFlags().Lookup(name)
		return flag != nil && flag.NoOptDefVal == ""
	}
	name := strings.TrimPrefix(token, "-")
	if name == "" {
		return false
	}
	flag := cmd.LocalFlags().ShorthandLookup(string(name[0]))
	return flag != nil && flag.NoOptDefVal == "" && len(name) == 1
}

func findGeneratedOperationCommand(root *cobra.Command, args []string) (*cobra.Command, int) {
	cmd := root
	for i := 1; i < len(args); i++ {
		token := args[i]
		if token == "--" {
			return nil, -1
		}
		if isFlagLikeToken(token) {
			if generatedFlagConsumesNext(cmd, token) && i+1 < len(args) {
				i++
			}
			continue
		}
		next := childCommandForToken(cmd, token)
		if next == nil {
			return nil, -1
		}
		cmd = next
		if cmd.Annotations[generatedOperationAnnotation] == "true" || cmd.Annotations[generatedOperationRequiredTypesAnnotation] != "" {
			return cmd, i + 1
		}
	}
	return nil, -1
}

func childCommandForToken(cmd *cobra.Command, token string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == token {
			return child
		}
		for _, alias := range child.Aliases {
			if alias == token {
				return child
			}
		}
	}
	return nil
}

func generatedFlagConsumesNext(cmd *cobra.Command, token string) bool {
	if token == "--" || !isFlagLikeToken(token) || strings.Contains(token, "=") {
		return false
	}
	if strings.HasPrefix(token, "--") {
		name := strings.TrimPrefix(token, "--")
		flag := generatedCommandFlag(cmd, name)
		return flag != nil && flag.NoOptDefVal == ""
	}
	name := strings.TrimPrefix(token, "-")
	if name == "" {
		return false
	}
	flag := generatedCommandShorthandFlag(cmd, name[0])
	return flag != nil && flag.NoOptDefVal == "" && len(name) == 1
}

func generatedCommandFlag(cmd *cobra.Command, name string) *pflag.Flag {
	for current := cmd; current != nil; current = current.Parent() {
		if flag := current.Flags().Lookup(name); flag != nil {
			return flag
		}
		if flag := current.PersistentFlags().Lookup(name); flag != nil {
			return flag
		}
	}
	return nil
}

func generatedCommandShorthandFlag(cmd *cobra.Command, shorthand byte) *pflag.Flag {
	for current := cmd; current != nil; current = current.Parent() {
		if flag := current.Flags().ShorthandLookup(string(shorthand)); flag != nil {
			return flag
		}
		if flag := current.PersistentFlags().ShorthandLookup(string(shorthand)); flag != nil {
			return flag
		}
	}
	return nil
}

func shouldShieldGeneratedArg(requiredTypes []string, pos int, token string) bool {
	if pos >= len(requiredTypes) || !isGeneratedNumericType(requiredTypes[pos]) {
		return false
	}
	return isNegativeNumberToken(token)
}

func isGeneratedNumericType(typ string) bool {
	return typ == "number" || typ == "integer"
}

func isFlagLikeToken(token string) bool {
	return strings.HasPrefix(token, "-") && token != "-"
}

func isNegativeNumberToken(token string) bool {
	if !strings.HasPrefix(token, "-") || token == "-" || strings.HasPrefix(token, "--") {
		return false
	}
	_, err := strconv.ParseFloat(token, 64)
	return err == nil
}

func encodeGeneratedNegativeNumberArg(token string) string {
	return generatedNegativeNumberArgPrefix + url.QueryEscape(token)
}

func restoreGeneratedNegativeNumberArgs(args []string) []string {
	var restored []string
	for i, arg := range args {
		if !strings.HasPrefix(arg, generatedNegativeNumberArgPrefix) {
			continue
		}
		if restored == nil {
			restored = append([]string(nil), args...)
		}
		value := strings.TrimPrefix(arg, generatedNegativeNumberArgPrefix)
		if decoded, err := url.QueryUnescape(value); err == nil {
			restored[i] = decoded
		}
	}
	if restored != nil {
		return restored
	}
	return args
}

func showGeneratedOperationHelpAll(cmd *cobra.Command) error {
	orig := cmd.UsageTemplate()
	cmd.SetUsageTemplate(groupedUsageTemplate)
	err := cmd.Help()
	cmd.SetUsageTemplate(orig)
	return err
}

func (c *CLI) printGeneratedBodyExample(help spec.OperationHelp, contentType string) error {
	request, err := c.generatedBodyExampleRequest(help, contentType)
	if err != nil {
		return err
	}
	if request != nil && request.RawBinary {
		fmt.Fprintln(c.Stdout, "@input.bin")
		return nil
	}
	if request != nil && strings.TrimSpace(request.Example) != "" {
		fmt.Fprintln(c.Stdout, request.Example)
		return nil
	}
	fmt.Fprintln(c.Stdout, "{}")
	return nil
}

func (c *CLI) generatedBodyExampleRequest(help spec.OperationHelp, contentType string) (*spec.OperationBodyHelp, error) {
	if strings.TrimSpace(contentType) == "" {
		return help.Request, nil
	}
	requested := strings.TrimSpace(contentType)
	if alias := c.content.MIMETypeForName(requested); alias != "" {
		requested = alias
	}
	for _, request := range help.Requests {
		if mediaTypesMatch(request.MediaType, requested) {
			selected := request
			return &selected, nil
		}
	}
	if help.Request != nil && mediaTypesMatch(help.Request.MediaType, requested) {
		return help.Request, nil
	}
	available := make([]string, 0, len(help.Requests))
	for _, request := range help.Requests {
		if strings.TrimSpace(request.MediaType) != "" {
			available = append(available, request.MediaType)
		}
	}
	if len(available) == 0 && help.Request != nil && strings.TrimSpace(help.Request.MediaType) != "" {
		available = append(available, help.Request.MediaType)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("request body content type %q is not declared for this operation", contentType)
	}
	return nil, fmt.Errorf("request body content type %q is not declared for this operation; available: %s", contentType, strings.Join(available, ", "))
}

func mediaTypesMatch(a, b string) bool {
	if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
		return true
	}
	aBase, _, aErr := mime.ParseMediaType(a)
	bBase, _, bErr := mime.ParseMediaType(b)
	return aErr == nil && bErr == nil && strings.EqualFold(aBase, bBase)
}

func (c *CLI) generatedOperationAcceptHeader(mediaTypes []string, fallback string) string {
	if header := c.content.AcceptHeaderFor(mediaTypes); header != "" {
		return header
	}
	return fallback
}

func appendGeneratedOperationHelp(long string, required, optional []*paramInfo, help spec.OperationHelp) string {
	var b strings.Builder
	if strings.TrimSpace(long) != "" {
		b.WriteString(strings.TrimRight(long, "\n"))
	}
	appendSection := func(title string) {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(title)
		b.WriteString("\n")
	}

	if hasParamSchemas(required) {
		appendSection("Argument Schema:")
		b.WriteString("```schema\n{\n")
		for _, p := range required {
			if p.schema == "" {
				continue
			}
			b.WriteString("  " + p.flagName + ": " + indentSchemaContinuation(p.schema, "  ") + "\n")
		}
		b.WriteString("}\n```")
	}

	if hasParamSchemas(optional) {
		appendSection("Option Schema:")
		b.WriteString("```schema\n{\n")
		for _, p := range optional {
			if p.schema == "" || p.hidden {
				continue
			}
			b.WriteString("  --" + p.flagName + ": " + indentSchemaContinuation(p.schema, "  ") + "\n")
		}
		b.WriteString("}\n```")
	}

	if help.Request != nil {
		if help.Request.Example != "" {
			appendSection("Input Example:")
			b.WriteString("```json\n")
			b.WriteString(help.Request.Example)
			b.WriteString("\n```")
		}
		if help.Request.Schema != "" {
			title := "Request Schema"
			if help.Request.MediaType != "" {
				title += " (" + help.Request.MediaType + ")"
			}
			appendSection(title + ":")
			b.WriteString("```schema\n")
			b.WriteString(help.Request.Schema)
			b.WriteString("\n```")
		}
	}

	for _, resp := range help.Responses {
		if len(resp.Codes) == 0 {
			continue
		}
		prefix := "Response"
		if len(resp.Codes) > 1 {
			prefix = "Responses"
		}
		title := prefix + " " + strings.Join(resp.Codes, "/")
		if resp.MediaType != "" {
			title += " (" + resp.MediaType + ")"
		}
		appendSection(title + ":")
		if resp.Description != "" && len(resp.Codes) == 1 {
			b.WriteString(resp.Description)
			b.WriteString("\n\n")
		}
		if len(resp.Headers) > 0 {
			b.WriteString("Headers: ")
			b.WriteString(strings.Join(resp.Headers, ", "))
			b.WriteString("\n\n")
		}
		if resp.NoBody && resp.Schema == "" {
			b.WriteString("Response has no body")
			continue
		}
		if resp.Example != "" {
			b.WriteString("```json\n")
			b.WriteString(resp.Example)
			b.WriteString("\n```\n\n")
		}
		if resp.Schema != "" {
			b.WriteString("```schema\n")
			b.WriteString(resp.Schema)
			b.WriteString("\n```")
		}
	}

	return b.String()
}

func hasParamSchemas(params []*paramInfo) bool {
	for _, p := range params {
		if p.schema != "" && !p.hidden {
			return true
		}
	}
	return false
}

func indentSchemaContinuation(schema, indent string) string {
	lines := strings.Split(schema, "\n")
	if len(lines) <= 1 {
		return schema
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

func generatedOperationExamples(commandName, apiName, use string, examples []string) string {
	if line := generatedOperationExampleLine(commandName, apiName, use, examples); line != "" && len(examples) <= 1 {
		return line
	}
	var b strings.Builder
	for _, ex := range examples {
		line := generatedOperationExampleLine(commandName, apiName, use, []string{ex})
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func generatedOperationExampleLine(commandName, apiName, use string, examples []string) string {
	if commandName == "" {
		commandName = "restish"
	}
	prefix := commandName
	if apiName != "" {
		prefix = commandName + " " + apiName
	}
	hasBody := strings.Contains(use, " [body...]") || strings.Contains(use, " <body...>")
	use = strings.TrimSuffix(use, " [body...]")
	use = strings.TrimSuffix(use, " <body...>")
	if len(examples) == 0 {
		example := "  " + prefix + " " + use
		if hasBody {
			example += " --rsh-generate-body"
		}
		return example
	}
	ex := examples[0]
	example := "  " + prefix + " " + use
	if ex != "" {
		example += " " + shellQuoteGeneratedExample(ex)
	}
	return example
}

func shellQuoteGeneratedExample(ex string) string {
	if ex == "" || strings.HasPrefix(ex, "@") || strings.HasPrefix(ex, "<") || !strings.ContainsAny(ex, " \t,[]{}()\"'") {
		return ex
	}
	return "'" + strings.ReplaceAll(ex, "'", "'\\''") + "'"
}

// runGeneratedOp is the RunE handler for generated operation commands.
func (c *CLI) runGeneratedOp(
	cmd *cobra.Command,
	apiName, opPath, operationServer, method, requestMediaType, responseMediaType string,
	requestMultipartContentTypes map[string]string,
	help spec.OperationHelp,
	bodyRequired bool,
	rawBinaryBody bool,
	noAuth bool,
	optionalAuth bool,
	credentialAlternatives []spec.CredentialAlternative,
	required, optional []*paramInfo,
	args []string,
) error {
	// Substitute required params into the path, query string, and headers.
	path := opPath
	var query []generatedQueryParam
	var extraHeaders []string
	bodyArgStart := len(required)

	for i, p := range required {
		val := args[i]
		if err := validateGeneratedParamValues(p, []string{val}, "argument "+p.flagName); err != nil {
			return err
		}
		var err error
		path, query, extraHeaders, err = addGeneratedParam(path, query, extraHeaders, p, []string{val})
		if err != nil {
			return err
		}
	}

	// Collect optional param flags.
	if err := validateGeneratedObjectChildFlagConflicts(cmd, optional); err != nil {
		return err
	}
	contentChildren := map[*paramInfo]map[string]any{}
	var contentChildParents []*paramInfo
	for _, p := range optional {
		if !cmd.Flags().Changed(p.flagName) {
			continue
		}
		values, err := generatedFlagValues(cmd, p)
		if err != nil {
			return err
		}
		if err := validateGeneratedParamValues(p, values, "--"+p.flagName); err != nil {
			return err
		}
		if isGeneratedJSONContentChild(p) {
			if _, ok := contentChildren[p.parent]; !ok {
				contentChildren[p.parent] = map[string]any{}
				contentChildParents = append(contentChildParents, p.parent)
			}
			value, err := generatedJSONContentChildValue(p, values)
			if err != nil {
				return err
			}
			contentChildren[p.parent][p.objectKey] = value
			continue
		}
		path, query, extraHeaders, err = addGeneratedParam(path, query, extraHeaders, p, values)
		if err != nil {
			return err
		}
	}
	for _, parent := range contentChildParents {
		data, err := json.Marshal(contentChildren[parent])
		if err != nil {
			return err
		}
		var errAdd error
		path, query, extraHeaders, errAdd = addGeneratedParam(path, query, extraHeaders, parent, []string{string(data)})
		if errAdd != nil {
			return errAdd
		}
	}

	rawURL, err := c.generatedOperationURL(apiName, c.profileFromCmd(cmd), path, operationServer, query)
	if err != nil {
		return err
	}

	bodyArgs := args[bodyArgStart:]
	gf := globalFlagsFromContext(requestContext(cmd))
	var validationSchema map[string]any
	var validationMediaType string
	var validationSchemaDialect string
	validateBody, _ := cmd.Flags().GetBool("rsh-validate")
	if validateBody {
		validationRequest, err := c.generatedBodyExampleRequest(help, gf.ContentType)
		if err != nil {
			return err
		}
		if validationRequest != nil {
			validationSchema = validationRequest.JSONSchema
			validationMediaType = validationRequest.MediaType
			validationSchemaDialect = validationRequest.JSONSchemaDialect
		}
	}
	return c.runHTTPWithOptions(cmd, method, append([]string{rawURL}, bodyArgs...), false, extraHeaders, noAuth, "", requestMediaType, requestBodyOptions{
		multipartPartContentTypes: requestMultipartContentTypes,
		acceptOverride:            responseMediaType,
		validationSchema:          validationSchema,
		validationMediaType:       validationMediaType,
		validationSchemaDialect:   validationSchemaDialect,
		validationRequested:       validateBody,
		bodyRequired:              bodyRequired,
		rawBinaryBody:             rawBinaryBody,
		explicitAPIName:           apiName,
		operationAuth: &operationAuthPolicy{
			OptionalAuth:           optionalAuth,
			NoAuth:                 noAuth,
			CredentialAlternatives: credentialAlternatives,
			Override:               gf.Auth,
		},
	})
}

func generatedFlagValues(cmd *cobra.Command, p *paramInfo) ([]string, error) {
	switch p.typ {
	case "boolean":
		v, err := cmd.Flags().GetBool(p.flagName)
		if err != nil {
			return nil, err
		}
		return []string{strconv.FormatBool(v)}, nil
	case "integer":
		v, err := cmd.Flags().GetInt(p.flagName)
		if err != nil {
			return nil, err
		}
		return []string{strconv.Itoa(v)}, nil
	case "number":
		v, err := cmd.Flags().GetFloat64(p.flagName)
		if err != nil {
			return nil, err
		}
		return []string{strconv.FormatFloat(v, 'f', -1, 64)}, nil
	case "array":
		values, err := cmd.Flags().GetStringArray(p.flagName)
		if err != nil {
			return nil, err
		}
		return values, nil
	default:
		v, err := cmd.Flags().GetString(p.flagName)
		if err != nil {
			return nil, err
		}
		return []string{v}, nil
	}
}

func isGeneratedJSONContentChild(p *paramInfo) bool {
	return p != nil &&
		p.parent != nil &&
		p.in == "query" &&
		p.parent.contentMediaType != "" &&
		isJSONMediaType(p.parent.contentMediaType)
}

func generatedJSONContentChildValue(p *paramInfo, values []string) (any, error) {
	switch p.typ {
	case "boolean":
		if len(values) == 0 {
			return false, nil
		}
		return strconv.ParseBool(values[0])
	case "integer":
		if len(values) == 0 {
			return int64(0), nil
		}
		return strconv.ParseInt(values[0], 10, 64)
	case "number":
		if len(values) == 0 {
			return float64(0), nil
		}
		return strconv.ParseFloat(values[0], 64)
	case "array":
		return openapiparam.NormalizeArrayValues(values), nil
	case "object":
		parsed, err := shorthand.Unmarshal(strings.Join(values, " "), shorthand.ParseOptions{EnableObjectDetection: true}, nil)
		if err != nil {
			return nil, fmt.Errorf("parse JSON parameter child: %w", err)
		}
		return parsed, nil
	default:
		if len(values) == 0 {
			return "", nil
		}
		return values[0], nil
	}
}

func validateGeneratedParamValues(p *paramInfo, values []string, label string) error {
	if p == nil {
		return nil
	}
	for _, value := range values {
		if err := validateGeneratedScalarValue(p.typ, value); err != nil {
			return fmt.Errorf("%s must be %s", label, generatedScalarTypeLabel(p.typ))
		}
	}
	return nil
}

func validateGeneratedScalarValue(typ, value string) error {
	switch typ {
	case "integer":
		_, err := strconv.ParseInt(value, 10, 64)
		return err
	case "number":
		_, err := strconv.ParseFloat(value, 64)
		return err
	case "boolean":
		_, err := strconv.ParseBool(value)
		return err
	default:
		return nil
	}
}

func generatedScalarTypeLabel(typ string) string {
	switch typ {
	case "integer":
		return "integer"
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	default:
		return typ
	}
}

func (c *CLI) generatedOperationURL(apiName, profileName, path, operationServer string, query []generatedQueryParam) (string, error) {
	apiCfg, err := c.requireAPI(apiName)
	if err != nil {
		return "", err
	}
	var rawURL string
	baseURL := effectiveProfileBaseURL(apiCfg, profileName)
	operationBase := effectiveOperationBase(apiCfg, profileName)
	if operationServer != "" {
		rawURL = strings.TrimRight(operationServer, "/") + path
		_, rewritten, err := config.ApplyURLOverrides(rawURL, effectiveURLOverrides(apiCfg, profileName))
		if err != nil {
			return "", fmt.Errorf("url_overrides: %w", err)
		}
		if !rewritten && !config.OperationOriginAllowed(operationServer, apiCfg.AllowedOperationOrigins) {
			return "", fmt.Errorf("operation server %s is outside API base_url and is not allowed; add allowed_operation_origins[]: %s", operationServerOrigin(operationServer), suggestedOperationOrigin(operationServer))
		}
	} else if operationBase != "" {
		resolvedBase, err := config.ResolveOperationBaseURL(baseURL, operationBase)
		if err != nil {
			return "", fmt.Errorf("operation_base: %w", err)
		}
		rawURL = strings.TrimRight(resolvedBase, "/") + path
	} else {
		rawURL = apiName + path
	}
	if qs := encodeGeneratedQuery(query); qs != "" {
		rawURL += "?" + qs
	}
	return rawURL, nil
}

func operationServerOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host
}

func suggestedOperationOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	if strings.HasSuffix(host, ".do-ai.run") {
		return u.Scheme + "://*.do-ai.run"
	}
	return u.Scheme + "://" + u.Host
}

type generatedQueryParam struct {
	name          string
	value         string
	allowReserved bool
}

func generatedParamDescriptor(p *paramInfo) openapiparam.Param {
	return openapiparam.Param{
		Name:          p.name,
		In:            p.in,
		Type:          p.typ,
		Style:         p.style,
		Explode:       p.explode,
		AllowReserved: p.allowReserved,
	}
}

func addGeneratedParam(path string, q []generatedQueryParam, extraHeaders []string, p *paramInfo, values []string) (string, []generatedQueryParam, []string, error) {
	if len(values) == 0 {
		return path, q, extraHeaders, nil
	}
	if p.parent != nil {
		if p.in != "query" {
			return path, q, extraHeaders, nil
		}
		value, err := generatedParamValue(p, values)
		if err != nil {
			return path, q, extraHeaders, err
		}
		q = append(q, generatedQueryParam{
			name:          p.name + "[" + p.objectKey + "]",
			value:         value.Scalar,
			allowReserved: p.allowReserved,
		})
		return path, q, extraHeaders, nil
	}
	switch p.in {
	case "path":
		serialized, err := serializeGeneratedPathParam(p, values)
		if err != nil {
			return path, q, extraHeaders, err
		}
		path = strings.ReplaceAll(path, "{"+p.name+"}", serialized)
	case "query":
		parts, err := serializeGeneratedQueryParam(p, values)
		if err != nil {
			return path, q, extraHeaders, err
		}
		q = append(q, parts...)
	case "header":
		headerValues, err := serializeGeneratedHeaderParam(p, values)
		if err != nil {
			return path, q, extraHeaders, err
		}
		for _, value := range headerValues {
			extraHeaders = append(extraHeaders, p.name+": "+value)
		}
	case "cookie":
		cookies, err := serializeGeneratedCookieParam(p, values)
		if err != nil {
			return path, q, extraHeaders, err
		}
		if len(cookies) > 0 {
			extraHeaders = append(extraHeaders, "Cookie: "+strings.Join(cookies, "; "))
		}
	}
	return path, q, extraHeaders, nil
}

func serializeGeneratedPathParam(p *paramInfo, values []string) (string, error) {
	if p.contentMediaType != "" {
		encoded, err := serializeGeneratedContentParam(p, values)
		if err != nil {
			return "", err
		}
		return url.PathEscape(encoded), nil
	}
	value, err := generatedParamValue(p, values)
	if err != nil {
		return "", err
	}
	return openapiparam.SerializePathParam(generatedParamDescriptor(p), value)
}

func serializeGeneratedQueryParam(p *paramInfo, values []string) ([]generatedQueryParam, error) {
	if p.contentMediaType != "" {
		encoded, err := serializeGeneratedContentParam(p, values)
		if err != nil {
			return nil, err
		}
		return []generatedQueryParam{{name: p.name, value: encoded, allowReserved: p.allowReserved}}, nil
	}
	value, err := generatedParamValue(p, values)
	if err != nil {
		return nil, err
	}
	parts, err := openapiparam.SerializeQueryParam(generatedParamDescriptor(p), value)
	if err != nil {
		return nil, err
	}
	out := make([]generatedQueryParam, 0, len(parts))
	for _, part := range parts {
		out = append(out, generatedQueryParam{name: part.Name, value: part.Value, allowReserved: part.AllowReserved})
	}
	return out, nil
}

func serializeGeneratedHeaderParam(p *paramInfo, values []string) ([]string, error) {
	if p.contentMediaType != "" {
		encoded, err := serializeGeneratedContentParam(p, values)
		if err != nil {
			return nil, err
		}
		return []string{encoded}, nil
	}
	value, err := generatedParamValue(p, values)
	if err != nil {
		return nil, err
	}
	return openapiparam.SerializeHeaderParam(generatedParamDescriptor(p), value)
}

func serializeGeneratedCookieParam(p *paramInfo, values []string) ([]string, error) {
	if p.contentMediaType != "" {
		encoded, err := serializeGeneratedContentParam(p, values)
		if err != nil {
			return nil, err
		}
		return []string{p.name + "=" + url.QueryEscape(encoded)}, nil
	}
	value, err := generatedParamValue(p, values)
	if err != nil {
		return nil, err
	}
	return openapiparam.SerializeCookieParam(generatedParamDescriptor(p), value)
}

func generatedParamValue(p *paramInfo, values []string) (openapiparam.Value, error) {
	switch p.typ {
	case "array":
		return openapiparam.ArrayValue(openapiparam.NormalizeArrayValues(values)), nil
	case "object":
		fields, err := generatedObjectFields(values)
		if err != nil {
			return openapiparam.Value{}, err
		}
		return openapiparam.ObjectValue(fields), nil
	default:
		if len(values) == 0 {
			return openapiparam.ScalarValue(""), nil
		}
		return openapiparam.ScalarValue(values[0]), nil
	}
}

func generatedObjectFields(values []string) ([]openapiparam.Field, error) {
	parsed, err := shorthand.Unmarshal(strings.Join(values, " "), shorthand.ParseOptions{EnableObjectDetection: true}, nil)
	if err != nil {
		return nil, fmt.Errorf("parse structured parameter: %w", err)
	}
	fields := map[string]string{}
	switch v := parsed.(type) {
	case map[string]any:
		for key, value := range v {
			fields[key] = fmt.Sprint(value)
		}
	case map[any]any:
		for key, value := range v {
			fields[fmt.Sprint(key)] = fmt.Sprint(value)
		}
	default:
		return nil, fmt.Errorf("structured parameter must be an object, got %T", parsed)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]openapiparam.Field, 0, len(keys))
	for _, key := range keys {
		out = append(out, openapiparam.Field{Key: key, Value: fields[key]})
	}
	return out, nil
}

func serializeGeneratedContentParam(p *paramInfo, values []string) (string, error) {
	if !isJSONMediaType(p.contentMediaType) {
		return strings.Join(values, " "), nil
	}
	var value any
	if len(values) == 1 {
		if err := json.Unmarshal([]byte(values[0]), &value); err == nil {
			data, _ := json.Marshal(value)
			return string(data), nil
		}
	}
	if p.typ == "array" {
		data, err := json.Marshal(openapiparam.NormalizeArrayValues(values))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	parsed, err := shorthand.Unmarshal(strings.Join(values, " "), shorthand.ParseOptions{EnableObjectDetection: true}, nil)
	if err != nil {
		return "", fmt.Errorf("parse JSON parameter: %w", err)
	}
	data, jerr := json.Marshal(parsed)
	if jerr != nil {
		return "", jerr
	}
	return string(data), nil
}

func isJSONMediaType(mediaType string) bool {
	mt := strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func encodeGeneratedQuery(parts []generatedQueryParam) string {
	if len(parts) == 0 {
		return ""
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, url.QueryEscape(part.name)+"="+encodeGeneratedQueryValue(part.value, part.allowReserved))
	}
	return strings.Join(out, "&")
}

const openAPIReservedChars = ":/?#[]@!$&'()*+,;="

func encodeGeneratedQueryValue(value string, allowReserved bool) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if isQueryUnreserved(ch) || (allowReserved && ch != '+' && strings.ContainsRune(openAPIReservedChars, rune(ch))) {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[ch>>4])
		b.WriteByte(hex[ch&0x0f])
	}
	return b.String()
}

func isQueryUnreserved(ch byte) bool {
	switch {
	case ch >= 'A' && ch <= 'Z':
		return true
	case ch >= 'a' && ch <= 'z':
		return true
	case ch >= '0' && ch <= '9':
		return true
	case ch == '-' || ch == '.' || ch == '_' || ch == '~':
		return true
	default:
		return false
	}
}

// extractPathParamNames returns path parameter names in left-to-right order
// from a path template like "/stores/{storeId}/items/{itemId}".
var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

func extractPathParamNames(path string) []string {
	var names []string
	for _, m := range pathParamRe.FindAllStringSubmatch(path, -1) {
		names = append(names, m[1])
	}
	return names
}

// toKebabCase converts a camelCase or PascalCase identifier to kebab-case.
// "getItemById" → "get-item-by-id", "ListUsers" → "list-users".
func toKebabCase(s string) string {
	s = normalizeKnownIdentifierAcronyms(s)
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if prev != 0 && unicode.IsUpper(r) {
			// Look ahead to get the next rune without allocating a rune slice.
			var next rune
			if j := i + utf8.RuneLen(r); j < len(s) {
				next, _ = utf8.DecodeRuneInString(s[j:])
			}
			if unicode.IsLower(prev) || (unicode.IsUpper(prev) && unicode.IsLower(next)) {
				b.WriteRune('-')
			}
		}
		b.WriteRune(unicode.ToLower(r))
		prev = r
	}
	return slugify(b.String())
}

func normalizeKnownIdentifierAcronyms(s string) string {
	replacements := []struct {
		from string
		to   string
	}{
		{"OAuth", "Oauth"},
		{"APIs", "Apis"},
		{"API", "Api"},
		{"URLs", "Urls"},
		{"URL", "Url"},
		{"JSON", "Json"},
	}
	for _, repl := range replacements {
		s = strings.ReplaceAll(s, repl.from, repl.to)
	}
	return s
}

func fallbackOperationName(method, path string) string {
	return slugify(strings.ToLower(method) + "-" + strings.Trim(path, "/"))
}

func operationNamePath(path, operationBase string) string {
	path = normalizeOperationNamePath(path)
	base := normalizeOperationNamePath(operationBase)
	base = strings.TrimRight(base, "/")
	if base == "" {
		return path
	}
	if path == base {
		return "/"
	}
	if strings.HasPrefix(path, base+"/") {
		return strings.TrimPrefix(path, base)
	}
	return path
}

func normalizeOperationNamePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = urlpath.Clean(path)
	if path == "." {
		return "/"
	}
	return path
}

func slugify(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// deprecatedNotice returns the cobra Deprecated string when flagged, else "".
func deprecatedNotice(deprecated bool) string {
	if deprecated {
		return "this operation is deprecated"
	}
	return ""
}
