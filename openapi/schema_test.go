package openapi

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
)

func TestSchemaGuessArray(t *testing.T) {
	s := &openapi3.Schema{
		Items: &openapi3.SchemaRef{
			Value: &openapi3.Schema{
				Type: "string",
			},
		},
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "[\n  (string) \n]", out)
}

func TestSchemaGuessObject(t *testing.T) {
	tr := true
	s := &openapi3.Schema{
		AdditionalPropertiesAllowed: &tr,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "{\n  <any>: <any>\n}", out)
}

func TestValidatorNullable(t *testing.T) {
	s := &openapi3.Schema{
		Type:     "boolean",
		Nullable: true,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(boolean nullable:true) ", out)
}

func TestValidatorMin(t *testing.T) {
	min := 5.0
	s := &openapi3.Schema{
		Type: "number",
		Min:  &min,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(number min:5) ", out)
}

func TestValidatorExclusiveMin(t *testing.T) {
	min := 5.0
	s := &openapi3.Schema{
		Type:         "number",
		Min:          &min,
		ExclusiveMin: true,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(number exclusiveMin:5) ", out)
}

func TestValidatorMax(t *testing.T) {
	max := 5.0
	s := &openapi3.Schema{
		Type: "number",
		Max:  &max,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(number max:5) ", out)
}

func TestValidatorExclusiveMax(t *testing.T) {
	max := 5.0
	s := &openapi3.Schema{
		Type:         "number",
		Max:          &max,
		ExclusiveMax: true,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(number exclusiveMax:5) ", out)
}

func TestValidatorMultiple(t *testing.T) {
	value := 5.0
	s := &openapi3.Schema{
		Type:       "number",
		MultipleOf: &value,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(number multiple:5) ", out)
}

func TestValidatorDefault(t *testing.T) {
	s := &openapi3.Schema{
		Type:    "number",
		Default: 5,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(number default:5) ", out)
}

func TestValidatorFormat(t *testing.T) {
	s := &openapi3.Schema{
		Type:   "string",
		Format: "date",
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(string format:date) ", out)
}

func TestValidatorPattern(t *testing.T) {
	s := &openapi3.Schema{
		Type:    "string",
		Pattern: "^[a-z]+$",
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(string pattern:^[a-z]+$) ", out)
}

func TestValidatorMinLength(t *testing.T) {
	s := &openapi3.Schema{
		Type:      "string",
		MinLength: 5,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(string minLen:5) ", out)
}

func TestValidatorMaxLength(t *testing.T) {
	var max uint64 = 5
	s := &openapi3.Schema{
		Type:      "string",
		MaxLength: &max,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(string maxLen:5) ", out)
}

func TestValidatorEnum(t *testing.T) {
	s := &openapi3.Schema{
		Type: "string",
		Enum: []interface{}{"one", "two"},
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(string enum:one,two) ", out)
}

func TestSchemaEmptyObject(t *testing.T) {
	s := &openapi3.Schema{
		Type: "object",
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "(object)", out)
}

func TestSchemaAdditionalProps(t *testing.T) {
	tr := true
	s := &openapi3.Schema{
		Type:                        "object",
		AdditionalPropertiesAllowed: &tr,
	}

	out := renderSchema(s, "", modeRead)
	assert.Equal(t, "{\n  <any>: <any>\n}", out)
}
