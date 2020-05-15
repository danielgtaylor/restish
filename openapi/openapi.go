package openapi

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/restish/cli"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gosimple/slug"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

// Resolver is able to resolve relative URIs against a base.
type Resolver interface {
	Resolve(uri string) (*url.URL, error)
}

func getRequestInfo(op *openapi3.Operation) (string, string, []interface{}) {
	mts := make(map[string][]interface{})

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		for mt, item := range op.RequestBody.Value.Content {
			var schema string
			var examples []interface{}

			if item.Schema != nil && item.Schema.Value != nil {
				// Let's make this a bit more concise. Since it has special JSON
				// marshalling functions, we do a dance to get it into plain JSON before
				// converting to YAML.
				data, err := json.Marshal(item.Schema.Value)
				if err != nil {
					continue
				}

				var unmarshalled interface{}
				json.Unmarshal(data, &unmarshalled)

				data, err = yaml.Marshal(unmarshalled)
				if err == nil {
					schema = string(data)
				}
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

			mts[mt] = []interface{}{schema, examples}
		}
	}

	// Prefer JSON.
	for mt, item := range mts {
		if strings.Contains(mt, "json") {
			return mt, item[0].(string), item[1].([]interface{})
		}
	}

	// Fall back to YAML next.
	for mt, item := range mts {
		if strings.Contains(mt, "yaml") {
			return mt, item[0].(string), item[1].([]interface{})
		}
	}

	// Last resort: return the first we find!
	for mt, item := range mts {
		return mt, item[0].(string), item[1].([]interface{})
	}

	return "", "", nil
}

func openapiOperation(cmd *cobra.Command, method string, uriTemplate *url.URL, op *openapi3.Operation) *cli.Operation {
	pathParams := []*cli.Param{}
	queryParams := []*cli.Param{}
	headerParams := []*cli.Param{}

	for _, p := range op.Parameters {
		if p.Value != nil {
			var def interface{}
			var example interface{}

			typ := "string"
			if p.Value.Schema != nil && p.Value.Schema.Value != nil {
				typ = p.Value.Schema.Value.Type

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

			param := &cli.Param{
				Type:        typ,
				Name:        p.Value.Name,
				Description: p.Value.Description,
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

	mediaType := ""
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		mediaType, _, _ = getRequestInfo(op)
	}

	return &cli.Operation{
		Name:          slug.Make(op.OperationID),
		Short:         op.Summary,
		Long:          op.Description,
		Method:        method,
		URITemplate:   uriTemplate.String(),
		PathParams:    pathParams,
		QueryParams:   queryParams,
		HeaderParams:  headerParams,
		BodyMediaType: mediaType,
	}
}

func loadOpenAPI3(cfg Resolver, cmd *cobra.Command, location *url.URL, resp *http.Response) []*cli.Operation {
	loader := openapi3.NewSwaggerLoader()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	swagger, err := loader.LoadSwaggerFromDataWithPath(data, location)
	if err != nil {
		panic(err)
	}
	// spew.Dump(swagger)

	// See if this server has any base path prefix we need to account for.
	// TODO: handle variables in the server path?
	basePath := ""
	prefix := location.Scheme + "://" + location.Host
	for _, s := range swagger.Servers {
		if strings.HasPrefix(s.URL, prefix) {
			base, err := url.Parse(s.URL)
			if err != nil {
				panic(err)
			}
			basePath = base.Path
		}
	}

	operations := []*cli.Operation{}
	for uri, path := range swagger.Paths {
		resolved, err := cfg.Resolve(basePath + uri)
		if err != nil {
			panic(err)
		}
		if path.Get != nil {
			operations = append(operations, openapiOperation(cmd, http.MethodGet, resolved, path.Get))
		}
		if path.Post != nil {
			operations = append(operations, openapiOperation(cmd, http.MethodPost, resolved, path.Post))
		}
		if path.Put != nil {
			operations = append(operations, openapiOperation(cmd, http.MethodPut, resolved, path.Put))
		}
		if path.Patch != nil {
			operations = append(operations, openapiOperation(cmd, http.MethodPatch, resolved, path.Patch))
		}
		if path.Delete != nil {
			operations = append(operations, openapiOperation(cmd, http.MethodDelete, resolved, path.Delete))
		}
	}

	return operations
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

	if strings.Contains(string(body), "openapi: 3") {
		return true
	}

	return false
}

func (l *loader) Load(entrypoint, spec url.URL, resp *http.Response) []*cli.Operation {
	l.location = &spec
	l.base = &entrypoint
	return loadOpenAPI3(l, cli.Root, &spec, resp)
}

// New creates a new OpenAPI loader.
func New() cli.Loader {
	return &loader{}
}
