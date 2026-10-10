package spec

import (
	"reflect"

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

func securitySchemeDPoPRequired(scheme *v3.SecurityScheme) bool {
	return scheme != nil && scheme.Extensions != nil && extValue[bool](scheme.Extensions.GetOrZero("x-dpop-required"))
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
