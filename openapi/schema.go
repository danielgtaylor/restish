package openapi

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

type schemaMode int

const (
	modeRead schemaMode = iota
	modeWrite
)

func renderSchema(s *openapi3.Schema, indent string, mode schemaMode) string {
	doc := s.Title
	if doc == "" {
		doc = s.Description
	}

	// Fix missing type if it can be inferred
	if s.Type == "" {
		if s.Items != nil && s.Items.Value != nil {
			s.Type = "array"
		}

		if len(s.Properties) > 0 || (s.AdditionalProperties != nil && s.AdditionalProperties.Value != nil) {
			s.Type = "object"
		}
	}

	// TODO: handle one-of, all-of, not
	// TODO: detect circular references

	switch s.Type {
	case "boolean", "integer", "number", "string":
		tags := []string{}

		// TODO: handler more validators
		if s.Min != nil {
			tags = append(tags, fmt.Sprintf("min:%g", *s.Min))
		}

		if s.Max != nil {
			tags = append(tags, fmt.Sprintf("max:%g", *s.Max))
		}

		if s.Default != nil {
			tags = append(tags, fmt.Sprintf("default:%v", s.Default))
		}

		if s.Pattern != "" {
			tags = append(tags, fmt.Sprintf("pattern:%s", s.Pattern))
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

		return fmt.Sprintf("(%s%s) %s", s.Type, tagStr, doc)
	case "array":
		arr := "(array) [\n  " + indent + renderSchema(s.Items.Value, indent+"  ", mode) + "\n" + indent + "]"
		return arr
	case "object":
		// Special case: object with nothing defined
		if len(s.Properties) == 0 && (s.AdditionalProperties == nil || s.AdditionalProperties.Value == nil) {
			return "(object)"
		}

		obj := "(object) {\n"

		keys := []string{}
		for name := range s.Properties {
			keys = append(keys, name)
		}
		sort.Strings(keys)

		for _, name := range keys {
			prop := s.Properties[name].Value

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

			obj += indent + "  " + name + ": " + renderSchema(prop, indent+"  ", mode) + "\n"
		}

		if s.AdditionalProperties != nil && s.AdditionalProperties.Value != nil && s.AdditionalProperties.Value.Type != "" {
			obj += indent + "  " + "<any>: " + renderSchema(s.AdditionalProperties.Value, indent+"  ", mode) + "\n"
		}

		obj += indent + "}"
		return obj
	}

	return ""
}
