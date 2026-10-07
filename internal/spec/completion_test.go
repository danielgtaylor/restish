package spec

import (
	"strings"
	"testing"
)

func TestOperationSetExtractsParameterCompletion(t *testing.T) {
	loaded, err := (OpenAPILoader{}).Load([]byte(`openapi: 3.1.0
info: {title: Completion, version: "1"}
paths:
  /projects:
    get:
      operationId: listProjects
      parameters:
        - {name: account, in: query, required: true, schema: {type: string}}
      responses: {"200": {description: OK}}
  /accounts/{account}/servers:
    get:
      operationId: listServers
      parameters:
        - {name: account, in: path, required: true, schema: {type: string}}
        - name: project
          in: query
          schema: {type: string}
          x-cli-completion:
            operation_id: listProjects
            bindings: {query.account: path.account}
            value_path: body.items.id
            description_path: body.items.name
      responses: {"200": {description: OK}}
`))
	if err != nil {
		t.Fatal(err)
	}
	set, err := loaded.OperationSet(OperationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	completion := set.Operations[1].Parameters[1].XCLI.Completion
	if completion == nil || completion.OperationID != "listProjects" || completion.Bindings["query.account"] != "path.account" {
		t.Fatalf("completion = %#v", completion)
	}
	if completion.ValuePath != "body.items.id" || completion.DescriptionPath != "body.items.name" {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestOperationSetRejectsUnsafeParameterCompletion(t *testing.T) {
	loaded, err := (OpenAPILoader{}).Load([]byte(`openapi: 3.1.0
info: {title: Completion, version: "1"}
paths:
  /projects:
    post:
      operationId: listProjects
      responses: {"200": {description: OK}}
  /servers:
    get:
      operationId: listServers
      parameters:
        - name: project
          in: query
          schema: {type: string}
          x-cli-completion: {operation_id: listProjects, value_path: body.items.id}
      responses: {"200": {description: OK}}
`))
	if err != nil {
		t.Fatal(err)
	}
	set, err := loaded.OperationSet(OperationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if set.Operations[1].Parameters[0].XCLI.Completion != nil {
		t.Fatal("unsafe provider was accepted")
	}
	if len(set.Warnings) != 1 || !strings.Contains(set.Warnings[0], "must use GET or HEAD") {
		t.Fatalf("warnings = %#v", set.Warnings)
	}
}

func TestOperationSetRejectsUnboundProviderParameter(t *testing.T) {
	loaded, err := (OpenAPILoader{}).Load([]byte(`openapi: 3.1.0
info: {title: Completion, version: "1"}
paths:
  /projects:
    get:
      operationId: listProjects
      parameters:
        - {name: account, in: query, required: true, schema: {type: string}}
      responses: {"200": {description: OK}}
  /servers:
    get:
      operationId: listServers
      parameters:
        - name: project
          in: query
          schema: {type: string}
          x-cli-completion: {operation_id: listProjects, value_path: body.items.id}
      responses: {"200": {description: OK}}
`))
	if err != nil {
		t.Fatal(err)
	}
	set, err := loaded.OperationSet(OperationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if set.Operations[1].Parameters[0].XCLI.Completion != nil {
		t.Fatal("unbound provider was accepted")
	}
	if len(set.Warnings) != 1 || !strings.Contains(set.Warnings[0], "has no binding") {
		t.Fatalf("warnings = %#v", set.Warnings)
	}
}

func TestParameterCompletionRejectsHEADBodySelector(t *testing.T) {
	err := validateParamCompletion(Operation{}, Operation{Method: "HEAD"}, ParamCompletion{ValuePath: "body.items.id"})
	if err == nil || !strings.Contains(err.Error(), "cannot read body") {
		t.Fatalf("error = %v", err)
	}
}

func TestParameterCompletionRejectsHostBinding(t *testing.T) {
	current := Operation{Parameters: []Param{{Name: "host", In: "query"}}}
	provider := Operation{Method: "GET", Parameters: []Param{{Name: "Host", In: "header", Required: true}}}
	err := validateParamCompletion(current, provider, ParamCompletion{
		ValuePath: "body.id", Bindings: map[string]string{"header.Host": "query.host"},
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP authority") {
		t.Fatalf("error = %v", err)
	}
}
