package cli

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/logrusorgru/aurora"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/peterhellberg/link"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Root command (entrypoint) of the CLI.
var Root *cobra.Command

// Cache is used to store temporary data between runs.
var Cache *viper.Viper

// Formatter is the currently configured response output formatter.
var Formatter ResponseFormatter

// Stdout is a cross-platform, color-safe writer if colors are enabled,
// otherwise it defaults to `os.Stdout`.
var Stdout io.Writer = os.Stdout

// Stderr is a cross-platform, color-safe writer if colors are enabled,
// otherwise it defaults to `os.Stderr`.
var Stderr io.Writer = os.Stderr

// Ugh, see https://github.com/spf13/cobra/issues/836
var usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if (not .Parent)}}{{if (gt (len .Commands) 8)}}

Available API Commands:{{range .Commands}}{{if (not (or (eq .Name "help") (eq .Name "get") (eq .Name "put") (eq .Name "post") (eq .Name "patch") (eq .Name "delete") (eq .Name "head") (eq .Name "options")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Generic Commands:{{range .Commands}}{{if (or (eq .Name "help") (eq .Name "get") (eq .Name "put") (eq .Name "post") (eq .Name "patch") (eq .Name "delete") (eq .Name "head") (eq .Name "options"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{else}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

var tty bool
var au aurora.Aurora
var loaders []Loader

// Loader is used to detect and load an API spec, turning it into CLI commands.
type Loader interface {
	LocationHints() []string
	Detect(resp *http.Response) bool
	Load(entrypoint, spec url.URL, resp *http.Response) []*Operation
}

func generic(method string, addr string, args []string) {
	var body io.Reader

	if len(args) > 0 {
		d, err := GetBody("application/json", args)
		if err != nil {
			panic(err)
		}
		body = strings.NewReader(d)
	}

	req, _ := http.NewRequest(method, fixAddress(addr), body)
	MakeRequestAndFormat(req)
}

// Init will set up the CLI.
func Init() {
	initConfig("restish", "")
	initCache("restish")

	// Determine if we are using a TTY or colored output is forced-on.
	tty = false
	if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) || viper.GetBool("color") {
		tty = true
	}

	if viper.GetBool("nocolor") {
		// If forced off, ignore all of the above!
		tty = false
	}

	if tty {
		// Support colored output across operating systems.
		Stdout = colorable.NewColorableStdout()
		Stderr = colorable.NewColorableStderr()
	}

	au = aurora.NewAurora(tty)

	Formatter = NewDefaultFormatter(tty)

	Root = &cobra.Command{
		Use:     filepath.Base(os.Args[0]),
		Long:    "A generic client for REST-ish APIs <https://rest.sh/>",
		Version: "0.1",
		Example: `  # Get a URL
  $ restish google.com

  # Specify verb, header, and body shorthand
  $ restish post :8888/users -H authorization:abc123 name: Kari, role: admin`,
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			generic(http.MethodGet, args[0], args[1:])
		},
	}
	Root.SetUsageTemplate(usageTemplate)

	head := &cobra.Command{
		Use:   "head url",
		Short: "Head a URL",
		Long:  "Perform an HTTP HEAD on the given URL",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			generic(http.MethodHead, args[0], args[1:])
		},
	}
	Root.AddCommand(head)

	options := &cobra.Command{
		Use:   "options url",
		Short: "Options a URL",
		Long:  "Perform an HTTP OPTIONS on the given URL",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			generic(http.MethodOptions, args[0], args[1:])
		},
	}
	Root.AddCommand(options)

	get := &cobra.Command{
		Use:   "get url",
		Short: "Get a URL",
		Long:  "Perform an HTTP GET on the given URL",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			generic(http.MethodGet, args[0], args[1:])
		},
	}
	Root.AddCommand(get)

	post := &cobra.Command{
		Use:   "post url [body...]",
		Short: "Post a URL",
		Long:  "Perform an HTTP POST on the given URL",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			generic(http.MethodPost, args[0], args[1:])
		},
	}
	Root.AddCommand(post)

	put := &cobra.Command{
		Use:   "put url [body...]",
		Short: "Put a URL",
		Long:  "Perform an HTTP PUT on the given URL",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			generic(http.MethodPut, args[0], args[1:])
		},
	}
	Root.AddCommand(put)

	patch := &cobra.Command{
		Use:   "patch url [body...]",
		Short: "Patch a URL",
		Long:  "Perform an HTTP PATCH on the given URL",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			generic(http.MethodPatch, args[0], args[1:])
		},
	}
	Root.AddCommand(patch)

	delete := &cobra.Command{
		Use:   "delete url [body...]",
		Short: "Delete a URL",
		Long:  "Perform an HTTP DELETE on the given URL",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			generic(http.MethodDelete, args[0], args[1:])
		},
	}
	Root.AddCommand(delete)

	AddGlobalFlag("rsh-verbose", "v", "Enable verbose log output", false, false)
	AddGlobalFlag("rsh-output-format", "o", "Output format [auto, json, yaml]", "auto", false)
	AddGlobalFlag("rsh-filter", "f", "Filter / project results using JMESPath Plus", "", false)
	AddGlobalFlag("rsh-raw", "r", "Output result of query as raw rather than an escaped JSON string or list", false, false)
	AddGlobalFlag("rsh-server", "s", "Override server URL", "", false)
	AddGlobalFlag("rsh-header", "H", "Add custom header", []string{}, true)
	AddGlobalFlag("rsh-query", "q", "Add custom query param", []string{}, true)
	AddGlobalFlag("rsh-no-paginate", "", "Disable auto-pagination", false, false)
	AddGlobalFlag("rsh-profile", "p", "API auth profile", "default", false)

	initAPIConfig()
}

func userHomeDir() string {
	if runtime.GOOS == "windows" {
		home := os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		return home
	}
	return os.Getenv("HOME")
}

func initConfig(appName, envPrefix string) {
	// One-time setup to ensure the path exists so we can write files into it
	// later as needed.
	configDir := path.Join(userHomeDir(), "."+appName)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		panic(err)
	}

	// Load configuration from file(s) if provided.
	viper.SetConfigName("config")
	viper.AddConfigPath("/etc/" + appName + "/")
	viper.AddConfigPath("$HOME/." + appName + "/")
	viper.ReadInConfig()

	// Load configuration from the environment if provided. Flags below get
	// transformed automatically, e.g. `client-id` -> `PREFIX_CLIENT_ID`.
	viper.SetEnvPrefix(envPrefix)
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	// Save a few things that will be useful elsewhere.
	viper.Set("app-name", appName)
	viper.Set("config-directory", configDir)
	viper.SetDefault("server-index", 0)
}

func initCache(appName string) {
	Cache = viper.New()
	Cache.SetConfigName("cache")
	Cache.AddConfigPath("$HOME/." + appName + "/")

	// Write a blank cache if no file is already there. Later you can use
	// cli.Cache.SaveConfig() to write new values.
	filename := path.Join(viper.GetString("config-directory"), "cache.json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		if err := ioutil.WriteFile(filename, []byte("{}"), 0600); err != nil {
			panic(err)
		}
	}

	Cache.ReadInConfig()
}

// AddLoader adds a new API spec loader to the CLI.
func AddLoader(loader Loader) {
	loaders = append(loaders, loader)
}

// Load will hydrate the command tree for an API, possibly refreshing the
// API spec if the cache is out of date.
func Load(entrypoint string, root *cobra.Command) {
	uris := []string{}

	// TODO: load from cache if present

	LogInfo("Refreshing service definition spec...")

	uri, err := url.Parse(entrypoint)
	if err != nil {
		panic(err)
	}

	name, config := findAPI(entrypoint)
	found := false
	if name != "" && len(config.SpecFiles) > 0 {
		// Load the cached files
		for _, filename := range config.SpecFiles {
			resp := &http.Response{
				Proto:      "HTTP/1.1",
				StatusCode: 200,
			}

			body, err := ioutil.ReadFile(filename)
			if err != nil {
				panic(err)
			}

			for _, l := range loaders {
				// Reset the body
				resp.Body = ioutil.NopCloser(bytes.NewReader(body))

				if l.Detect(resp) {
					found = true
					resp.Body = ioutil.NopCloser(bytes.NewReader(body))
					operations := l.Load(*uri, *uri, resp)
					for _, op := range operations {
						root.AddCommand(op.command())
					}
				}
			}
		}

		if found {
			return
		}
	}

	LogDebug("Checking %s", entrypoint)
	resp, err := http.Get(entrypoint)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	links := link.ParseResponse(resp)
	if serviceDesc := links["service-desc"]; serviceDesc != nil {
		uris = append(uris, serviceDesc.URI)
	}

	// Try hints next
	for _, l := range loaders {
		uris = append(uris, l.LocationHints()...)
	}

	uris = append(uris, uri.String())

	for _, checkURI := range uris {
		parsed, err := url.Parse(checkURI)
		if err != nil {
			panic(err)
		}
		resolved := uri.ResolveReference(parsed)
		LogDebug("Checking %s", resolved)

		resp, err := http.Get(resolved.String()) // TODO: use HEAD request
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}

		for _, l := range loaders {
			// Reset the body
			resp.Body = ioutil.NopCloser(bytes.NewReader(body))

			if l.Detect(resp) {
				resp.Body = ioutil.NopCloser(bytes.NewReader(body))
				operations := l.Load(*uri, *resolved, resp)
				for _, op := range operations {
					root.AddCommand(op.command())
				}
				return
			}
		}
	}

	panic(fmt.Errorf("could not detect API type: %s", entrypoint))
}

// Run the CLI! Parse arguments, make requests, print responses.
func Run() {
	// We need to register new commands at runtime based on the selected API
	// so that we don't have to potentially refresh and parse every single
	// registered API just to run. So this is a little hacky, but we hijack
	// the root command's flag parsing to parse twice, get the first arg, and
	// if it isn't from a well-known set try to load that API.
	Root.SetOutput(ioutil.Discard)
	Root.ParseFlags(os.Args)
	Root.SetOutput(Stderr)

	// Now that flags are parsed we can enable verbose mode if requested.
	if viper.GetBool("rsh-verbose") {
		enableVerbose = true

		settings := viper.AllSettings()
		LogDebug("Configuration: %v", settings)
	}

	// Load the API commands if we can.
	if len(Root.Flags().Args()) > 1 {
		apiName := Root.Flags().Args()[1]

		if apiName == "help" && len(Root.Flags().Args()) > 2 {
			// The explicit `help` command is followed by the actual commands
			// you want help with. The first one is the API name.
			apiName = Root.Flags().Args()[2]
		}

		if apiName != "help" && apiName != "head" && apiName != "options" && apiName != "get" && apiName != "post" && apiName != "put" && apiName != "patch" && apiName != "delete" {
			// Try to find the registered config for this API. If not found,
			// there is no need to do anything since the normal flow will catch
			// the command being missing and print help.
			if cfg, ok := configs[apiName]; ok {
				for _, cmd := range Root.Commands() {
					if cmd.Use == apiName {
						Load(cfg.Base, cmd)
						break
					}
				}
			}
		}
	}

	// Phew, we made it. Execute the command now that everything is loaded
	// and all the relevant sub-commands are registered.
	if err := Root.Execute(); err != nil {
		LogError("Error: %v", err)
	}
}
