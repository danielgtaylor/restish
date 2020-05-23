package cli

import (
	"io/ioutil"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// apis holds the per-API configuration.
var apis *viper.Viper

// APIAuth describes the auth type and parameters for an API.
type APIAuth struct {
	Name   string            `json:"name"`
	Params map[string]string `json:"params"`
}

// APIProfile contains account-specific API information
type APIProfile struct {
	Headers map[string]string `json:"headers"`
	Query   map[string]string `json:"query"`
	Auth    *APIAuth          `json:"auth"`
}

// APIConfig describes per-API configuration options like the base URI and
// auth scheme, if any.
type APIConfig struct {
	name      string
	Base      string                 `json:"base"`
	SpecFiles []string               `json:"spec_files,omitempty" mapstructure:"spec_files,omitempty"`
	Profiles  map[string]*APIProfile `json:"profiles,omitempty" mapstructure:",omitempty"`
}

// Save the API configuration to disk.
func (a APIConfig) Save() error {
	apis.Set(a.name, a)
	return apis.WriteConfig()
}

type apiConfigs map[string]*APIConfig

var configs apiConfigs
var apiCommand *cobra.Command
var profileCommand *cobra.Command

func initAPIConfig() {
	apis = viper.New()

	apis.SetConfigName("apis")
	apis.AddConfigPath("$HOME/." + viper.GetString("app-name") + "/")

	// Write a blank cache if no file is already there. Later you can use
	// configs.SaveConfig() to write new values.
	filename := path.Join(viper.GetString("config-directory"), "apis.json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		if err := ioutil.WriteFile(filename, []byte("{}"), 0600); err != nil {
			panic(err)
		}
	}

	apis.ReadInConfig()

	// Register api init sub-command to register the API.
	apiCommand = &cobra.Command{
		Use:   "api",
		Short: "API management commands",
	}
	Root.AddCommand(apiCommand)

	apiCommand.AddCommand(&cobra.Command{
		Use:     "configure short-name",
		Aliases: []string{"config"},
		Short:   "Initialize an API",
		Long:    "Initializes an API with a short interactive prompt session to set up the base URI and auth if needed.",
		Args:    cobra.ExactArgs(1),
		Run:     askInitAPI,
	})

	// Register API sub-commands
	configs = apiConfigs{}
	if err := apis.Unmarshal(&configs); err != nil {
		panic(err)
	}

	for apiName, config := range configs {
		config.name = apiName
		configs[apiName] = config

		n := apiName
		c := config
		cmd := &cobra.Command{
			Use:   n,
			Short: c.Base,
			Run: func(cmd *cobra.Command, args []string) {
				cmd.Help()
			},
		}
		Root.AddCommand(cmd)
	}
}

func findAPI(uri string) (string, *APIConfig) {
	for name, config := range configs {
		if strings.HasPrefix(uri, config.Base) {
			// TODO: find the longest matching base?
			return name, config
		}
	}

	return "", nil
}
