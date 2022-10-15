package openapi

import "github.com/getkin/kin-openapi/openapi3"

// genExample creates a dummy example from a given schema.
func genExample(schema *openapi3.Schema) interface{} {
	return genLimitedExample(schema, 5, 0)
}

// genLimitedExample creates a dummy example from a given schema.
// examples will go no more than `maxDepth` deep to avoid blowing
// up on circular references
func genLimitedExample(schema *openapi3.Schema, maxDepth int, currentDepth int) interface{} {
	if schema.Example != nil {
		return schema.Example
	}

	if schema.Default != nil {
		return schema.Default
	}

	switch schema.Type {
	case "null":
		return nil
	case "bool":
		return true
	case "integer":
		return 1
	case "number":
		return 1.0
	case "string":
		return "string"
	case "array":
		if currentDepth >= maxDepth {
			return nil
		}
		item := genLimitedExample(schema.Items.Value, maxDepth, currentDepth+1)
		count := 1
		if schema.MinItems > 0 {
			count = int(schema.MinItems)
		}

		value := []interface{}{}
		for i := 0; i < count; i++ {
			value = append(value, item)
		}
		return value
	case "object":
		if currentDepth >= maxDepth {
			return nil
		}
		value := map[string]interface{}{}
		for k, s := range schema.Properties {
			value[k] = genLimitedExample(s.Value, maxDepth, currentDepth+1)
		}
		return value
	}

	return nil
}
