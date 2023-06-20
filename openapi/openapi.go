package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/danielgtaylor/casing"
	"github.com/danielgtaylor/restish/cli"
	"github.com/danielgtaylor/shorthand/v2"
	"github.com/gosimple/slug"
	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/resolver"
	"github.com/pb33f/libopenapi/utils"
	"github.com/spf13/cobra"
	"golang.org/x/exp/maps"
)

// reOpenAPI3 is a regex used to detect OpenAPI files from their contents.
var reOpenAPI3 = regexp.MustCompile(`['"]?openapi['"]?\s*:\s*['"]?3`)

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

	// Custom auto-configuration for CLIs
	ExtCLIConfig = "x-cli-config"
)

type autoConfig struct {
	Security string                       `json:"security"`
	Headers  map[string]string            `json:"headers,omitempty"`
	Prompt   map[string]cli.AutoConfigVar `json:"prompt,omitempty"`
	Params   map[string]string            `json:"params,omitempty"`
}

// Resolver is able to resolve relative URIs against a base.
type Resolver interface {
	GetBase() *url.URL
	Resolve(uri string) (*url.URL, error)
}

// getExt returns an extension converted to some type with the given default
// returned if the extension is not found or cannot be cast to that type.
func getExt[T any](v map[string]any, key string, def T) T {
	if v != nil {
		if i := v[key]; i != nil {
			if t, ok := i.(T); ok {
				return t
			}
		}
	}

	return def
}

// getExtSlice returns an extension converted to some type with the given
// default returned if the extension is not found or cannot be converted to
// a slice of the correct type.
func getExtSlice[T any](v map[string]any, key string, def []T) []T {
	if v != nil {
		if i := v[key]; i != nil {
			if s, ok := i.([]any); ok && len(s) > 0 {
				n := make([]T, len(s))
				for i := 0; i < len(s); i++ {
					if si, ok := s[i].(T); ok {
						n[i] = si
					}
				}
				return n
			}
		}
	}

	return def
}

// getBasePath returns the basePath to which the operation paths need to be appended (if any)
// It assumes the open-api description has been validated before: the casts should always succeed
// if the description adheres to the openapi spec schema.
func getBasePath(location *url.URL, servers []*v3.Server) (string, error) {
	prefix := fmt.Sprintf("%s://%s", location.Scheme, location.Host)

	for _, s := range servers {
		// Interpret all operation paths as relative to the provided location
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
	return location.Path, nil
}

func getRequestInfo(op *v3.Operation) (string, *base.Schema, []interface{}) {
	mts := make(map[string][]interface{})

	if op.RequestBody != nil {
		for mt, item := range op.RequestBody.Content {
			var examples []any

			if item.Example != nil {
				examples = append(examples, item.Example)
			}
			if len(item.Examples) > 0 {
				keys := maps.Keys(item.Examples)
				sort.Strings(keys)
				for _, key := range keys {
					ex := item.Examples[key]
					if ex.Value != nil {
						examples = append(examples, ex.Value)
					}
				}
			}

			var schema *base.Schema
			if item.Schema != nil && item.Schema.Schema() != nil {
				schema = item.Schema.Schema()
			}

			if schema != nil && len(examples) == 0 {
				examples = append(examples, GenExample(schema, modeWrite))
			}

			mts[mt] = []any{schema, examples}
		}
	}

	// Prefer JSON, fall back to YAML next, otherwise return the first one.
	for _, short := range []string{"json", "yaml", "*"} {
		for mt, item := range mts {
			if strings.Contains(mt, short) || short == "*" {
				return mt, item[0].(*base.Schema), item[1].([]interface{})
			}
		}
	}

	return "", nil, nil
}

func openapiOperation(cmd *cobra.Command, method string, uriTemplate *url.URL, path *v3.PathItem, op *v3.Operation) cli.Operation {
	var pathParams, queryParams, headerParams []*cli.Param

	// Combine path and operation parameters, with operation params having
	// precedence when there are name conflicts.
	combinedParams := []*v3.Parameter{}
	seen := map[string]bool{}
	for _, p := range op.Parameters {
		combinedParams = append(combinedParams, p)
		seen[p.Name] = true
	}
	for _, p := range path.Parameters {
		if !seen[p.Name] {
			combinedParams = append(combinedParams, p)
		}
	}

	for _, p := range combinedParams {
		if getExt(p.Extensions, ExtIgnore, false) {
			continue
		}

		var def interface{}
		var example interface{}

		typ := "string"
		if p.Schema != nil && p.Schema.Schema() != nil {
			s := p.Schema.Schema()
			if len(s.Type) > 0 {
				// TODO: support params of multiple types?
				typ = s.Type[0]
			}

			if typ == "array" {
				if s.Items != nil && s.Items.IsA() {
					items := s.Items.A.Schema()
					if len(items.Type) > 0 {
						typ += "[" + items.Type[0] + "]"
					}
				}
			}

			def = s.Default
			example = s.Example
		}

		if p.Example != nil {
			example = p.Example
		}

		style := cli.StyleSimple
		if p.Style == "form" {
			style = cli.StyleForm
		}

		displayName := getExt(p.Extensions, ExtName, "")
		description := getExt(p.Extensions, ExtDescription, p.Description)

		param := &cli.Param{
			Type:        typ,
			Name:        p.Name,
			DisplayName: displayName,
			Description: description,
			Style:       style,
			Default:     def,
			Example:     example,
		}

		if p.Explode != nil {
			param.Explode = *p.Explode
		}

		switch p.In {
		case "path":
			if pathParams == nil {
				pathParams = []*cli.Param{}
			}
			pathParams = append(pathParams, param)
		case "query":
			if queryParams == nil {
				queryParams = []*cli.Param{}
			}
			queryParams = append(queryParams, param)
		case "header":
			if headerParams == nil {
				headerParams = []*cli.Param{}
			}
			headerParams = append(headerParams, param)
		}
	}

	aliases := getExtSlice(op.Extensions, ExtAliases, []string{})

	name := casing.Kebab(op.OperationId)
	if override := getExt(op.Extensions, ExtName, ""); override != "" {
		name = override
	} else if oldName := slug.Make(op.OperationId); oldName != name {
		// For backward-compatibility, add the old naming scheme as an alias
		// if it is different. See https://github.com/danielgtaylor/restish/issues/29
		// for additional context; we prefer kebab casing for readability.
		aliases = append(aliases, oldName)
	}

	desc := getExt(op.Extensions, ExtDescription, op.Description)
	hidden := getExt(op.Extensions, ExtHidden, false)

	mediaType := ""
	var examples []string
	if op.RequestBody != nil {
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
					}

					// Since we use `<` and `>` we need to disable HTML escaping.
					buffer := &bytes.Buffer{}
					encoder := json.NewEncoder(buffer)
					encoder.SetIndent("", "  ")
					encoder.SetEscapeHTML(false)
					encoder.Encode(ex)
					b := buffer.Bytes()

					if !wroteHeader {
						desc += "\n## Input Example\n"
						wroteHeader = true
					}

					desc += "\n```json\n" + strings.Trim(string(b), "\n") + "\n```\n"
					continue
				}

				if ex.(string) == "<input.json" {
					continue
				}

				if !wroteHeader {
					desc += "\n## Input Example\n"
					wroteHeader = true
				}

				desc += "\n```\n" + strings.Trim(ex.(string), "\n") + "\n```\n"
			}
		}

		if reqSchema != nil {
			desc += "\n## Request Schema (" + mt + ")\n\n```schema\n" + renderSchema(reqSchema, "", modeWrite) + "\n```\n"
		}
	}

	codes := []string{}
	respMap := map[string]*v3.Response{}
	for k, v := range op.Responses.Codes {
		codes = append(codes, k)
		respMap[k] = v
	}
	if op.Responses.Default != nil {
		codes = append(codes, "default")
		respMap["default"] = op.Responses.Default
	}
	sort.Strings(codes)

	type schemaEntry struct {
		code   string
		ct     string
		schema *base.Schema
	}
	schemaMap := map[[32]byte][]schemaEntry{}
	for _, code := range codes {
		var resp *v3.Response
		if respMap[code] == nil {
			continue
		}

		resp = respMap[code]

		hash := [32]byte{}
		if len(resp.Content) > 0 {
			for ct, typeInfo := range resp.Content {
				var s *base.Schema
				hash = [32]byte{}
				if typeInfo.Schema != nil {
					s = typeInfo.Schema.Schema()
					hash = s.GoLow().Hash()
				}
				if schemaMap[hash] == nil {
					schemaMap[hash] = []schemaEntry{}
				}
				schemaMap[hash] = append(schemaMap[hash], schemaEntry{
					code:   code,
					ct:     ct,
					schema: s,
				})
			}
		} else {
			if schemaMap[hash] == nil {
				schemaMap[hash] = []schemaEntry{}
			}
			schemaMap[hash] = append(schemaMap[hash], schemaEntry{
				code: code,
			})
		}
	}

	schemaKeys := maps.Keys(schemaMap)
	sort.Slice(schemaKeys, func(i, j int) bool {
		return schemaMap[schemaKeys[i]][0].code < schemaMap[schemaKeys[j]][0].code
	})

	for _, s := range schemaKeys {
		entries := schemaMap[s]

		var resp *v3.Response
		if len(entries) == 1 && respMap[entries[0].code] != nil {
			resp = respMap[entries[0].code]
		}

		codeNums := []string{}
		for _, v := range entries {
			codeNums = append(codeNums, v.code)
		}

		hasSchema := s != [32]byte{}

		ct := ""
		if hasSchema {
			ct = " (" + entries[0].ct + ")"
		}

		if resp != nil {
			desc += "\n## Response " + entries[0].code + ct + "\n"
			respDesc := getExt(resp.Extensions, ExtDescription, resp.Description)
			if respDesc != "" {
				desc += "\n" + respDesc + "\n"
			} else if !hasSchema {
				desc += "\nResponse has no body\n"
			}
		} else {
			desc += "\n## Responses " + strings.Join(codeNums, "/") + ct + "\n"
			if !hasSchema {
				desc += "\nResponse has no body\n"
			}
		}

		headers := respMap[entries[0].code].Headers
		if len(headers) > 0 {
			keys := maps.Keys(headers)
			sort.Strings(keys)
			desc += "\nHeaders: " + strings.Join(keys, ", ") + "\n"
		}

		if hasSchema {
			desc += "\n```schema\n" + renderSchema(entries[0].schema, "", modeRead) + "\n```\n"
		}
	}

	tmpl := uriTemplate.String()
	if s, err := url.PathUnescape(uriTemplate.String()); err == nil {
		tmpl = s
	}

	// Try to add a group: if there's more than 1 tag, we'll just pick the
	// first one as a best guess
	group := ""
	if len(op.Tags) > 0 {
		group = op.Tags[0]
	}

	dep := ""
	if op.Deprecated != nil && *op.Deprecated {
		dep = "do not use"
	}

	return cli.Operation{
		Name:          name,
		Group:         group,
		Aliases:       aliases,
		Short:         op.Summary,
		Long:          strings.Trim(desc, "\n") + "\n",
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

func loadOpenAPI3(cfg Resolver, cmd *cobra.Command, location *url.URL, resp *http.Response) (cli.API, error) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return cli.API{}, err
	}

	doc, err := libopenapi.NewDocument(data)
	if err != nil {
		return cli.API{}, err
	}

	var model v3.Document
	switch doc.GetSpecInfo().SpecType {
	case utils.OpenApi3:
		result, errs := doc.BuildV3Model()
		// Allow circular reference errors
		for _, err := range errs {
			if refErr, ok := err.(*resolver.ResolvingError); ok {
				if refErr.CircularReference == nil {
					return cli.API{}, fmt.Errorf("errors %v", errs)
				}
			} else {
				return cli.API{}, fmt.Errorf("errors %v", errs)
			}
		}
		model = result.Model
	default:
		return cli.API{}, fmt.Errorf("unsupported OpenAPI document")
	}

	// See if this server has any base path prefix we need to account for.
	basePath, err := getBasePath(cfg.GetBase(), model.Servers)
	if err != nil {
		return cli.API{}, err
	}

	operations := []cli.Operation{}
	if model.Paths != nil {
		for uri, path := range model.Paths.PathItems {
			if getExt(path.Extensions, ExtIgnore, false) {
				continue
			}

			resolved, err := cfg.Resolve(strings.TrimSuffix(basePath, "/") + uri)
			if err != nil {
				return cli.API{}, err
			}

			for method, operation := range path.GetOperations() {
				if operation == nil || getExt(operation.Extensions, ExtIgnore, false) {
					continue
				}

				operations = append(operations, openapiOperation(cmd, strings.ToUpper(method), resolved, path, operation))
			}
		}
	}

	authSchemes := []cli.APIAuth{}
	if model.Components != nil && model.Components.SecuritySchemes != nil {
		keys := maps.Keys(model.Components.SecuritySchemes)
		sort.Strings(keys)

		for _, key := range keys {
			scheme := model.Components.SecuritySchemes[key]
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
								"token_url":     cc.TokenUrl,
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
								"authorize_url": ac.AuthorizationUrl,
								"token_url":     ac.TokenUrl,
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
	if model.Info != nil {
		short = getExt(model.Info.Extensions, ExtName, model.Info.Title)
		long = getExt(model.Info.Extensions, ExtDescription, model.Info.Description)
	}

	api := cli.API{
		Short:      short,
		Long:       long,
		Operations: operations,
	}

	if len(authSchemes) > 0 {
		api.Auth = authSchemes
	}

	loadAutoConfig(&api, &model)

	return api, nil
}

func loadAutoConfig(api *cli.API, model *v3.Document) {
	var config *autoConfig

	cfg := model.Extensions[ExtCLIConfig]
	if cfg == nil {
		return
	}

	low := model.GoLow()
	for k, v := range low.Extensions {
		if k.Value == ExtCLIConfig {
			if err := v.ValueNode.Decode(&config); err != nil {
				fmt.Fprintf(os.Stderr, "Unable to unmarshal x-cli-config: %v", err)
				return
			}
			break
		}
	}

	authName := config.Security
	params := map[string]string{}

	if model.Components.SecuritySchemes != nil {
		scheme := model.Components.SecuritySchemes[config.Security]

		// Convert it to the Restish security type and set some default params.
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
					params["authorize_url"] = ac.AuthorizationUrl
					params["token_url"] = ac.TokenUrl
				} else if scheme.Flows.ClientCredentials != nil {
					authName = "oauth-client-credentials"
					cc := scheme.Flows.ClientCredentials
					params["client_id"] = ""
					params["client_secret"] = ""
					params["token_url"] = cc.TokenUrl
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

func (l *loader) GetBase() *url.URL {
	return l.base
}

func (l *loader) Resolve(relURI string) (*url.URL, error) {
	parsed, err := url.Parse(relURI)
	if err != nil {
		return nil, err
	}

	return l.base.ResolveReference(parsed), nil
}

func (l *loader) LocationHints() []string {
	return []string{"/openapi.json", "/openapi.yaml", "openapi.json", "openapi.yaml"}
}

func (l *loader) Detect(resp *http.Response) bool {
	// Try to detect via header first
	if strings.HasPrefix(resp.Header.Get("content-type"), "application/vnd.oai.openapi") {
		return true
	}

	// Fall back to looking for the OpenAPI version in the body.
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()

	return reOpenAPI3.Match(body)
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
