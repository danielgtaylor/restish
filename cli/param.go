package cli

import (
	"fmt"
	"log"
	"reflect"

	"github.com/iancoleman/strcase"
	"github.com/spf13/pflag"
)

// Style is an encoding style for parameters.
type Style int

const (
	// StyleSimple corresponds to OpenAPI 3 simple parameters
	StyleSimple Style = iota

	// StyleForm corresponds to OpenAPI 3 form parameters
	StyleForm
)

func typeConvert(from, to interface{}) interface{} {
	return reflect.ValueOf(from).Convert(reflect.TypeOf(to)).Interface()
}

// Param represents an API operation input parameter.
type Param struct {
	Type        string      `json:"type"`
	Name        string      `json:"name"`
	DisplayName string      `json:"displayName,omitempty"`
	Description string      `json:"description,omitempty"`
	Style       Style       `json:"style,omitempty"`
	Explode     bool        `json:"explode,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Example     interface{} `json:"example,omitempty"`
}

// Parse the parameter from a string input (e.g. command line argument)
func (p Param) Parse(value string) (interface{}, error) {
	// TODO: parse based on the type, used mostly for path parameter parsing
	// which is almost always a string anyway.
	return value, nil
}

// Serialize the parameter based on the type/style/explode configuration.
func (p Param) Serialize(value interface{}) []string {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
		value = v.Interface()
	}

	switch p.Type {
	case "boolean", "integer", "number", "string":
		switch p.Style {
		case StyleForm:
			return []string{fmt.Sprintf("%s=%v", p.Name, value)}
		case StyleSimple:
			return []string{fmt.Sprintf("%v", value)}
		}

	case "array[boolean]", "array[integer]", "array[number]", "array[string]":
		tmp := []string{}
		switch p.Style {
		case StyleForm:
			for i := 0; i < v.Len(); i++ {
				item := v.Index(i)
				if p.Explode {
					tmp = append(tmp, fmt.Sprintf("%v", item.Interface()))
				} else {
					if len(tmp) == 0 {
						tmp = append(tmp, "")
					}

					tmp[0] += fmt.Sprintf("%v", item.Interface())
					if i < v.Len()-1 {
						tmp[0] += ","
					}
				}
			}
		case StyleSimple:
			tmp = append(tmp, "")
			for i := 0; i < v.Len(); i++ {
				item := v.Index(i)
				tmp[0] += fmt.Sprintf("%v", item.Interface())
				if i < v.Len()-1 {
					tmp[0] += ","
				}
			}
		}
		return tmp
	}

	return nil
}

// OptionName returns the commandline option name for this parameter.
func (p Param) OptionName() string {
	name := p.Name
	if p.DisplayName != "" {
		name = p.DisplayName
	}
	return strcase.ToDelimited(name, '-')
}

// AddFlag adds a new option flag to a command's flag set for this parameter.
func (p Param) AddFlag(flags *pflag.FlagSet) interface{} {
	name := p.OptionName()
	def := p.Default

	switch p.Type {
	case "boolean":
		if def == nil {
			def = false
		}
		return flags.Bool(name, def.(bool), p.Description)
	case "integer":
		if def == nil {
			def = 0
		}
		return flags.Int(name, typeConvert(def, 0).(int), p.Description)
	case "number":
		if def == nil {
			def = 0.0
		}
		return flags.Float64(name, typeConvert(def, float64(0.0)).(float64), p.Description)
	case "string":
		if def == nil {
			def = ""
		}
		return flags.String(name, def.(string), p.Description)
	case "array[boolean]":
		if def == nil {
			def = []bool{}
		}
		return flags.BoolSlice(name, def.([]bool), p.Description)
	case "array[integer]":
		if def == nil {
			def = []int{}
		}
		return flags.IntSlice(name, def.([]int), p.Description)
	case "array[number]":
		log.Printf("number slice not implemented for param %s", p.Name)
		return nil
	// Float slices aren't implemented in the pflag package...
	// if def == nil {
	// 	def = []float64{}
	// }
	// return flags.Float64Slice(p.Name, def.([]float64), p.Description)
	case "array[string]":
		if def == nil {
			def = []string{}
		} else {
			tmp := []string{}
			for _, item := range def.([]interface{}) {
				tmp = append(tmp, item.(string))
			}
			def = tmp
		}
		return flags.StringSlice(name, def.([]string), p.Description)
	}

	return nil
}
