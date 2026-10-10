package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rest-sh/restish/v2/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	rootGroupHTTP    = "http"
	rootGroupConfig  = "config"
	rootGroupPlugin  = "plugin"
	rootGroupAPI     = "api"
	rootGroupUtility = "utility"
	rootGroupHelp    = "help"
)

const securityCompletionAnnotation = "restish.securityCompletions"

func (c *CLI) newRootCmd() *cobra.Command {
	use := c.commandName
	if use == "" {
		use = "restish"
	}
	short := c.commandShort
	if short == "" {
		short = "A CLI for interacting with REST-ish HTTP APIs"
	}
	long := c.commandLong
	if long == "" {
		long = rootLongDefault
	}
	root := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Example: fmt.Sprintf(`  %s get https://api.example.com/items
  %s api connect demo https://api.example.com
  %s doctor`, use, use, use),
		Version:                    c.currentVersion(),
		SilenceUsage:               true,
		SilenceErrors:              true,
		SuggestionsMinimumDistance: 2,
		// ArbitraryArgs prevents cobra's legacyArgs validator from rejecting
		// unrecognised args before our RunE can inspect them (which we need for
		// bare-URL dispatch: "restish https://api.example.com").
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: c.completeRootURL,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			gf, err := parseGlobalFlags(cmd)
			if err != nil {
				return err
			}
			c.silentMode = gf.Silent
			cmd.SetContext(withGlobalFlags(cmd.Context(), gf))
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			// A bare URL (no explicit verb) infers the HTTP method from input:
			// GET without a body, POST when shorthand or stdin supplies a body.
			// Anything containing . : or / is likely a URL; a word matching a
			// registered API name is also routed as an inferred generic request.
			if strings.ContainsAny(args[0], ".:/") || c.isAPIShortName(args[0]) {
				return c.runInferredHTTP(cmd, args)
			}
			return unknownCommandError(cmd, args[0], rootUnknownCommandHint(cmd, args))
		},
	}
	if strings.Contains(use, " ") {
		root.Annotations = map[string]string{cobra.CommandDisplayNameAnnotation: use}
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return newUsageError(suggestFlagError(cmd, err))
	})

	addRootCommandGroups(root)
	setupGroupedUsage(root)
	c.addGlobalFlags(root)
	c.addHTTPCommands(root)
	c.addEditCommand(root)
	c.addCertCommand(root)
	c.addAPICommand(root)
	c.addCacheCommand(root)
	c.addConfigCommand(root)
	c.addCompletionCommand(root)
	c.addShellCommand(root)
	c.addLinksCommand(root)
	c.addVersionCommand(root)
	c.addDoctorCommand(root)
	c.addPluginCommand(root)
	c.addCommandPlugins(root)
	c.setupMarkdownHelp(root)
	return root
}

func rootUnknownCommandHint(cmd *cobra.Command, args []string) string {
	base := "run " + strconvQuote(cmd.CommandPath()+" --help") + " to see available commands or use a full URL"
	for _, arg := range args[1:] {
		if strings.ContainsAny(arg, ".:/") {
			return "a URL appears later in the command; check whether a flag value with spaces needs quotes; " + base
		}
	}
	return base
}

func suggestFlagError(cmd *cobra.Command, err error) error {
	if err == nil || cmd == nil {
		return err
	}
	msg := err.Error()
	const prefix = "unknown flag: --"
	if !strings.HasPrefix(msg, prefix) {
		return err
	}
	name := strings.TrimSpace(strings.TrimPrefix(msg, prefix))
	if name == "" {
		return err
	}
	if suggestion := flagSuggestion(cmd, name); suggestion != "" {
		return fmt.Errorf("%s; did you mean --%s?", msg, suggestion)
	}
	return err
}

func flagSuggestion(cmd *cobra.Command, name string) string {
	seen := map[string]struct{}{}
	var names []string
	for current := cmd; current != nil; current = current.Parent() {
		collectFlagNames(current.LocalFlags(), seen, &names)
		collectFlagNames(current.PersistentFlags(), seen, &names)
	}
	collectFlagNames(cmd.Root().PersistentFlags(), seen, &names)
	sort.Strings(names)
	if !strings.HasPrefix(name, "rsh-") {
		prefixedName := "rsh-" + name
		if _, ok := seen[prefixedName]; ok {
			return prefixedName
		}
		if suggestion := nearestFlagSuggestion(prefixedName, names); suggestion != "" {
			return suggestion
		}
	}
	return nearestFlagSuggestion(name, names)
}

func nearestFlagSuggestion(name string, names []string) string {
	var matches []string
	for _, candidate := range names {
		if levenshteinDistance(name, candidate) <= 2 {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func collectFlagNames(flags *pflag.FlagSet, seen map[string]struct{}, names *[]string) {
	if flags == nil {
		return
	}
	flags.VisitAll(func(flag *pflag.Flag) {
		if flag == nil || flag.Name == "" {
			return
		}
		if _, ok := seen[flag.Name]; ok {
			return
		}
		seen[flag.Name] = struct{}{}
		*names = append(*names, flag.Name)
	})
}

func (c *CLI) addVersionCommand(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:     "version",
		Short:   "Print the Restish version",
		Long:    versionLong,
		GroupID: rootGroupUtility,
		Args:    usageNoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectResponseTransformFlags(cmd); err != nil {
				return err
			}
			fmt.Fprintln(c.Stdout, c.currentVersion())
			return nil
		},
	})
}

func (c *CLI) currentVersion() string {
	if c.commandVersion != "" {
		return c.commandVersion
	}
	return Version
}

func (c *CLI) commandNameOrDefault() string {
	if c.commandName != "" {
		return c.commandName
	}
	return "restish"
}

func addRootCommandGroups(root *cobra.Command) {
	root.AddGroup(
		&cobra.Group{ID: rootGroupHTTP, Title: "Generic HTTP Commands"},
		&cobra.Group{ID: rootGroupConfig, Title: "Configuration and Setup"},
		&cobra.Group{ID: rootGroupPlugin, Title: "Plugin Commands"},
		&cobra.Group{ID: rootGroupAPI, Title: "Registered APIs"},
		&cobra.Group{ID: rootGroupUtility, Title: "Utilities"},
		&cobra.Group{ID: rootGroupHelp, Title: "Help"},
	)
	root.SetHelpCommandGroupID(rootGroupHelp)
	root.SetCompletionCommandGroupID(rootGroupHelp)
}

func rootCommandHasGroup(root *cobra.Command, id string) bool {
	for _, group := range root.Groups() {
		if group.ID == id {
			return true
		}
	}
	return false
}

// addGlobalFlags registers persistent flags that apply to all commands.
func (c *CLI) addGlobalFlags(root *cobra.Command) {
	pf := root.PersistentFlags()
	pf.StringArrayP("rsh-header", "H", nil, `Request header in "Name: Value" format (repeatable)`)
	pf.StringArrayP("rsh-query", "q", nil, `Query parameter in "key=value" format (repeatable)`)
	pf.StringP("rsh-server", "s", "", "Override scheme://host for all requests (e.g. https://staging.example.com)")
	pf.StringP("rsh-output-format", "o", "auto", "Output format for rendered response bodies: "+output.FormatterNames(c.formatters)+" (use -o lines for shell-friendly filtered values; see --rsh-columns, --rsh-sort-by for table)")
	pf.String("rsh-print", "auto", "Output parts to print: auto or any of H=request headers, B=request body, h=response headers, b=rendered body, p=pretty, c=color")
	pf.BoolP("rsh-silent", "S", false, "Suppress all output; only the exit code conveys success or failure")
	pf.String("rsh-columns", "", "Comma-separated column names for -o table (e.g. id,name,status)")
	pf.String("rsh-sort-by", "", "Sort -o table rows by this column name")
	pf.StringP("rsh-content-type", "c", "", `Request body content type, e.g. json, yaml, cbor (default: json)`)
	pf.StringP("rsh-filter", "f", "", "Filter/project the response using shorthand or jq (auto-detected)")
	pf.String("rsh-filter-lang", "", "Force filter language: shorthand or jq")
	pf.Bool("rsh-headers", false, "Shorthand for -f headers")
	pf.Bool("rsh-status", false, "Shorthand for -f status")
	pf.CountP("rsh-verbose", "v", "Verbose output: -v shows request/response headers, -vv adds TLS details")
	pf.Bool("rsh-insecure", false, "Disable TLS certificate verification")
	pf.String("rsh-client-cert", "", "Path to a PEM encoded client certificate for mTLS")
	pf.String("rsh-client-key", "", "Path to a PEM encoded private key for mTLS")
	pf.String("rsh-tls-signer", "", "TLS signer plugin to use for mTLS client certificate signing")
	pf.StringArray("rsh-tls-signer-param", nil, `TLS signer plugin parameter in "key=value" format (repeatable)`)
	pf.String("rsh-ca-cert", "", "Path to a PEM encoded CA certificate to trust")
	pf.String("rsh-tls-min-version", "", "Minimum TLS version: TLS1.2 or TLS1.3 (default TLS1.2)")
	pf.Bool("rsh-ignore-status-code", false, "Always exit 0 regardless of HTTP status")
	pf.StringP("rsh-timeout", "t", "", "Request timeout, e.g. 30s")
	pf.StringP("rsh-profile", "p", "", "API profile to use (overrides RSH_PROFILE env var; default: \"default\")")
	pf.String("rsh-auth", "", `Generated operation auth override, e.g. "PartnerKey" or "UserOAuth+PartnerKey"`)
	pf.Bool("rsh-no-cache", false, "Bypass the HTTP response cache (no read, no write)")
	pf.Bool("rsh-no-browser", false, "Disable automatic browser launch for interactive auth flows")
	pf.Int("rsh-retry", -1, "Maximum retry attempts for network errors and transient HTTP responses (0 = disable)")
	if flag := pf.Lookup("rsh-retry"); flag != nil {
		flag.DefValue = "2"
	}
	pf.Bool("rsh-retry-unsafe", false, "Allow retries for POST, PUT, PATCH, and DELETE requests")
	pf.String("rsh-retry-max-wait", "", "Maximum wait for Retry-After/X-Retry-In delays (default: 5m)")
	pf.Bool("rsh-no-paginate", false, "Disable automatic pagination (return only the first page)")
	pf.Bool("rsh-collect", false, "Collect paginated items before filtering (default: filter/render items as they arrive)")
	pf.Int("rsh-max-pages", 25, "Maximum number of pages to fetch (0 = unlimited)")
	pf.Int("rsh-max-items", 0, "Maximum number of paginated items or streamed events/lines to process (0 = unlimited)")
	pf.Int("rsh-max-body-size", 0, fmt.Sprintf("Maximum response body size in MiB (0 = default %d MiB)", output.DefaultMaxBodyBytes/(1024*1024)))
	pf.String("rsh-config", "", "Path to the restish config file (overrides RSH_CONFIG and the platform default)")
	pf.Bool("help-all", false, "Show all inherited Restish flags in help")

	c.registerFlagCompletions(root)
}

// registerFlagCompletions installs shell-completion functions for the global
// persistent flags that benefit from dynamic or well-known value suggestions.
func (c *CLI) registerFlagCompletions(root *cobra.Command) {
	// -o / --rsh-output-format: static list from registered formatters.
	_ = root.RegisterFlagCompletionFunc("rsh-output-format", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		fmts := c.formatters
		if fmts == nil {
			fmts = output.DefaultFormatters()
		}
		names := make([]string, 0, len(fmts))
		for name := range fmts {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	})
	_ = root.RegisterFlagCompletionFunc("rsh-print", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"auto", "hbpc", "b", "bp", "HBhbp"}, cobra.ShellCompDirectiveNoFileComp
	})

	// -p / --rsh-profile: dynamic list from all API profiles in config.
	_ = root.RegisterFlagCompletionFunc("rsh-profile", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		if c.cfg == nil {
			return []string{"default"}, cobra.ShellCompDirectiveNoFileComp
		}
		seen := map[string]struct{}{"default": {}}
		for _, api := range c.cfg.APIs {
			for name := range api.Profiles {
				seen[name] = struct{}{}
			}
		}
		names := make([]string, 0, len(seen))
		for name := range seen {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	})

	_ = root.RegisterFlagCompletionFunc("rsh-auth", func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		for current := cmd; current != nil; current = current.Parent() {
			if current.Annotations == nil {
				continue
			}
			raw := current.Annotations[securityCompletionAnnotation]
			if raw == "" {
				continue
			}
			return strings.Split(raw, "\n"), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

	// -c / --rsh-content-type: dynamic list from registered content types.
	_ = root.RegisterFlagCompletionFunc("rsh-content-type", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		if c.content == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, ct := range c.content.ContentTypes() {
			if ct.Name != "" {
				names = append(names, ct.Name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})

	// --rsh-filter-lang: static list of supported filter languages.
	_ = root.RegisterFlagCompletionFunc("rsh-filter-lang", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"shorthand", "jq"}, cobra.ShellCompDirectiveNoFileComp
	})

	// --rsh-tls-min-version: static list of supported TLS floors.
	_ = root.RegisterFlagCompletionFunc("rsh-tls-min-version", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"TLS1.2\tMinimum TLS 1.2 (default)", "TLS1.3\tRequire TLS 1.3"}, cobra.ShellCompDirectiveNoFileComp
	})
}
