package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var surveyOpts = []survey.AskOpt{}

type asker interface {
	askConfirm(message string, def bool, help string) bool
	askInput(message string, def string, required bool, help string) string
	askSelect(message string, options []string, def interface{}, help string) string
}

type defaultAsker struct{}

func (a defaultAsker) askConfirm(message string, def bool, help string) bool {
	resp := false
	err := survey.AskOne(&survey.Confirm{Message: message, Default: def, Help: help}, &resp)
	if err == terminal.InterruptErr {
		os.Exit(0)
	}
	if err != nil {
		panic(err)
	}
	return resp
}

func (a defaultAsker) askInput(message string, def string, required bool, help string) string {
	resp := ""

	options := []survey.AskOpt{}
	if required {
		options = append(options, survey.WithValidator(survey.Required))
	} else {
		message += " (optional)"
	}

	var prompt survey.Prompt = &survey.Input{Message: message, Default: def, Help: help}
	if strings.Contains(message, "password") || strings.Contains(message, "secret") {
		prompt = &survey.Password{Message: message, Help: help}
	}

	err := survey.AskOne(prompt, &resp, options...)
	if err == terminal.InterruptErr {
		os.Exit(0)
	}
	if err != nil {
		panic(err)
	}
	return resp
}

func (a defaultAsker) askSelect(message string, options []string, def interface{}, help string) string {
	resp := ""
	err := survey.AskOne(&survey.Select{
		Message: message,
		Options: options,
		Default: def,
		Help:    help,
	}, &resp, surveyOpts...)
	if err == terminal.InterruptErr {
		os.Exit(0)
	}
	if err != nil {
		panic(err)
	}
	return resp
}

func askBaseURI(a asker, config *APIConfig) {
	config.Base = a.askInput("Base URI", config.Base, true, "The entrypoint of the API, where Restish can look for an API description document and apply authentication.\nExample: https://api.example.com")

	askLoadBaseAPI(a, config)
}

func askLoadBaseAPI(a asker, config *APIConfig) {
	var auth APIAuth

	dummy := &cobra.Command{}
	if api, err := Load(config.Base, dummy); err == nil {
		// Found an API, auto-load settings.

		if api.AutoConfig.Auth.Name != "" {
			// Found auto-configuration settings.
			fmt.Println("Found API auto-configuration, setting up default profile...")
			ac := api.AutoConfig
			responses := map[string]string{}

			// Get inputs from the user.
			for name, v := range ac.Prompt {
				def := ""
				if v.Default != nil {
					def = fmt.Sprintf("%v", v.Default)
				}

				// If a description is present, prefer to display it over the variable
				// name used a server arguments or in templates, since you don't
				// always have control over that value.
				promptText := name
				if v.Description != "" {
					promptText = v.Description
				}

				if len(v.Enum) > 0 {
					enumStr := []string{}
					for val := range v.Enum {
						enumStr = append(enumStr, fmt.Sprintf("%v", val))
					}
					responses[name] = a.askSelect(promptText, enumStr, def, "")
				} else {
					responses[name] = a.askInput(promptText, def, v.Default == nil, "")
				}
			}

			// Generate params from user inputs.
			params := map[string]string{}
			for name, resp := range responses {
				// Only include the param if the variable wasn't excluded.
				if !ac.Prompt[name].Exclude {
					params[name] = resp
				}
			}

			for name, template := range ac.Auth.Params {
				rendered := template

				// Render by replacing `{name}` with the value.
				for rn, rv := range responses {
					rendered = strings.ReplaceAll(rendered, "{"+rn+"}", rv)
				}

				params[name] = rendered
			}

			// Set up auth for the profile based on the rendered params.
			auth = APIAuth{
				Name:   ac.Auth.Name,
				Params: params,
			}
		}

		if auth.Name == "" && len(api.Auth) > 0 {
			// No auto-configuration present or successful, so fall back to the first
			// available defined security scheme.
			auth = api.Auth[0]
		}

		if config.Profiles == nil {
			config.Profiles = map[string]*APIProfile{}
		}

		// Set up the default profile, taking care not to blast away any existing
		// custom configuration if we are just updating the values.
		def := config.Profiles["default"]

		if def == nil {
			def = &APIProfile{}
			config.Profiles["default"] = def
		}

		if def.Auth == nil {
			def.Auth = &APIAuth{}
		}

		if auth.Name != "" {
			def.Auth.Name = auth.Name
			def.Auth.Params = map[string]string{}
			for k, v := range auth.Params {
				def.Auth.Params[k] = v
			}
		}
	}
}

func askAuth(a asker, auth *APIAuth) {
	authTypes := []string{}
	for k := range authHandlers {
		authTypes = append(authTypes, k)
	}

	var name interface{}
	if auth.Name != "" {
		name = auth.Name
	}
	choice := a.askSelect("API auth type", authTypes, name, "This is how you authenticate with the API. Autodetected if possible.")

	auth.Name = choice

	if auth.Params == nil {
		auth.Params = map[string]string{}
	}

	prev := auth.Params
	auth.Params = map[string]string{}

	for _, p := range authHandlers[choice].Parameters() {
		auth.Params[p.Name] = a.askInput("Auth parameter "+p.Name, prev[p.Name], p.Required, p.Help)
	}

	for {
		if !a.askConfirm("Add additional auth param?", false, "") {
			break
		}

		k := a.askInput("Param key", "", true, "")
		v := a.askInput("Param value", prev[k], true, "")
		auth.Params[k] = v
	}
}

func askEditProfile(a asker, name string, profile *APIProfile) {
	if profile.Headers == nil {
		profile.Headers = map[string]string{}
	}

	if profile.Query == nil {
		profile.Query = map[string]string{}
	}

	for {
		options := []string{
			"Add header",
		}

		for k := range profile.Headers {
			options = append(options, "Edit header "+k)
		}
		for k := range profile.Headers {
			options = append(options, "Delete header "+k)
		}

		options = append(options, "Add query param")

		for k := range profile.Query {
			options = append(options, "Edit query param "+k)
		}
		for k := range profile.Query {
			options = append(options, "Delete query param "+k)
		}

		options = append(options, "Add custom base URL")
		if profile.Base != "" {
			options = append(options, "Remove custom base URL")
		}

		options = append(options, "Set up auth", "Finished with profile")

		choice := a.askSelect("Select option for profile `"+name+"`", options, nil, "")

		switch {
		case choice == "Add header":
			key := a.askInput("Header name", "", true, "")
			profile.Headers[key] = a.askInput("Header value", "", false, "")
		case strings.HasPrefix(choice, "Edit header"):
			h := strings.SplitN(choice, " ", 3)[2]
			key := a.askInput("Header name", h, true, "")
			profile.Headers[key] = a.askInput("Header value", profile.Headers[key], false, "")
		case strings.HasPrefix(choice, "Delete header"):
			h := strings.SplitN(choice, " ", 3)[2]
			if a.askConfirm("Are you sure you want to delete the "+h+" header?", false, "") {
				delete(profile.Headers, h)
			}
		case choice == "Add query param":
			key := a.askInput("Query param name", "", true, "")
			profile.Query[key] = a.askInput("Query param value", "", false, "")
		case strings.HasPrefix(choice, "Edit query param"):
			q := strings.SplitN(choice, " ", 4)[3]
			key := a.askInput("Query param name", q, true, "")
			profile.Headers[key] = a.askInput("Query param value", profile.Query[key], false, "")
		case strings.HasPrefix(choice, "Delete query param"):
			q := strings.SplitN(choice, " ", 4)[3]
			if a.askConfirm("Are you sure you want to delete the "+q+" query param?", false, "") {
				delete(profile.Query, q)
			}
		case choice == "Set up auth":
			if profile.Auth == nil {
				profile.Auth = &APIAuth{}
			}
			askAuth(a, profile.Auth)
		case choice == "Add custom base URL":
			url := a.askInput("Base URL", "", true, "")
			profile.Base = url
		case choice == "Remove custom base URL":
			profile.Base = ""
		case choice == "Finished with profile":
			return
		}
	}
}

func askAddProfile(a asker, config *APIConfig) {
	name := a.askInput("Profile name", "default", true, "")

	if config.Profiles == nil {
		config.Profiles = map[string]*APIProfile{}
	}

	config.Profiles[name] = &APIProfile{}
	askEditProfile(a, name, config.Profiles[name])
}

func askTLSConfig(a asker, config *APIConfig) {
	if config.TLS == nil {
		config.TLS = &TLSConfig{}
	}

	for {
		options := make([]string, 0, 7)

		if config.TLS.InsecureSkipVerify {
			options = append(options, "Delete insecure")
		} else {
			options = append(options, "Set insecure")
		}

		if config.TLS.Cert == "" {
			options = append(options, "Set certificate")
		} else {
			options = append(options, "Edit certificate", "Delete certificate")
		}

		if config.TLS.Key == "" {
			options = append(options, "Set key")
		} else {
			options = append(options, "Edit key", "Delete key")
		}

		if config.TLS.CACert == "" {
			options = append(options, "Set CA certificate")
		} else {
			options = append(options, "Edit CA certificate", "Delete CA certificate")
		}

		options = append(options, "Finished with TLS configuration")

		switch choice := a.askSelect("Select TLS configuration options", options, nil, ""); choice {
		case "Delete insecure":
			config.TLS.InsecureSkipVerify = false
		case "Set insecure":
			config.TLS.InsecureSkipVerify = true
		case "Set certificate":
			config.TLS.Cert = a.askInput("Certificate path", "", false, "")
		case "Edit certificate":
			config.TLS.Cert = a.askInput("Certificate path", config.TLS.Cert, false, "")
		case "Delete certificate":
			config.TLS.Cert = ""
		case "Set key":
			config.TLS.Key = a.askInput("Key path", "", false, "")
		case "Edit key":
			config.TLS.Key = a.askInput("Key path", config.TLS.Key, false, "")
		case "Delete key":
			config.TLS.Key = ""
		case "Set CA certificate":
			config.TLS.CACert = a.askInput("CA Certificate path", "", false, "")
		case "Edit CA certificate":
			config.TLS.CACert = a.askInput("CA Certificate path", config.TLS.CACert, false, "")
		case "Delete CA certificate":
			config.TLS.CACert = ""
		case "Finished with TLS configuration":
			return
		}
	}
}

func askInitAPI(a asker, cmd *cobra.Command, args []string) {
	var config *APIConfig = configs[args[0]]

	if config == nil {
		config = &APIConfig{
			name:     args[0],
			Profiles: map[string]*APIProfile{},
			TLS: &TLSConfig{
				InsecureSkipVerify: viper.GetBool("rsh-insecure"),
				Cert:               viper.GetString("rsh-client-cert"),
				Key:                viper.GetString("rsh-client-key"),
				CACert:             viper.GetString("rsh-ca-cert"),
			},
		}
		configs[args[0]] = config

		// Do an initial setup with a default profile first.
		if len(args) == 1 {
			askBaseURI(a, config)
		} else {
			config.Base = args[1]
			askLoadBaseAPI(a, config)
		}

		if config.Profiles["default"] == nil {
			fmt.Println("Setting up a `default` profile")
			config.Profiles["default"] = &APIProfile{}

			askEditProfile(a, "default", config.Profiles["default"])
		}
	}

	for {
		options := []string{
			"Change base URI (" + config.Base + ")",
			"Add profile",
		}

		for k := range config.Profiles {
			options = append(options, "Edit profile "+k)
		}

		if (config.TLS != nil) && (*config.TLS != TLSConfig{}) {
			options = append(options, "Edit TLS configuration")
		}

		options = append(options, "Save and exit")

		choice := a.askSelect("Select option", options, nil, "")

		switch {
		case strings.HasPrefix(choice, "Change base URI"):
			askBaseURI(a, config)
		case choice == "Add profile":
			askAddProfile(a, config)
		case strings.HasPrefix(choice, "Edit profile"):
			profile := strings.SplitN(choice, " ", 3)[2]
			askEditProfile(a, profile, config.Profiles[profile])
		case choice == "Edit TLS configuration":
			askTLSConfig(a, config)
		case choice == "Save and exit":
			config.Save()
			return
		}
	}
}

func askInitAPIDefault(cmd *cobra.Command, args []string) {
	askInitAPI(defaultAsker{}, cmd, args)
}
