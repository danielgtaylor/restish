package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// apis holds the per-API configuration.
var apis *viper.Viper

// APIAuth describes the auth type and parameters for an API.
type APIAuth struct {
	Name   string
	Params map[string]string
}

// APIProfile contains account-specific API information
type APIProfile struct {
	Headers map[string]string
	Query   map[string]string
	Auth    APIAuth
}

// APIConfig describes per-API configuration options like the base URI and
// auth scheme, if any.
type APIConfig struct {
	Base       string
	SpecFiles  []string  `mapstructure:"spec_files"`
	CacheUntil time.Time `mapstructure:"cache_until"`
	Profiles   map[string]APIProfile
}

type apiConfigs map[string]APIConfig

var configs apiConfigs

func initAPIConfig() {
	apis = viper.New()

	apis.SetConfigName("apis")
	apis.AddConfigPath("$HOME/." + viper.GetString("app-name") + "/")
	apis.ReadInConfig()

	// Register api add sub-command
	// TODO...

	// Register API sub-commands
	configs = apiConfigs{}
	if err := apis.Unmarshal(&configs); err != nil {
		panic(err)
	}

	for apiName, config := range configs {
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

func findAPI(uri string) (string, APIConfig) {
	for name, config := range configs {
		if strings.HasPrefix(uri, config.Base) {
			// TODO: find the longest matching base?
			return name, config
		}
	}

	return "", APIConfig{}
}
