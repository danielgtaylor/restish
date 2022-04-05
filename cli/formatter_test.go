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
