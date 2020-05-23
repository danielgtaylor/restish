package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/spf13/cobra"
)

func askConfirm(message string, def bool, help string) bool {
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

func askInput(message string, def string, required bool, help string) string {
	resp := ""

	options := []survey.AskOpt{}
	if required {
		options = append(options, survey.WithValidator(survey.Required))
	} else {
		message += " (optional)"
	}

	err := survey.AskOne(&survey.Input{Message: message, Default: def, Help: help}, &resp, options...)
	if err == terminal.InterruptErr {
		os.Exit(0)
	}
	if err != nil {
		panic(err)
	}
	return resp
}

func askSelect(message string, options []string, def interface{}, help string) string {
	resp := ""
	err := survey.AskOne(&survey.Select{
		Message: message,
		Options: options,
		Default: def,
		Help:    help,
	}, &resp)
	if err == terminal.InterruptErr {
		os.Exit(0)
	}
	if err != nil {
		panic(err)
	}
	return resp
}

func askBaseURI(config *APIConfig) {
	config.Base = askInput("Base URI", config.Base, true, "The entrypoint of the API, where Restish can look for an API description document and apply authentication.\nExample: https://api.example.com")

	dummy := &cobra.Command{}
	if api, err := Load(config.Base, dummy); err == nil {
		// Found an API, auto-load settings.
		if len(api.Auth) > 0 {
			auth := api.Auth[0]

			if config.Profiles == nil {
				config.Profiles = map[string]*APIProfile{}
			}

			def := config.Profiles["default"]

			if def == nil {
				def = &APIProfile{}
				config.Profiles["default"] = def
			}

			if def.Auth == nil {
				def.Auth = &APIAuth{}
			}

			if def.Auth.Name == "" {
				def.Auth.Name = auth.Name
				def.Auth.Params = map[string]string{}
				for k, v := range auth.Params {
					def.Auth.Params[k] = v
				}
			}
		}
	}
}

func askAuth(auth *APIAuth) {
	authTypes := []string{}
	for k := range authHandlers {
		authTypes = append(authTypes, k)
	}

	var name interface{}
	if auth.Name != "" {
		name = auth.Name
	}
	choice := askSelect("API auth type", authTypes, name, "This is how you authenticate with the API. Autodetected if possible.")

	auth.Name = choice

	if auth.Params == nil {
		auth.Params = map[string]string{}
	}

	prev := auth.Params
	auth.Params = map[string]string{}

	for _, p := range authHandlers[choice].Parameters() {
		auth.Params[p.Name] = askInput("Auth parameter "+p.Name, prev[p.Name], p.Required, p.Help)
	}

	for {
		if !askConfirm("Add additional auth param?", false, "") {
			break
		}

		k := askInput("Param key", "", true, "")
		v := askInput("Param value", prev[k], true, "")
		auth.Params[k] = v
	}
}

func askEditProfile(name string, profile *APIProfile) {
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

		options = append(options, "Setup auth", "Finished with profile")

		choice := askSelect("Select option for profile `"+name+"`", options, nil, "")

		switch {
		case choice == "Add header":
			key := askInput("Header name", "", true, "")
			profile.Headers[key] = askInput("Header value", "", false, "")
		case strings.HasPrefix(choice, "Edit header"):
			h := strings.SplitN(choice, " ", 3)[2]
			key := askInput("Header name", h, true, "")
			profile.Headers[key] = askInput("Header value", profile.Headers[key], false, "")
		case strings.HasPrefix(choice, "Delete header"):
			h := strings.SplitN(choice, " ", 3)[2]
			if askConfirm("Are you sure you want to delete the "+h+" header?", false, "") {
				delete(profile.Headers, h)
			}
		case choice == "Add query param":
			key := askInput("Query param name", "", true, "")
			profile.Query[key] = askInput("Query param value", "", false, "")
		case strings.HasPrefix(choice, "Edit query param"):
			q := strings.SplitN(choice, " ", 4)[3]
			key := askInput("Query param name", q, true, "")
			profile.Headers[key] = askInput("Query param value", profile.Query[key], false, "")
		case strings.HasPrefix(choice, "Delete query param"):
			q := strings.SplitN(choice, " ", 4)[3]
			if askConfirm("Are you sure you want to delete the "+q+" query param?", false, "") {
				delete(profile.Query, q)
			}
		case choice == "Setup auth":
			if profile.Auth == nil {
				profile.Auth = &APIAuth{}
			}
			askAuth(profile.Auth)
		case choice == "Finished with profile":
			return
		}
	}
}

func askAddProfile(config *APIConfig) {
	name := askInput("Profile name", "default", true, "")

	if config.Profiles == nil {
		config.Profiles = map[string]*APIProfile{}
	}

	config.Profiles[name] = &APIProfile{}
	askEditProfile(name, config.Profiles[name])
}

func askInitAPI(cmd *cobra.Command, args []string) {
	var config *APIConfig = configs[args[0]]

	if config == nil {
		config = &APIConfig{name: args[0], Profiles: map[string]*APIProfile{}}
		configs[args[0]] = config

		// Do an initial setup with a default profile first.
		askBaseURI(config)
		fmt.Println("Setting up a `default` profile")
		config.Profiles["default"] = &APIProfile{}
		askEditProfile("default", config.Profiles["default"])
	}

	for {
		options := []string{
			"Change base URI (" + config.Base + ")",
			"Add profile",
		}

		for k := range config.Profiles {
			options = append(options, "Edit profile "+k)
		}

		options = append(options, "Save and exit")

		choice := askSelect("Select option", options, nil, "")
		fmt.Println(choice)

		switch {
		case strings.HasPrefix(choice, "Change base URI"):
			askBaseURI(config)
		case choice == "Add profile":
			askAddProfile(config)
		case strings.HasPrefix(choice, "Edit profile"):
			profile := strings.SplitN(choice, " ", 3)[2]
			askEditProfile(profile, config.Profiles[profile])
		case choice == "Save and exit":
			config.Save()
			return
		}
	}
}
