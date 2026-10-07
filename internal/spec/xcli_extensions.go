package spec

import (
	"fmt"
	"sort"
	"strings"

	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
)

// XCLIExtensionReport describes behavior-changing x-cli-* OpenAPI extensions
// found in a document. It intentionally excludes x-cli-description, which only
// changes help prose.
type XCLIExtensionReport struct {
	Details []XCLIExtensionDetail `json:"details,omitempty" cbor:"details,omitempty"`
}

// XCLIExtensionDetail is one behavior-changing x-cli-* extension use.
type XCLIExtensionDetail struct {
	Kind      string `json:"kind" cbor:"kind"`
	Extension string `json:"extension" cbor:"extension"`
	Location  string `json:"location" cbor:"location"`
	Name      string `json:"name,omitempty" cbor:"name,omitempty"`
	Value     string `json:"value,omitempty" cbor:"value,omitempty"`
	Effect    string `json:"effect" cbor:"effect"`
}

// Empty reports whether the document had any behavior-changing x-cli-* uses.
func (r XCLIExtensionReport) Empty() bool {
	return len(r.Details) == 0
}

// Summary returns stable, compact phrases for human connect/sync output.
func (r XCLIExtensionReport) Summary() []string {
	if len(r.Details) == 0 {
		return nil
	}
	counts := map[string]int{}
	for _, detail := range r.Details {
		counts[detail.Kind]++
	}
	var summary []string
	for _, item := range xcliExtensionSummaryOrder {
		count := counts[item.kind]
		if count == 0 {
			continue
		}
		if item.noCount {
			summary = append(summary, item.singular)
			continue
		}
		summary = append(summary, fmt.Sprintf("%d %s", count, pluralize(count, item.singular, item.plural)))
	}
	return summary
}

// XCLIExtensionReport extracts behavior-changing x-cli-* extension uses from
// the parsed OpenAPI document before generated command filtering is applied.
func (s *APISpec) XCLIExtensionReport() (XCLIExtensionReport, error) {
	var report xcliExtensionReportBuilder
	if xcli, err := ReadXCLIConfig(s); err == nil && xcli != nil {
		report.add("config", "x-cli-config", "document", "", "", "pre-populates API config profiles")
	}
	model, err := s.V3Model()
	if err != nil || model == nil || model.Model.Paths == nil {
		return XCLIExtensionReport{Details: report.details()}, err
	}
	for rawPath, pathItem := range model.Model.Paths.PathItems.FromOldest() {
		if pathItem == nil {
			continue
		}
		if PathItemExtBool(pathItem, "x-cli-ignore") {
			report.add("path_ignored", "x-cli-ignore", rawPath, "", "true", "removes operations under this path from generated commands")
			continue
		}
		if PathItemExtBool(pathItem, "x-cli-hidden") {
			report.add("path_hidden", "x-cli-hidden", rawPath, "", "true", "hides operations under this path from generated help")
		}
		for _, methodOp := range PathItemMethods(pathItem) {
			if methodOp.Op == nil {
				continue
			}
			report.addOperationDetails(methodOp.Method, rawPath, pathItem.Parameters, methodOp.Op)
		}
	}
	return XCLIExtensionReport{Details: report.details()}, nil
}

type xcliExtensionReportBuilder struct {
	d []XCLIExtensionDetail
}

func (b *xcliExtensionReportBuilder) add(kind, extension, location, name, value, effect string) {
	b.d = append(b.d, XCLIExtensionDetail{
		Kind:      kind,
		Extension: extension,
		Location:  location,
		Name:      name,
		Value:     value,
		Effect:    effect,
	})
}

func (b *xcliExtensionReportBuilder) addOperationDetails(method, rawPath string, pathParams []*v3.Parameter, op *v3.Operation) {
	location := method + " " + rawPath
	name := op.OperationId
	if OpExtBool(op, "x-cli-ignore") {
		b.add("operation_ignored", "x-cli-ignore", location, name, "true", "removes this operation from generated commands")
		return
	}
	if OpExtBool(op, "x-cli-hidden") {
		b.add("operation_hidden", "x-cli-hidden", location, name, "true", "hides this operation from generated help")
	}
	if value := OpExtString(op, "x-cli-name"); value != "" {
		b.add("operation_renamed", "x-cli-name", location, name, value, "renames the generated command")
	}
	if aliases := OpExtStrings(op, "x-cli-aliases"); len(aliases) > 0 {
		b.add("operation_aliases", "x-cli-aliases", location, name, strings.Join(aliases, ", "), "adds generated command aliases")
	}
	for _, param := range MergeParameters(pathParams, op.Parameters) {
		if param == nil {
			continue
		}
		paramLocation := fmt.Sprintf("%s parameter %s %s", location, param.In, param.Name)
		paramName := param.In + " " + param.Name
		if ParamExtBool(param, "x-cli-ignore") {
			b.add("parameter_ignored", "x-cli-ignore", paramLocation, paramName, "true", "removes this parameter from the generated command")
			continue
		}
		if ParamExtBool(param, "x-cli-hidden") {
			b.add("parameter_hidden", "x-cli-hidden", paramLocation, paramName, "true", "hides this parameter from generated help")
		}
		if value := ParamExtString(param, "x-cli-name"); value != "" {
			b.add("parameter_renamed", "x-cli-name", paramLocation, paramName, value, "renames the generated argument or flag")
		}
		if ParamHasExtension(param, "x-cli-completion") {
			b.add("parameter_completion", "x-cli-completion", paramLocation, paramName, "", "may call a declared GET or HEAD operation during shell completion")
		}
	}
}

func (b *xcliExtensionReportBuilder) details() []XCLIExtensionDetail {
	out := append([]XCLIExtensionDetail(nil), b.d...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Location != out[j].Location {
			return out[i].Location < out[j].Location
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Extension < out[j].Extension
	})
	return out
}

type xcliExtensionSummaryItem struct {
	kind     string
	singular string
	plural   string
	noCount  bool
}

var xcliExtensionSummaryOrder = []xcliExtensionSummaryItem{
	{kind: "config", singular: "x-cli-config", noCount: true},
	{kind: "path_ignored", singular: "ignored path", plural: "ignored paths"},
	{kind: "path_hidden", singular: "hidden path", plural: "hidden paths"},
	{kind: "operation_ignored", singular: "ignored operation", plural: "ignored operations"},
	{kind: "operation_hidden", singular: "hidden operation", plural: "hidden operations"},
	{kind: "operation_renamed", singular: "renamed operation", plural: "renamed operations"},
	{kind: "operation_aliases", singular: "operation with aliases", plural: "operations with aliases"},
	{kind: "parameter_ignored", singular: "ignored parameter", plural: "ignored parameters"},
	{kind: "parameter_hidden", singular: "hidden parameter", plural: "hidden parameters"},
	{kind: "parameter_renamed", singular: "renamed parameter", plural: "renamed parameters"},
	{kind: "parameter_completion", singular: "API-backed parameter completion", plural: "API-backed parameter completions"},
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
