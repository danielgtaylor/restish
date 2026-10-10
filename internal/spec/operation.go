package spec

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	base "github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
)

// OperationXCLI holds x-cli-* extension values extracted from an operation.
type OperationXCLI struct {
	Ignore      bool
	Hidden      bool
	Name        string
	Description string
	Aliases     []string
}

// ParamXCLI holds x-cli-* extension values extracted from a parameter.
type ParamXCLI struct {
	Ignore bool
	Hidden bool
	// Name overrides the kebab-case parameter name used for the flag.
	Name string
	// Description overrides the OpenAPI parameter description.
	Description string
}

// Param is a single request parameter (path, query, header, or cookie).
type Param struct {
	Name              string
	In                string // "path", "query", "header", "cookie"
	Desc              string
	Schema            string
	JSONSchema        map[string]any
	JSONSchemaDialect string
	Required          bool
	Type              string
	ItemType          string
	Default           string
	DefaultValues     []string
	HasDefault        bool
	Style             string
	Explode           *bool
	AllowReserved     bool
	ContentMediaType  string
	Enum              []string
	ObjectProperties  []ParamObjectProperty
	XCLI              ParamXCLI
}

// ParamObjectProperty is a simple scalar property from an object-capable
// parameter schema, used by generated commands to expose child flags.
type ParamObjectProperty struct {
	Name string
	Type string
	Desc string
	Enum []string
}

// OperationBodyHelp is a compact request/response body example extracted from
// OpenAPI schemas for generated command help.
type OperationBodyHelp struct {
	MediaType         string
	Schema            string
	JSONSchema        map[string]any
	JSONSchemaDialect string
	Example           string
	RawBinary         bool
}

// OperationResponseHelp is a compact response shape for generated command help.
// Codes may contain several status codes when they share the same schema.
type OperationResponseHelp struct {
	Codes       []string
	MediaType   string
	Headers     []string
	Schema      string
	Example     string
	NoBody      bool
	Description string
}

// OperationHelp stores pre-rendered help snippets for generated commands.
type OperationHelp struct {
	Request   *OperationBodyHelp
	Requests  []OperationBodyHelp
	Responses []OperationResponseHelp
	Examples  []string
}

// CredentialRequirement is a loader-neutral authentication requirement.
type CredentialRequirement struct {
	ID       string   // stable local ID, usually the OpenAPI security scheme name
	Ref      string   // canonical source ref or URI
	Kind     string   // oauth2, api-key, http-basic, http-bearer, openid, mtls, unknown
	Needs    []string // scopes, roles, or other requirement values
	In       string   // api-key location, e.g. query, header, cookie
	Name     string   // api-key parameter/header/cookie name
	Source   string   // loader identity, e.g. openapi
	External bool     // true when the source used a URI-style reference
	// Undeclared is true when OpenAPI security references an ID that is absent
	// from components.securitySchemes.
	Undeclared bool
	// Deprecated is true when the source security scheme is marked deprecated.
	Deprecated bool
}

// CredentialAlternative is one AND-set within OpenAPI's OR-list security model.
type CredentialAlternative []CredentialRequirement

// Operation is a single HTTP operation extracted from a spec, expressed in
// format-neutral terms so command generators need not import libopenapi.
type Operation struct {
	// ID is the operationId, or a kebab-case fallback when absent.
	ID     string
	Method string
	// Path is the full path template after applying any server base prefix.
	Path string
	// OperationServer is an absolute server URL from an operation/path-level
	// OpenAPI server whose origin differs from the configured API base URL.
	OperationServer string
	Summary         string
	Description     string
	Deprecated      bool
	Tags            []string
	Parameters      []Param
	// HasBody is true when the operation has a requestBody.
	HasBody bool
	// BodyRequired is true when requestBody.required is true.
	BodyRequired bool
	// NoAuth is true when the operation explicitly declares security: [].
	NoAuth bool
	// OptionalAuth is true when an operation allows anonymous calls alongside
	// one or more credential alternatives.
	OptionalAuth bool
	// CredentialAlternatives is the OR-list of AND credential requirements
	// derived from the effective OpenAPI security requirement.
	CredentialAlternatives []CredentialAlternative
	// MCPIgnore is true when x-mcp-ignore is set.
	MCPIgnore bool
	// RequestMediaType is the deterministic preferred content type from
	// requestBody.content, if the operation accepts a body.
	RequestMediaType string
	// ResponseMediaType is the deterministic preferred Accept value from
	// successful response content, if the operation declares one.
	ResponseMediaType string
	// ResponseMediaTypes are all declared successful response content types in
	// deterministic response-code and spec declaration order.
	ResponseMediaTypes []string
	// RequestMultipartContentTypes maps multipart/form-data property names to
	// per-part Content-Type values from the OpenAPI encoding object.
	RequestMultipartContentTypes map[string]string
	XCLI                         OperationXCLI
	Help                         OperationHelp
}

// OperationSet is the extracted operation list plus API-level metadata needed
// to present the generated command group without reparsing the raw spec.
type OperationSet struct {
	Info           APIInfo
	Operations     []Operation
	Warnings       []string
	XCLIExtensions XCLIExtensionReport
}

// OperationSet returns all operations with top-level API metadata. The result
// is keyed by base URL, operation base, and resolved OpenAPI server variable
// values via opts.
func (s *APISpec) OperationSet(opts OperationOptions) (OperationSet, error) {
	ops, warnings, err := s.operationResult(opts)
	if err != nil {
		return OperationSet{}, err
	}
	info, err := s.Info()
	if err != nil {
		return OperationSet{}, err
	}
	xcliExtensions, err := s.XCLIExtensionReport()
	if err != nil {
		return OperationSet{}, err
	}
	return OperationSet{Info: info, Operations: ops, Warnings: warnings, XCLIExtensions: xcliExtensions}, nil
}

// Operations returns all HTTP operations extracted from the spec's V3 model,
// with paths prefixed by the base path derived from the spec's servers[] list
// and opts.BaseURL. When opts.OperationBase is non-empty the spec servers are
// ignored and Operation.Path contains only the bare path template; the CLI
// resolves operationBase against baseURL at request time. opts.ServerVariables
// supplies values for OpenAPI server URL variables.
//
// Results are memoized per (baseURL, operationBase, server variables) tuple.
func (s *APISpec) Operations(opts OperationOptions) ([]Operation, error) {
	ops, _, err := s.operationResult(opts)
	return ops, err
}

func (s *APISpec) operationResult(opts OperationOptions) ([]Operation, []string, error) {
	key := operationOptionsKey(opts)

	s.opsCacheMu.Lock()
	if s.opsCache != nil {
		if e, ok := s.opsCache[key]; ok {
			s.opsCacheMu.Unlock()
			emitOperationWarnings(opts.Warnf, e.warnings)
			return e.ops, e.warnings, e.err
		}
	}
	s.opsCacheMu.Unlock()

	ops, warnings, err := s.buildOperations(opts)

	s.opsCacheMu.Lock()
	if s.opsCache == nil {
		s.opsCache = make(map[opsKey]opsEntry)
	}
	s.opsCache[key] = opsEntry{ops: ops, warnings: warnings, err: err}
	s.opsCacheMu.Unlock()

	emitOperationWarnings(opts.Warnf, warnings)
	return ops, warnings, err
}

// buildOperations performs the actual extraction without caching.
func (s *APISpec) buildOperations(opts OperationOptions) ([]Operation, []string, error) {
	model, err := s.V3Model()
	if err != nil || model == nil {
		return nil, nil, err
	}
	if model.Model.Paths == nil {
		return []Operation{}, nil, nil
	}

	// Use a non-nil empty slice so callers can distinguish "no paths in spec"
	// (nil return) from "paths exist but all were filtered" (empty non-nil slice).
	ops := make([]Operation, 0)
	warnings := make([]string, 0)
	warningsSeen := map[string]bool{}
	for rawPath, pathItem := range model.Model.Paths.PathItems.FromOldest() {
		if pathItem == nil {
			continue
		}
		if PathItemExtBool(pathItem, "x-cli-ignore") {
			continue
		}
		pathHidden := PathItemExtBool(pathItem, "x-cli-hidden")

		pathParams := pathItem.Parameters
		for _, mo := range PathItemMethods(pathItem) {
			if mo.Op == nil {
				continue
			}
			servers := model.Model.Servers
			scope := serverScopeDocument
			if len(pathItem.Servers) > 0 {
				servers = pathItem.Servers
				scope = serverScopePath
			}
			if len(mo.Op.Servers) > 0 {
				servers = mo.Op.Servers
				scope = serverScopeOperation
			}
			basePath, operationServer, serverWarnings, err := deriveBasePath(opts.BaseURL, opts.OperationBase, servers, opts.ServerVariables, scope)
			if err != nil {
				return nil, nil, fmt.Errorf("derive base path for %s %s: %w", mo.Method, rawPath, err)
			}
			for _, warning := range serverWarnings {
				if !warningsSeen[warning] {
					warningsSeen[warning] = true
					warnings = append(warnings, warning)
				}
			}
			fullPath := joinOperationPath(basePath, rawPath)
			op := extractOperation(mo.Method, fullPath, pathParams, mo.Op, model.Model.Security, securitySchemes(model.Model.Components), openAPIJSONSchemaDialect(model.Model))
			op.OperationServer = operationServer
			if op.XCLI.Ignore {
				continue
			}
			if pathHidden && !op.XCLI.Hidden {
				op.XCLI.Hidden = true
			}
			ops = append(ops, op)
		}
	}
	return ops, warnings, nil
}

func emitOperationWarnings(warnf func(format string, args ...any), warnings []string) {
	if warnf == nil {
		return
	}
	for _, warning := range warnings {
		warnf("%s", warning)
	}
}

// extractOperation converts a single libopenapi operation to the neutral form.
func extractOperation(method, path string, pathParams []*v3.Parameter, op *v3.Operation, docSecurity []*base.SecurityRequirement, schemes map[string]*v3.SecurityScheme, schemaDialect string) Operation {
	effectiveSecurity := docSecurity
	if op.Security != nil {
		effectiveSecurity = op.Security
	}
	o := Operation{
		ID:                 op.OperationId,
		Method:             method,
		Path:               path,
		Summary:            op.Summary,
		Description:        op.Description,
		Deprecated:         op.Deprecated != nil && *op.Deprecated,
		Tags:               op.Tags,
		HasBody:            op.RequestBody != nil,
		BodyRequired:       op.RequestBody != nil && op.RequestBody.Required != nil && *op.RequestBody.Required,
		NoAuth:             effectiveSecurity != nil && len(effectiveSecurity) == 0,
		MCPIgnore:          OpExtBool(op, "x-mcp-ignore"),
		RequestMediaType:   preferredRequestMediaType(op),
		ResponseMediaType:  preferredOperationResponseMediaType(op),
		ResponseMediaTypes: operationResponseMediaTypes(op),
		XCLI: OperationXCLI{
			Ignore:      OpExtBool(op, "x-cli-ignore"),
			Hidden:      OpExtBool(op, "x-cli-hidden"),
			Name:        OpExtString(op, "x-cli-name"),
			Description: OpExtString(op, "x-cli-description"),
			Aliases:     OpExtStrings(op, "x-cli-aliases"),
		},
	}
	o.Help = buildOperationHelp(op, o.RequestMediaType, schemaDialect)
	o.RequestMultipartContentTypes = buildRequestMultipartContentTypes(op, o.RequestMediaType)
	o.OptionalAuth, o.CredentialAlternatives = credentialAlternatives(effectiveSecurity, schemes)

	merged := MergeParameters(pathParams, op.Parameters)
	for _, p := range merged {
		if p == nil {
			continue
		}
		if isIgnoredOpenAPIHeaderParameter(p) {
			continue
		}
		var enum, defaultValues []string
		var objectProperties []ParamObjectProperty
		var jsonSchema map[string]any
		var paramType, itemType, defaultValue, schemaHelp, jsonSchemaDialect string
		var hasDefault bool
		if p.Schema != nil {
			if schema := p.Schema.Schema(); schema != nil {
				schemaHelp, paramType, itemType, defaultValue, defaultValues, hasDefault, enum, objectProperties, jsonSchema, jsonSchemaDialect = parameterSchemaDetails(schema, schemaDialect)
			}
		} else if schema := preferredParameterContentSchema(p); schema != nil {
			schemaHelp, paramType, itemType, defaultValue, defaultValues, hasDefault, enum, objectProperties, jsonSchema, jsonSchemaDialect = parameterSchemaDetails(schema, schemaDialect)
		}
		o.Parameters = append(o.Parameters, Param{
			Name:              p.Name,
			In:                p.In,
			Desc:              p.Description,
			Schema:            schemaHelp,
			JSONSchema:        jsonSchema,
			JSONSchemaDialect: jsonSchemaDialect,
			Required:          p.Required != nil && *p.Required,
			Type:              paramType,
			ItemType:          itemType,
			Default:           defaultValue,
			DefaultValues:     defaultValues,
			HasDefault:        hasDefault,
			Style:             p.Style,
			Explode:           p.Explode,
			AllowReserved:     p.AllowReserved,
			ContentMediaType:  preferredParameterContentMediaType(p),
			Enum:              enum,
			ObjectProperties:  objectProperties,
			XCLI: ParamXCLI{
				Ignore:      ParamExtBool(p, "x-cli-ignore"),
				Hidden:      ParamExtBool(p, "x-cli-hidden"),
				Name:        ParamExtString(p, "x-cli-name"),
				Description: ParamExtString(p, "x-cli-description"),
			},
		})
	}
	return o
}

func securitySchemes(components *v3.Components) map[string]*v3.SecurityScheme {
	if components == nil || components.SecuritySchemes == nil {
		return nil
	}
	out := map[string]*v3.SecurityScheme{}
	for name, scheme := range components.SecuritySchemes.FromOldest() {
		out[name] = scheme
	}
	return out
}

func credentialAlternatives(requirements []*base.SecurityRequirement, schemes map[string]*v3.SecurityScheme) (bool, []CredentialAlternative) {
	if len(requirements) == 0 {
		return false, nil
	}
	var optional bool
	alternatives := make([]CredentialAlternative, 0, len(requirements))
	for _, requirement := range requirements {
		if requirement == nil || requirement.Requirements == nil {
			optional = true
			continue
		}
		requirementCount := orderedmap.Len(requirement.Requirements)
		if requirementCount == 0 {
			optional = true
			continue
		}
		alternative := make(CredentialAlternative, 0, requirementCount)
		for id, needs := range requirement.Requirements.FromOldest() {
			scheme := schemes[id]
			requirement := CredentialRequirement{
				ID:         id,
				Ref:        credentialRequirementRef(id, scheme),
				Kind:       credentialRequirementKind(scheme),
				Needs:      append([]string(nil), needs...),
				In:         credentialRequirementIn(scheme),
				Name:       credentialRequirementName(scheme),
				Source:     "openapi",
				External:   credentialRequirementExternal(id, scheme),
				Undeclared: scheme == nil && !isAbsoluteURI(id),
				Deprecated: scheme != nil && scheme.Deprecated,
			}
			sort.Strings(requirement.Needs)
			alternative = append(alternative, requirement)
		}
		if len(alternative) == 0 {
			optional = true
			continue
		}
		alternatives = append(alternatives, alternative)
	}
	return optional, alternatives
}

func credentialRequirementIn(scheme *v3.SecurityScheme) string {
	if scheme == nil || scheme.Type != "apiKey" {
		return ""
	}
	return scheme.In
}

func credentialRequirementName(scheme *v3.SecurityScheme) string {
	if scheme == nil || scheme.Type != "apiKey" {
		return ""
	}
	return scheme.Name
}

func credentialRequirementRef(id string, scheme *v3.SecurityScheme) string {
	if scheme != nil && scheme.Reference != "" {
		return scheme.Reference
	}
	if isAbsoluteURI(id) {
		return id
	}
	return "#/components/securitySchemes/" + jsonPointerEscape(id)
}

func credentialRequirementKind(scheme *v3.SecurityScheme) string {
	if scheme == nil {
		return "unknown"
	}
	switch scheme.Type {
	case "apiKey":
		return "api-key"
	case "oauth2":
		if securitySchemeDPoPRequired(scheme) {
			return "oauth2-dpop"
		}
		return "oauth2"
	case "openIdConnect":
		if securitySchemeDPoPRequired(scheme) {
			return "oauth2-dpop"
		}
		return "openid"
	case "mutualTLS":
		return "mtls"
	case "http":
		switch strings.ToLower(scheme.Scheme) {
		case "basic":
			return "http-basic"
		case "bearer":
			return "http-bearer"
		case "dpop":
			return "http-dpop"
		default:
			return "http"
		}
	default:
		return "unknown"
	}
}

func credentialRequirementExternal(id string, scheme *v3.SecurityScheme) bool {
	return isAbsoluteURI(id) || (scheme != nil && isAbsoluteURI(scheme.Reference))
}

func isAbsoluteURI(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.IsAbs()
}

func jsonPointerEscape(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func isIgnoredOpenAPIHeaderParameter(p *v3.Parameter) bool {
	if p == nil || !strings.EqualFold(p.In, "header") {
		return false
	}
	return strings.EqualFold(p.Name, "Accept") ||
		strings.EqualFold(p.Name, "Content-Type") ||
		strings.EqualFold(p.Name, "Authorization")
}

type serverScope int

const (
	serverScopeDocument serverScope = iota
	serverScopePath
	serverScopeOperation
)

// deriveBasePath computes the path prefix to prepend to an operation path.
// When operationBase is set, no prefix is needed because baseURL+operationBase
// is resolved at call time. Document-level servers only contribute a path
// prefix; path- and operation-level servers may also select a different origin.
func deriveBasePath(baseURL, operationBase string, servers []*v3.Server, serverVariables map[string]string, scope serverScope) (string, string, []string, error) {
	if operationBase != "" || len(servers) == 0 {
		return "", "", nil, nil
	}
	warnings, err := validateConfiguredServerVariables(servers, serverVariables)
	if err != nil {
		return "", "", nil, err
	}

	location, err := url.Parse(baseURL)
	if err != nil {
		return "", "", nil, err
	}
	resolutionBase := serverResolutionBase(location)
	var firstCrossOrigin string

	if scope == serverScopeDocument {
		basePath := deriveDocumentServerBasePath(location, resolutionBase, servers, serverVariables)
		return basePath, "", warnings, nil
	}

	for _, server := range servers {
		if server == nil {
			continue
		}
		// Resolve each server variable once, using explicit local config values
		// where present and OpenAPI defaults otherwise. Enum values are
		// intentionally not expanded; remote specs must not be able to create a
		// Cartesian-product allocation during command generation.
		endpoint := resolveServerURLVariables(server, serverVariables)
		parsed, err := url.Parse(endpoint)
		if err != nil {
			continue
		}
		resolved := resolutionBase.ResolveReference(parsed)
		if resolved.Scheme != location.Scheme || resolved.Host != location.Host {
			if firstCrossOrigin == "" && resolved.IsAbs() && resolved.Host != "" && (resolved.Scheme == "http" || resolved.Scheme == "https") {
				firstCrossOrigin = strings.TrimRight(resolved.String(), "/")
			}
			continue
		}
		return strings.TrimSuffix(relativeServerBasePath(location.Path, resolved.Path), "/"), "", warnings, nil
	}

	if firstCrossOrigin != "" {
		return "", firstCrossOrigin, warnings, nil
	}

	// No matching server found — fall back to the configured API base URL.
	return "", "", warnings, nil
}

func deriveDocumentServerBasePath(location, resolutionBase *url.URL, servers []*v3.Server, serverVariables map[string]string) string {
	var firstResolved *url.URL
	for _, server := range servers {
		if server == nil {
			continue
		}
		endpoint := resolveServerURLVariables(server, serverVariables)
		parsed, err := url.Parse(endpoint)
		if err != nil {
			continue
		}
		resolved := resolutionBase.ResolveReference(parsed)
		if firstResolved == nil {
			copy := *resolved
			firstResolved = &copy
		}
		if resolved.Scheme == location.Scheme && resolved.Host == location.Host {
			return strings.TrimSuffix(relativeServerBasePath(location.Path, resolved.Path), "/")
		}
	}
	if firstResolved == nil {
		return ""
	}
	return strings.TrimSuffix(relativeServerBasePath(location.Path, firstResolved.Path), "/")
}

func serverResolutionBase(location *url.URL) *url.URL {
	base := *location
	base.RawQuery = ""
	base.Fragment = ""
	if base.Path == "" {
		base.Path = "/"
	}
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	return &base
}

func relativeServerBasePath(basePath, resolvedPath string) string {
	basePath = strings.TrimRight(basePath, "/")
	if basePath == "" {
		return resolvedPath
	}
	if resolvedPath == basePath {
		return ""
	}
	if strings.HasPrefix(resolvedPath, basePath+"/") {
		return strings.TrimPrefix(resolvedPath, basePath)
	}
	return relativeEscapedServerBasePath(basePath, resolvedPath)
}

func relativeEscapedServerBasePath(basePath, resolvedPath string) string {
	baseSegments := pathSegments(basePath)
	resolvedSegments := pathSegments(resolvedPath)
	common := 0
	for common < len(baseSegments) && common < len(resolvedSegments) && baseSegments[common] == resolvedSegments[common] {
		common++
	}
	var parts []string
	for i := common; i < len(baseSegments); i++ {
		parts = append(parts, "..")
	}
	parts = append(parts, resolvedSegments[common:]...)
	if len(parts) == 0 {
		return ""
	}
	return "/" + strings.Join(parts, "/")
}

func pathSegments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// resolveServerURLVariables returns one concrete URL string for a server by
// substituting explicit local values or OpenAPI defaults. Enum values may be
// useful for validation/help, but are never eagerly expanded.
func resolveServerURLVariables(server *v3.Server, values map[string]string) string {
	if server == nil {
		return ""
	}
	endpoint := server.URL
	if server.Variables == nil {
		return endpoint
	}
	for key, value := range server.Variables.FromOldest() {
		if value == nil {
			continue
		}
		placeholder := fmt.Sprintf("{%s}", key)
		replacement := value.Default
		if configured, ok := values[key]; ok {
			replacement = configured
		}
		endpoint = strings.ReplaceAll(endpoint, placeholder, replacement)
	}
	return endpoint
}

func validateConfiguredServerVariables(servers []*v3.Server, values map[string]string) ([]string, error) {
	var warnings []string
	for configuredName, configuredValue := range values {
		declared := false
		allowedByAnyEnum := false
		hasEnum := false
		hasDeclaredVariables := false
		for _, server := range servers {
			if server == nil || server.Variables == nil {
				continue
			}
			for range server.Variables.FromOldest() {
				hasDeclaredVariables = true
				break
			}
			variable := server.Variables.GetOrZero(configuredName)
			if variable == nil {
				continue
			}
			declared = true
			if len(variable.Enum) == 0 {
				allowedByAnyEnum = true
				continue
			}
			hasEnum = true
			for _, enumValue := range variable.Enum {
				if configuredValue == enumValue {
					allowedByAnyEnum = true
					break
				}
			}
		}
		if !declared {
			if !hasDeclaredVariables {
				continue
			}
			return warnings, fmt.Errorf("server variable %q is configured but not declared by the OpenAPI servers", configuredName)
		}
		if hasEnum && !allowedByAnyEnum {
			warnings = append(warnings, fmt.Sprintf("server variable %q value %q is outside the OpenAPI enum; using configured value anyway", configuredName, configuredValue))
		}
	}
	return warnings, nil
}

// MergeParameters merges path-level and operation-level parameters.
// Operation-level parameters override path-level ones with the same (in, name).
// Header names are matched case-insensitively to match HTTP semantics.
func MergeParameters(pathLevel, operationLevel []*v3.Parameter) []*v3.Parameter {
	if len(pathLevel) == 0 && len(operationLevel) == 0 {
		return nil
	}
	merged := make([]*v3.Parameter, 0, len(pathLevel)+len(operationLevel))
	indexes := make(map[string]int, len(pathLevel)+len(operationLevel))
	add := func(p *v3.Parameter) {
		if p == nil {
			return
		}
		key := parameterMergeKey(p)
		if idx, ok := indexes[key]; ok {
			merged[idx] = p
			return
		}
		indexes[key] = len(merged)
		merged = append(merged, p)
	}
	for _, p := range pathLevel {
		add(p)
	}
	for _, p := range operationLevel {
		add(p)
	}
	return merged
}

func parameterMergeKey(p *v3.Parameter) string {
	name := p.Name
	if strings.EqualFold(p.In, "header") {
		name = strings.ToLower(name)
	}
	return p.In + "\x00" + name
}

func schemaType(types []string) string {
	for _, t := range types {
		if t != "null" {
			return t
		}
	}
	if len(types) > 0 {
		return types[0]
	}
	return "string"
}

func parameterSchemaDetails(schema *base.Schema, defaultDialect string) (schemaHelp, paramType, itemType, defaultValue string, defaultValues []string, hasDefault bool, enum []string, objectProperties []ParamObjectProperty, jsonSchema map[string]any, jsonSchemaDialect string) {
	if schema == nil {
		return "", "", "", "", nil, false, nil, nil, nil, ""
	}
	schemaHelp = buildParameterSchemaHelp(schema)
	paramType = schemaType(schema.Type)
	objectProperties = scalarObjectProperties(schema)
	if len(objectProperties) > 0 && len(schema.Type) == 0 {
		if scalarType := composedScalarType(schema); scalarType != "" {
			paramType = scalarType
		} else {
			paramType = "object"
		}
	}
	if paramType == "array" && schema.Items != nil && schema.Items.IsA() && schema.Items.A != nil {
		if itemSchema := schema.Items.A.Schema(); itemSchema != nil {
			itemType = schemaType(itemSchema.Type)
		}
	}
	if schema.Default != nil {
		var decoded any
		if err := schema.Default.Decode(&decoded); err == nil {
			if paramType == "array" {
				defaultValues = scalarStrings(decoded)
			}
			defaultValue = scalarString(decoded)
			hasDefault = true
		}
	}
	enum = schemaEnumStrings(schema)
	if enumIncompatibleWithType(paramType, enum) {
		paramType = "string"
		schemaHelp = buildParameterSchemaHelpWithType(schema, paramType)
	}
	jsonSchema = schemaJSONMap(schema)
	jsonSchemaDialect = schemaJSONDialect(schema, defaultDialect)
	return schemaHelp, paramType, itemType, defaultValue, defaultValues, hasDefault, enum, objectProperties, jsonSchema, jsonSchemaDialect
}

func buildParameterSchemaHelpWithType(schema *base.Schema, typ string) string {
	if schema == nil || typ == "" {
		return buildParameterSchemaHelp(schema)
	}
	normalized := *schema
	normalized.Type = []string{typ}
	return buildParameterSchemaHelp(&normalized)
}

func schemaJSONMap(schema *base.Schema) map[string]any {
	if schema == nil {
		return nil
	}
	data, err := json.Marshal(schema)
	if err != nil || len(data) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}

func schemaJSONDialect(schema *base.Schema, defaultDialect string) string {
	if schema != nil && strings.TrimSpace(schema.SchemaTypeRef) != "" {
		return strings.TrimSpace(schema.SchemaTypeRef)
	}
	return strings.TrimSpace(defaultDialect)
}

func openAPIJSONSchemaDialect(doc v3.Document) string {
	if strings.TrimSpace(doc.JsonSchemaDialect) != "" {
		return strings.TrimSpace(doc.JsonSchemaDialect)
	}
	version := strings.TrimSpace(doc.Version)
	switch {
	case strings.HasPrefix(version, "3.2."):
		return "https://spec.openapis.org/oas/3.2/dialect/2025-09-17"
	case strings.HasPrefix(version, "3.1."):
		return "https://spec.openapis.org/oas/3.1/dialect/base"
	default:
		return ""
	}
}

func scalarObjectProperties(schema *base.Schema) []ParamObjectProperty {
	seen := map[string]bool{}
	var out []ParamObjectProperty
	var visit func(*base.Schema)
	visit = func(s *base.Schema) {
		if s == nil {
			return
		}
		if schemaType(s.Type) == "object" && s.Properties != nil {
			for name, proxy := range s.Properties.FromOldest() {
				if seen[name] {
					continue
				}
				prop := schemaFromProxy(proxy)
				propType := explicitSimpleScalarType(prop)
				if propType == "" {
					continue
				}
				seen[name] = true
				out = append(out, ParamObjectProperty{
					Name: name,
					Type: propType,
					Desc: prop.Description,
					Enum: schemaEnumStrings(prop),
				})
			}
		}
		for _, proxy := range s.AnyOf {
			visit(schemaFromProxy(proxy))
		}
		for _, proxy := range s.OneOf {
			visit(schemaFromProxy(proxy))
		}
		for _, proxy := range s.AllOf {
			visit(schemaFromProxy(proxy))
		}
	}
	visit(schema)
	return out
}

func explicitSimpleScalarType(schema *base.Schema) string {
	if schema == nil {
		return ""
	}
	if len(schema.Type) == 0 {
		return composedScalarType(schema)
	}
	typ := schemaType(schema.Type)
	switch typ {
	case "string", "integer", "number", "boolean":
		return typ
	default:
		return ""
	}
}

func composedScalarType(schema *base.Schema) string {
	if schema == nil {
		return ""
	}
	for _, proxies := range [][]*base.SchemaProxy{schema.AnyOf, schema.OneOf, schema.AllOf} {
		for _, proxy := range proxies {
			branch := schemaFromProxy(proxy)
			if branch == nil {
				continue
			}
			if typ := explicitSimpleScalarType(branch); typ != "" {
				return typ
			}
		}
	}
	return ""
}

func schemaEnumStrings(schema *base.Schema) []string {
	if schema == nil {
		return nil
	}
	var enum []string
	for _, node := range schema.Enum {
		if node != nil {
			enum = append(enum, node.Value)
		}
	}
	return enum
}

func enumIncompatibleWithType(typ string, enum []string) bool {
	if typ == "" || typ == "string" || len(enum) == 0 {
		return false
	}
	for _, value := range enum {
		if validateScalarType(typ, value) != nil {
			return true
		}
	}
	return false
}

func validateScalarType(typ, value string) error {
	switch typ {
	case "integer":
		_, err := strconv.ParseInt(value, 10, 64)
		return err
	case "number":
		_, err := strconv.ParseFloat(value, 64)
		return err
	case "boolean":
		_, err := strconv.ParseBool(value)
		return err
	default:
		return nil
	}
}

func scalarString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprint(v)
	case int64:
		return fmt.Sprint(v)
	case float64:
		return fmt.Sprint(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, scalarString(item))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v)
	}
}

func scalarStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, scalarString(item))
	}
	return values
}

func preferredRequestMediaType(op *v3.Operation) string {
	if op == nil || op.RequestBody == nil || op.RequestBody.Content == nil {
		return ""
	}
	var names []string
	for name := range op.RequestBody.Content.FromOldest() {
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	for _, name := range names {
		mt := strings.ToLower(strings.TrimSpace(strings.Split(name, ";")[0]))
		if mt == "application/json" || strings.HasSuffix(mt, "+json") {
			return name
		}
	}
	sort.Strings(names)
	return names[0]
}

func preferredParameterContentMediaType(p *v3.Parameter) string {
	if p == nil || p.Content == nil {
		return ""
	}
	var names []string
	for name := range p.Content.FromOldest() {
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	for _, name := range names {
		mt := strings.ToLower(strings.TrimSpace(strings.Split(name, ";")[0]))
		if mt == "application/json" || strings.HasSuffix(mt, "+json") {
			return name
		}
	}
	sort.Strings(names)
	return names[0]
}

func preferredParameterContentSchema(p *v3.Parameter) *base.Schema {
	name := preferredParameterContentMediaType(p)
	if name == "" || p == nil || p.Content == nil {
		return nil
	}
	return mediaTypeSchema(p.Content.GetOrZero(name))
}

func preferredOperationResponseMediaType(op *v3.Operation) string {
	if op == nil || op.Responses == nil {
		return ""
	}
	for _, code := range responseCodes(op) {
		if !isSuccessResponseCode(code) {
			continue
		}
		name, _ := preferredResponseMediaType(responseForCode(op, code))
		if name != "" {
			return name
		}
	}
	return ""
}

func operationResponseMediaTypes(op *v3.Operation) []string {
	if op == nil || op.Responses == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, code := range responseCodes(op) {
		if !isSuccessResponseCode(code) {
			continue
		}
		resp := responseForCode(op, code)
		if resp == nil || resp.Content == nil {
			continue
		}
		for mediaType := range resp.Content.FromOldest() {
			key := strings.ToLower(strings.TrimSpace(mediaType))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, mediaType)
		}
	}
	return out
}

func joinOperationPath(basePath, opPath string) string {
	if basePath == "" || basePath == "/" {
		return opPath
	}
	return strings.TrimRight(basePath, "/") + opPath
}
