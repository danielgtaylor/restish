package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
)

// PathBuffer is a low-allocation helper for building a path string like
// `foo.bar[2].baz`. It is not goroutine-safe, but the underlying buffer can
// be re-used within the same goroutine or via a `sync.Pool`.
type PathBuffer struct {
	buf []byte
	off int
}

func (b *PathBuffer) Push(s string) {
	if b.off > 0 && s[0] != '[' {
		b.buf = append(b.buf, '.')
		b.off++
	}
	b.buf = append(b.buf, s...)
	b.off += len(s)
}

func (b *PathBuffer) Pop() {
	for b.off > 0 {
		b.off--
		if b.buf[b.off] == '.' || b.buf[b.off] == '[' {
			break
		}
	}
	b.buf = b.buf[:b.off]
}

func (b *PathBuffer) Bytes() []byte {
	return b.buf[:b.off]
}

// NewPathBuffer creates a new path buffer with the given underlying byte slice
// and offset within that slice (for pre-loading with some path data).
func NewPathBuffer(buf []byte, offset int) *PathBuffer {
	return &PathBuffer{buf: buf, off: offset}
}

// validFirstRune returns true for runes that are valid
// as the first rune in an identifier.
func validFirstRune(r rune) bool {
	return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lm, unicode.Lo, unicode.Nl) || r == '$' || r == '_'
}

// identifier returns a JS-safe identifier string.
func identifier(s string) string {
	valid := true
	for i, r := range s {
		if i == 0 {
			valid = validFirstRune(r)
		} else {
			valid = validFirstRune(r) || unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc)
		}
		if !valid {
			break
		}
	}
	if valid {
		return s
	}

	return fmt.Sprintf(`["%s"]`, s)
}

// keyStr returns a string representation of a map key.
func keyStr(v reflect.Value) string {
	if v.Kind() == reflect.String {
		return v.String()
	}
	return fmt.Sprintf(`%v`, v.Interface())
}

// apnd appends any number of strings or byte slices to a byte slice.
func apnd(buf []byte, what ...any) []byte {
	for _, b := range what {
		if v, ok := b.([]byte); ok {
			buf = append(buf, v...)
		} else if v, ok := b.(string); ok {
			buf = append(buf, v...)
		}
	}
	return buf
}

func marshalGron(pb *PathBuffer, data any, isAnon bool, out []byte) ([]byte, error) {
	var err error

	v := reflect.Indirect(reflect.ValueOf(data))
	switch v.Kind() {
	case reflect.Struct:
		// Special case: time.Time!
		if v.Type() == reflect.TypeOf(time.Time{}) {
			out = apnd(out, pb.Bytes(), " = \"", v.Interface().(time.Time).Format(time.RFC3339Nano), "\";\n")
			break
		}

		if !isAnon {
			// Special case: anonymous embedded structs should not result in
			// redefinition of the parent's base type.
			out = apnd(out, pb.Bytes(), " = {};\n")
		}

		// Fields are output in definition order, including embedded structs. Field
		// overrides are not supported and will result in multiple output
		// definitions. The `omitempty` tag is ignored just to make grepping
		// for zero values easier.
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			ft := v.Type().Field(i)
			if !ft.IsExported() {
				// Ignore unexported (i.e. private) fields.
				continue
			}
			anon := false
			if ft.Anonymous {
				anon = true
			} else {
				// Try to determine the name using the standard Go rules.
				name := ft.Name
				if tag := ft.Tag.Get("json"); tag != "" {
					if tag == "-" {
						continue
					}
					name = strings.Split(tag, ",")[0]
				}
				pb.Push(identifier(name))
			}
			if out, err = marshalGron(pb, field.Interface(), anon, out); err != nil {
				return nil, err
			}
			if !anon {
				pb.Pop()
			}
		}
	case reflect.Map:
		out = apnd(out, pb.Bytes(), " = {};\n")
		keys := v.MapKeys()
		// Maps are output in sorted alphanum order.
		sort.Slice(keys, func(i, j int) bool {
			return keyStr(keys[i]) < keyStr(keys[j])
		})
		for _, key := range keys {
			pb.Push(identifier(keyStr(key)))
			if out, err = marshalGron(pb, v.MapIndex(key).Interface(), false, out); err != nil {
				return nil, err
			}
			pb.Pop()
		}
	case reflect.Slice:
		// Special case: []byte
		if v.Type().Elem().Kind() == reflect.Uint8 {
			out = apnd(out, pb.Bytes(), " = \"", base64.StdEncoding.EncodeToString(v.Bytes()), "\";\n")
			break
		}

		out = apnd(out, pb.Bytes(), " = [];\n")
		for i := 0; i < v.Len(); i++ {
			pb.Push(fmt.Sprintf("[%d]", i))
			if out, err = marshalGron(pb, v.Index(i).Interface(), false, out); err != nil {
				return nil, err
			}
			pb.Pop()
		}
	default:
		// This is a primitive type, just take the JSON representation.
		// The default encoder escapes '<', '>', and '&' which we don't want
		// since we are not a browser. Disable this with an encoder instance.
		// See https://stackoverflow.com/a/28596225/164268
		buf := &bytes.Buffer{}
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(makeJSONSafe(data)); err != nil {
			return nil, err
		}
		// Note: encoder adds it's own ending newline we need to strip out.
		b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
		out = apnd(out, pb.Bytes(), " = ", b, ";\n")
	}

	return out, nil
}
