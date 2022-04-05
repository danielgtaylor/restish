package cli

import (
	"io/fs"
	"os"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

func WithFakeStdin(data []byte, mode fs.FileMode, f func()) {
	fs := fstest.MapFS{
		"stdin": {
			Data: data,
			Mode: mode,
		},
	}
	stdinFile, _ := fs.Open("stdin")
	Stdin = stdinFile
	defer func() { Stdin = os.Stdin }()
	f()
}

func TestInputStructuredJSON(t *testing.T) {
	WithFakeStdin([]byte{}, fs.ModeCharDevice, func() {
		body, err := GetBody("application/json", []string{"foo: 1, bar: false"})
		assert.NoError(t, err)
		assert.Equal(t, `{"bar":false,"foo":1}`, body)
	})
}

func TestInputStructuredYAML(t *testing.T) {
	WithFakeStdin([]byte{}, fs.ModeCharDevice, func() {
		body, err := GetBody("application/yaml", []string{"foo: 1, bar: false"})
		assert.NoError(t, err)
		assert.Equal(t, "bar: false\nfoo: 1\n", body)
	})
}

func TestInputBinary(t *testing.T) {
	WithFakeStdin([]byte("This is not JSON!"), 0, func() {
		body, err := GetBody("", []string{})
		assert.NoError(t, err)
		assert.Equal(t, "This is not JSON!", body)
	})
}

func TestInputInvalidType(t *testing.T) {
	WithFakeStdin([]byte{}, fs.ModeCharDevice, func() {
		_, err := GetBody("application/unknown", []string{"foo: 1"})
		assert.Error(t, err)
	})
}
