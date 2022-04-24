package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReadableMarshal(t *testing.T) {
	created, _ := time.Parse(time.RFC3339, "2020-01-01T12:34:56Z")
	data := map[string]interface{}{
		"binary":     []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		"created":    created,
		"date":       created.Truncate(24 * time.Hour),
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
  created: 2020-01-01T12:34:56Z
  date: 2020-01-01
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

func TestSingleItemWithNewlines(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{
			"id":      1234,
			"created": "2020-08-12",
		},
	}

	encoded, err := MarshalReadable(data)
	assert.NoError(t, err)
	assert.Equal(t, `[
  {
    created: "2020-08-12"
    id: 1234
  }
]`, string(encoded))
}
