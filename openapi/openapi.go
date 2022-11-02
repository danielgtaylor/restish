package openapi

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/danielgtaylor/casing"
	"github.com/danielgtaylor/restish/cli"
	"github.com/danielgtaylor/shorthand/v2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gosimple/slug"
	"github.com/spf13/cobra"
)

// reOpenAPI3 is a regex used to detect OpenAPI files from their contents.
var reOpenAPI3 = regexp.MustCompile(`['"]?openapi['"]?:\s*['"]?3`)

// OpenAPI Extensions
const (
	// Change the CLI name for an operation or parameter
	ExtName = "x-cli-name"

	// Set additional command aliases for an operation
	ExtAliases = "x-cli-aliases"

	// Change the description of an operation or parameter
	ExtDescription = "x-cli-description"

	// Ignore a path, operation, or parameter
	ExtIgnore = "x-cli-ignore"

	// Create a hidden command for an operation. It will not show in the help,
	// but can still be called.
	ExtHidden = "x-cli-hidden"
)

type autoConfig struct {
	Security string                       `json:"security"`
	Headers  map[string]string            `json:"headers,omitempty"`
	Prompt   map[string]cli.AutoConfigVar `json:"prompt,omitempty"`
	Params   map[string]string            `json:"params,omitempty"`
}

// Resolver is able to resolve relative URIs against a base.
type Resolver interface {
	Resolve(uri string) (*url.URL, error)
}

// extStr returns the string value of an OpenAPI extension stored as a JSON
// raw message.
func extStr(v openapi3.ExtensionProps, key string) (decoded string) {
	i := v.Extensions[key]
	if i != nil {
		if err := json.Unmarshal(i.(json.RawMessage), &decoded); err != nil {
			cli.LogWarning("Cannot read extensions property %s", key)
			decoded = ""
		}
	}

	return
}

// extBool returns the boolean value of an OpenAPI extension.
func extBool(v openapi3.ExtensionProps, key string) (decoded bool) {
	if v.Extensions[ExtIgnore] != nil {
		json.Unmarshal(v.Extensions[ExtIgnore].(json.RawMessage), &decoded)
	}
	return
}

func getRequestInfo(op *openapi3.Operation) (string, *openapi3.Schema, []interface{}) {
	mts := make(map[string][]interface{})

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		for mt, item := range op.RequestBody.Value.Content {
			var schema *openapi3.Schema
			var examples []interface{}

			if item.Schema != nil && item.Schema.Value != nil {
				schema = item.Schema.Value
			}

			if item.Example != nil {
				examples = append(examples, item.Example)
			} else {
				for _, ex := range item.Examples {
					if ex.Value != nil {
						examples = append(examples, ex.Value.Value)
						break
					}
				}
			}

			if schema != nil && len(examples) == 0 {
				examples = append(examples, genExample(schema))
			}

			mts[mt] = []interface{}{schema, examples}
		}
	}

	// Prefer JSON.
	for mt, item := range mts {
		if strings.Contains(mt, "json") {
			return mt, item[0].(*openapi3.Schema), item[1].([]interface{})
		}
	}

	// Fall back to YAML next.
	for mt, item := range mts {
		if strings.Contains(mt, "yaml") {
			return mt, item[0].(*openapi3.Schema), item[1].([]interface{})
		}
	}

	// Last resort: return the first we find!
	for mt, item := range mts {
		return mt, item[0].(*openapi3.Schema), item[1].([]interface{})
	}

	return "", nil, nil
}

func openapiOperation(cmd *cobra.Command, method string, uriTemplate *url.URL, path *openapi3.PathItem, op *openapi3.Operation) cli.Operation {
	pathParams := []*cli.Param{}
	queryParams := []*cli.Param{}
	headerParams := []*cli.Param{}

	combinedParams := append(path.Parameters, op.Parameters...)

	for _, p := range combinedParams {
		if p.Value != nil {
			var def interface{}
			var example interface{}

			typ := "string"
			if p.Value.Schema != nil && p.Value.Schema.Value != nil {
				if p.Value.Schema.Value.Type != "" {
					typ = p.Value.Schema.Value.Type
				}

				if typ == "array" {
					// TODO: nil checks
					typ += "[" + p.Value.Schema.Value.Items.Value.Type + "]"
				}

				def = p.Value.Schema.Value.Default
				example = p.Value.Schema.Value.Example
			}

			if p.Value.Example != nil {
				example = p.Value.Example
			}

			style := cli.StyleSimple
			if p.Value.Style == "form" {
				style = cli.StyleForm
			}

			explode := false
			if p.Value.Explode != nil {
				explode = *p.Value.Explode
			}

			displayName := ""
			if override := extStr(p.Value.ExtensionProps, ExtName); override != "" {
				displayName = override
			}

			description := p.Value.Description
			if override := extStr(p.Value.ExtensionProps, ExtDescription); override != "" {
				description = override
			}

			if override := extBool(p.Value.ExtensionProps, ExtIgnore); override {
				continue
			}

			param := &cli.Param{
				Type:        typ,
				Name:        p.Value.Name,
				DisplayName: displayName,
				Description: description,
				Style:       style,
				Explode:     explode,
				Default:     def,
				Example:     example,
			}

			switch p.Value.In {
			case "path":
				pathParams = append(pathParams, param)
			case "query":
				queryParams = append(queryParams, param)
			case "header":
				headerParams = append(headerParams, param)
			}
		}
	}

	var aliases []string
	if op.Extensions[ExtAliases] != nil {
		// We need to decode the raw extension value into our string slice.
		json.Unmarshal(op.Extensions[ExtAliases].(json.RawMessage), &aliases)
	}

	name := casing.Kebab(op.OperationID)
	if override := extStr(op.ExtensionProps, ExtName); override != "" {
		name = override
	} else if oldName := slug.Make(op.OperationID); oldName != name {
		// For backward-compatibility, add the old naming scheme as an alias
		// if it is different. See https://github.com/danielgtaylor/restish/issues/29
		// for additional context; we prefer kebab casing for readability.
		aliases = append(aliases, oldName)
	}

	desc := op.Description
	if override := extStr(op.ExtensionProps, ExtDescription); override != "" {
		desc = override
	}

	hidden := false
	if override := extBool(path.ExtensionProps, ExtHidden); override {
		hidden = true
	}

	mediaType := ""
	var examples []string
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		mt, reqSchema, reqExamples := getRequestInfo(op)
		mediaType = mt

		if len(reqExamples) > 0 {
			wroteHeader := false
			for _, ex := range reqExamples {
				if _, ok := ex.(string); !ok {
					// Not a string, so it's structured data. Let's marshal it to the
					// shorthand syntax if we can.
					if m, ok := ex.(map[string]interface{}); ok {
						exs := shorthand.MarshalCLI(m)

						if len(exs) < 150 {
							examples = append(examples, exs)
						} else {
							found := false
							for _, e := range examples {
								if e == "<input.json" {
									found = true
									break
								}
							}
							if !found {
								examples = append(examples, "<input.json")
							}
						}

						continue
					}

					b, _ := json.Marshal(ex)

					if !wroteHeader {
						desc += "\n## Input Example\n\n"
						wroteHeader = true
					}

					desc += "\n" + string(b) + "\n"
					continue
				}

				if !wroteHeader {
					desc += "\n## Input Example\n\n"
					wroteHeader = true
				}

				desc += "\n" + ex.(string) + "\n"
			}
		}

		if reqSchema != nil {
			desc += "\n## Request Schema (" + mt + ")\n\n```schema\n" + renderSchema(reqSchema, "", modeWrite) + "\n```\n"
		}
	}

	codes := []string{}
	for code := range op.Responses {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	for _, code := range codes {
		if op.Responses[code] == nil || op.Responses[code].Value == nil {
			continue
		}

		resp := op.Responses[code].Value

		if len(resp.Content) > 0 {
			for ct, typeInfo := range resp.Content {
				if len(desc) > 0 && !strings.HasSuffix(desc, "\n") {
					desc += "\n"
				}
				desc += "\n## Response " + code + " (" + ct + ")\n"
				if resp.Description != nil && *resp.Description != "" {
					desc += "\n" + *resp.Description + "\n"
				}

				if typeInfo.Schema != nil && typeInfo.Schema.Value != nil {
					desc += "\n```schema\n" + renderSchema(typeInfo.Schema.Value, "", modeRead) + "\n```\n"
				}
			}
		} else {
			if len(desc) > 0 && !strings.HasSuffix(desc, "\n") {
				desc += "\n"
			}
			desc += "\n## Response " + code + "\n"
			if resp.Description != nil && *resp.Description != "" {
				desc += "\n" + *resp.Description + "\n"
			}
		}
	}

	tmpl, err := url.PathUnescape(uriTemplate.String())
	if err != nil {
		// Unescape didn't work, just fall back to the original template.
		tmpl = uriTemplate.String()
	}

	// Try to add a group: if there's more than 1 tag, we'll just pick the
	// first one as a best guess
	group := ""
	if len(op.Tags) > 0 {
		group = op.Tags[0]
	}

	dep := ""
	if op.Deprecated {
		dep = "do not use"
	}

	return cli.Operation{
		Name:          name,
		Group:         group,
		Aliases:       aliases,
		Short:         op.Summary,
		Long:          desc,
		Method:        method,
		URITemplate:   tmpl,
		PathParams:    pathParams,
		QueryParams:   queryParams,
		HeaderParams:  headerParams,
		BodyMediaType: mediaType,
		Examples:      examples,
		Hidden:        hidden,
		Deprecated:    dep,
	}
}

// getBasePath returns the basePath to which the operation paths need to be appended (if any)
// It assumes the open-api description has been validated before: the casts should always succeed
// if the description adheres to the openapi spec schema.
func getBasePath(location *url.URL, servers openapi3.Servers) (string, error) {
	prefix := fmt.Sprintf("%s://%s", location.Scheme, location.Host)

	for _, s := range servers {
		// Interprete all operation paths as relative to the provided location
		if strings.HasPrefix(s.URL, "/") {
			return s.URL, nil
		}

		// localhost special casing?

		// Create a list with all possible parametrised server names
		endpoints := []string{s.URL}
		for k, v := range s.Variables {
			key := fmt.Sprintf("{%s}", k)
			if len(v.Enum) == 0 {
				for i := range endpoints {
					endpoints[i] = strings.ReplaceAll(
						endpoints[i],
						key,
						v.Default,
					)
				}
			} else {
				nEndpoints := make([]string, len(v.Enum)*len(endpoints))
				for j := range v.Enum {
					val := v.Enum[j]
					for i := range endpoints {
						nEndpoints[i+j*len(endpoints)] = strings.ReplaceAll(
							endpoints[i],
							key,
							val,
						)
					}
				}
				endpoints = nEndpoints
			}
		}

		for i := range endpoints {
			if strings.HasPrefix(endpoints[i], prefix) {
				base, err := url.Parse(endpoints[i])
				if err != nil {
					return "", err
				}
				return strings.TrimSuffix(base.Path, "/"), nil
			}
		}
	}
	return "", nil
}

func loadOpenAPI3(cfg Resolver, cmd *cobra.Command, location *url.URL, resp *http.Response) (cli.API, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return cli.API{}, err
	}

	swagger, err := loader.LoadFromDataWithPath(data, location)
	if err != nil {
		return cli.API{}, err
	}
	// spew.Dump(swagger)

	// See if this server has any base path prefix we need to account for.
	basePath, err := getBasePath(location, swagger.Servers)
	if err != nil {
		return cli.API{}, err
	}

	operations := []cli.Operation{}
	for uri, path := range swagger.Paths {
		if override := extBool(path.ExtensionProps, ExtIgnore); override {
			continue
		}

		resolved, err := cfg.Resolve(basePath + uri)
		if err != nil {
			return cli.API{}, err
		}

		for method, operation := range path.Operations() {
			if override := extBool(operation.ExtensionProps, ExtIgnore); override {
				continue
			}

			operations = append(operations, openapiOperation(cmd, method, resolved, path, operation))
		}
	}

	authSchemes := []cli.APIAuth{}
	for _, v := range swagger.Components.SecuritySchemes {
		if v != nil && v.Value != nil {
			scheme := v.Value

			switch scheme.Type {
			case "apiKey":
				// TODO: api key auth
			case "http":
				if scheme.Scheme == "basic" {
					authSchemes = append(authSchemes, cli.APIAuth{
						Name: "http-basic",
						Params: map[string]string{
							"username": "",
							"password": "",
						},
					})
				}
				// TODO: bearer
			case "oauth2":
				flows := scheme.Flows
				if flows != nil {
					if flows.ClientCredentials != nil {
						cc := flows.ClientCredentials
						authSchemes = append(authSchemes, cli.APIAuth{
							Name: "oauth-client-credentials",
							Params: map[string]string{
								"client_id":     "",
								"client_secret": "",
								"token_url":     cc.TokenURL,
								// TODO: scopes
							},
						})
					}

					if flows.AuthorizationCode != nil {
						ac := flows.AuthorizationCode
						authSchemes = append(authSchemes, cli.APIAuth{
							Name: "oauth-authorization-code",
							Params: map[string]string{
								"client_id":     "",
								"authorize_url": ac.AuthorizationURL,
								"token_url":     ac.TokenURL,
								// TODO: scopes
							},
						})
					}
				}
			}
		}
	}

	short := ""
	long := ""
	if swagger.Info != nil {
		short = swagger.Info.Title
		long = swagger.Info.Description

		if override := extStr(swagger.Info.ExtensionProps, ExtName); override != "" {
			short = override
		}

		if override := extStr(swagger.Info.ExtensionProps, ExtDescription); override != "" {
			long = override
		}
	}

	api := cli.API{
		Short:      short,
		Long:       long,
		Operations: operations,
		Auth:       authSchemes,
	}

	if swagger.Extensions["x-cli-config"] != nil {
		loadAutoConfig(&api, swagger)
	}

	return api, nil
}

func loadAutoConfig(api *cli.API, swagger *openapi3.T) {
	var config *autoConfig

	if cfg, ok := swagger.Extensions["x-cli-config"].(json.RawMessage); ok {
		if err := json.Unmarshal(cfg, &config); err != nil {
			fmt.Fprintf(os.Stderr, "Unable to unmarshal x-cli-config: %v", err)
			return
		}
	} else {
		fmt.Fprintf(os.Stderr, "Unknown type for x-cli-config")
	}

	authName := config.Security
	params := map[string]string{}

	if swagger.Components.SecuritySchemes != nil {
		ref := swagger.Components.SecuritySchemes[config.Security]
		if ref != nil && ref.Value != nil {
			// The config references a named security scheme.
			scheme := ref.Value

			// Conver it to the Restish security type and set some default params.
			switch scheme.Type {
			case "http":
				if scheme.Scheme == "basic" {
					authName = "http-basic"
				}
			case "oauth2":
				if scheme.Flows != nil {
					if scheme.Flows.AuthorizationCode != nil {
						// Prefer auth code if multiple auth types are available.
						authName = "oauth-authorization-code"
						ac := scheme.Flows.AuthorizationCode
						params["client_id"] = ""
						params["authorize_url"] = ac.AuthorizationURL
						params["token_url"] = ac.TokenURL
					} else if scheme.Flows.ClientCredentials != nil {
						authName = "oauth-client-credentials"
						cc := scheme.Flows.ClientCredentials
						params["client_id"] = ""
						params["client_secret"] = ""
						params["token_url"] = cc.TokenURL
					}
				}
			}
		}
	}

	// Params can override the values above if needed.
	for k, v := range config.Params {
		params[k] = v
	}

	api.AutoConfig = cli.AutoConfig{
		Headers: config.Headers,
		Prompt:  config.Prompt,
		Auth: cli.APIAuth{
			Name:   authName,
			Params: params,
		},
	}
}

type loader struct {
	location *url.URL
	base     *url.URL
}

func (l *loader) Resolve(relURI string) (*url.URL, error) {
	parsed, err := url.Parse(relURI)
	if err != nil {
		return nil, err
	}

	return l.base.ResolveReference(parsed), nil
}

func (l *loader) LocationHints() []string {
	return []string{"/openapi.json", "/openapi.yaml"}
}

func (l *loader) Detect(resp *http.Response) bool {
	// Try to detect via header first
	if strings.HasPrefix(resp.Header.Get("content-type"), "application/vnd.oai.openapi") {
		return true
	}

	// Fall back to looking for the OpenAPI version in the body.
	body, _ := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()

	if reOpenAPI3.Match(body) {
		return true
	}

	return false
}

func (l *loader) Load(entrypoint, spec url.URL, resp *http.Response) (cli.API, error) {
	l.location = &spec
	l.base = &entrypoint
	return loadOpenAPI3(l, cli.Root, &spec, resp)
}

// New creates a new OpenAPI loader.
func New() cli.Loader {
	return &loader{}
}
