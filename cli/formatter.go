package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/alecthomas/chroma"
	"github.com/alecthomas/chroma/quick"
	"github.com/alecthomas/chroma/styles"
	jmespath "github.com/danielgtaylor/go-jmespath-plus"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"

	"github.com/alexeyco/simpletable"
	"github.com/eliukblau/pixterm/pkg/ansimage"
)

func init() {
	// Simple 256-color theme for JSON/YAML output in a terminal.
	styles.Register(chroma.MustNewStyle("cli-dark", chroma.StyleEntries{
		// Used for JSON/YAML/Readable
		chroma.Comment:      "#9e9e9e",
		chroma.Keyword:      "#ff5f87",
		chroma.Punctuation:  "#9e9e9e",
		chroma.NameTag:      "#5fafd7",
		chroma.Number:       "#d78700",
		chroma.String:       "#afd787",
		chroma.StringSymbol: "italic #D6FFB7",
		chroma.Date:         "#af87af",
		chroma.NumberHex:    "#ffd7d7",

		// Used for HTTP
		chroma.Name:          "#5fafd7",
		chroma.NameFunction:  "#ff5f87",
		chroma.NameNamespace: "#b2b2b2",

		// Used for Markdown
		chroma.GenericHeading:    "#5fafd7",
		chroma.GenericSubheading: "#5fafd7",
		chroma.GenericEmph:       "italic #ffd7d7",
		chroma.GenericStrong:     "bold #af87af",
		chroma.GenericDeleted:    "#3a3a3a",
		chroma.NameAttribute:     "underline",
	}))
}

// makeJSONSafe walks an interface to ensure all maps use string keys so that
// encoding to JSON (or YAML) works. Some unmarshallers (e.g. CBOR) will
// create map[interface{}]interface{} which causes problems marshalling.
// See https://github.com/fxamacker/cbor/issues/206
func makeJSONSafe(obj interface{}) interface{} {
	value := reflect.ValueOf(obj)

	switch value.Kind() {
	case reflect.Slice:
		returnSlice := make([]interface{}, value.Len())
		for i := 0; i < value.Len(); i++ {
			returnSlice[i] = makeJSONSafe(value.Index(i).Interface())
		}
		return returnSlice
	case reflect.Map:
		tmpData := make(map[string]interface{})
		for _, k := range value.MapKeys() {
			kStr := ""
			if s, ok := k.Interface().(string); ok {
				kStr = s
			} else {
				kStr = fmt.Sprintf("%v", k.Interface())
			}
			tmpData[kStr] = makeJSONSafe(value.MapIndex(k).Interface())
		}
		return tmpData
	// case reflect.Struct:
	// 	for i := 0; i < value.NumField(); i++ {
	// 		field := value.Field(i)
	// 		spew.Dump(field, field.Kind(), field.CanSet())
	// 		switch field.Kind() {
	// 		case reflect.Slice, reflect.Map, reflect.Struct, reflect.Ptr:
	// 			if field.CanSet() {
	// 				field.Set(reflect.ValueOf(makeJSONSafe(field.Interface())))
	// 			}
	// 		}
	// 	}
	case reflect.Ptr:
		return makeJSONSafe(value.Elem().Interface())
	}

	return obj
}

// Highlight a block of data with the given lexer.
func Highlight(lexer string, data []byte) ([]byte, error) {
	sb := &strings.Builder{}
	if err := quick.Highlight(sb, string(data), lexer, "terminal256", "cli-dark"); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}

// ResponseFormatter will filter, prettify, and print out the results of a call.
type ResponseFormatter interface {
	Format(Response) error
}

// DefaultFormatter can apply JMESPath queries and can output prettyfied JSON
// and YAML output. If Stdout is a TTY, then colorized output is provided. The
// default formatter uses the `rsh-filter` and `rsh-output-format` configuration
// values to perform JMESPath queries and set JSON (default) or YAML output.
type DefaultFormatter struct {
	tty bool
}

// NewDefaultFormatter creates a new formatted with autodetected TTY
// capabilities.
func NewDefaultFormatter(tty bool) *DefaultFormatter {
	return &DefaultFormatter{
		tty: tty,
	}
}

// Format will filter, prettify, colorize and output the data.
func (f *DefaultFormatter) Format(resp Response) error {
	outFormat := viper.GetString("rsh-output-format")

	var data interface{} = resp.Map()

	filter := viper.GetString("rsh-filter")
	if filter != "" {
		// JMESPath can't support maps with arbitrary key types, so we convert
		// to map[string]interface{} before filtering.
		data = makeJSONSafe(data)
		result, err := jmespath.Search(filter, data)

		if err != nil {
			return err
		}

		if outFormat == "auto" {
			// Filtering in auto mode means we just return JSON
			outFormat = "json"
		}

		if result == nil {
			return nil
		}

		data = result
	}

	// Encode to the requested output format using nice formatting.
	var encoded []byte
	var err error
	var lexer string

	handled := false
	kind := reflect.ValueOf(data).Kind()

	// Handle table formatting
	if viper.GetBool("rsh-table") && kind == reflect.Slice {
		d, ok := data.([]interface{})
		if ok {
			ret, err := setTable(d)
			if err != nil {
				return err
			}
			encoded = *ret
			handled = true
		} else {
			return errors.New("error building table. Collection not supported. Must be array of objects")
		}
	}

	if viper.GetBool("rsh-raw") && kind == reflect.String {
		handled = true
		dStr := data.(string)
		encoded = []byte(dStr)
		lexer = ""

		if len(dStr) != 0 && (dStr[0] == '{' || dStr[0] == '[') {
			// Looks like JSON to me!
			lexer = "json"
		}
	} else if viper.GetBool("rsh-raw") && kind == reflect.Slice {
		scalars := true

		for _, item := range data.([]interface{}) {
			switch item.(type) {
			case nil, bool, int, int64, float64, string:
				// The above are scalars used by decoders
			default:
				scalars = false
			}
		}

		if scalars {
			handled = true
			for _, item := range data.([]interface{}) {
				if item == nil {
					encoded = append(encoded, []byte("null\n")...)
				} else {
					encoded = append(encoded, []byte(fmt.Sprintf("%v\n", item))...)
				}
			}
		}
	}

	if !handled {
		if outFormat == "auto" {
			text := fmt.Sprintf("%s %d %s\n", resp.Proto, resp.Status, http.StatusText(resp.Status))

			headerNames := []string{}
			for k := range resp.Headers {
				headerNames = append(headerNames, k)
			}
			sort.Strings(headerNames)

			for _, name := range headerNames {
				text += name + ": " + resp.Headers[name] + "\n"
			}

			var e []byte

			ct := resp.Headers["Content-Type"]
			if ct == "image/png" || ct == "image/jpeg" || ct == "image/webp" || ct == "image/gif" {
				// This is likely an image. Let's display it if we can! Get the window
				// size, read and scale the image, and display it using unicode.
				w, h, err := terminal.GetSize(0)
				if err != nil {
					// Default to standard terminal size
					w, h = 80, 24
				}

				image, err := ansimage.NewScaledFromReader(bytes.NewReader(resp.Body.([]byte)), h*2, w*1, color.Transparent, ansimage.ScaleModeFit, ansimage.NoDithering)
				if err == nil {
					e = []byte(image.Render())
					handled = true
				} else {
					LogWarning("Unable to display image: %v", err)
				}
			}

			if !handled {
				if s, ok := resp.Body.(string); ok {
					text += "\n" + s
				} else if reflect.ValueOf(resp.Body).Kind() != reflect.Invalid {
					e, err = MarshalReadable(resp.Body)
					if err != nil {
						return err
					}

					if f.tty {
						// Uncomment to debug lexer...
						// iter, err := ReadableLexer.Tokenise(&chroma.TokeniseOptions{State: "root"}, string(e))
						// if err != nil {
						// 	panic(err)
						// }
						// for _, token := range iter.Tokens() {
						// 	fmt.Println(token.Type, token.Value)
						// }

						if e, err = Highlight("readable", e); err != nil {
							return err
						}
					}
				}
			}

			if f.tty {
				encoded, err = Highlight("http", []byte(text))
				if err != nil {
					return err
				}
			} else {
				encoded = []byte(text)
			}

			if len(e) > 0 {
				encoded = append(encoded, '\n')
				encoded = append(encoded, e...)
			}
		} else if outFormat == "yaml" {
			data = makeJSONSafe(data)
			encoded, err = yaml.Marshal(data)

			if err != nil {
				return err
			}

			lexer = "yaml"
		} else {
			data = makeJSONSafe(data)
			encoded, err = json.MarshalIndent(data, "", "  ")

			if err != nil {
				return err
			}

			lexer = "json"
		}
	}

	// Make sure we end with a newline, otherwise things won't look right
	// in the terminal.
	if len(encoded) > 0 && encoded[len(encoded)-1] != '\n' {
		encoded = append(encoded, '\n')
	}

	// Only colorize if we are a TTY.
	if f.tty && lexer != "" {
		encoded, err = Highlight(lexer, encoded)
		if err != nil {
			return err
		}
	}

	if len(encoded) > 0 && encoded[len(encoded)-1] != '\n' {
		encoded = append(encoded, '\n')
	}

	fmt.Fprint(Stdout, string(encoded))

	return nil
}

// Only applicable to collection of repeating objects.
// Filter down to a collection of objects first then apply --table.
// Simpletable has much more styling that can be applied.
func setTable(data []interface{}) (*[]byte, error) {
	table := simpletable.New()

	var headerCells []*simpletable.Cell
	defineHeader := true
	for _, maps := range data {
		var bodyCells []*simpletable.Cell
		if mapData, ok := maps.(map[string]interface{}); ok {
			// Discover headers for repeating objects
			// Iterate first instance of one of the repeating objects
			if defineHeader {
				for k, _ := range mapData {
					headerCells = append(headerCells, &simpletable.Cell{Align: simpletable.AlignCenter, Text: k})
				}
			}
			defineHeader = false

			// Add body cells based on order of header cells
			// Will gt out of order otherwise
			for _, cellKey := range headerCells {
				if val, ok := mapData[cellKey.Text]; ok {
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

	table.SetStyle(simpletable.StyleCompactLite)

	ret := []byte(table.String())
	return &ret, nil
}
