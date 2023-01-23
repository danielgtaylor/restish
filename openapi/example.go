package openapi

import (
	"strings"

	"github.com/lucasjones/reggen"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	"golang.org/x/exp/maps"
)

// GenExample creates a dummy example from a given schema.
func GenExample(schema *base.Schema, mode schemaMode) interface{} {
	return genExampleInternal(schema, mode, map[[32]byte]bool{})
}

func genExampleInternal(s *base.Schema, mode schemaMode, known map[[32]byte]bool) any {
	inferType(s)

	// TODO: handle  not
	if len(s.OneOf) > 0 {
		return genExampleInternal(sortedSchemas(s.OneOf)[0].Schema(), mode, known)
	}

	if len(s.AnyOf) > 0 {
		return genExampleInternal(sortedSchemas(s.AnyOf)[0].Schema(), mode, known)
	}

	if len(s.AllOf) > 0 {
		result := map[string]any{}
		for _, proxy := range sortedSchemas(s.AllOf) {
			tmp := genExampleInternal(proxy.Schema(), mode, known)
			if m, ok := tmp.(map[string]any); ok {
				maps.Copy(result, m)
			}
		}
		return result
	}

	if s.Example != nil {
		return s.Example
	}

	if len(s.Examples) > 0 {
		return s.Examples[0]
	}

	if s.Default != nil {
		return s.Default
	}

	if s.Minimum != nil {
		if s.ExclusiveMinimum != nil && s.ExclusiveMinimum.IsA() && s.ExclusiveMinimum.A {
			return *s.Minimum + 1
		}
		return *s.Minimum
	} else if s.ExclusiveMinimum != nil && (s.ExclusiveMinimum.IsB()) {
		return s.ExclusiveMinimum.B + 1
	}

	if s.Maximum != nil {
		if s.ExclusiveMaximum != nil && s.ExclusiveMaximum.IsA() && s.ExclusiveMaximum.A {
			return *s.Maximum - 1
		}
		return *s.Maximum
	} else if s.ExclusiveMaximum != nil && s.ExclusiveMaximum.IsB() {
		return s.ExclusiveMaximum.B - 1
	}

	if s.MultipleOf != nil && *s.MultipleOf != 0 {
		return *s.MultipleOf
	}

	if len(s.Enum) > 0 {
		return s.Enum[0]
	}

	if s.Pattern != "" {
		if g, err := reggen.NewGenerator(s.Pattern); err == nil {
			// We need stable/reproducible outputs, so use a constant seed.
			g.SetSeed(1589525091)
			return g.Generate(3)
		}
	}

	switch s.Format {
	case "date":
		return "2020-05-14"
	case "time":
		return "23:44:51-07:00"
	case "date-time":
		return "2020-05-14T23:44:51-07:00"
	case "duration":
		return "P30S"
	case "email", "idn-email":
		return "user@example.com"
	case "hostname", "idn-hostname":
		return "example.com"
	case "ipv4":
		return "192.0.2.1"
	case "ipv6":
		return "2001:db8::1"
	case "uuid":
		return "3e4666bf-d5e5-4aa7-b8ce-cefe41c7568a"
	case "uri", "iri":
		return "https://example.com/"
	case "uri-reference", "iri-reference":
		return "/example"
	case "uri-template":
		return "https://example.com/{id}"
	case "json-pointer":
		return "/example/0/id"
	case "relative-json-pointer":
		return "0/id"
	case "regex":
		return "ab+c"
	case "password":
		return "********"
	}

	typ := ""
	for _, t := range s.Type {
		// Find the first non-null type and use that for now.
		if t != "null" {
			typ = t
			break
		}
	}

	switch typ {
	case "boolean":
		return true
	case "integer":
		return 1
	case "number":
		return 1.0
	case "string":
		if s.MinLength != nil && *s.MinLength > 6 {
			sb := strings.Builder{}
			for i := int64(0); i < *s.MinLength; i++ {
				sb.WriteRune('s')
			}
			return sb.String()
		}

		if s.MaxLength != nil && *s.MaxLength < 6 {
			sb := strings.Builder{}
			for i := int64(0); i < *s.MaxLength; i++ {
				sb.WriteRune('s')
			}
			return sb.String()
		}

		return "string"
	case "array":
		if s.Items != nil && s.Items.IsA() {
			items := s.Items.A.Schema()
			simple := isSimpleSchema(items)
			hash := items.GoLow().Hash()
			if simple || !known[hash] {
				known[hash] = true
				item := genExampleInternal(items, mode, known)
				known[hash] = false

				count := 1
				if s.MinItems != nil && *s.MinItems > 0 {
					count = int(*s.MinItems)
				}

				value := make([]any, 0, count)
				for i := 0; i < count; i++ {
					value = append(value, item)
				}
				return value
			}

			return []any{nil}
		}
		return "[<any>]"
	case "object":
		value := map[string]any{}

		// Special case: object with nothing defined
		if len(s.Properties) == 0 && s.AdditionalProperties == nil {
			return value
		}

		for name, proxy := range s.Properties {
			prop := proxy.Schema()
			if prop == nil {
				continue
			}

			if prop.ReadOnly && mode == modeWrite {
				continue
			} else if prop.WriteOnly && mode == modeRead {
				continue
			}

			simple := isSimpleSchema(prop)
			hash := prop.GoLow().Hash()
			if simple || !known[hash] {
				known[hash] = true
				value[name] = genExampleInternal(prop, mode, known)
				known[hash] = false
			} else {
				value[name] = nil
			}
		}

		if s.AdditionalProperties != nil {
			ap := s.AdditionalProperties
			if sp, ok := ap.(*base.SchemaProxy); ok {
				addl := sp.Schema()
				simple := isSimpleSchema(addl)
				hash := addl.GoLow().Hash()
				if simple || !known[hash] {
					known[hash] = true
					value["<any>"] = genExampleInternal(addl, mode, known)
					known[hash] = false
				} else {
					value["<any>"] = nil
				}
			}
			if b, ok := ap.(bool); ok && b {
				value["<any>"] = nil
			}
		}

		return value
	}

	return nil
}
