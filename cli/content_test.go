package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var contentTests = []struct {
	name   string
	types  []string
	ct     ContentType
	data   []byte
	pretty []byte
}{
	{"text", []string{"text/plain", "text/html"}, &Text{}, []byte("hello world"), nil},
	{"json", []string{"application/json", "foo+json"}, &JSON{}, []byte("{\"hello\":\"world\"}\n"), []byte("{\n  \"hello\": \"world\"\n}\n")},
	{"yaml", []string{"application/yaml", "foo+yaml"}, &YAML{}, []byte("hello: world\n"), nil},
	{"cbor", []string{"application/cbor", "foo+cbor"}, &CBOR{}, []byte("\xf6"), nil},
	{"msgpack", []string{"application/msgpack", "application/x-msgpack", "application/vnd.msgpack", "foo+msgpack"}, &MsgPack{}, []byte("\x81\xa5\x68\x65\x6c\x6c\x6f\xa5\x77\x6f\x72\x6c\x64"), nil},
	{"ion", []string{"application/ion", "foo+ion"}, &Ion{}, []byte("\xe0\x01\x00\xea\x0f"), []byte("null")},
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

			if tt.pretty != nil {
				if p, ok := tt.ct.(PrettyMarshaller); ok {
					b, err := p.MarshalPretty(data)
					assert.NoError(t, err)
					assert.Equal(t, tt.pretty, b)
				} else {
					t.Fatal("not a pretty marshaller")
				}
			}

			assert.Equal(t, tt.data, b)
		})
	}
}
