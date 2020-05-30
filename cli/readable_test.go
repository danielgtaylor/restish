package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReadableMarshal(t *testing.T) {
	data := map[string]interface{}{
		"binary":     []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		"created":    time.Time{},
		"id":         "test",
		"emptyMap":   map[string]interface{}{},
		"emptyArray": []string{},
		"nested": map[string]interface{}{
			"saved": true,
			"self":  "https://example.com/nested",
		},
		"pointer": nil,
		"tags":    []string{"one", "tw\"o", "three"},
		"value":   123,
		"float":   1.2,
	}

	encoded, err := MarshalReadable(data)
	assert.NoError(t, err)
	assert.Equal(t, `{
  binary: 0x00010203040506070809...
  created: 0001-01-01T00:00:00Z
  emptyArray: []
  emptyMap: {}
  float: 1.2
  id: "test"
  nested: {
    saved: true
    self: "https://example.com/nested"
  }
  pointer: null
  tags: ["one", "tw\"o", "three"]
  value: 123
}`, string(encoded))
}
