package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/fxamacker/cbor/v2"
	"github.com/shamaton/msgpack"
	"gopkg.in/yaml.v2"
)

// ContentType is used to marshal/unmarshal data to various formats.
type ContentType interface {
	Detect(contentType string) bool
	Marshal(value interface{}) ([]byte, error)
	Unmarshal(data []byte, value interface{}) error
}

type contentTypeEntry struct {
	name string
	q    float32
	ct   ContentType
}

// contentTypes is a list of acceptable content types
var contentTypes []contentTypeEntry = []contentTypeEntry{}

// AddContentType adds a new content type marshaller with the given default
// content type name and q factor (0-1.0, higher has priority).
func AddContentType(name string, q float32, ct ContentType) {
	contentTypes = append(contentTypes, contentTypeEntry{
		name: name,
		q:    q,
		ct:   ct,
	})
}

func buildAcceptHeader() string {
	accept := []string{}

	for _, entry := range contentTypes {
		accept = append(accept, fmt.Sprintf("%s;q=%.3g", entry.name, entry.q))
	}

	accept = append(accept, "*/*")

	return strings.Join(accept, ",")
}

// Marshal a value to the given content type if possible.
func Marshal(contentType string, value interface{}) ([]byte, error) {
	for _, entry := range contentTypes {
		if entry.ct.Detect(contentType) {
			return entry.ct.Marshal(value)
		}
	}

	return nil, fmt.Errorf("cannot marshal %s", contentType)
}

// Unmarshal raw data from the given content type into a value.
func Unmarshal(contentType string, data []byte, value interface{}) error {
	for _, entry := range contentTypes {
		if entry.ct.Detect(contentType) {
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
	return strings.HasPrefix(contentType, "text/")
}

// Marshal the value to a text string.
func (t Text) Marshal(value interface{}) ([]byte, error) {
	if s, ok := value.(stringer); ok {
		return []byte(s.String()), nil
	}

	return nil, fmt.Errorf("cannot convert to string: %v", value)
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
	return json.Marshal(value)
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
	if first == "application/yaml" || strings.HasSuffix(first, "+yaml") {
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
	return msgpack.Encode(value)
}

// Unmarshal the value from encoded YAML.
func (m MsgPack) Unmarshal(data []byte, value interface{}) error {
	return msgpack.Decode(data, value)
}
