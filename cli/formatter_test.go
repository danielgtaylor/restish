package cli

import (
	"bytes"
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

func TestFileDownload(t *testing.T) {
	formatter := NewDefaultFormatter(false)
	buf := &bytes.Buffer{}
	Stdout = buf
	viper.Set("rsh-raw", true)
	viper.Set("rsh-filter", "")
	formatter.Format(Response{
		Body: []byte{0, 1, 2, 3},
	})
	assert.Equal(t, []byte{0, 1, 2, 3}, buf.Bytes())
}

func TestRawLargeJSONNumbers(t *testing.T) {
	formatter := NewDefaultFormatter(false)
	buf := &bytes.Buffer{}
	Stdout = buf
	viper.Set("rsh-raw", true)
	viper.Set("rsh-filter", "body")
	formatter.Format(Response{
		Body: []interface{}{
			nil,
			float64(1000000000000000),
			float64(1.2e5),
			float64(1.234),
			float64(0.00000000000005), // This should still use scientific notation!
		},
	})
	assert.Equal(t, "null\n1000000000000000\n120000\n1.234\n5e-14\n", buf.String())
}

func TestFormatEmptyImage(t *testing.T) {
	formatter := NewDefaultFormatter(false)
	buf := &bytes.Buffer{}
	Stdout = buf
	viper.Set("rsh-raw", false)
	viper.Set("rsh-filter", "")

	// This should not panic!
	formatter.Format(Response{
		Headers: map[string]string{
			"Content-Type":   "image/jpeg",
			"Content-Length": "0",
		},
		Body: nil,
	})
}

func TestJSONEscape(t *testing.T) {
	formatter := NewDefaultFormatter(false)
	buf := &bytes.Buffer{}
	Stdout = buf
	viper.Set("rsh-raw", false)
	viper.Set("rsh-filter", "")
	viper.Set("rsh-output-format", "json")
	defer func() { viper.Set("rsh-output_format", "auto") }()

	formatter.Format(Response{
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: map[string]string{
			"test": "<em> and & shouldn't get escaped",
		},
	})

	assert.Contains(t, buf.String(), "<em> and & shouldn't get escaped")
}
