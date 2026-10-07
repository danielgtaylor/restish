package spec

import (
	"strings"
	"testing"
)

func TestXCLIExtensionReportFindsBehaviorChangingExtensions(t *testing.T) {
	raw := []byte(`openapi: 3.1.0
info:
  title: XCLI API
  version: "1.0"
x-cli-config:
  profiles:
    default:
      headers: ["Accept: application/json"]
paths:
  /admin:
    x-cli-hidden: true
    get:
      operationId: listAdmin
      responses:
        "200": {}
  /hidden:
    get:
      operationId: hiddenOp
      x-cli-hidden: true
      responses:
        "200": {}
  /ignored:
    get:
      operationId: ignoredOp
      x-cli-ignore: true
      x-cli-hidden: true
      x-cli-name: renamed-ignored
      x-cli-aliases: [ig]
      responses:
        "200": {}
  /items/{id}:
    get:
      operationId: getItem
      x-cli-name: item
      x-cli-aliases: [it]
      x-cli-description: harmless help text
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
          x-cli-name: item-id
        - name: debug
          in: query
          schema:
            type: string
          x-cli-hidden: true
          x-cli-completion:
            operation_id: listAdmin
            value_path: body.items.id
        - name: internal
          in: query
          schema:
            type: string
          x-cli-ignore: true
          x-cli-hidden: true
          x-cli-name: ignored-internal
      responses:
        "200": {}`)
	loaded, err := (OpenAPILoader{}).Load(raw)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	report, err := loaded.XCLIExtensionReport()
	if err != nil {
		t.Fatalf("XCLIExtensionReport: %v", err)
	}
	gotSummary := strings.Join(report.Summary(), ", ")
	for _, want := range []string{
		"x-cli-config",
		"1 hidden path",
		"1 ignored operation",
		"1 hidden operation",
		"1 renamed operation",
		"1 operation with aliases",
		"1 ignored parameter",
		"1 hidden parameter",
		"1 renamed parameter",
		"1 API-backed parameter completion",
	} {
		if !strings.Contains(gotSummary, want) {
			t.Fatalf("summary %q missing %q", gotSummary, want)
		}
	}
	if strings.Contains(gotSummary, "description") {
		t.Fatalf("x-cli-description should not be summarized: %q", gotSummary)
	}
	wantKinds := map[string]bool{
		"config":               false,
		"path_hidden":          false,
		"operation_ignored":    false,
		"operation_hidden":     false,
		"operation_renamed":    false,
		"operation_aliases":    false,
		"parameter_ignored":    false,
		"parameter_hidden":     false,
		"parameter_renamed":    false,
		"parameter_completion": false,
	}
	for _, detail := range report.Details {
		if _, ok := wantKinds[detail.Kind]; ok {
			wantKinds[detail.Kind] = true
		}
		if detail.Extension == "x-cli-description" {
			t.Fatalf("x-cli-description should be omitted from details: %#v", detail)
		}
	}
	for kind, seen := range wantKinds {
		if !seen {
			t.Fatalf("missing detail kind %q in %#v", kind, report.Details)
		}
	}
}

func TestXCLIExtensionReportStopsAtIgnoredPath(t *testing.T) {
	raw := []byte(`openapi: 3.1.0
info:
  title: XCLI API
  version: "1.0"
paths:
  /ignored:
    x-cli-ignore: true
    get:
      operationId: ignoredOp
      x-cli-name: renamed-ignored
      parameters:
        - name: hidden
          in: query
          schema:
            type: string
          x-cli-hidden: true
      responses:
        "200": {}
  /visible:
    get:
      operationId: visibleOp
      x-cli-name: visible
      responses:
        "200": {}`)
	loaded, err := (OpenAPILoader{}).Load(raw)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	report, err := loaded.XCLIExtensionReport()
	if err != nil {
		t.Fatalf("XCLIExtensionReport: %v", err)
	}
	gotSummary := strings.Join(report.Summary(), ", ")
	for _, want := range []string{"1 ignored path", "1 renamed operation"} {
		if !strings.Contains(gotSummary, want) {
			t.Fatalf("summary %q missing %q", gotSummary, want)
		}
	}
	for _, detail := range report.Details {
		if detail.Location == "GET /ignored" || strings.Contains(detail.Location, "GET /ignored parameter") {
			t.Fatalf("ignored path should not report nested extension detail: %#v", detail)
		}
	}
}
