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
)

// Operation represents an API action, e.g. list-things or create-user
type Operation struct {
	Name          string   `json:"name"`
	Aliases       []string `json:"aliases,omitempty"`
	Short         string   `json:"short,omitempty"`
	Long          string   `json:"long,omitempty"`
	Method        string   `json:"method,omitempty"`
	URITemplate   string   `json:"uriTemplate"`
	PathParams    []*Param `json:"pathParams,omitempty"`
	QueryParams   []*Param `json:"queryParams,omitempty"`
	HeaderParams  []*Param `json:"headerParams,omitempty"`
	BodyMediaType string   `json:"bodyMediaType,omitempty"`
	Hidden        bool     `json:"hidden,omitempty"`
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
	if tty {
		if l, err := Highlight("markdown", []byte(o.Long)); err == nil {
			long = string(l)
		}
	}

	sub := &cobra.Command{
		Use:     use,
		Aliases: o.Aliases,
		Short:   o.Short,
		Long:    long,
		Args:    argSpec,
		Hidden:  o.Hidden,
		Run: func(cmd *cobra.Command, args []string) {
			uri := o.URITemplate
			for i, param := range o.PathParams {
				value, err := param.Parse(args[i])
				if err != nil {
					value := param.Serialize(args[i])[0]
					log.Fatalf("could not parse param %s with input %s: %v", param.Name, value, err)
				}
				// Replaces URL-encoded `{`+name+`}` in the template.
				uri = strings.Replace(uri, "%7B"+param.Name+"%7D", fmt.Sprintf("%v", value), 1)
			}

			query := url.Values{}
			for _, param := range o.QueryParams {
				for _, v := range param.Serialize(flags[param.Name]) {
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

			var body io.Reader

			if o.BodyMediaType != "" {
				b, err := GetBody(o.BodyMediaType, args[len(o.PathParams):])
				if err != nil {
					panic(err)
				}
				body = strings.NewReader(b)
			}

			req, _ := http.NewRequest(o.Method, uri, body)
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
