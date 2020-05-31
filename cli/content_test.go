package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var contentTests = []struct {
	name  string
	types []string
	ct    ContentType
	data  []byte
}{
	{"text", []string{"text/plain", "text/html"}, &Text{}, []byte("hello world")},
	{"json", []string{"application/json", "foo+json"}, &JSON{}, []byte(`{"hello":"world"}`)},
	{"yaml", []string{"application/yaml", "foo+yaml"}, &YAML{}, []byte("hello: world\n")},
	{"cbor", []string{"application/cbor", "foo+cbor"}, &CBOR{}, []byte("\xf6")},
	{"msgpack", []string{"application/msgpack", "application/x-msgpack", "application/vnd.msgpack", "foo+msgpack"}, &MsgPack{}, []byte("\x81\xa5\x68\x65\x6c\x6c\x6f\xa5\x77\x6f\x72\x6c\x64")},
	{"ion", []string{"application/ion", "foo+ion"}, &Ion{}, []byte("\xe0\x01\x00\xea\x0f")},
}

func TestContentTypes(parent *testing.T) {
	for _, tt := range contentTests {
		parent.Run(tt.name, func(t *testing.T) {
			for _, typ := range tt.types {
				assert.True(t, tt.ct.Detect(typ))
			}

			assert.False(t, tt.ct.Detect("bad-content-type"))

			var data interface{}
			err := tt.ct.Unmarshal(tt.data, &data)
			assert.NoError(t, err)

			b, err := tt.ct.Marshal(data)
			assert.NoError(t, err)

			assert.Equal(t, tt.data, b)
		})
	}
}
