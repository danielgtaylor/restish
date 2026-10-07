package spec

import (
	"fmt"
	"reflect"
	"strings"

	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
)

// MethodOp pairs an HTTP method name with its OpenAPI operation.
type MethodOp struct {
	Method string
	Op     *v3.Operation
}

// PathItemMethods returns all HTTP method/operation pairs for a path item
// in the conventional order GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS,
// TRACE.
// Callers should check Op for nil before use.
func PathItemMethods(item *v3.PathItem) []MethodOp {
	if item == nil {
		return nil
	}
	return []MethodOp{
		{"GET", item.Get},
		{"POST", item.Post},
		{"PUT", item.Put},
		{"PATCH", item.Patch},
		{"DELETE", item.Delete},
		{"HEAD", item.Head},
		{"OPTIONS", item.Options},
		{"TRACE", item.Trace},
	}
}

// OpExtBool reads a boolean OpenAPI extension from an operation.
func OpExtBool(op *v3.Operation, key string) bool {
	if op.Extensions == nil {
		return false
	}
	return extValue[bool](op.Extensions.GetOrZero(key))
}

// OpExtString reads a string OpenAPI extension from an operation.
func OpExtString(op *v3.Operation, key string) string {
	if op.Extensions == nil {
		return ""
	}
	return extValue[string](op.Extensions.GetOrZero(key))
}

// OpExtStrings reads a string-slice OpenAPI extension from an operation.
func OpExtStrings(op *v3.Operation, key string) []string {
	if op.Extensions == nil {
		return nil
	}
	return extValue[[]string](op.Extensions.GetOrZero(key))
}

// PathItemExtBool reads a boolean OpenAPI extension from a path item.
func PathItemExtBool(item *v3.PathItem, key string) bool {
	if item == nil {
		return false
	}
	if item.Extensions == nil {
		return false
	}
	return extValue[bool](item.Extensions.GetOrZero(key))
}

// ParamExtString reads a string OpenAPI extension from a parameter.
func ParamExtString(p *v3.Parameter, key string) string {
	if p == nil {
		return ""
	}
	if p.Extensions == nil {
		return ""
	}
	return extValue[string](p.Extensions.GetOrZero(key))
}

// ParamExtBool reads a boolean OpenAPI extension from a parameter.
func ParamExtBool(p *v3.Parameter, key string) bool {
	if p == nil {
		return false
	}
	if p.Extensions == nil {
		return false
	}
	return extValue[bool](p.Extensions.GetOrZero(key))
}

// ParamCompletionExt decodes and validates the shape of x-cli-completion.
func ParamCompletionExt(p *v3.Parameter) (*ParamCompletion, error) {
	if p == nil || p.Extensions == nil {
		return nil, nil
	}
	node := p.Extensions.GetOrZero("x-cli-completion")
	if nilDecodableNode(node) {
		return nil, nil
	}
	var fields map[string]any
	if err := node.Decode(&fields); err != nil || fields == nil {
		return nil, fmt.Errorf("must be an object")
	}
	allowed := map[string]bool{"operation_id": true, "value_path": true, "description_path": true, "bindings": true}
	for key := range fields {
		if !allowed[key] {
			return nil, fmt.Errorf("unknown field %q", key)
		}
	}
	var completion ParamCompletion
	if err := node.Decode(&completion); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	completion.OperationID = strings.TrimSpace(completion.OperationID)
	completion.ValuePath = strings.TrimSpace(completion.ValuePath)
	completion.DescriptionPath = strings.TrimSpace(completion.DescriptionPath)
	bindings := make(map[string]string, len(completion.Bindings))
	if len(completion.Bindings) > 32 {
		return nil, fmt.Errorf("bindings must contain at most 32 entries")
	}
	for target, source := range completion.Bindings {
		target = strings.TrimSpace(target)
		source = strings.TrimSpace(source)
		if target == "" || source == "" || len(target) > 256 || len(source) > 256 {
			return nil, fmt.Errorf("binding references must be 1 to 256 bytes")
		}
		if _, exists := bindings[target]; exists {
			return nil, fmt.Errorf("duplicate binding target %q", target)
		}
		bindings[target] = source
	}
	completion.Bindings = bindings
	if completion.OperationID == "" || len(completion.OperationID) > 256 {
		return nil, fmt.Errorf("operation_id must be 1 to 256 bytes")
	}
	if !validCompletionPath(completion.ValuePath) {
		return nil, fmt.Errorf("value_path must be a dot-separated path starting with body, headers, headers_all, status, or proto")
	}
	if completion.DescriptionPath != "" && !validCompletionPath(completion.DescriptionPath) {
		return nil, fmt.Errorf("description_path must be a dot-separated path starting with body, headers, headers_all, status, or proto")
	}
	return &completion, nil
}

func validCompletionPath(value string) bool {
	if len(value) > 512 || strings.ContainsAny(value, "[]|()") {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) > 32 {
		return false
	}
	rootOK := map[string]bool{"body": true, "headers": true, "headers_all": true, "status": true, "proto": true}
	if !rootOK[parts[0]] {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.TrimSpace(part) != part {
			return false
		}
	}
	return true
}

// ParamHasExtension reports whether a parameter declares an extension.
func ParamHasExtension(p *v3.Parameter, key string) bool {
	return p != nil && p.Extensions != nil && !nilDecodableNode(p.Extensions.GetOrZero(key))
}

type decodableNode interface {
	Decode(any) error
}

func extValue[T any](node decodableNode) T {
	var zero T
	if node == nil {
		return zero
	}
	valueOf := reflect.ValueOf(node)
	if valueOf.Kind() == reflect.Pointer && valueOf.IsNil() {
		return zero
	}
	var value T
	_ = node.Decode(&value)
	return value
}

func nilDecodableNode(node decodableNode) bool {
	if node == nil {
		return true
	}
	valueOf := reflect.ValueOf(node)
	return valueOf.Kind() == reflect.Pointer && valueOf.IsNil()
}
