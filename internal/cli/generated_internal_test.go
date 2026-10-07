package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel"
	v2high "github.com/pb33f/libopenapi/datamodel/high/v2"
	v3high "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/index"
	"github.com/spf13/cobra"

	"github.com/rest-sh/restish/v2/config"
	"github.com/rest-sh/restish/v2/internal/spec"
)

type failingDocument struct {
	err error
}

func TestEffectiveOpenAPIExtensionAliases(t *testing.T) {
	c := New()
	c.cfg = &config.Config{OpenAPIExtensionAliases: map[string]string{
		config.XCLIPositionExtension: "x-global-position",
	}}
	global := c.effectiveOpenAPIExtensionAliases(nil)
	if global[config.XCLIPositionExtension] != "x-global-position" {
		t.Fatalf("global alias = %q", global[config.XCLIPositionExtension])
	}
	apiCfg := &config.APIConfig{OpenAPIExtensionAliases: map[string]string{
		config.XCLIPositionExtension: "x-api-position",
	}}
	got := c.effectiveOpenAPIExtensionAliases(apiCfg)
	if got[config.XCLIPositionExtension] != "x-api-position" {
		t.Fatalf("alias = %q, want per-API override", got[config.XCLIPositionExtension])
	}
	apiCfg.OpenAPIExtensionAliases[config.XCLIPositionExtension] = config.XCLIPositionExtension
	got = c.effectiveOpenAPIExtensionAliases(apiCfg)
	if got[config.XCLIPositionExtension] != config.XCLIPositionExtension {
		t.Fatalf("alias = %q, want canonical API override", got[config.XCLIPositionExtension])
	}
	merged := mergeDefaultConfigForEmbedding(c.cfg, &config.Config{OpenAPIExtensionAliases: apiCfg.OpenAPIExtensionAliases})
	if merged.OpenAPIExtensionAliases[config.XCLIPositionExtension] != config.XCLIPositionExtension {
		t.Fatalf("merged alias = %q, want user config override", merged.OpenAPIExtensionAliases[config.XCLIPositionExtension])
	}
}

func TestPositionGeneratedRequiredParams(t *testing.T) {
	first, second, third := 1, 2, 3
	tests := []struct {
		name    string
		params  []*paramInfo
		want    []string
		wantErr string
	}{
		{
			name: "fills unpositioned slots in default order",
			params: []*paramInfo{
				{name: "id"},
				{name: "account"},
				{name: "scope", position: &first},
			},
			want: []string{"scope", "id", "account"},
		},
		{
			name: "duplicate",
			params: []*paramInfo{
				{name: "id", position: &second},
				{name: "scope", position: &second},
			},
			wantErr: "both use x-cli-position 2",
		},
		{
			name:    "out of range",
			params:  []*paramInfo{{name: "scope", position: &third}},
			wantErr: "expected a value from 1 to 1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := positionGeneratedRequiredParams(tc.params)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			names := make([]string, len(got))
			for i, param := range got {
				names[i] = param.name
			}
			if !reflect.DeepEqual(names, tc.want) {
				t.Fatalf("order = %#v, want %#v", names, tc.want)
			}
		})
	}
}

func TestBuildOperationCommandRejectsUnusablePositions(t *testing.T) {
	position := 1
	tests := []struct {
		name    string
		param   spec.Param
		creds   []spec.CredentialAlternative
		wantErr string
	}{
		{
			name:    "optional parameter",
			param:   spec.Param{Name: "scope", In: "query", XCLI: spec.ParamXCLI{Position: &position}},
			wantErr: "is not required",
		},
		{
			name:    "authentication parameter",
			param:   spec.Param{Name: "api_key", In: "query", Required: true, XCLI: spec.ParamXCLI{Position: &position}},
			creds:   []spec.CredentialAlternative{{{Kind: "api-key", In: "query", Name: "api_key"}}},
			wantErr: "supplied by authentication",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New().buildOperationCommand("api", "restish api", spec.Operation{
				ID:                     "getItem",
				Method:                 "GET",
				Path:                   "/items",
				Parameters:             []spec.Param{tc.param},
				CredentialAlternatives: tc.creds,
			}, "")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func (d failingDocument) GetVersion() string                                 { return "3.1.0" }
func (d failingDocument) GetRolodex() *index.Rolodex                         { return nil }
func (d failingDocument) GetSpecInfo() *datamodel.SpecInfo                   { return nil }
func (d failingDocument) SetConfiguration(*datamodel.DocumentConfiguration)  {}
func (d failingDocument) GetConfiguration() *datamodel.DocumentConfiguration { return nil }
func (d failingDocument) BuildV2Model() (*libopenapi.DocumentModel[v2high.Swagger], error) {
	return nil, errors.New("unsupported")
}
func (d failingDocument) BuildV3Model() (*libopenapi.DocumentModel[v3high.Document], error) {
	return nil, d.err
}
func (d failingDocument) RenderAndReload() ([]byte, libopenapi.Document, *libopenapi.DocumentModel[v3high.Document], error) {
	return nil, nil, nil, nil
}
func (d failingDocument) Render() ([]byte, error)    { return nil, nil }
func (d failingDocument) Serialize() ([]byte, error) { return nil, nil }
func (d failingDocument) Release()                   {}

func TestBuildAPICommandWarnsOnModelFailure(t *testing.T) {
	c := New()
	var errOut strings.Builder
	c.Stderr = &errOut

	apiCmd := c.buildAPICommand("broken", &config.APIConfig{SpecURL: "https://api.example.com/openapi.json"}, &spec.APISpec{
		Document: failingDocument{err: errors.New("model boom")},
	})
	if apiCmd != nil {
		t.Fatal("expected nil API command when V3 model build fails")
	}
	if !strings.Contains(errOut.String(), "warning: skipping generated commands for API \"broken\"") {
		t.Fatalf("expected warning about skipped generated commands, got: %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "model boom") {
		t.Fatalf("expected underlying model error in warning, got: %q", errOut.String())
	}
}

func TestBuildOperationCommandDoesNotParseOptionalDefaultsAsFlagValues(t *testing.T) {
	c := New()
	cmd, err := c.buildOperationCommand("myapi", "", spec.Operation{
		ID:     "search",
		Method: "GET",
		Path:   "/search",
		Parameters: []spec.Param{
			{Name: "enabled", In: "query", Type: "boolean", Default: "definitely", HasDefault: true},
		},
	}, "")
	if err != nil {
		t.Fatalf("build operation command: %v", err)
	}
	flag := cmd.Flags().Lookup("enabled")
	if flag == nil {
		t.Fatal("expected --enabled flag")
	}
	if !strings.Contains(flag.Usage, "Default: definitely") {
		t.Fatalf("flag usage = %q, want documented server default", flag.Usage)
	}
}

func TestBuildOperationCommandDisambiguatesOperatorFlagNames(t *testing.T) {
	c := New()
	cmd, err := c.buildOperationCommand("myapi", "", spec.Operation{
		ID:     "search",
		Method: "GET",
		Path:   "/search",
		Parameters: []spec.Param{
			{Name: "StartTime", In: "query", Type: "string"},
			{Name: "StartTime<", In: "query", Type: "string"},
			{Name: "StartTime<=", In: "query", Type: "string"},
			{Name: "StartTime>", In: "query", Type: "string"},
			{Name: "StartTime>=", In: "query", Type: "string"},
			{Name: "StartTime=", In: "query", Type: "string"},
			{Name: "StartTime!=", In: "query", Type: "string"},
		},
	}, "")
	if err != nil {
		t.Fatalf("build operation command: %v", err)
	}
	for _, name := range []string{
		"start-time",
		"start-time-lt",
		"start-time-lte",
		"start-time-gt",
		"start-time-gte",
		"start-time-eq",
		"start-time-ne",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("expected generated flag --%s", name)
		}
	}
}

func TestBuildOperationCommandDisambiguatesNonOperatorFlagNames(t *testing.T) {
	c := New()
	var errOut strings.Builder
	c.Stderr = &errOut

	cmd, err := c.buildOperationCommand("myapi", "", spec.Operation{
		ID:     "search",
		Method: "GET",
		Path:   "/search",
		Parameters: []spec.Param{
			{Name: "start_time", In: "query", Type: "string"},
			{Name: "start-time", In: "query", Type: "string"},
			{Name: "foo-bar", In: "query", Type: "string"},
			{Name: "foo_bar", In: "query", Type: "string"},
		},
	}, "")
	if err != nil {
		t.Fatalf("build operation command: %v", err)
	}
	if cmd.Flags().Lookup("start-time") == nil {
		t.Fatal("expected generated flag --start-time")
	}
	if cmd.Flags().Lookup("start-dash-time") == nil {
		t.Fatal("expected disambiguated generated flag --start-dash-time")
	}
	if cmd.Flags().Lookup("foo-bar") == nil {
		t.Fatal("expected generated flag --foo-bar")
	}
	if cmd.Flags().Lookup("foo-underscore-bar") == nil {
		t.Fatal("expected disambiguated generated flag --foo-underscore-bar")
	}
	if cmd.Flags().Lookup("foo-bar-foo-underscore-bar") != nil {
		t.Fatal("did not expect duplicated-base generated flag --foo-bar-foo-underscore-bar")
	}
	if !strings.Contains(errOut.String(), "parameter flag collision") {
		t.Fatalf("expected fallback warning, got: %q", errOut.String())
	}
}

func TestBuildOperationCommandDisambiguatesReservedFlagNames(t *testing.T) {
	c := New()
	var errOut strings.Builder
	c.Stderr = &errOut

	cmd, err := c.buildOperationCommand("myapi", "", spec.Operation{
		ID:     "search",
		Method: "GET",
		Path:   "/search",
		Parameters: []spec.Param{
			{Name: "help", In: "query", Type: "string"},
			{Name: "rsh-output-format", In: "query", Type: "string"},
			{Name: "help-all", In: "header", Type: "string"},
			{Name: "rsh-generate-body", In: "cookie", Type: "string"},
		},
	}, "")
	if err != nil {
		t.Fatalf("build operation command: %v", err)
	}
	for _, name := range []string{
		"query-help",
		"query-rsh-output-format",
		"header-help-all",
		"cookie-rsh-generate-body",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("expected generated flag --%s", name)
		}
	}
	if cmd.Flags().Lookup("help") != nil {
		t.Fatal("did not expect generated --help flag to shadow Cobra help")
	}
	if cmd.Flags().Lookup("rsh-output-format") != nil {
		t.Fatal("did not expect generated --rsh-output-format flag to shadow global output")
	}
	if !strings.Contains(errOut.String(), "--query-help") ||
		!strings.Contains(errOut.String(), "--query-rsh-output-format") {
		t.Fatalf("expected reserved-name warnings, got: %q", errOut.String())
	}
}

func TestBuildOperationCommandDocumentsUndescribedPathArgument(t *testing.T) {
	c := New()
	cmd, err := c.buildOperationCommand("myapi", "", spec.Operation{
		ID:     "getPet",
		Method: "GET",
		Path:   "/pets/{petId}",
		Parameters: []spec.Param{
			{Name: "petId", In: "path", Type: "integer", Required: true},
		},
	}, "")
	if err != nil {
		t.Fatalf("build operation command: %v", err)
	}
	if !strings.Contains(cmd.Long, "pet-id") || !strings.Contains(cmd.Long, "path parameter") {
		t.Fatalf("Long help = %q, want path argument row", cmd.Long)
	}
}

func TestBuildOperationCommandFallbackNameUsesOperationBase(t *testing.T) {
	c := New()
	cmd, err := c.buildOperationCommand("myapi", "", spec.Operation{
		Method:     "GET",
		Path:       "/api/rest/foo/{id}",
		Parameters: []spec.Param{{Name: "id", In: "path", Type: "string", Required: true}},
	}, "/api/rest")
	if err != nil {
		t.Fatalf("build operation command: %v", err)
	}
	if cmd.Name() != "get-foo-id" {
		t.Fatalf("command name = %q, want get-foo-id", cmd.Name())
	}

	cmd, err = c.buildOperationCommand("myapi", "", spec.Operation{
		ID:     "getApiRestFoo",
		Method: "GET",
		Path:   "/api/rest/foo",
	}, "/api/rest")
	if err != nil {
		t.Fatalf("build operation command with operationId: %v", err)
	}
	if cmd.Name() != "get-api-rest-foo" {
		t.Fatalf("operationId command name = %q, want get-api-rest-foo", cmd.Name())
	}
}

func TestOperationNamePathOnlyStripsOperationBaseSegment(t *testing.T) {
	tests := []struct {
		path          string
		operationBase string
		want          string
	}{
		{path: "/api/rest/foo", operationBase: "/api/rest", want: "/foo"},
		{path: "/api/rest", operationBase: "/api/rest", want: "/"},
		{path: "/apiary/foo", operationBase: "/api", want: "/apiary/foo"},
		{path: "/api/foo", operationBase: "/", want: "/api/foo"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			if got := operationNamePath(tc.path, tc.operationBase); got != tc.want {
				t.Fatalf("operationNamePath(%q, %q) = %q, want %q", tc.path, tc.operationBase, got, tc.want)
			}
		})
	}
}

func TestGeneratedParamSatisfiedByAPIKeySecurity(t *testing.T) {
	p := &paramInfo{in: "query", name: "key"}
	if !generatedParamSatisfiedByAPIKeySecurity(p, []spec.CredentialAlternative{
		{{ID: "ApiKey", Kind: "api-key", In: "query", Name: "key"}},
	}) {
		t.Fatal("expected matching apiKey query security to satisfy duplicated query param")
	}
	if generatedParamSatisfiedByAPIKeySecurity(p, []spec.CredentialAlternative{
		{{ID: "ApiKey", Kind: "api-key", In: "query", Name: "key"}},
		{{ID: "Bearer", Kind: "http-bearer"}},
	}) {
		t.Fatal("did not expect one OR alternative to satisfy all duplicated query params")
	}
	if generatedParamSatisfiedByAPIKeySecurity(&paramInfo{in: "header", name: "X-Key"}, []spec.CredentialAlternative{
		{{ID: "ApiKey", Kind: "api-key", In: "header", Name: "X-Key"}},
	}) {
		t.Fatal("only duplicated query params should be omitted from generated arguments")
	}
}

func TestValidateGeneratedParamValuesRejectsInvalidScalars(t *testing.T) {
	tests := []struct {
		name  string
		param *paramInfo
		value string
		want  string
	}{
		{name: "integer", param: &paramInfo{typ: "integer"}, value: "abc", want: "argument pet-id must be integer"},
		{name: "number", param: &paramInfo{typ: "number"}, value: "abc", want: "argument pet-id must be number"},
		{name: "boolean", param: &paramInfo{typ: "boolean"}, value: "maybe", want: "argument pet-id must be boolean"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGeneratedParamValues(tc.param, []string{tc.value}, "argument pet-id")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateGeneratedFlagValueTokensRejectsFlagLikeMissingValue(t *testing.T) {
	c := New()
	opCmd, err := c.buildOperationCommand("myapi", "", spec.Operation{
		ID:     "search",
		Method: "GET",
		Path:   "/search",
		Parameters: []spec.Param{
			{Name: "federal", In: "query", Type: "string", Enum: []string{"true", "false"}},
			{Name: "optional", In: "query", Type: "boolean"},
		},
	}, "")
	if err != nil {
		t.Fatalf("build operation command: %v", err)
	}
	root := &cobra.Command{Use: "restish"}
	apiCmd := &cobra.Command{Use: "myapi"}
	apiCmd.AddCommand(opCmd)
	root.AddCommand(apiCmd)

	err = validateGeneratedFlagValueTokens(root, []string{"restish", "myapi", "search", "--federal", "--optional=false"})
	if err == nil || !strings.Contains(err.Error(), "--federal requires a value") {
		t.Fatalf("error = %v, want --federal missing value diagnostic", err)
	}
	if err := validateGeneratedFlagValueTokens(root, []string{"restish", "myapi", "search", "--federal=true", "--optional=false"}); err != nil {
		t.Fatalf("unexpected error for equals form: %v", err)
	}
}

func TestMatchAPIProfileDeduplicatesOperationBaseMatches(t *testing.T) {
	c := New()
	c.cfg = &config.Config{APIs: map[string]*config.APIConfig{
		"petmock": {
			BaseURL:       "https://api.example.com/",
			OperationBase: "/",
		},
	}}
	match, ok, err := c.matchAPIProfile("https://api.example.com/pets/1", "default")
	if err != nil {
		t.Fatalf("match API profile: %v", err)
	}
	if !ok || match.apiName != "petmock" {
		t.Fatalf("match = %#v, ok=%v; want petmock", match, ok)
	}
}

func TestToKebabCaseKnownAcronyms(t *testing.T) {
	tests := map[string]string{
		"listAPIs":      "list-apis",
		"getURL":        "get-url",
		"parseJSONBody": "parse-json-body",
		"OAuthToken":    "oauth-token",
		"listItems":     "list-items",
	}
	for input, want := range tests {
		if got := toKebabCase(input); got != want {
			t.Fatalf("toKebabCase(%q) = %q, want %q", input, got, want)
		}
	}
}

// ---- Name-collision handling -----------------------------------------------

func buildSpecWithPaths(t *testing.T, yamlBody string) *spec.APISpec {
	t.Helper()
	loaders := spec.DefaultLoaders()
	s, err := loaders[0].LoadWithOptions([]byte(yamlBody), spec.LoadOptions{})
	if err != nil {
		t.Fatalf("build spec: %v", err)
	}
	return s
}

func TestBuildAPICommand_DuplicateOperationIDDisambiguated(t *testing.T) {
	// Two operations that kebab to the same name: one GET and one POST.
	specBody := `
openapi: "3.1.0"
info:
  title: DupTest
  version: "1.0.0"
paths:
  /items:
    get:
      operationId: listItems
      summary: List items
      responses:
        "200": {}
    post:
      operationId: listItems
      summary: Also list items (duplicate operationId)
      responses:
        "200": {}
`
	s := buildSpecWithPaths(t, specBody)
	c := New()
	var errOut strings.Builder
	c.Stderr = &errOut

	apiCmd := c.buildAPICommand("myapi", &config.APIConfig{BaseURL: "https://api.example.com"}, s)
	if apiCmd == nil {
		t.Fatal("expected non-nil API command")
	}

	// One of the two commands should have been disambiguated.
	names := make(map[string]bool)
	for _, sub := range apiCmd.Commands() {
		names[sub.Name()] = true
	}
	if len(names) != 2 {
		t.Errorf("expected 2 distinct commands, got %d: %v", len(names), names)
	}
	// A warning should have been emitted.
	if !strings.Contains(errOut.String(), "collision") {
		t.Errorf("expected collision warning, got: %q", errOut.String())
	}
}

func TestBuildAPICommand_AliasCollisionWarnsAndDropsAlias(t *testing.T) {
	specBody := `
openapi: "3.1.0"
info:
  title: AliasCollision
  version: "1.0.0"
paths:
  /items:
    get:
      operationId: listItems
      x-cli-aliases: [shared]
      responses:
        "200": {}
  /widgets:
    get:
      operationId: listWidgets
      x-cli-aliases: [shared]
      responses:
        "200": {}
`
	s := buildSpecWithPaths(t, specBody)
	c := New()
	var errOut strings.Builder
	c.Stderr = &errOut

	apiCmd := c.buildAPICommand("myapi", &config.APIConfig{BaseURL: "https://api.example.com"}, s)
	if apiCmd == nil {
		t.Fatal("expected non-nil API command")
	}
	sharedCount := 0
	for _, sub := range apiCmd.Commands() {
		for _, alias := range sub.Aliases {
			if alias == "shared" {
				sharedCount++
			}
		}
	}
	if sharedCount != 1 {
		t.Fatalf("shared alias count = %d, want 1", sharedCount)
	}
	if !strings.Contains(errOut.String(), "alias collision") {
		t.Errorf("expected alias collision warning, got: %q", errOut.String())
	}
}

func TestBuildAPICommand_MissingPathParamErrors(t *testing.T) {
	// Path references {petId} but no parameter is declared.
	specBody := `
openapi: "3.1.0"
info:
  title: MissingParam
  version: "1.0.0"
paths:
  /pets/{petId}:
    get:
      operationId: getPet
      summary: Get a pet
      responses:
        "200": {}
`
	s := buildSpecWithPaths(t, specBody)
	c := New()
	var errOut strings.Builder
	c.Stderr = &errOut

	apiCmd := c.buildAPICommand("myapi", &config.APIConfig{BaseURL: "https://api.example.com"}, s)
	if apiCmd == nil {
		t.Fatal("expected non-nil API command group (even with skipped ops)")
	}
	// The operation should have been skipped with a warning.
	if !strings.Contains(errOut.String(), "warning: skipping") {
		t.Errorf("expected skipping warning for missing path param, got: %q", errOut.String())
	}
}

func TestBuildAPICommand_DuplicatePathTemplateParamErrors(t *testing.T) {
	specBody := `
openapi: "3.1.0"
info:
  title: DuplicatePathParam
  version: "1.0.0"
paths:
  /pets/{id}/aliases/{id}:
    get:
      operationId: getPetAlias
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200": {}
`
	s := buildSpecWithPaths(t, specBody)
	c := New()
	var errOut strings.Builder
	c.Stderr = &errOut

	apiCmd := c.buildAPICommand("myapi", &config.APIConfig{BaseURL: "https://api.example.com"}, s)
	if apiCmd == nil {
		t.Fatal("expected non-nil API command group")
	}
	if !strings.Contains(errOut.String(), "repeats path parameter") {
		t.Errorf("expected duplicate path parameter warning, got: %q", errOut.String())
	}
}

func TestBuildAPICommand_DuplicateHeaderParamsWithDifferentCaseDoNotSkipOperation(t *testing.T) {
	specBody := `
openapi: "3.1.0"
info:
  title: DuplicateHeaderParam
  version: "1.0.0"
paths:
  /items:
    parameters:
      - name: Idempotency-Key
        in: header
        description: Path-level idempotency key
        schema:
          type: string
    post:
      operationId: createItem
      parameters:
        - name: idempotency-key
          in: header
          description: Operation-level idempotency key
          schema:
            type: string
      responses:
        "200": {}
  /widgets:
    post:
      operationId: createWidget
      parameters:
        - name: Idempotency-Key
          in: header
          description: First idempotency key
          schema:
            type: string
        - name: idempotency-key
          in: header
          description: Second idempotency key
          schema:
            type: string
      responses:
        "200": {}
`
	s := buildSpecWithPaths(t, specBody)
	c := New()
	var errOut strings.Builder
	c.Stderr = &errOut

	apiCmd := c.buildAPICommand("myapi", &config.APIConfig{BaseURL: "https://api.example.com"}, s)
	if apiCmd == nil {
		t.Fatal("expected non-nil API command group")
	}
	cmd := childCommandForToken(apiCmd, "create-item")
	if cmd == nil {
		t.Fatalf("expected create-item command, got commands: %v; stderr: %q", apiCmd.Commands(), errOut.String())
	}
	if flag := cmd.Flags().Lookup("idempotency-key"); flag == nil {
		t.Fatalf("expected generated --idempotency-key flag, got stderr: %q", errOut.String())
	}
	widgetCmd := childCommandForToken(apiCmd, "create-widget")
	if widgetCmd == nil {
		t.Fatalf("expected create-widget command, got commands: %v; stderr: %q", apiCmd.Commands(), errOut.String())
	}
	if flag := widgetCmd.Flags().Lookup("idempotency-key"); flag == nil {
		t.Fatalf("expected generated --idempotency-key flag on duplicate operation-level params, got stderr: %q", errOut.String())
	}
	if strings.Contains(errOut.String(), "duplicate generated flag") || strings.Contains(errOut.String(), "warning: skipping") {
		t.Fatalf("expected operation to generate without duplicate flag skip, got stderr: %q", errOut.String())
	}
}

func TestBuildAPICommand_PathParamXCLIIgnoreErrors(t *testing.T) {
	specBody := `
openapi: "3.1.0"
info:
  title: IgnoredPathParam
  version: "1.0.0"
paths:
  /pets/{id}:
    get:
      operationId: getPet
      parameters:
        - name: id
          in: path
          required: true
          x-cli-ignore: true
          schema:
            type: string
      responses:
        "200": {}
`
	s := buildSpecWithPaths(t, specBody)
	c := New()
	var errOut strings.Builder
	c.Stderr = &errOut

	apiCmd := c.buildAPICommand("myapi", &config.APIConfig{BaseURL: "https://api.example.com"}, s)
	if apiCmd == nil {
		t.Fatal("expected non-nil API command group")
	}
	if !strings.Contains(errOut.String(), "references missing path parameter") {
		t.Errorf("expected ignored path parameter warning, got: %q", errOut.String())
	}
}

func TestGeneratedQueryParamSerialization(t *testing.T) {
	t.Run("form arrays and reserved values", func(t *testing.T) {
		p := &paramInfo{name: "tag", in: "query", typ: "array", style: "form", explode: boolPtr(true), allowReserved: true}
		parts, err := serializeGeneratedQueryParam(p, []string{"red/blue", "green"})
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		got := encodeGeneratedQuery(parts)
		if got != "tag=red/blue&tag=green" {
			t.Fatalf("query = %q, want repeated reserved values", got)
		}

		p.explode = boolPtr(false)
		parts, err = serializeGeneratedQueryParam(p, []string{"red", "blue"})
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		got = encodeGeneratedQuery(parts)
		if got != "tag=red,blue" {
			t.Fatalf("query = %q, want comma-joined reserved array", got)
		}
	})

	t.Run("space pipe and deep object", func(t *testing.T) {
		space := &paramInfo{name: "ids", in: "query", typ: "array", style: "spaceDelimited"}
		parts, err := serializeGeneratedQueryParam(space, []string{"a", "b"})
		if err != nil {
			t.Fatalf("space serialize: %v", err)
		}
		if got := encodeGeneratedQuery(parts); got != "ids=a%20b" {
			t.Fatalf("space query = %q, want ids=a%%20b", got)
		}

		pipe := &paramInfo{name: "ids", in: "query", typ: "array", style: "pipeDelimited"}
		parts, err = serializeGeneratedQueryParam(pipe, []string{"a", "b"})
		if err != nil {
			t.Fatalf("pipe serialize: %v", err)
		}
		if got := encodeGeneratedQuery(parts); got != "ids=a%7Cb" {
			t.Fatalf("pipe query = %q, want ids=a%%7Cb", got)
		}

		obj := &paramInfo{name: "filter", in: "query", typ: "object", style: "deepObject"}
		parts, err = serializeGeneratedQueryParam(obj, []string{"limit:", "10,", "q:", "cats"})
		if err != nil {
			t.Fatalf("object serialize: %v", err)
		}
		got := encodeGeneratedQuery(parts)
		if got != "filter%5Blimit%5D=10&filter%5Bq%5D=cats" {
			t.Fatalf("deep object query = %q", got)
		}
	})
}

func TestEncodeGeneratedQueryValueReservedBytes(t *testing.T) {
	tests := []struct {
		name          string
		value         string
		allowReserved bool
		want          string
	}{
		{name: "literal plus stays encoded", value: "a+b", allowReserved: true, want: "a%2Bb"},
		{name: "space uses percent encoding", value: "a b", allowReserved: true, want: "a%20b"},
		{name: "encoded plus input preserves percent", value: "a%2Bb", allowReserved: true, want: "a%252Bb"},
		{name: "slash allowed", value: "a/b", allowReserved: true, want: "a/b"},
		{name: "comma allowed", value: "a,b", allowReserved: true, want: "a,b"},
		{name: "reserved set allowed", value: ":/?#[]@!$&'()*+,;=", allowReserved: true, want: ":/?#[]@!$&'()*%2B,;="},
		{name: "slash encoded without allow reserved", value: "a/b", allowReserved: false, want: "a%2Fb"},
		{name: "comma encoded without allow reserved", value: "a,b", allowReserved: false, want: "a%2Cb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeGeneratedQueryValue(tc.value, tc.allowReserved); got != tc.want {
				t.Fatalf("encodeGeneratedQueryValue(%q, %v) = %q, want %q", tc.value, tc.allowReserved, got, tc.want)
			}
		})
	}
}

func TestGeneratedPathHeaderCookieAndContentParamSerialization(t *testing.T) {
	matrix := &paramInfo{name: "id", in: "path", typ: "array", style: "matrix", explode: boolPtr(true)}
	gotPath, err := serializeGeneratedPathParam(matrix, []string{"a", "b"})
	if err != nil {
		t.Fatalf("matrix serialize: %v", err)
	}
	if gotPath != ";id=a;id=b" {
		t.Fatalf("matrix path = %q, want ;id=a;id=b", gotPath)
	}

	label := &paramInfo{name: "filter", in: "path", typ: "object", style: "label", explode: boolPtr(true)}
	gotPath, err = serializeGeneratedPathParam(label, []string{"limit:", "10,", "q:", "cats"})
	if err != nil {
		t.Fatalf("label serialize: %v", err)
	}
	if gotPath != ".limit=10.q=cats" {
		t.Fatalf("label path = %q, want .limit=10.q=cats", gotPath)
	}

	labelArray := &paramInfo{name: "id", in: "path", typ: "array", style: "label", explode: boolPtr(false)}
	gotPath, err = serializeGeneratedPathParam(labelArray, []string{"a", "b"})
	if err != nil {
		t.Fatalf("label array serialize: %v", err)
	}
	if gotPath != ".a,b" {
		t.Fatalf("label array path = %q, want .a,b", gotPath)
	}

	labelObject := &paramInfo{name: "filter", in: "path", typ: "object", style: "label", explode: boolPtr(false)}
	gotPath, err = serializeGeneratedPathParam(labelObject, []string{"limit:", "10,", "q:", "cats"})
	if err != nil {
		t.Fatalf("label object serialize: %v", err)
	}
	if gotPath != ".limit,10,q,cats" {
		t.Fatalf("label object path = %q, want .limit,10,q,cats", gotPath)
	}

	header := &paramInfo{name: "X-IDs", in: "header", typ: "array"}
	gotHeaders, err := serializeGeneratedHeaderParam(header, []string{"a", "b"})
	if err != nil {
		t.Fatalf("header serialize: %v", err)
	}
	if !reflect.DeepEqual(gotHeaders, []string{"a,b"}) {
		t.Fatalf("headers = %#v, want a,b", gotHeaders)
	}

	cookie := &paramInfo{name: "session", in: "cookie", typ: "array", style: "form", explode: boolPtr(true)}
	gotCookies, err := serializeGeneratedCookieParam(cookie, []string{"a", "b"})
	if err != nil {
		t.Fatalf("cookie serialize: %v", err)
	}
	if !reflect.DeepEqual(gotCookies, []string{"session=a", "session=b"}) {
		t.Fatalf("cookies = %#v", gotCookies)
	}

	content := &paramInfo{name: "filter", in: "query", contentMediaType: "application/json"}
	parts, err := serializeGeneratedQueryParam(content, []string{"limit:", "10,", "q:", "cats"})
	if err != nil {
		t.Fatalf("content serialize: %v", err)
	}
	if got := encodeGeneratedQuery(parts); got != "filter=%7B%22limit%22%3A10%2C%22q%22%3A%22cats%22%7D" {
		t.Fatalf("content query = %q", got)
	}

	contentArray := &paramInfo{name: "ids", in: "query", typ: "array", contentMediaType: "application/json"}
	parts, err = serializeGeneratedQueryParam(contentArray, []string{"a", "b"})
	if err != nil {
		t.Fatalf("content array serialize: %v", err)
	}
	if got := encodeGeneratedQuery(parts); got != "ids=%5B%22a%22%2C%22b%22%5D" {
		t.Fatalf("content array query = %q", got)
	}

	parts, err = serializeGeneratedQueryParam(contentArray, []string{`["raw","json"]`})
	if err != nil {
		t.Fatalf("content array raw JSON serialize: %v", err)
	}
	if got := encodeGeneratedQuery(parts); got != "ids=%5B%22raw%22%2C%22json%22%5D" {
		t.Fatalf("content array raw JSON query = %q", got)
	}
}

func TestAppendGeneratedParamSupportNote(t *testing.T) {
	got := appendGeneratedParamSupportNote("Filter value", spec.Param{In: "query", Style: "deepSpaceObject"})
	if !strings.Contains(got, `OpenAPI style "deepSpaceObject" is not fully supported`) {
		t.Fatalf("expected unsupported style note, got %q", got)
	}

	got = appendGeneratedParamSupportNote("", spec.Param{ContentMediaType: "text/plain"})
	if got != "parameter content type text/plain is sent as raw text" {
		t.Fatalf("content note = %q", got)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
