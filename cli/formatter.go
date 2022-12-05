package cli

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/color"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma"
	"github.com/alecthomas/chroma/quick"
	"github.com/alecthomas/chroma/styles"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/danielgtaylor/shorthand/v2"
	"github.com/spf13/viper"
	"golang.org/x/exp/maps"
	"golang.org/x/term"

	"github.com/eliukblau/pixterm/pkg/ansimage"
)

// DisplayRanges includes all viewable Unicode characters along with white
// space.
var DisplayRanges = []*unicode.RangeTable{
	unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.White_Space,
}

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

		// Used for Markdown & diffs
		chroma.GenericHeading:    "#5fafd7",
		chroma.GenericSubheading: "#5fafd7",
		chroma.GenericEmph:       "italic #ffd7d7",
		chroma.GenericStrong:     "bold #af87af",
		chroma.GenericDeleted:    "#ff5f87",
		chroma.GenericInserted:   "#afd787",
		chroma.NameAttribute:     "underline",

		// Used for matching `{`, `}, `[`, and `]` characters.
		IndentLevel1: "#d78700",
		IndentLevel2: "#af87af",
		IndentLevel3: "#5fafd7",
	}))
}

func boolPtr(b bool) *bool       { return &b }
func stringPtr(s string) *string { return &s }
func uintPtr(u uint) *uint       { return &u }

var MarkdownStyle = ansi.StyleConfig{
	Document: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockPrefix: "\n",
			BlockSuffix: "\n",
			// Color:       stringPtr("#eee"),
		},
		Margin: uintPtr(2),
	},
	BlockQuote: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Color: stringPtr("#ffd7d7"),
		},
		Indent:      uintPtr(1),
		IndentToken: stringPtr("│ "),
	},
	List: ansi.StyleList{
		LevelIndent: 2,
	},
	Heading: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockSuffix: "\n",
			Color:       stringPtr("#5fafd7"),
			Bold:        boolPtr(true),
		},
	},
	H1: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix:          " ",
			Suffix:          " ",
			Color:           stringPtr("#000"),
			BackgroundColor: stringPtr("#ff5f87"),
			Bold:            boolPtr(true),
		},
	},
	H2: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "## ",
		},
	},
	H3: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "### ",
		},
	},
	H4: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "#### ",
		},
	},
	H5: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "##### ",
		},
	},
	H6: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix: "###### ",
			Color:  stringPtr("35"),
			Bold:   boolPtr(false),
		},
	},
	Strikethrough: ansi.StylePrimitive{
		CrossedOut: boolPtr(true),
	},
	Emph: ansi.StylePrimitive{
		Italic: boolPtr(true),
	},
	Strong: ansi.StylePrimitive{
		Bold: boolPtr(true),
	},
	HorizontalRule: ansi.StylePrimitive{
		Color:  stringPtr("240"),
		Format: "\n--------\n",
	},
	Item: ansi.StylePrimitive{
		BlockPrefix: "• ",
	},
	Enumeration: ansi.StylePrimitive{
		BlockPrefix: ". ",
	},
	Task: ansi.StyleTask{
		StylePrimitive: ansi.StylePrimitive{},
		Ticked:         "[✓] ",
		Unticked:       "[ ] ",
	},
	Link: ansi.StylePrimitive{
		Color:     stringPtr("#D6FFB7"),
		Italic:    boolPtr(true),
		Underline: boolPtr(true),
	},
	LinkText: ansi.StylePrimitive{
		Color: stringPtr("#afd787"),
		Bold:  boolPtr(true),
	},
	Image: ansi.StylePrimitive{
		Color:     stringPtr("212"),
		Underline: boolPtr(true),
	},
	ImageText: ansi.StylePrimitive{
		Color:  stringPtr("243"),
		Format: "Image: {{.text}} →",
	},
	Code: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Prefix:          " ",
			Suffix:          " ",
			Color:           stringPtr("#d78700"),
			BackgroundColor: stringPtr("236"),
		},
	},
	CodeBlock: ansi.StyleCodeBlock{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: stringPtr("244"),
			},
			Margin: uintPtr(2),
		},
		Chroma: &ansi.Chroma{
			Text: ansi.StylePrimitive{
				Color: stringPtr("#C4C4C4"),
			},
			// Error: ansi.StylePrimitive{
			// 	Color:           stringPtr("#F1F1F1"),
			// 	BackgroundColor: stringPtr("#F05B5B"),
			// },
			Comment: ansi.StylePrimitive{
				Color: stringPtr("#9e9e9e"),
			},
			CommentPreproc: ansi.StylePrimitive{
				Color: stringPtr("#FF875F"),
			},
			Keyword: ansi.StylePrimitive{
				Color: stringPtr("#ff5f87"),
			},
			KeywordReserved: ansi.StylePrimitive{
				Color: stringPtr("#ff5f87"),
			},
			KeywordNamespace: ansi.StylePrimitive{
				Color: stringPtr("#ff5f87"),
			},
			KeywordType: ansi.StylePrimitive{
				Color: stringPtr("#af87af"),
			},
			Operator: ansi.StylePrimitive{
				Color: stringPtr("#ffd7d7"),
			},
			Punctuation: ansi.StylePrimitive{
				Color: stringPtr("#9e9e9e"),
			},
			Name: ansi.StylePrimitive{
				Color: stringPtr("#C4C4C4"),
			},
			NameBuiltin: ansi.StylePrimitive{
				Color: stringPtr("#af87af"),
			},
			NameTag: ansi.StylePrimitive{
				Color: stringPtr("#5fafd7"),
			},
			NameAttribute: ansi.StylePrimitive{
				Color: stringPtr("#5fafd7"),
			},
			NameClass: ansi.StylePrimitive{
				Color:     stringPtr("#F1F1F1"),
				Underline: boolPtr(true),
				Bold:      boolPtr(true),
			},
			NameDecorator: ansi.StylePrimitive{
				Color: stringPtr("#FED2AF"),
			},
			NameFunction: ansi.StylePrimitive{
				Color: stringPtr("#5fafd7"),
			},
			LiteralNumber: ansi.StylePrimitive{
				Color: stringPtr("#d78700"),
			},
			LiteralString: ansi.StylePrimitive{
				Color: stringPtr("#afd787"),
			},
			LiteralStringEscape: ansi.StylePrimitive{
				Color: stringPtr("#D6FFB7"),
			},
			GenericDeleted: ansi.StylePrimitive{
				Color: stringPtr("#ff5f87"),
			},
			GenericEmph: ansi.StylePrimitive{
				Italic: boolPtr(true),
			},
			GenericInserted: ansi.StylePrimitive{
				Color: stringPtr("#afd787"),
			},
			GenericStrong: ansi.StylePrimitive{
				Bold: boolPtr(true),
			},
			GenericSubheading: ansi.StylePrimitive{
				Color: stringPtr("#777777"),
			},
			Background: ansi.StylePrimitive{
				BackgroundColor: stringPtr("#373737"),
			},
		},
	},
	Table: ansi.StyleTable{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{},
		},
		CenterSeparator: stringPtr("┼"),
		ColumnSeparator: stringPtr("│"),
		RowSeparator:    stringPtr("─"),
	},
	DefinitionDescription: ansi.StylePrimitive{
		BlockPrefix: "\n🠶 ",
	},
}

// makeJSONSafe walks an interface to ensure all maps use string keys so that
// encoding to JSON (or YAML) works. Some unmarshallers (e.g. CBOR) will
// create map[interface{}]interface{} which causes problems marshalling.
// See https://github.com/fxamacker/cbor/issues/206
func makeJSONSafe(obj interface{}) interface{} {
	value := reflect.ValueOf(obj)

	for value.Kind() == reflect.Ptr {
		value = value.Elem()
	}

	switch value.Kind() {
	case reflect.Slice:
		if _, ok := obj.([]byte); ok {
			// Special case: byte slices get special encoding rules in various
			// formats, so keep them as-is. Without this is breaks the base64
			// encoding for JSON and gives you an array of integers instead.
			return obj
		}
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
	}

	return obj
}

// printable returns true if the given body can be printed to a terminal
// based on displayable unicode character ranges and whitespace. If true,
// then the body is also returned as a byte slice ready to be written to
// stdout.
func printable(body interface{}) ([]byte, bool) {
	if s, ok := body.(string); ok {
		return []byte(s), true
	}

	if b, ok := body.([]byte); ok {
		// This was not a known format we could parse, and was not likely an
		// image. If it looks like displayable text, then let's try to display
		// it as such, up to 100KiB.
		if len(b) < 102400 && utf8.Valid(b) {
			display := true
			for i, r := range string(b) {
				if i == 0 && r == '\uFEFF' {
					// Skip unicode BOM
					continue
				}
				if i > 100 {
					// Only examine the first 100 bytes, which is long enough to
					// detect non-printable characters in most file preambles or
					// magic number file signatures.
					break
				}
				if !unicode.In(r, DisplayRanges...) {
					display = false
					break
				}
			}

			if display {
				return b, true
			}
		}
	}
	return nil, false
}

// Highlight a block of data with the given lexer.
func Highlight(lexer string, data []byte) ([]byte, error) {
	// Reset indent level used by the `readable` lexer.
	indentLevel = 0

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
	tty   bool
	color bool
}

// NewDefaultFormatter creates a new formatted with autodetected TTY
// capabilities.
func NewDefaultFormatter(tty, color bool) *DefaultFormatter {
	return &DefaultFormatter{
		tty:   tty,
		color: color,
	}
}

// filterData filters the current response using shorthand query and returns the
// result.
func (f *DefaultFormatter) filterData(filter string, data map[string]any) (any, error) {
	keys := maps.Keys(data)
	sort.Strings(keys)
	found := strings.HasPrefix(filter, "*") || strings.HasPrefix(filter, "..") || strings.HasPrefix(filter, "{")
	if !found {
		for _, k := range keys {
			if strings.HasPrefix(filter, k) {
				if len(filter) > len(k) && filter[len(k)] == '{' {
					// Catch a common typo 'body{id}` vs `body.{id}`
					return nil, fmt.Errorf("expected '.' or '[' after '%s' but found %s", k, filter)
				}
				if len(filter) == len(k) || len(filter) > len(k) && (filter[len(k)] == '.' || filter[len(k)] == '[') {
					// Matches e.g. `body`, `body.`, `body[...]`, etc.
					found = true
					break
				}
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("filter must begin with one of '%v' and use '.' delimiters", strings.Join(keys, "', '"))
	}

	opts := shorthand.GetOptions{}
	if enableVerbose {
		opts.DebugLogger = LogDebug
	}

	result, _, err := shorthand.GetPath(filter, data, opts)
	return result, err
}

func (f *DefaultFormatter) formatRaw(data any) ([]byte, string, bool) {
	kind := reflect.ValueOf(data).Kind()
	lexer := ""

	if kind == reflect.String {
		dStr := data.(string)
		if len(dStr) != 0 && (dStr[0] == '{' || dStr[0] == '[') {
			// Looks like JSON to me!
			lexer = "json"
		}
		return []byte(dStr), lexer, true
	}

	if kind == reflect.Slice {
		scalars := true

		if d, ok := data.([]byte); ok {
			// Special case: binary data which should be represented by base64.
			encoded := make([]byte, base64.StdEncoding.EncodedLen(len(d)))
			base64.StdEncoding.Encode(encoded, d)
			return encoded, lexer, true
		}

		for _, item := range data.([]interface{}) {
			switch item.(type) {
			case nil, bool, int, int64, float64, string:
				// The above are scalars used by decoders
			default:
				scalars = false
			}
			if !scalars {
				break
			}
		}

		if scalars {
			var encoded []byte
			for _, item := range data.([]interface{}) {
				if item == nil {
					encoded = append(encoded, []byte("null\n")...)
				} else if f, ok := item.(float64); ok && f == float64(int64(f)) {
					// This is likely an integer from JSON that was loaded as a float64!
					// Prevent the use of scientific notation!
					encoded = append(strconv.AppendFloat(encoded, f, 'f', -1, 64), '\n')
				} else {
					encoded = append(encoded, []byte(fmt.Sprintf("%v\n", item))...)
				}
			}
			return encoded, lexer, true
		}
	}

	return nil, "", false
}

// nl prepends a new line to a slice of bytes.
func (f *DefaultFormatter) nl(v []byte) []byte {
	result := append([]byte{'\n'}, v...)
	if result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	return result
}

// formatAuto formats the response as a human-readable terminal display
// friendly format.
func (f *DefaultFormatter) formatAuto(format string, resp Response) ([]byte, error) {
	text := fmt.Sprintf("%s %d %s\n", resp.Proto, resp.Status, http.StatusText(resp.Status))

	headerNames := []string{}
	for k := range resp.Headers {
		headerNames = append(headerNames, k)
	}
	sort.Strings(headerNames)

	for _, name := range headerNames {
		text += name + ": " + resp.Headers[name] + "\n"
	}

	var err error
	var encoded []byte

	if f.color {
		encoded, err = Highlight("http", []byte(text))
		if err != nil {
			return nil, err
		}
	} else {
		encoded = []byte(text)
	}

	ct := resp.Headers["Content-Type"]
	if resp.Body != nil && (ct == "image/png" || ct == "image/jpeg" || ct == "image/webp" || ct == "image/gif") {
		if b, ok := resp.Body.([]byte); ok {
			// This is likely an image. Let's display it if we can! Get the window
			// size, read and scale the image, and display it using unicode.
			w, h, err := term.GetSize(0)
			if err != nil {
				// Default to standard terminal size
				w, h = 80, 24
			}

			image, err := ansimage.NewScaledFromReader(bytes.NewReader(b), h*2, w*1, color.Transparent, ansimage.ScaleModeFit, ansimage.NoDithering)
			if err == nil {
				return append(encoded, f.nl([]byte(image.Render()))...), nil
			} else {
				LogWarning("Unable to display image: %v", err)
			}
		}
	}

	if b, ok := printable(resp.Body); ok {
		return append(encoded, f.nl(b)...), nil
	}

	if reflect.ValueOf(resp.Body).Kind() != reflect.Invalid {
		b, err := MarshalShort(format, true, resp.Body)
		if err != nil {
			return nil, err
		}

		if f.color {
			// Uncomment to debug lexer...
			// iter, err := ReadableLexer.Tokenise(&chroma.TokeniseOptions{State: "root"}, string(readable))
			// if err != nil {
			// 	panic(err)
			// }
			// for _, token := range iter.Tokens() {
			// 	fmt.Println(token.Type, token.Value)
			// }

			if b, err = Highlight(format, b); err != nil {
				return nil, err
			}
		}

		return append(encoded, f.nl(b)...), nil
	}

	// No body to display.
	return nil, nil
}

// Format will filter, prettify, colorize and output the data.
func (f *DefaultFormatter) Format(resp Response) error {
	var err error
	outFormat := viper.GetString("rsh-output-format")
	filter := viper.GetString("rsh-filter")

	// Special case: raw response output mode. The response wasn't decoded so we
	// have a bunch of bytes and the user asked for raw output, so just write it.
	// This enables completely bypassing decoding and file downloads.
	if filter == "" && (viper.GetBool("rsh-raw") || !f.tty) {
		if b, ok := resp.Body.([]byte); ok {
			Stdout.Write(b)
			return nil
		}
	}

	// Output defaults. Bypass by passing output options.
	if outFormat == "auto" {
		if f.tty {
			// Live terminal: readable output
			outFormat = "readable"
		} else {
			// Redirected (e.g. file or pipe) output: JSON for easier scripting.
			outFormat = "json"
		}
	}
	if !f.tty && filter == "" {
		filter = "body"
	}

	var data any = resp.Map()

	// Filter the data if requested via shorthand query.
	if filter != "" && filter != "@" {
		// Optimization: select just the body
		if filter == "body" {
			data = resp.Body
		} else {
			data, err = f.filterData(filter, data.(map[string]any))
			if err != nil || data == nil {
				return err
			}
		}
	}

	// Encode to the requested output format using nice formatting.
	var encoded []byte
	var lexer string
	handled := false

	// Special case: raw output with scalars or an array of scalars. This enables
	// shell-friendly output without quotes or with each item on its own line
	// which is easy to use in e.g. bash `for` loops.
	if viper.GetBool("rsh-raw") {
		var ok bool
		if encoded, lexer, ok = f.formatRaw(data); ok {
			handled = true
		}
	}

	if !handled {
		if (f.tty && filter == "") || (outFormat == "readable" && (filter == "" || filter == "@")) {
			encoded, err = f.formatAuto(outFormat, resp)
		} else {
			encoded, err = MarshalShort(outFormat, true, data)
			lexer = outFormat
		}
	}

	if err != nil {
		return err
	}

	// Only colorize if we have a lexer and color is enabled.
	if f.color && lexer != "" {
		encoded, err = Highlight(lexer, encoded)
		if err != nil {
			return err
		}
	}

	// Make sure we end with a newline, otherwise things won't look right
	// in the terminal.
	if len(encoded) > 0 && encoded[len(encoded)-1] != '\n' {
		encoded = append(encoded, '\n')
	}

	Stdout.Write(encoded)

	return nil
}
