package cli

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/exp/maps"
)

// apis holds the per-API configuration.
var apis *viper.Viper

// APIAuth describes the auth type and parameters for an API.
type APIAuth struct {
	Name   string            `json:"name" yaml:"name"`
	Params map[string]string `json:"params,omitempty" yaml:"params,omitempty"`
}

// TLSConfig contains the TLS setup for the HTTP client
type TLSConfig struct {
	InsecureSkipVerify bool   `json:"insecure,omitempty" yaml:"insecure,omitempty" mapstructure:"insecure"`
	Cert               string `json:"cert,omitempty" yaml:"cert,omitempty"`
	Key                string `json:"key,omitempty" yaml:"key,omitempty"`
	CACert             string `json:"ca_cert,omitempty" yaml:"ca_cert,omitempty" mapstructure:"ca_cert"`
}

// APIProfile contains account-specific API information
type APIProfile struct {
	Base    string            `json:"base,omitempty" yaml:"base,omitempty"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Query   map[string]string `json:"query,omitempty" yaml:"query,omitempty"`
	Auth    *APIAuth          `json:"auth,omitempty" yaml:"auth,omitempty"`
}

// APIConfig describes per-API configuration options like the base URI and
// auth scheme, if any.
type APIConfig struct {
	name      string
	Base      string                 `json:"base" yaml:"base"`
	SpecFiles []string               `json:"spec_files,omitempty" yaml:"spec_files,omitempty" mapstructure:"spec_files,omitempty"`
	Profiles  map[string]*APIProfile `json:"profiles,omitempty" yaml:"profiles,omitempty" mapstructure:",omitempty"`
	TLS       *TLSConfig             `json:"tls,omitempty" yaml:"tls,omitempty" mapstructure:",omitempty"`
}

// Save the API configuration to disk.
func (a APIConfig) Save() error {
	apis.Set(a.name, a)
	return apis.WriteConfig()
}

// Return colorized string of configuration in JSON or YAML
func (a APIConfig) GetPrettyDisplay(outFormat string) ([]byte, error) {
	var prettyConfig []byte

	// marshal
	if outFormat == "auto" {
		outFormat = "json"
	}
	marshalled, err := MarshalShort(outFormat, true, a)
	if err != nil {
		return nil, errors.New("unable to render configuration")
	}

	// colorize
	prettyConfig, err = Highlight(outFormat, marshalled)
	if err != nil {
		return nil, errors.New("unable to colorize output")
	}

	return prettyConfig, nil
}

type apiConfigs map[string]*APIConfig

var configs apiConfigs
var apiCommand *cobra.Command

func initAPIConfig() {
	apis = viper.New()

	apis.SetConfigName("apis")
	apis.AddConfigPath(viper.GetString("config-directory"))

	// Write a blank cache if no file is already there. Later you can use
	// configs.SaveConfig() to write new values.
	filename := path.Join(viper.GetString("config-directory"), "apis.json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		if err := os.WriteFile(filename, []byte("{}"), 0600); err != nil {
			panic(err)
		}
	}

	err := apis.ReadInConfig()
	if err != nil {
		panic(err)
	}

	// Register api init sub-command to register the API.
	apiCommand = &cobra.Command{
		GroupID: "generic",
		Use:     "api",
		Short:   "API management commands",
	}
	Root.AddCommand(apiCommand)

	apiCommand.AddCommand(&cobra.Command{
		Use:     "content-types",
		Aliases: []string{"ct", "cts"},
		Short:   "Show content types",
		Long:    "Show registered content-type information",
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			keys := []string{}
			for k := range contentTypes {
				if contentTypes[k].name != "" {
					keys = append(keys, k)
				}
			}

			// Sort content types by priority
			sort.Slice(keys, func(i, j int) bool {
				return contentTypes[keys[i]].q > contentTypes[keys[j]].q
			})

			fmt.Fprintln(Stdout, "Content types (most to least preferred):")
			for _, k := range keys {
				fmt.Fprintln(Stdout, contentTypes[k].name)
			}

			// Sort output formats alphabetically
			keys = maps.Keys(contentTypes)
			sort.Strings(keys)
			fmt.Fprintln(Stdout, "\nOutput formats:")
			for _, k := range keys {
				fmt.Fprintln(Stdout, k)
			}
		},
	})

	apiCommand.AddCommand(&cobra.Command{
		Use:     "configure short-name",
		Aliases: []string{"config"},
		Short:   "Initialize an API",
		Long:    "Initializes an API with a short interactive prompt session to set up the base URI and auth if needed.",
		Args:    cobra.MinimumNArgs(1),
		Run:     askInitAPIDefault,
	})

	apiCommand.AddCommand(&cobra.Command{
		Use:   "show short-name",
		Short: "Show API config",
		Long:  "Show an API configuration as JSON/YAML.",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			config := configs[args[0]]
			if config == nil {
				panic("API " + args[0] + " not found")
			}

			outFormat := viper.GetString("rsh-output-format")
			if prettyString, err := config.GetPrettyDisplay(outFormat); err == nil {
				Stdout.Write(prettyString)
			} else {
				panic(err)
			}
		},
	})

	apiCommand.AddCommand(&cobra.Command{
		Use:   "sync short-name",
		Short: "Sync an API",
		Long:  "Force-fetch the latest API description and update the local cache.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			viper.Set("rsh-no-cache", true)
			_, err := Load(fixAddress(args[0]), Root)
			if err != nil {
				panic(err)
			}
		},
	})

	// Register API sub-commands
	configs = apiConfigs{}
	if err := apis.Unmarshal(&configs); err != nil {
		panic(err)
	}

	seen := map[string]bool{}
	for apiName, config := range configs {
		func(config *APIConfig) {
			if seen[config.Base] {
				panic(fmt.Errorf("multiple APIs configured with the same base URL: %s", config.Base))
			}
			seen[config.Base] = true
			config.name = apiName
			configs[apiName] = config

			n := apiName
			cmd := &cobra.Command{
				GroupID: "api",
				Use:     n,
				Short:   config.Base,
				Run: func(cmd *cobra.Command, args []string) {
					cmd.Help()
				},
			}
			Root.AddCommand(cmd)
		}(config)
	}
}

func findAPI(uri string) (string, *APIConfig) {
	apiName := viper.GetString("api-name")

	for name, config := range configs {
		// fixes https://github.com/danielgtaylor/restish/issues/128
		if len(apiName) > 0 && name != apiName {
			continue
		}

		profile := viper.GetString("rsh-profile")
		if profile != "default" {
			if config.Profiles[profile] == nil {
				continue
			}
			if config.Profiles[profile].Base != "" {
				if strings.HasPrefix(uri, config.Profiles[profile].Base) {
					return name, config
				}
			} else if strings.HasPrefix(uri, config.Base) {
				return name, config
			}
		} else {
			if strings.HasPrefix(uri, config.Base) {
				// TODO: find the longest matching base?
				return name, config
			}
		}
	}

	return "", nil
}
