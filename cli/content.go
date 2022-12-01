package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/alexeyco/simpletable"
	"github.com/amzn/ion-go/ion"
	"github.com/fxamacker/cbor/v2"
	"github.com/shamaton/msgpack/v2"
	"gopkg.in/yaml.v2"
)

// ContentType is used to marshal/unmarshal data to various formats.
type ContentType interface {
	Detect(contentType string) bool
	Marshal(value interface{}) ([]byte, error)
	Unmarshal(data []byte, value interface{}) error
}

// PrettyMarshaller describes an optional method that ContentTypes can implement
// to provide nicer output for humans. This is optional because only some
// formats support the pretty/indented concept.
type PrettyMarshaller interface {
	MarshalPretty(value any) ([]byte, error)
}

type contentTypeEntry struct {
	name string
	q    float32
	ct   ContentType
}

// contentTypes is a list of acceptable content types
var contentTypes map[string]contentTypeEntry = map[string]contentTypeEntry{}

// AddContentType adds a new content type marshaller with the given default
// content type name and q factor (0-1.0, higher has priority).
func AddContentType(short, name string, q float32, ct ContentType) {
	contentTypes[short] = contentTypeEntry{
		name: name,
		q:    q,
		ct:   ct,
	}
}

func buildAcceptHeader() string {
	accept := []string{}

	for _, entry := range contentTypes {
		if entry.q >= 0 {
			accept = append(accept, fmt.Sprintf("%s;q=%.3g", entry.name, entry.q))
		}
	}

	accept = append(accept, "*/*")

	return strings.Join(accept, ",")
}

// Marshal a value to the given content type, e.g. `application/json`.
func Marshal(contentType string, value interface{}) ([]byte, error) {
	for _, entry := range contentTypes {
		if entry.ct.Detect(contentType) {
			return entry.ct.Marshal(value)
		}
	}

	return nil, fmt.Errorf("cannot marshal %s", contentType)
}

// MarshalShort marshals a value given a short name, e.g. `json`. If pretty is
// true then the output will be pretty/indented if the marshaler supports
// pretty output (e.g. JSON).
func MarshalShort(name string, pretty bool, value any) ([]byte, error) {
	var encoded []byte
	if cte, ok := contentTypes[name]; ok {
		var err error

		if pm, ok := cte.ct.(PrettyMarshaller); ok && pretty {
			encoded, err = pm.MarshalPretty(value)
		} else {
			encoded, err = cte.ct.Marshal(value)
		}
		if err != nil {
			return nil, err
		}
		if encoded[len(encoded)-1] != '\n' {
			encoded = append(encoded, '\n')
		}
	} else {
		return nil, fmt.Errorf("unknown format %s", name)
	}

	return encoded, nil
}

// Unmarshal raw data from the given content type into a value.
func Unmarshal(contentType string, data []byte, value interface{}) error {
	for _, entry := range contentTypes {
		if entry.ct.Detect(contentType) {
			LogDebug("Unmarshalling from %s", entry.name)
			return entry.ct.Unmarshal(data, value)
		}
	}

	return fmt.Errorf("cannot unmarshal %s", contentType)
}

type stringer interface {
	String() string
}

// Text describes content types like `text/plain` or `text/html`.
type Text struct{}

// Detect if the content type is text.
func (t Text) Detect(contentType string) bool {
	if strings.HasPrefix(contentType, "text/") {
		return true
	}

	// Other known text formats
	known := []string{
		"application/javascript",
	}
	for _, ct := range known {
		if strings.Contains(contentType, ct) {
			return true
		}
	}

	return false
}

// Marshal the value to a text string.
func (t Text) Marshal(value interface{}) ([]byte, error) {
	if s, ok := value.(string); ok {
		return []byte(s), nil
	}

	if s, ok := value.(stringer); ok {
		return []byte(s.String()), nil
	}

	return []byte(fmt.Sprintf("%v", value)), nil
}

// Unmarshal the value from a text string.
func (t Text) Unmarshal(data []byte, value interface{}) error {
	v := reflect.ValueOf(value)

	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("value must be pointer but found %s", v.Kind())
	}

	if !v.Elem().CanSet() {
		return fmt.Errorf("interface value cannot be set")
	}

	v.Elem().Set(reflect.ValueOf(string(data)))
	return nil
}

// Table describes an output format for terminal tables.
type Table struct{}

// Detect if the content type is table.
func (t Table) Detect(contentType string) bool {
	return false
}

// Marshal the value to a table string.
func (t Table) Marshal(value interface{}) ([]byte, error) {
	d, ok := makeJSONSafe(value).([]interface{})
	if !ok {
		return nil, fmt.Errorf("error building table. Must be array of objects")
	}

	return setTable(d)
}

// Unmarshal the value from a table string.
func (t Table) Unmarshal(data []byte, value interface{}) error {
	return fmt.Errorf("unimplemented")
}

// Only applicable to collection of repeating objects.
// Filter down to a collection of objects first then apply the table output.
// Simpletable has much more styling that can be applied.
func setTable(data []interface{}) ([]byte, error) {
	table := simpletable.New()

	var headerCells []*simpletable.Cell
	defineHeader := true
	for _, maps := range data {
		var bodyCells []*simpletable.Cell
		if mapData, ok := maps.(map[string]interface{}); ok {
			// Discover headers for repeating objects
			// Iterate first instance of one of the repeating objects
			if defineHeader {
				for k := range mapData {
					headerCells = append(headerCells, &simpletable.Cell{Align: simpletable.AlignCenter, Text: k})
				}
				sort.Slice(headerCells, func(i, j int) bool {
					return headerCells[i].Text < headerCells[j].Text
				})
			}
			defineHeader = false

			// Add body cells based on order of header cells
			// Will get out of order otherwise
			for _, cellKey := range headerCells {
				if val, ok := mapData[cellKey.Text]; ok {
					if s, ok := val.([]any); ok {
						converted := make([]string, len(s))
						for i := 0; i < len(s); i++ {
							converted[i] = fmt.Sprintf("%v", s[i])
						}
						val = strings.Join(converted, ", ")
					}
					bodyCells = append(bodyCells, &simpletable.Cell{Align: simpletable.AlignRight, Text: fmt.Sprintf("%v", val)})
				} else {
					return nil, fmt.Errorf("error building table. Header Key not found in repeating object: %s", cellKey.Text)
				}
			}
			table.Body.Cells = append(table.Body.Cells, bodyCells)
		} else {
			// Defensive just in case
			return nil, errors.New("error building table. Collection not supported")
		}
	}

	table.Header = &simpletable.Header{
		Cells: headerCells,
	}

	table.SetStyle(simpletable.StyleUnicode)

	ret := []byte(table.String())
	return ret, nil
}

// JSON describes content types like `application/json` or
// `application/problem+json`.
type JSON struct{}

// Detect if the content type is JSON.
func (j JSON) Detect(contentType string) bool {
	first := strings.Split(contentType, ";")[0]
	if first == "application/json" || strings.HasSuffix(first, "+json") {
		return true
	}

	return false
}

// Marshal the value to encoded JSON.
func (j JSON) Marshal(value interface{}) ([]byte, error) {
	// The default encoder escapes '<', '>', and '&' which we don't want
	// since we are not a browser. Disable this with an encoder instance.
	// See https://stackoverflow.com/a/28596225/164268
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(makeJSONSafe(value)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MarshalPretty the value to pretty encoded JSON.
func (j JSON) MarshalPretty(value interface{}) ([]byte, error) {
	// The default encoder escapes '<', '>', and '&' which we don't want
	// since we are not a browser. Disable this with an encoder instance.
	// See https://stackoverflow.com/a/28596225/164268
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(makeJSONSafe(value)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal the value from encoded JSON.
func (j JSON) Unmarshal(data []byte, value interface{}) error {
	return json.Unmarshal(data, value)
}

// YAML describes content types like `application/yaml` or
// `application/foo+yaml`.
type YAML struct{}

// Detect if the content type is YAML.
func (y YAML) Detect(contentType string) bool {
	first := strings.Split(contentType, ";")[0]
	if first == "application/yaml" || first == "application/x-yaml" || first == "text/yaml" || strings.HasSuffix(first, "+yaml") {
		return true
	}

	return false
}

// Marshal the value to encoded YAML.
func (y YAML) Marshal(value interface{}) ([]byte, error) {
	return yaml.Marshal(value)
}

// Unmarshal the value from encoded YAML.
func (y YAML) Unmarshal(data []byte, value interface{}) error {
	return yaml.Unmarshal(data, value)
}

// CBOR describes content types like `application/cbor` or
// `application/foo+cbor`. http://cbor.io/
type CBOR struct{}

// Detect if the content type is YAML.
func (c CBOR) Detect(contentType string) bool {
	first := strings.Split(contentType, ";")[0]
	if first == "application/cbor" || strings.HasSuffix(first, "+cbor") {
		return true
	}

	return false
}

// Marshal the value to encoded YAML.
func (c CBOR) Marshal(value interface{}) ([]byte, error) {
	return cbor.Marshal(value)
}

// Unmarshal the value from encoded YAML.
func (c CBOR) Unmarshal(data []byte, value interface{}) error {
	return cbor.Unmarshal(data, value)
}

// MsgPack describes content types like `application/msgpack` or
// `application/foo+msgpack`. https://msgpack.org/
type MsgPack struct{}

// Detect if the content type is YAML.
func (m MsgPack) Detect(contentType string) bool {
	first := strings.Split(contentType, ";")[0]
	if first == "application/msgpack" || first == "application/x-msgpack" || first == "application/vnd.msgpack" || strings.HasSuffix(first, "+msgpack") {
		return true
	}

	return false
}

// Marshal the value to encoded YAML.
func (m MsgPack) Marshal(value interface{}) ([]byte, error) {
	return msgpack.Marshal(value)
}

// Unmarshal the value from encoded YAML.
func (m MsgPack) Unmarshal(data []byte, value interface{}) error {
	return msgpack.Unmarshal(data, value)
}

// Ion describes content types like `application/ion`.
type Ion struct{}

// Detect if the content type is Ion.
func (i Ion) Detect(contentType string) bool {
	first := strings.Split(contentType, ";")[0]
	if first == "application/ion" || strings.HasSuffix(first, "+ion") {
		return true
	}

	return false
}

// Marshal the value to encoded binary Ion.
func (i Ion) Marshal(value interface{}) ([]byte, error) {
	return ion.MarshalBinary(makeJSONSafe(value))
}

// MarshalPretty the value to pretty encoded JSON.
func (i Ion) MarshalPretty(value interface{}) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	tw := ion.NewTextWriterOpts(buf, ion.TextWriterPretty|ion.TextWriterQuietFinish)
	err := ion.MarshalTo(tw, makeJSONSafe(value))
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal the value form encoded binary or text Ion.
func (i Ion) Unmarshal(data []byte, value interface{}) error {
	return ion.Unmarshal(data, value)
}

// Readable describes a readable marshaller.
type Readable struct{}

// Detect if the content type is Ion.
func (r Readable) Detect(contentType string) bool {
	return false
}

// Marshal the value to encoded binary Ion.
func (r Readable) Marshal(value interface{}) ([]byte, error) {
	return MarshalReadable(value)
}

// Unmarshal the value form encoded binary or text Ion.
func (i Readable) Unmarshal(data []byte, value interface{}) error {
	return fmt.Errorf("unimplemented")
}
