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

var exampleTests = []struct {
	name string
	mode schemaMode
	in   string
	out  any
}{
	{
		name: "boolean",
		in:   `{type: boolean}`,
		out:  true,
	},
	{
		name: "example",
		in:   `{type: number, example: 5}`,
		out:  5,
	},
	{
		name: "examples",
		in:   `{type: number, examples: [5]}`,
		out:  5,
	},
	{
		name: "guess-array",
		in:   `items: {type: string}`,
		out:  []any{"string"},
	},
	{
		name: "guess-object",
		in:   `additionalProperties: true`,
		out:  map[string]any{"<any>": nil},
	},
	{
		name: "min",
		in:   `{type: number, minimum: 5}`,
		out:  5,
	},
	{
		name: "exclusive-min",
		in:   `{type: number, minimum: 5, exclusiveMinimum: true}`,
		out:  6,
	},
	{
		name: "exclusive-min-31",
		in:   `{type: number, exclusiveMinimum: 5}`,
		out:  6,
	},
	{
		name: "max",
		in:   `{type: number, maximum: 5}`,
		out:  5,
	},
	{
		name: "exclusive-max",
		in:   `{type: number, maximum: 5, exclusiveMaximum: true}`,
		out:  4,
	},
	{
		name: "exclusive-max-31",
		in:   `{type: number, exclusiveMaximum: 5}`,
		out:  4,
	},
	{
		name: "multiple-of",
		in:   `{type: number, multipleOf: 5}`,
		out:  5,
	},
	{
		name: "default-scalar",
		in:   `{type: number, default: 5.0}`,
		out:  5,
	},
	{
		name: "default-object",
		in:   `{type: object, default: {foo: hello}}`,
		out:  map[string]any{"foo": "hello"},
	},
	{
		name: "string-format-date",
		in:   `{type: string, format: date}`,
		out:  "2020-05-14",
	},
	{
		name: "string-format-time",
		in:   `{type: string, format: time}`,
		out:  "23:44:51-07:00",
	},
	{
		name: "string-format-date-time",
		in:   `{type: string, format: date-time}`,
		out:  "2020-05-14T23:44:51-07:00",
	},
	{
		name: "string-format-duration",
		in:   `{type: string, format: duration}`,
		out:  "P30S",
	},
	{
		name: "string-format-email",
		in:   `{type: string, format: email}`,
		out:  "user@example.com",
	},
	{
		name: "string-format-hostname",
		in:   `{type: string, format: hostname}`,
		out:  "example.com",
	},
	{
		name: "string-format-ipv4",
		in:   `{type: string, format: ipv4}`,
		out:  "192.0.2.1",
	},
	{
		name: "string-format-ipv6",
		in:   `{type: string, format: ipv6}`,
		out:  "2001:db8::1",
	},
	{
		name: "string-format-uuid",
		in:   `{type: string, format: uuid}`,
		out:  "3e4666bf-d5e5-4aa7-b8ce-cefe41c7568a",
	},
	{
		name: "string-format-uri",
		in:   `{type: string, format: uri}`,
		out:  "https://example.com/",
	},
	{
		name: "string-format-uri-ref",
		in:   `{type: string, format: uri-reference}`,
		out:  "/example",
	},
	{
		name: "string-format-uri-template",
		in:   `{type: string, format: uri-template}`,
		out:  "https://example.com/{id}",
	},
	{
		name: "string-format-json-pointer",
		in:   `{type: string, format: json-pointer}`,
		out:  "/example/0/id",
	},
	{
		name: "string-format-rel-json-pointer",
		in:   `{type: string, format: relative-json-pointer}`,
		out:  "0/id",
	},
	{
		name: "string-format-regex",
		in:   `{type: string, format: regex}`,
		out:  "ab+c",
	},
	{
		name: "string-format-password",
		in:   `{type: string, format: password}`,
		out:  "********",
	},
	{
		name: "string-pattern",
		in:   `{type: string, pattern: "^[a-z]+$"}`,
		out:  "qne",
	},
	{
		name: "string-min-length",
		in:   `{type: string, minLength: 10}`,
		out:  "ssssssssss",
	},
	{
		name: "string-max-length",
		in:   `{type: string, maxLength: 3}`,
		out:  "sss",
	},
	{
		name: "string-enum",
		in:   `{type: string, enum: [one, two]}`,
		out:  "one",
	},
	{
		name: "empty-array",
		in:   `{type: array}`,
		out:  "[<any>]",
	},
	{
		name: "array",
		in:   `{type: array, items: {type: number}}`,
		out:  []any{1.0},
	},
	{
		name: "array-min-items",
		in:   `{type: array, items: {type: number}, minItems: 2}`,
		out:  []any{1.0, 1.0},
	},
	{
		name: "object-empty",
		in:   `{type: object}`,
		out:  map[string]any{},
	},
	{
		name: "object-prop-null",
		in:   `{type: object, properties: {foo: null}}`,
		out:  map[string]any{"foo": nil},
	},
	{
		name: "object",
		in:   `{type: object, properties: {foo: {type: string}, bar: {type: integer}}, required: [foo]}`,
		out: map[string]any{
			"foo": "string",
			"bar": 1,
		},
	},
	{
		name: "object-read-only",
		mode: modeRead,
		in:   `{type: object, properties: {foo: {type: string, readOnly: true}, bar: {type: string, writeOnly: true}}}`,
		out:  map[string]any{"foo": "string"},
	},
	{
		name: "object-write-only",
		mode: modeWrite,
		in:   `{type: object, properties: {foo: {type: string, readOnly: true}, bar: {type: string, writeOnly: true}}}`,
		out:  map[string]any{"bar": "string"},
	},
	{
		name: "object-additional-props-bool",
		in:   `{type: object, additionalProperties: true}`,
		out:  map[string]any{"<any>": nil},
	},
	{
		name: "object-additional-props-scehma",
		in:   `{type: object, additionalProperties: {type: string}}`,
		out:  map[string]any{"<any>": "string"},
	},
	{
		name: "all-of",
		in:   `{allOf: [{type: object, properties: {a: {type: string}}}, {type: object, properties: {foo: {type: string}, bar: {type: number, description: desc}}}]}`,
		out: map[string]any{
			"a":   "string",
			"bar": 1.0,
			"foo": "string",
		},
	},
	{
		name: "one-of",
		in:   `{oneOf: [{type: boolean}, {type: object, properties: {foo: {type: string}, bar: {type: number, description: desc}}}]}`,
		out:  true,
	},
	{
		name: "any-of",
		in:   `{anyOf: [{type: boolean}, {type: object, properties: {foo: {type: string}, bar: {type: number, description: desc}}}]}`,
		out:  true,
	},
	{
		name: "recusive-prop",
		in:   `{type: object, properties: {person: {type: object, properties: {friend: {$ref: "#/properties/person"}}}}}`,
		out: map[string]any{
			"person": map[string]any{
				"friend": nil,
			},
		},
	},
	{
		name: "recusive-array",
		in:   `{type: object, properties: {person: {type: object, properties: {friend: {type: array, items: {$ref: "#/properties/person"}}}}}}`,
		out: map[string]any{
			"person": map[string]any{
				"friend": []any{nil},
			},
		},
	},
	{
		name: "recusive-additional-props",
		in:   `{type: object, properties: {person: {type: object, properties: {friend: {type: object, additionalProperties: {$ref: "#/properties/person"}}}}}}`,
		out: map[string]any{
			"person": map[string]any{
				"friend": map[string]any{
					"<any>": nil,
				},
			},
		},
	},
}

func TestExample(t *testing.T) {
	for _, example := range exampleTests {
		t.Run(example.name, func(t *testing.T) {
			var rootNode yaml.Node
			var ls lowbase.Schema

			require.NoError(t, yaml.Unmarshal([]byte(example.in), &rootNode))
			require.NoError(t, low.BuildModel(rootNode.Content[0], &ls))
			require.NoError(t, ls.Build(rootNode.Content[0], index.NewSpecIndex(&rootNode)))

			// spew.Dump(ls)

			s := base.NewSchema(&ls)
			assert.EqualValues(t, example.out, genExample(s, example.mode))
		})
	}
}
