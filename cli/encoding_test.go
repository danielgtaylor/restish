package cli

import (
	"bytes"
	"compress/gzip"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/stretchr/testify/assert"
)

func gzipEnc(data string) []byte {
	b := bytes.NewBuffer(nil)
	w := gzip.NewWriter(b)
	w.Write([]byte(data))
	w.Close()
	return b.Bytes()
}

func brEnc(data string) []byte {
	b := bytes.NewBuffer(nil)
	w := brotli.NewWriter(b)
	w.Write([]byte(data))
	w.Close()
	return b.Bytes()
}

var encodingTests = []struct {
	name   string
	header string
	data   []byte
}{
	{"none", "", []byte("hello world")},
	{"gzip", "gzip", gzipEnc("hello world")},
	{"brotli", "br", brEnc("hello world")},
}

func TestEncodings(parent *testing.T) {
	for _, tt := range encodingTests {
		parent.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				Header: http.Header{
					"Content-Encoding": []string{tt.header},
				},
				Body: ioutil.NopCloser(bytes.NewReader(tt.data)),
			}

			err := DecodeResponse(resp)
			assert.NoError(t, err)

			data, err := ioutil.ReadAll(resp.Body)
			assert.NoError(t, err)
			assert.Equal(t, "hello world", string(data))
		})
	}
}
