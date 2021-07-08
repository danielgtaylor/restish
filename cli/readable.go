package cli

import (
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MarshalReadable marshals a value into a human-friendly readable format.
func MarshalReadable(v interface{}) ([]byte, error) {
	return marshalReadable("", v)
}

func marshalReadable(indent string, v interface{}) ([]byte, error) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Invalid:
		return []byte("null"), nil
	case reflect.Ptr:
		if rv.IsZero() {
			return []byte("null"), nil
		}

		return marshalReadable(indent, rv.Elem().Interface())
	case reflect.Bool:
		if v.(bool) {
			return []byte("true"), nil
		}

		return []byte("false"), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		i := rv.Convert(reflect.TypeOf(int64(0))).Interface().(int64)
		return []byte(strconv.FormatInt(i, 10)), nil
	case reflect.Float32, reflect.Float64:
		// Copied from https://golang.org/src/encoding/json/encode.go
		f := rv.Float()
		abs := math.Abs(f)
		fmtByte := byte('f')
		bits := 64
		if rv.Kind() == reflect.Float32 {
			bits = 32
		}
		if abs != 0 {
			if bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
				fmtByte = 'e'
			}
		}
		b := []byte(strconv.FormatFloat(f, fmtByte, -1, bits))
		if fmtByte == 'e' {
			// clean up e-09 to e-9
			n := len(b)
			if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
				b[n-2] = b[n-1]
				b = b[:n-1]
			}
		}
		return b, nil
	case reflect.String:
		// Escape quotes
		s := strings.Replace(v.(string), `"`, `\"`, -1)

		// Trim trailing newlines & add indentation
		s = strings.TrimRight(s, "\n")
		s = strings.Replace(s, "\n", "\n  "+indent, -1)

		return []byte(`"` + s + `"`), nil
	case reflect.Array:
		return marshalReadable(indent, rv.Slice(0, rv.Len()).Interface())
	case reflect.Slice:
		// Special case: empty slice should go in-line.
		if rv.Len() == 0 {
			return []byte("[]"), nil
		}

		// Detect binary []byte values and display the first few bytes as hex,
		// since that is easier to process in your head than base64.
		if binary, ok := v.([]byte); ok {
			suffix := ""
			if len(binary) > 10 {
				binary = binary[:10]
				suffix = "..."
			}
			return []byte("0x" + hex.EncodeToString(binary) + suffix), nil
		}

		// Otherwise, print out the slice.
		length := 0
		hasNewlines := false
		lines := []string{}
		for i := 0; i < rv.Len(); i++ {
			encoded, err := marshalReadable(indent+"  ", rv.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			length += len(encoded) // TODO: handle multi-byte runes?
			if strings.Contains(string(encoded), "\n") {
				hasNewlines = true
			}
			lines = append(lines, string(encoded))
		}

		s := ""
		if !hasNewlines && len(indent)+(len(lines)*2)+length < 80 {
			// Special-case: short array gets inlined like [1, 2, 3]
			s += "[" + strings.Join(lines, ", ") + "]"
		} else {
			s += "[\n" + indent + "  " + strings.Join(lines, ",\n  "+indent) + "\n" + indent + "]"
		}

		return []byte(s), nil
	case reflect.Map:
		// Special case: empty map should go in-line
		if rv.Len() == 0 {
			return []byte("{}"), nil
		}

		m := "{\n"

		// Sort the keys
		keys := rv.MapKeys()
		stringKeys := []string{}
		reverse := map[string]reflect.Value{}
		for _, k := range keys {
			ks := fmt.Sprintf("%v", k)
			stringKeys = append(stringKeys, ks)
			reverse[ks] = k
		}

		sort.Strings(stringKeys)

		// Write out each key/value pair.
		for _, k := range stringKeys {
			v := rv.MapIndex(reverse[k])
			encoded, err := marshalReadable(indent+"  ", v.Interface())
			if err != nil {
				return nil, err
			}
			m += indent + "  " + k + ": " + string(encoded) + ",\n"
		}

		m += indent + "}"

		return []byte(m), nil
	case reflect.Struct:
		if t, ok := v.(time.Time); ok {
			if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
				// Special case: date only
				return []byte(t.UTC().Format("2006-01-02")), nil
			}
			return []byte(t.UTC().Format(time.RFC3339Nano)), nil
		}

		// TODO: user-defined structs, go through each field.
	}

	return nil, fmt.Errorf("unknown kind %s", rv.Kind())
}
