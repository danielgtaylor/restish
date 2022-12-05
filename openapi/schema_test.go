package openapi

import (
	"testing"

	"github.com/pb33f/libopenapi/datamodel/high/base"
	"github.com/pb33f/libopenapi/datamodel/low"
	lowbase "github.com/pb33f/libopenapi/datamodel/low/base"
	"github.com/pb33f/libopenapi/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var schemaTests = []struct {
	name string
	mode schemaMode
	in   string
	out  string
}{
	{
		name: "guess-array",
		in:   `items: {type: string}`,
		out:  "[\n  (string)\n]",
	},
	{
		name: "guess-object",
		in:   `additionalProperties: true`,
		out:  "{\n  <any>: <any>\n}",
	},
	{
		name: "nullable",
		in:   `{type: boolean, nullable: true}`,
		out:  "(boolean nullable:true)",
	},
	{
		name: "nullable31",
		in:   `{type: [null, boolean]}`,
		out:  "(null|boolean)",
	},
	{
		name: "min",
		in:   `{type: number, minimum: 5}`,
		out:  "(number min:5)",
	},
	{
		name: "exclusive-min",
		in:   `{type: number, minimum: 5, exclusiveMinimum: true}`,
		out:  "(number exclusiveMin:5)",
	},
	{
		name: "exclusive-min-31",
		in:   `{type: number, exclusiveMinimum: 5}`,
		out:  "(number exclusiveMin:5)",
	},
	{
		name: "max",
		in:   `{type: number, maximum: 5}`,
		out:  "(number max:5)",
	},
	{
		name: "exclusive-max",
		in:   `{type: number, maximum: 5, exclusiveMaximum: true}`,
		out:  "(number exclusiveMax:5)",
	},
	{
		name: "exclusive-max-31",
		in:   `{type: number, exclusiveMaximum: 5}`,
		out:  "(number exclusiveMax:5)",
	},
	{
		name: "multiple-of",
		in:   `{type: number, multipleOf: 5}`,
		out:  "(number multiple:5)",
	},
	{
		name: "default-scalar",
		in:   `{type: number, default: 5.0}`,
		out:  "(number default:5)",
	},
	{
		name: "string-format",
		in:   `{type: string, format: date}`,
		out:  "(string format:date)",
	},
	{
		name: "string-pattern",
		in:   `{type: string, pattern: "^[a-z]+$"}`,
		out:  "(string pattern:^[a-z]+$)",
	},
	{
		name: "string-min-length",
		in:   `{type: string, minLength: 5}`,
		out:  "(string minLen:5)",
	},
	{
		name: "string-max-length",
		in:   `{type: string, maxLength: 5}`,
		out:  "(string maxLen:5)",
	},
	{
		name: "string-enum",
		in:   `{type: string, enum: [one, two]}`,
		out:  "(string enum:one,two)",
	},
	{
		name: "empty-array",
		in:   `{type: array}`,
		out:  "[<any>]",
	},
	{
		name: "array",
		in:   `{type: array, items: {type: number}}`,
		out:  "[\n  (number)\n]",
	},
	{
		name: "object-empty",
		in:   `{type: object}`,
		out:  "(object)",
	},
	{
		name: "object-prop-null",
		in:   `{type: object, properties: {foo: null}}`,
		out:  "{\n  foo: <any>\n}",
	},
	{
		name: "object",
		in:   `{type: object, properties: {foo: {type: string}, bar: {type: integer}}, required: [foo]}`,
		out:  "{\n  bar: (integer)\n  foo*: (string)\n}",
	},
	{
		name: "object-read-only",
		mode: modeRead,
		in:   `{type: object, properties: {foo: {type: string, readOnly: true}, bar: {type: string, writeOnly: true}}}`,
		out:  "{\n  foo: (string)\n}",
	},
	{
		name: "object-write-only",
		mode: modeWrite,
		in:   `{type: object, properties: {foo: {type: string, readOnly: true}, bar: {type: string, writeOnly: true}}}`,
		out:  "{\n  bar: (string)\n}",
	},
	{
		name: "object-additional-props-bool",
		in:   `{type: object, additionalProperties: true}`,
		out:  "{\n  <any>: <any>\n}",
	},
	{
		name: "object-additional-props-scehma",
		in:   `{type: object, additionalProperties: {type: string}}`,
		out:  "{\n  <any>: (string)\n}",
	},
	{
		name: "all-of",
		in:   `{allOf: [{type: object, properties: {a: {type: string}}}, {type: object, properties: {foo: {type: string}, bar: {type: number, description: desc}}}]}`,
		out:  "allOf{\n  {\n    a: (string)\n  }\n  {\n    bar: (number) desc\n    foo: (string)\n  }\n}",
	},
	{
		name: "one-of",
		in:   `{oneOf: [{type: boolean}, {type: object, properties: {foo: {type: string}, bar: {type: number, description: desc}}}]}`,
		out:  "oneOf{\n  (boolean)\n  {\n    bar: (number) desc\n    foo: (string)\n  }\n}",
	},
	{
		name: "any-of",
		in:   `{anyOf: [{type: boolean}, {type: object, properties: {foo: {type: string}, bar: {type: number, description: desc}}}]}`,
		out:  "anyOf{\n  (boolean)\n  {\n    bar: (number) desc\n    foo: (string)\n  }\n}",
	},
	{
		name: "recusive-prop",
		in:   `{type: object, properties: {person: {type: object, properties: {friend: {$ref: "#/properties/person"}}}}}`,
		out:  "{\n  person: {\n    friend: <rescurive ref>\n  }\n}",
	},
	{
		name: "recusive-array",
		in:   `{type: object, properties: {person: {type: object, properties: {friend: {type: array, items: {$ref: "#/properties/person"}}}}}}`,
		out:  "{\n  person: {\n    friend: [<recursive ref>]\n  }\n}",
	},
	{
		name: "recusive-additional-props",
		in:   `{type: object, properties: {person: {type: object, properties: {friend: {type: object, additionalProperties: {$ref: "#/properties/person"}}}}}}`,
		out:  "{\n  person: {\n    friend: {\n      <any>: <rescurive ref>\n    }\n  }\n}",
	},
}

func TestSchema(t *testing.T) {
	for _, example := range schemaTests {
		t.Run(example.name, func(t *testing.T) {
			var rootNode yaml.Node
			var ls lowbase.Schema

			require.NoError(t, yaml.Unmarshal([]byte(example.in), &rootNode))
			require.NoError(t, low.BuildModel(rootNode.Content[0], &ls))
			require.NoError(t, ls.Build(rootNode.Content[0], index.NewSpecIndex(&rootNode)))

			// spew.Dump(ls)

			s := base.NewSchema(&ls)
			assert.Equal(t, example.out, renderSchema(s, "", example.mode))
		})
	}
}
