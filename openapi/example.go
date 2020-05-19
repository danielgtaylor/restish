package openapi

import "github.com/getkin/kin-openapi/openapi3"

// genExample creates a dummy example from a given schema.
func genExample(schema *openapi3.Schema) interface{} {
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
		item := genExample(schema.Items.Value)
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
		value := map[string]interface{}{}
		for k, s := range schema.Properties {
			value[k] = genExample(s.Value)
		}
		return value
	}

	return nil
}
