package cli

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestPrintable(t *testing.T) {
	// Printable with BOM
	var body interface{} = []byte("\uFEFF\t\r\n Just a tést!.$%^{}/")
	_, ok := printable(body)
	assert.True(t, ok)

	// Non-printable
	body = []byte{0}
	_, ok = printable(body)
	assert.False(t, ok)

	// Long printable
	tmp := make([]byte, 150)
	for i := 0; i < 150; i++ {
		tmp[i] = 'a'
	}
	_, ok = printable(tmp)
	assert.True(t, ok)

	// Too long
	tmp = make([]byte, 1000000)
	for i := 0; i < 1000000; i++ {
		tmp[i] = 'a'
	}
	_, ok = printable(tmp)
	assert.False(t, ok)
}

var img, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAEklEQVR42mP8/5+hngEIGGEMADlqBP1mY/qhAAAAAElFTkSuQmCC")

var formatterTests = []struct {
	name    string
	tty     bool
	color   bool
	raw     bool
	format  string
	filter  string
	headers map[string]string
	body    any
	result  any
	err     string
}{
	{
		name:   "body-string",
		tty:    true,
		body:   "string",
		result: " 0 \n\nstring\n",
	},
	{
		name: "image",
		tty:  true,
		headers: map[string]string{
			"Content-Type": "image/png",
		},
		body:   img,
		result: []byte{0x20, 0x30, 0x20, 0xa, 0x43, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x2d, 0x54, 0x79, 0x70, 0x65, 0x3a, 0x20, 0x69, 0x6d, 0x61, 0x67, 0x65, 0x2f, 0x70, 0x6e, 0x67, 0xa, 0xa},
	},
	{
		name: "image-empty",
		tty:  true,
		headers: map[string]string{
			"Content-Type":   "image/png",
			"Content-Length": "0",
		},
		body:   []byte{},
		result: []byte{0x20, 0x30, 0x20, 0xa, 0x43, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x2d, 0x4c, 0x65, 0x6e, 0x67, 0x74, 0x68, 0x3a, 0x20, 0x30, 0xa, 0x43, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x2d, 0x54, 0x79, 0x70, 0x65, 0x3a, 0x20, 0x69, 0x6d, 0x61, 0x67, 0x65, 0x2f, 0x70, 0x6e, 0x67, 0xa, 0xa},
	},
	{
		name:   "json-pretty-explicit-full",
		tty:    true,
		color:  true,
		format: "json",
		filter: "@",
		body:   "string",
		result: "\x1b[38;5;247m{\x1b[0m\n  \x1b[38;5;74m\"body\"\x1b[0m\x1b[38;5;247m:\x1b[0m \x1b[38;5;150m\"string\"\x1b[0m\x1b[38;5;247m,\x1b[0m\n  \x1b[38;5;74m\"headers\"\x1b[0m\x1b[38;5;247m:\x1b[0m \x1b[38;5;247m{},\x1b[0m\n  \x1b[38;5;74m\"links\"\x1b[0m\x1b[38;5;247m:\x1b[0m \x1b[38;5;247m{},\x1b[0m\n  \x1b[38;5;74m\"proto\"\x1b[0m\x1b[38;5;247m:\x1b[0m \x1b[38;5;150m\"\"\x1b[0m\x1b[38;5;247m,\x1b[0m\n  \x1b[38;5;74m\"status\"\x1b[0m\x1b[38;5;247m:\x1b[0m \x1b[38;5;172m0\x1b[0m\n\x1b[38;5;247m}\x1b[0m\n",
	},
	{
		name:   "json-escape",
		format: "json",
		body:   "<em> and & shouldn't get escaped",
		result: `"<em> and & shouldn't get escaped"` + "\n",
	},
	{
		name:   "json-bytes",
		format: "json",
		filter: "body",
		body:   []byte{0, 1, 2, 3, 4, 5},
		result: "\"AAECAwQF\"\n",
	},
	{
		name:   "json-filter",
		format: "json",
		filter: "body.id",
		body: []any{
			map[string]any{"id": 1},
			map[string]any{"id": 2},
		},
		result: "[\n  1,\n  2\n]\n",
	},
	{
		name:   "table",
		format: "table",
		filter: "body",
		body: []any{
			map[string]any{"id": 1, "registered": true},
			map[string]any{"id": 2, "registered": false},
		},
		result: `╔════╤════════════╗
║ id │ registered ║
╟━━━━┼━━━━━━━━━━━━╢
║  1 │       true ║
║  2 │      false ║
╚════╧════════════╝
`,
	},
	{
		name:   "raw-bytes",
		tty:    true,
		raw:    true,
		body:   []byte{0, 1, 2, 3, 4, 5},
		result: []byte{0, 1, 2, 3, 4, 5},
	},
	{
		name:   "raw-filtered-value",
		tty:    true,
		raw:    true,
		filter: "body",
		body:   "[1, 2, 3]",
		result: "[1, 2, 3]\n",
	},
	{
		name:   "raw-filtered-bytes",
		tty:    true,
		raw:    true,
		filter: "body",
		body:   []byte{0, 1, 2, 3, 4, 5},
		result: "AAECAwQF\n",
	},
	{
		name:   "raw-large-json-num",
		raw:    true,
		filter: "body",
		body: []interface{}{
			nil,
			float64(1000000000000000),
			float64(1.2e5),
			float64(1.234),
			float64(0.00000000000005), // This should still use scientific notation!
		},
		result: "null\n1000000000000000\n120000\n1.234\n5e-14\n",
	},
	{
		name:   "redirect-bytes",
		body:   []byte{0, 1, 2, 3},
		result: []byte{0, 1, 2, 3},
	},
	{
		name: "redirect-json",
		body: map[string]any{"example": true},
		result: `{
  "example": true
}
`,
	},
	{
		name:   "redirect-explicit-full-response",
		body:   "foo",
		filter: "@",
		result: "{\n  \"body\": \"foo\",\n  \"headers\": {},\n  \"links\": {},\n  \"proto\": \"\",\n  \"status\": 0\n}\n",
	},
	{
		name:   "redirect-yaml",
		format: "yaml",
		body:   map[string]any{"example": true},
		result: "example: true\n",
	},
	{
		name:   "error-prefix",
		filter: "boby.id", // should be body.id
		body:   map[string]any{"id": 123},
		err:    "filter must begin with one of",
	},
	{
		name:   "error-missing-dot",
		filter: "body{id}", // should be body.{id}
		body:   map[string]any{"id": 123},
		err:    "expected '.'",
	},
}

func TestFormatter(t *testing.T) {
	for _, input := range formatterTests {
		t.Run(input.name, func(t *testing.T) {
			formatter := NewDefaultFormatter(input.tty, input.color)
			buf := &bytes.Buffer{}
			Stdout = buf
			viper.Reset()
			viper.Set("rsh-raw", input.raw)
			viper.Set("rsh-filter", input.filter)
			if input.format != "" {
				viper.Set("rsh-output-format", input.format)
			} else {
				viper.Set("rsh-output-format", "auto")
			}
			err := formatter.Format(Response{
				Headers: input.headers,
				Body:    input.body,
			})
			if input.err != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), input.err)
			} else {
				assert.NoError(t, err)
				if b, ok := input.result.([]byte); ok {
					assert.Equal(t, b, buf.Bytes())
				} else {
					assert.Equal(t, input.result, buf.String())
				}
			}
		})
	}
}
