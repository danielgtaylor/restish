package cli

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gosimple/slug"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Operation represents an API action, e.g. list-things or create-user
type Operation struct {
	Name          string   `json:"name"`
	Group         string   `json:"group,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
	Short         string   `json:"short,omitempty"`
	Long          string   `json:"long,omitempty"`
	Method        string   `json:"method,omitempty"`
	URITemplate   string   `json:"uriTemplate"`
	PathParams    []*Param `json:"pathParams,omitempty"`
	QueryParams   []*Param `json:"queryParams,omitempty"`
	HeaderParams  []*Param `json:"headerParams,omitempty"`
	BodyMediaType string   `json:"bodyMediaType,omitempty"`
	Examples      []string `json:"examples,omitempty"`
	Hidden        bool     `json:"hidden,omitempty"`
	Deprecated    string   `json:"deprecated,omitempty"`
}

// command returns a Cobra command instance for this operation.
func (o Operation) command() *cobra.Command {
	flags := map[string]interface{}{}

	use := slug.Make(o.Name)
	for _, p := range o.PathParams {
		use += " " + slug.Make(p.Name)
	}

	argSpec := cobra.ExactArgs(len(o.PathParams))
	if o.BodyMediaType != "" {
		argSpec = cobra.MinimumNArgs(len(o.PathParams))
	}

	long := o.Long

	examples := ""
	for _, ex := range o.Examples {
		examples += fmt.Sprintf("  %s %s %s\n", Root.CommandPath(), use, ex)
	}

	sub := &cobra.Command{
		Use:        use,
		GroupID:    o.Group,
		Aliases:    o.Aliases,
		Short:      o.Short,
		Long:       long,
		Example:    examples,
		Args:       argSpec,
		Hidden:     o.Hidden,
		Deprecated: o.Deprecated,
		Run: func(cmd *cobra.Command, args []string) {
			uri := o.URITemplate

			for i, param := range o.PathParams {
				value, err := param.Parse(args[i])
				if err != nil {
					value := param.Serialize(args[i])[0]
					log.Fatalf("could not parse param %s with input %s: %v", param.Name, value, err)
				}
				// Replaces URL-encoded `{`+name+`}` in the template.
				uri = strings.Replace(uri, "{"+param.Name+"}", fmt.Sprintf("%v", value), 1)
			}

			query := url.Values{}
			for _, param := range o.QueryParams {
				if !cmd.Flags().Changed(param.OptionName()) {
					// This option was not passed from the shell, so there is no need to
					// send it, even if it is the default or zero value.
					continue
				}

				flag := flags[param.Name]
				for _, v := range param.Serialize(flag) {
					query.Add(param.Name, v)
				}
			}
			queryEncoded := query.Encode()
			if queryEncoded != "" {
				if strings.Contains(uri, "?") {
					uri += "&"
				} else {
					uri += "?"
				}
				uri += queryEncoded
			}

			customServer := viper.GetString("rsh-server")
			if customServer != "" {
				// Adjust the server based on the customized input.
				orig, _ := url.Parse(uri)
				custom, _ := url.Parse(customServer)

				orig.Scheme = custom.Scheme
				orig.Host = custom.Host

				if custom.Path != "" && custom.Path != "/" {
					orig.Path = strings.TrimSuffix(custom.Path, "/") + orig.Path
				}

				uri = orig.String()
			}

			headers := http.Header{}
			for _, param := range o.HeaderParams {
				if !cmd.Flags().Changed(param.OptionName()) {
					// This option was not passed from the shell, so there is no need to
					// send it, even if it is the default or zero value.
					continue
				}

				for _, v := range param.Serialize(flags[param.Name]) {
					headers.Add(param.Name, v)
				}
			}

			var body io.Reader

			if o.BodyMediaType != "" {
				b, err := GetBody(o.BodyMediaType, args[len(o.PathParams):])
				if err != nil {
					panic(err)
				}
				body = strings.NewReader(b)
			}

			req, _ := http.NewRequest(o.Method, uri, body)
			req.Header = headers
			MakeRequestAndFormat(req)
		},
	}

	for _, p := range o.QueryParams {
		flags[p.Name] = p.AddFlag(sub.Flags())
	}

	for _, p := range o.HeaderParams {
		flags[p.Name] = p.AddFlag(sub.Flags())
	}

	return sub
}
