package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/spec"
	"github.com/spf13/cobra"
)

const (
	completionBlockStart = "# >>> restish completion >>>"
	completionBlockEnd   = "# <<< restish completion <<<"
)

type completionInstallOptions struct {
	Shell               string
	DryRun              bool
	Yes                 bool
	NoDesc              bool
	SuppressRestartHint bool
}

func (c *CLI) addCompletionCommand(root *cobra.Command) {
	completionCmd := c.newCompletionCommand(root)
	completionCmd.Hidden = true
	root.AddCommand(completionCmd)
}

func (c *CLI) newCompletionCommand(root *cobra.Command) *cobra.Command {
	var noDesc bool
	completionCmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate or install shell completion scripts",
		Long:  completionLong,
		Example: fmt.Sprintf(`  %s shell completion zsh
  %s shell completion bash > restish.bash
  %s shell completion install zsh`, c.commandNameOrDefault(), c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown completion command %q", args[0])
			}
			return nil
		},
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	if rootCommandHasGroup(root, rootGroupHelp) {
		completionCmd.GroupID = rootGroupHelp
	}

	shortDesc := "Generate the autocompletion script for %s"
	bash := &cobra.Command{
		Use:                   "bash",
		Short:                 fmt.Sprintf(shortDesc, "bash"),
		Long:                  shellCompletionLong("bash", "restish shell completion bash > restish.bash"),
		Example:               fmt.Sprintf("  %s shell completion bash > restish.bash", c.commandNameOrDefault()),
		Args:                  usageNoArgs,
		DisableFlagsInUseLine: true,
		ValidArgsFunction:     cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return generateCompletionScript(cmd.Root(), "bash", noDesc, c.Stdout)
		},
	}
	zsh := &cobra.Command{
		Use:   "zsh",
		Short: fmt.Sprintf(shortDesc, "zsh"),
		Long:  shellCompletionLong("zsh", "restish shell completion install zsh"),
		Example: fmt.Sprintf(`  %s shell completion zsh > _restish
  %s shell completion install zsh`, c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args:              usageNoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return generateCompletionScript(cmd.Root(), "zsh", noDesc, c.Stdout)
		},
	}
	fish := &cobra.Command{
		Use:   "fish",
		Short: fmt.Sprintf(shortDesc, "fish"),
		Long:  shellCompletionLong("fish", "restish shell completion install fish"),
		Example: fmt.Sprintf(`  %s shell completion fish > restish.fish
  %s shell completion install fish`, c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args:              usageNoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return generateCompletionScript(cmd.Root(), "fish", noDesc, c.Stdout)
		},
	}
	powershell := &cobra.Command{
		Use:               "powershell",
		Short:             fmt.Sprintf(shortDesc, "powershell"),
		Long:              shellCompletionLong("powershell", "restish shell completion powershell | Out-String | Invoke-Expression"),
		Example:           fmt.Sprintf("  %s shell completion powershell | Out-String | Invoke-Expression", c.commandNameOrDefault()),
		Args:              usageNoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return generateCompletionScript(cmd.Root(), "powershell", noDesc, c.Stdout)
		},
	}
	for _, cmd := range []*cobra.Command{bash, zsh, fish, powershell} {
		cmd.Flags().BoolVar(&noDesc, "no-descriptions", false, "disable completion descriptions")
	}

	installCmd := &cobra.Command{
		Use:   "install <shell>",
		Short: "Install shell completion for your user account",
		Long:  completionInstallLong,
		Example: fmt.Sprintf(`  %s shell completion install zsh
  %s shell completion install fish --dry-run`, c.commandNameOrDefault(), c.commandNameOrDefault()),
		Args:              usageExactArgs(1),
		ValidArgs:         []string{"zsh", "fish"},
		ValidArgsFunction: cobra.FixedCompletions([]cobra.Completion{"zsh", "fish"}, cobra.ShellCompDirectiveNoFileComp),
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			yes, _ := cmd.Flags().GetBool("yes")
			noDesc, _ := cmd.Flags().GetBool("no-descriptions")
			return c.installCompletion(cmd, completionInstallOptions{
				Shell:  strings.ToLower(args[0]),
				DryRun: dryRun,
				Yes:    yes,
				NoDesc: noDesc,
			})
		},
	}
	installCmd.Flags().Bool("dry-run", false, "Show what would be written without modifying files")
	installCmd.Flags().BoolP("yes", "y", false, "Apply changes without confirmation prompt")
	installCmd.Flags().Bool("no-descriptions", false, "disable completion descriptions")

	completionCmd.AddCommand(bash, zsh, fish, powershell, installCmd)
	return completionCmd
}

func (c *CLI) completeRootURL(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if !looksLikeURLCompletion(toComplete) {
		return nil, cobra.ShellCompDirectiveDefault
	}
	return c.completeOperationURLs(cmd, "GET", args, toComplete, false)
}

func (c *CLI) completeHTTPURL(method string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return c.completeOperationURLs(cmd, method, args, toComplete, true)
	}
}

func (c *CLI) completeOperationURLs(cmd *cobra.Command, method string, _ []string, toComplete string, seedAPIs bool) ([]string, cobra.ShellCompDirective) {
	if c == nil || c.cfg == nil || len(c.cfg.APIs) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	profileName := completionProfileName(cmd)
	method = strings.ToUpper(method)

	var out []string
	seen := map[string]bool{}
	add := func(value, desc string) {
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		if desc != "" {
			value += "\t" + completionDescription(desc)
		}
		out = append(out, value)
	}

	names := sortedAPINames(c.cfg)
	for _, apiName := range names {
		apiCfg := c.cfg.APIs[apiName]
		if apiCfg == nil {
			continue
		}
		if seedAPIs && (toComplete == "" || (!strings.Contains(toComplete, "/") && strings.HasPrefix(apiName+"/", toComplete))) {
			add(apiName+"/", "API URL paths")
			continue
		}

		set, ok := c.completionOperationSet(cmd, apiName, apiCfg, profileName)
		if !ok {
			continue
		}
		for _, op := range set.Operations {
			if op.XCLI.Hidden || !strings.EqualFold(op.Method, method) {
				continue
			}
			desc := op.Summary
			if desc == "" {
				desc = fmt.Sprintf("%s %s", op.Method, op.Path)
			}
			for _, candidate := range c.operationURLCompletionCandidates(apiName, apiCfg, profileName, op) {
				for _, completed := range completeURLTemplate(toComplete, candidate) {
					add(completed, desc)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return completionValue(out[i]) < completionValue(out[j])
	})

	directive := cobra.ShellCompDirectiveNoFileComp
	if len(out) > 0 && allCompletionValuesEndWithSlash(out) {
		directive |= cobra.ShellCompDirectiveNoSpace
	}
	return out, directive
}

func (c *CLI) completionOperationSet(cmd *cobra.Command, apiName string, apiCfg *config.APIConfig, profileName string) (spec.OperationSet, bool) {
	opOpts := c.openAPIOperationOptions(apiCfg, profileName)
	stateName := c.apiStateName(apiName)
	if set, _, ok := spec.LoadOperationSetFromCacheStatus(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles, opOpts, true); ok {
		return set, true
	}
	s, err := spec.LoadStaleFromCache(c.specCacheDir(), stateName, Version, apiCfg.SpecFiles, c.loaders)
	if err != nil {
		return spec.OperationSet{}, false
	}
	if s == nil && spec.HasLocalSpecFiles(apiCfg.SpecFiles) {
		s, err = c.discoverSpec(cmd.Context(), apiName)
	}
	if err != nil || s == nil {
		return spec.OperationSet{}, false
	}
	set, err := s.OperationSet(opOpts)
	if err != nil {
		return spec.OperationSet{}, false
	}
	_ = spec.StoreOperationSetInCache(c.specCacheDir(), stateName, Version, opOpts, set)
	return set, true
}

type operationURLCompletionCandidate struct {
	template   string
	pathParams map[string]spec.Param
}

func (c *CLI) operationURLCompletionCandidates(apiName string, apiCfg *config.APIConfig, profileName string, op spec.Operation) []operationURLCompletionCandidate {
	baseURL, operationBase := completionOperationBase(apiCfg, profileName)
	pathParams := completionPathParams(op)
	var candidates []operationURLCompletionCandidate
	add := func(template string) {
		if template != "" {
			candidates = append(candidates, operationURLCompletionCandidate{template: template, pathParams: pathParams})
		}
	}
	if shortPath := shortOperationCompletionPath(baseURL, operationBase, op.Path); shortPath != "" {
		add(apiName + "/" + strings.TrimLeft(shortPath, "/"))
	}
	if full := fullOperationCompletionURL(baseURL, operationBase, op.Path); full != "" {
		add(full)
		if scheme, rest, ok := strings.Cut(full, "://"); ok && (scheme == "http" || scheme == "https") {
			add(rest)
		}
	}
	return candidates
}

func completionPathParams(op spec.Operation) map[string]spec.Param {
	params := map[string]spec.Param{}
	for _, p := range op.Parameters {
		if p.In == "path" {
			params[p.Name] = p
		}
	}
	return params
}

func completionOperationBase(apiCfg *config.APIConfig, profileName string) (string, string) {
	if apiCfg == nil {
		return "", ""
	}
	baseURL := apiCfg.BaseURL
	operationBase := apiCfg.OperationBase
	if prof := profileForName(apiCfg, profileName); prof != nil {
		if prof.BaseURL != "" {
			baseURL = prof.BaseURL
		}
		if prof.OperationBase != "" {
			operationBase = prof.OperationBase
		}
	}
	return baseURL, operationBase
}

func shortOperationCompletionPath(baseURL, operationBase, opPath string) string {
	if operationBase == "" {
		return opPath
	}
	full := fullOperationCompletionURL(baseURL, operationBase, opPath)
	base, baseErr := url.Parse(baseURL)
	target, targetErr := url.Parse(full)
	if baseErr != nil || targetErr != nil || !base.IsAbs() || !target.IsAbs() || !sameURLOrigin(base, target) {
		return ""
	}
	basePath := strings.TrimRight(base.EscapedPath(), "/")
	if basePath == "" {
		basePath = "/"
	}
	targetPath := target.EscapedPath()
	if targetPath == "" {
		targetPath = "/"
	}
	rel := relativeURLPath(basePath, targetPath)
	if rel == "." {
		return "/"
	}
	return rel
}

func relativeURLPath(basePath, targetPath string) string {
	baseParts := splitCleanURLPath(basePath)
	targetParts := splitCleanURLPath(targetPath)
	i := 0
	for i < len(baseParts) && i < len(targetParts) && baseParts[i] == targetParts[i] {
		i++
	}
	var parts []string
	for j := i; j < len(baseParts); j++ {
		parts = append(parts, "..")
	}
	parts = append(parts, targetParts[i:]...)
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

func splitCleanURLPath(raw string) []string {
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "/")
}

func fullOperationCompletionURL(baseURL, operationBase, opPath string) string {
	if baseURL == "" {
		return ""
	}
	if operationBase != "" {
		resolved, err := config.ResolveOperationBaseURL(baseURL, operationBase)
		if err != nil {
			return ""
		}
		return cleanCompletionURL(strings.TrimRight(resolved, "/") + opPath)
	}
	return cleanCompletionURL(strings.TrimRight(baseURL, "/") + opPath)
}

func cleanCompletionURL(rawURL string) string {
	cleaned := cleanExpandedAPIURL(rawURL)
	cleaned = strings.ReplaceAll(cleaned, "%7B", "{")
	cleaned = strings.ReplaceAll(cleaned, "%7D", "}")
	return cleaned
}

func completeURLTemplate(toComplete string, candidate operationURLCompletionCandidate) []string {
	template := candidate.template
	if toComplete == "" {
		return completeURLTemplateRemainder(strings.Split(template, "/"), 0)
	}
	if strings.Contains(toComplete, "?") || strings.Contains(toComplete, "#") {
		return nil
	}
	inParts := strings.Split(toComplete, "/")
	tplParts := strings.Split(template, "/")
	if len(inParts) > len(tplParts) {
		return nil
	}
	last := len(inParts) - 1
	for i, part := range inParts {
		if i >= len(tplParts) {
			return nil
		}
		tplPart := tplParts[i]
		if templatePathPart(tplPart) {
			param := candidate.pathParams[templatePathName(tplPart)]
			if i == last {
				return completeTemplatePathParam(tplParts, i, part, param)
			}
			if part != "" {
				tplParts[i] = part
				if len(param.Enum) > 0 && !stringInSlice(part, param.Enum) {
					return nil
				}
				continue
			}
			return nil
		}
		if i == last {
			if !strings.HasPrefix(tplPart, part) {
				return nil
			}
			tplParts[i] = tplPart
			return completeURLTemplateRemainder(tplParts, i+1)
		}
		if part != tplPart {
			return nil
		}
	}
	return completeURLTemplateRemainder(tplParts, len(inParts))
}

func completeTemplatePathParam(tplParts []string, idx int, part string, param spec.Param) []string {
	prefix := completionPrefix(tplParts, idx)
	if len(param.Enum) > 0 {
		var out []string
		for _, value := range param.Enum {
			if strings.HasPrefix(value, part) {
				out = append(out, prefix+value)
			}
		}
		return out
	}
	if part == "" {
		return nil
	}
	tplParts = append([]string(nil), tplParts...)
	tplParts[idx] = part
	return completeURLTemplateRemainder(tplParts, idx+1)
}

func completeURLTemplateRemainder(tplParts []string, start int) []string {
	for i := start; i < len(tplParts); i++ {
		if templatePathPart(tplParts[i]) {
			return []string{completionPrefix(tplParts, i)}
		}
	}
	return []string{strings.Join(tplParts, "/")}
}

func completionPrefix(parts []string, end int) string {
	if end <= 0 {
		return ""
	}
	return strings.Join(parts[:end], "/") + "/"
}

func templatePathName(part string) string {
	start := strings.Index(part, "{")
	end := strings.LastIndex(part, "}")
	if start < 0 || end <= start {
		return ""
	}
	return part[start+1 : end]
}

func stringInSlice(needle string, haystack []string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func allCompletionValuesEndWithSlash(values []string) bool {
	for _, value := range values {
		if !strings.HasSuffix(completionValue(value), "/") {
			return false
		}
	}
	return len(values) > 0
}

func templatePathPart(part string) bool {
	return strings.Contains(part, "{") && strings.Contains(part, "}")
}

func looksLikeURLCompletion(toComplete string) bool {
	if toComplete == "" {
		return false
	}
	if strings.Contains(toComplete, "://") || strings.ContainsAny(toComplete, ".:") {
		return true
	}
	apiName, _, ok := strings.Cut(toComplete, "/")
	return ok && apiName != ""
}

func completionProfileName(cmd *cobra.Command) string {
	if cmd != nil {
		if flag := cmd.Flag("rsh-profile"); flag != nil && flag.Value.String() != "" {
			return flag.Value.String()
		}
		if root := cmd.Root(); root != nil {
			if flag := root.Flag("rsh-profile"); flag != nil && flag.Value.String() != "" {
				return flag.Value.String()
			}
		}
	}
	if profile := os.Getenv("RSH_PROFILE"); profile != "" {
		return profile
	}
	return "default"
}

func completionDescription(desc string) string {
	return strings.Join(strings.Fields(desc), " ")
}

func completionValue(value string) string {
	value, _, _ = strings.Cut(value, "\t")
	return value
}

func allCompletionValuesAreAPISeeds(values []string) bool {
	for _, value := range values {
		if !strings.HasSuffix(completionValue(value), "/") {
			return false
		}
	}
	return len(values) > 0
}

func shellCompletionLong(shell, installExample string) string {
	return fmt.Sprintf("Generate the autocompletion script for `%s`.\n\n"+
		"This writes the script to stdout for package managers and manual shell setup. For managed user-level installation where supported, use:\n\n"+
		"```bash\n%s\n```\n", shell, installExample)
}

func generateCompletionScript(root *cobra.Command, shell string, noDesc bool, out io.Writer) error {
	switch shell {
	case "bash":
		return root.GenBashCompletionV2(out, !noDesc)
	case "zsh":
		if noDesc {
			return root.GenZshCompletionNoDesc(out)
		}
		return root.GenZshCompletion(out)
	case "fish":
		return root.GenFishCompletion(out, !noDesc)
	case "powershell":
		if noDesc {
			return root.GenPowerShellCompletion(out)
		}
		return root.GenPowerShellCompletionWithDesc(out)
	default:
		return fmt.Errorf("unsupported shell %q; supported: bash, zsh, fish, powershell", shell)
	}
}

func (c *CLI) installCompletion(cmd *cobra.Command, opts completionInstallOptions) error {
	switch opts.Shell {
	case "zsh":
		return c.installZshCompletion(cmd, opts)
	case "fish":
		return c.installFishCompletion(cmd, opts)
	default:
		return fmt.Errorf("completion install: unsupported shell %q; supported: zsh, fish", opts.Shell)
	}
}

func (c *CLI) installZshCompletion(cmd *cobra.Command, opts completionInstallOptions) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("completion install: cannot determine home directory: %w", err)
	}

	scriptPath := c.completionScriptPath(cmd.Root().Name(), opts.Shell)
	rcPath := filepath.Join(home, ".zshrc")

	var script bytes.Buffer
	if err := generateCompletionScript(cmd.Root(), opts.Shell, opts.NoDesc, &script); err != nil {
		return err
	}
	rcBlock := zshCompletionRCBlock(scriptPath)

	existingRCBytes, _ := os.ReadFile(rcPath)
	existingRC := string(existingRCBytes)
	updatedRC, rcChanged := upsertManagedBlock(existingRC, completionBlockStart, completionBlockEnd, rcBlock)

	existingScript, _ := os.ReadFile(scriptPath)
	scriptChanged := !bytes.Equal(existingScript, script.Bytes())

	style := humanTextStyleFor(c.Stdout)
	if !scriptChanged && !rcChanged {
		fmt.Fprintf(c.Stdout, "Zsh completion %s: %s\n", style.ok("already installed"), scriptPath)
		return nil
	}

	if opts.DryRun {
		fmt.Fprintf(c.Stdout, "%s zsh completion script to %s\n", style.hint("Would write"), scriptPath)
		if rcChanged {
			fmt.Fprintf(c.Stdout, "%s %s with:\n%s\n", style.hint("Would update"), rcPath, rcBlock)
		}
		return nil
	}

	if !opts.Yes {
		fmt.Fprintf(c.Stdout, "Install zsh completion and update %s? [y/N]: ", rcPath)
		ok, err := c.confirm(cmd.Context())
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(c.Stdout, "%s.\n", style.warn("Cancelled"))
			return fmt.Errorf("completion install: cancelled")
		}
	}

	if scriptChanged {
		if err := atomicWriteTextFile(scriptPath, script.Bytes(), 0o600, 0o700); err != nil {
			return fmt.Errorf("completion install: write %s: %w", scriptPath, err)
		}
	}
	if rcChanged {
		if info, err := os.Stat(rcPath); err == nil && info.Mode().Perm()&0o077 != 0 {
			c.warnf("%s is more permissive than recommended; consider chmod 600 %s", rcPath, rcPath)
		}
		if err := atomicWriteTextFile(rcPath, []byte(updatedRC), 0o600, 0o700); err != nil {
			return fmt.Errorf("completion install: write %s: %w", rcPath, err)
		}
	}

	fmt.Fprintf(c.Stdout, "%s zsh completion: %s\n", style.ok("Installed"), scriptPath)
	if rcChanged {
		fmt.Fprintf(c.Stdout, "%s %s\n", style.ok("Updated"), rcPath)
	}
	if !opts.SuppressRestartHint {
		fmt.Fprintf(c.Stdout, "%s source %s\n", style.hint("Restart your shell or run:"), rcPath)
	}
	return nil
}

func (c *CLI) installFishCompletion(cmd *cobra.Command, opts completionInstallOptions) error {
	scriptPath, err := fishCompletionScriptPath(cmd.Root().Name())
	if err != nil {
		return err
	}

	var script bytes.Buffer
	if err := generateCompletionScript(cmd.Root(), opts.Shell, opts.NoDesc, &script); err != nil {
		return err
	}

	existingScript, _ := os.ReadFile(scriptPath)
	scriptChanged := !bytes.Equal(existingScript, script.Bytes())
	style := humanTextStyleFor(c.Stdout)
	if !scriptChanged {
		fmt.Fprintf(c.Stdout, "Fish completion %s: %s\n", style.ok("already installed"), scriptPath)
		return nil
	}

	if opts.DryRun {
		fmt.Fprintf(c.Stdout, "%s fish completion script to %s\n", style.hint("Would write"), scriptPath)
		return nil
	}

	if !opts.Yes {
		fmt.Fprintf(c.Stdout, "Install fish completion to %s? [y/N]: ", scriptPath)
		ok, err := c.confirm(cmd.Context())
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(c.Stdout, "%s.\n", style.warn("Cancelled"))
			return fmt.Errorf("completion install: cancelled")
		}
	}

	if err := atomicWriteTextFile(scriptPath, script.Bytes(), 0o600, 0o700); err != nil {
		return fmt.Errorf("completion install: write %s: %w", scriptPath, err)
	}

	fmt.Fprintf(c.Stdout, "%s fish completion: %s\n", style.ok("Installed"), scriptPath)
	fmt.Fprintf(c.Stdout, "%s\n", style.hint("Start a new fish session for completion to take effect."))
	return nil
}

func (c *CLI) completionScriptPath(commandName, shell string) string {
	return filepath.Join(filepath.Dir(c.configFilePath()), "completions", completionScriptFilename(commandName, shell))
}

func fishCompletionScriptPath(commandName string) (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("completion install: cannot determine home directory: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "fish", "completions", sanitizePathComponent(commandName)+".fish"), nil
}

func completionScriptFilename(commandName, shell string) string {
	name := sanitizePathComponent(commandName)
	switch shell {
	case "zsh":
		return "_" + name + ".zsh"
	default:
		return name + "." + shell
	}
}

func sanitizePathComponent(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "restish"
	}
	return b.String()
}

func zshCompletionRCBlock(scriptPath string) string {
	quotedPath := shellSingleQuote(scriptPath)
	return strings.Join([]string{
		completionBlockStart,
		"# Managed by `restish completion install zsh`.",
		"autoload -Uz compinit",
		"if ! whence -w compdef >/dev/null 2>&1; then",
		"  compinit",
		"fi",
		"if [ -r " + quotedPath + " ]; then",
		"  source " + quotedPath,
		"fi",
		completionBlockEnd,
		"",
	}, "\n")
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func upsertManagedBlock(existing, start, end, block string) (string, bool) {
	startIdx := strings.Index(existing, start)
	if startIdx >= 0 {
		endIdx := strings.Index(existing[startIdx:], end)
		if endIdx >= 0 {
			endIdx += startIdx + len(end)
			for endIdx < len(existing) && (existing[endIdx] == '\r' || existing[endIdx] == '\n') {
				endIdx++
			}
			updated := existing[:startIdx] + block + existing[endIdx:]
			return updated, updated != existing
		}
	}

	prefix := existing
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	updated := prefix + block
	return updated, updated != existing
}
