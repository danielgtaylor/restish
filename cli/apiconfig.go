package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

// apis holds the per-API configuration.
var apis *viper.Viper

// APIAuth describes the auth type and parameters for an API.
type APIAuth struct {
	Name   string            `json:"name"`
	Params map[string]string `json:"params"`
}

// TLSConfig contains the TLS setup for the HTTP client
type TLSConfig struct {
	InsecureSkipVerify bool   `json:"insecure" mapstructure:"insecure"`
	Cert               string `json:"cert"`
	Key                string `json:"key"`
	CACert             string `json:"ca_cert" mapstructure:"ca_cert"`
}

// APIProfile contains account-specific API information
type APIProfile struct {
	Base    string            `json:"base",omitempty`
	Headers map[string]string `json:"headers,omitempty"`
	Query   map[string]string `json:"query,omitempty"`
	Auth    *APIAuth          `json:"auth"`
}

// APIConfig describes per-API configuration options like the base URI and
// auth scheme, if any.
type APIConfig struct {
	name      string
	Base      string                 `json:"base"`
	SpecFiles []string               `json:"spec_files,omitempty" mapstructure:"spec_files,omitempty"`
	Profiles  map[string]*APIProfile `json:"profiles,omitempty" mapstructure:",omitempty"`
	TLS       *TLSConfig             `json:"tls,omitempty" mapstructure:",omitempty"`
}

// Save the API configuration to disk.
func (a APIConfig) Save() error {
	apis.Set(a.name, a)
	return apis.WriteConfig()
}

// Return colorized string of configuration in JSON or YAML
func (a APIConfig) GetPrettyDisplay(outFormat string) (string, error) {
	var prettyConfig []byte
	var marshalled []byte
	var err error

	// marshal
	if outFormat == "yaml" {
		marshalled, err = yaml.Marshal(a)
	} else {
		outFormat = "json"
		marshalled, err = json.MarshalIndent(&a, "", "  ")
	}

	if err != nil {
		return "", errors.New("unable to render configuration")
	}

	// colorize
	prettyConfig, err = Highlight(outFormat, marshalled)
	if err != nil {
		return "", errors.New("unable to colorize output")
	}

	return string(prettyConfig), nil
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
		Use:     "configure short-name",
		Aliases: []string{"config"},
		Short:   "Initialize an API",
		Long:    "Initializes an API with a short interactive prompt session to set up the base URI and auth if needed.",
		Args:    cobra.MinimumNArgs(1),
		Run:     askInitAPIDefault,
	})

	apiCommand.AddCommand(&cobra.Command{
		Use:     "show short-name",
		Aliases: []string{"show"},
		Short:   "Show an API",
		Long:    "Show an API configuration.",
		Args:    cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			config := configs[args[0]]
			if config == nil {
				panic("API not found")
			}

			outFormat := viper.Get("rsh-output-format").(string)
			if prettyString, err := config.GetPrettyDisplay(outFormat); err == nil {
				fmt.Println(prettyString)
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
	for name, config := range configs {
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
