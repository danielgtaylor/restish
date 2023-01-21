package openapi

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/pb33f/libopenapi/datamodel/high/base"
)

type schemaMode int

const (
	modeRead schemaMode = iota
	modeWrite
)

// inferType fixes missing type if it is missing & can be inferred
func inferType(s *base.Schema) {
	if len(s.Type) == 0 {
		if s.Items != nil {
			s.Type = []string{"array"}
		}

		if len(s.Properties) > 0 || s.AdditionalProperties != nil {
			s.Type = []string{"object"}
		}
	}
}

// isSimpleSchema returns whether this schema is a scalar or array as these
// can't be circular references. Objects result in `false` and that triggers
// circular ref checks.
func isSimpleSchema(s *base.Schema) bool {
	if len(s.Type) == 0 {
		return true
	}

	return s.Type[0] != "object"
}

// sortedSchemas is a hack to provide stable outputs from the schema and example
// generation code because libopenapi has a race condition that determines the
// order of resolved lists of schema proxies for all-of, any-of, one-of, etc.
func sortedSchemas(s []*base.SchemaProxy) []*base.SchemaProxy {
	sort.Slice(s, func(i, j int) bool {
		ih := s[i].GoLow().Hash()
		jh := s[j].GoLow().Hash()
		return base64.StdEncoding.EncodeToString(ih[:]) < base64.StdEncoding.EncodeToString(jh[:])
	})
	return s
}

func renderSchema(s *base.Schema, indent string, mode schemaMode) string {
	return renderSchemaInternal(s, indent, mode, map[[32]byte]bool{})
}

func renderSchemaInternal(s *base.Schema, indent string, mode schemaMode, known map[[32]byte]bool) string {
	doc := s.Title
	if doc == "" {
		doc = s.Description
	}

	inferType(s)

	// TODO: handle not
	for _, of := range []struct {
		label   string
		schemas []*base.SchemaProxy
	}{
		{label: "allOf", schemas: s.AllOf},
		{label: "oneOf", schemas: s.OneOf},
		{label: "anyOf", schemas: s.AnyOf},
	} {
		if len(of.schemas) > 0 {
			out := of.label + "{\n"
			for _, possible := range sortedSchemas(of.schemas) {
				out += indent + "  " + renderSchemaInternal(possible.Schema(), indent+"  ", mode, known) + "\n"
			}
			return out + indent + "}"
		}
	}

	// TODO: list type alternatives somehow?
	typ := ""
	for _, t := range s.Type {
		// Find the first non-null type and use that for now.
		if t != "null" {
			typ = t
			break
		}
	}

	switch typ {
	case "boolean", "integer", "number", "string":
		tags := []string{}

		// TODO: handle more validators
		if s.Nullable != nil && *s.Nullable {
			tags = append(tags, "nullable:true")
		}

		if s.Minimum != nil {
			key := "min"
			if s.ExclusiveMinimum != nil && s.ExclusiveMinimum.IsA() && s.ExclusiveMinimum.A {
				key = "exclusiveMin"
			}
			tags = append(tags, fmt.Sprintf("%s:%d", key, *s.Minimum))
		} else if s.ExclusiveMinimum != nil && s.ExclusiveMinimum.IsB() {
			tags = append(tags, fmt.Sprintf("exclusiveMin:%d", s.ExclusiveMinimum.B))
		}

		if s.Maximum != nil {
			key := "max"
			if s.ExclusiveMaximum != nil && s.ExclusiveMaximum.IsA() && s.ExclusiveMaximum.A {
				key = "exclusiveMax"
			}
			tags = append(tags, fmt.Sprintf("%s:%d", key, *s.Maximum))
		} else if s.ExclusiveMaximum != nil && s.ExclusiveMaximum.IsB() {
			tags = append(tags, fmt.Sprintf("exclusiveMax:%d", s.ExclusiveMaximum.B))
		}

		if s.MultipleOf != nil && *s.MultipleOf != 0 {
			tags = append(tags, fmt.Sprintf("multiple:%d", *s.MultipleOf))
		}

		if s.Default != nil {
			tags = append(tags, fmt.Sprintf("default:%v", s.Default))
		}

		if s.Format != "" {
			tags = append(tags, fmt.Sprintf("format:%v", s.Format))
		}

		if s.Pattern != "" {
			tags = append(tags, fmt.Sprintf("pattern:%s", s.Pattern))
		}

		if s.MinLength != nil && *s.MinLength != 0 {
			tags = append(tags, fmt.Sprintf("minLen:%d", *s.MinLength))
		}

		if s.MaxLength != nil && *s.MaxLength != 0 {
			tags = append(tags, fmt.Sprintf("maxLen:%d", *s.MaxLength))
		}

		if len(s.Enum) > 0 {
			enums := []string{}
			for _, e := range s.Enum {
				enums = append(enums, fmt.Sprintf("%v", e))
			}

			tags = append(tags, fmt.Sprintf("enum:%s", strings.Join(enums, ",")))
		}

		tagStr := ""
		if len(tags) > 0 {
			tagStr = " " + strings.Join(tags, " ")
		}

		if doc != "" {
			doc = " " + doc
		}
		return fmt.Sprintf("(%s%s)%s", strings.Join(s.Type, "|"), tagStr, doc)
	case "array":
		if s.Items != nil && s.Items.IsA() {
			items := s.Items.A.Schema()
			simple := isSimpleSchema(items)
			hash := items.GoLow().Hash()
			if simple || !known[hash] {
				known[hash] = true
				arr := "[\n  " + indent + renderSchemaInternal(items, indent+"  ", mode, known) + "\n" + indent + "]"
				known[hash] = false
				return arr
			}

			return "[<recursive ref>]"
		}
		return "[<any>]"
	case "object":
		// Special case: object with nothing defined
		if len(s.Properties) == 0 && s.AdditionalProperties == nil {
			return "(object)"
		}

		obj := "{\n"

		keys := []string{}
		for name := range s.Properties {
			keys = append(keys, name)
		}
		sort.Strings(keys)

		for _, name := range keys {
			prop := s.Properties[name].Schema()
			if prop == nil {
				continue
			}

			if prop.ReadOnly && mode == modeWrite {
				continue
			} else if prop.WriteOnly && mode == modeRead {
				continue
			}

			for _, req := range s.Required {
				if req == name {
					name += "*"
					break
				}
			}

			simple := isSimpleSchema(prop)
			hash := prop.GoLow().Hash()
			if simple || !known[hash] {
				known[hash] = true
				obj += indent + "  " + name + ": " + renderSchemaInternal(prop, indent+"  ", mode, known) + "\n"
				known[hash] = false
			} else {
				obj += indent + "  " + name + ": <rescurive ref>\n"
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
					obj += indent + "  " + "<any>: " + renderSchemaInternal(addl, indent+"  ", mode, known) + "\n"
				} else {
					obj += indent + "  <any>: <rescurive ref>\n"
				}
			}
			if b, ok := ap.(bool); ok && b {
				obj += indent + "  <any>: <any>\n"
			}
		}

		obj += indent + "}"
		return obj
	}

	return "<any>"
}
