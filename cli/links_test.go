package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSirenParser(t *testing.T) {
	r := &Response{
		Links: map[string][]*Link{},
		Body: map[string]interface{}{
			"links": []map[string]interface{}{
				{"rel": []string{"self"}, "href": "/self"},
				{"rel": []string{"one", "two"}, "href": "/multi"},
			},
		},
	}
	s := SirenParser{}
	err := s.ParseLinks(r)
	assert.NoError(t, err)
	assert.Equal(t, r.Links["self"][0].URI, "/self")
	assert.Equal(t, r.Links["one"][0].URI, "/multi")
	assert.Equal(t, r.Links["two"][0].URI, "/multi")
}

func TestJSONAPIParser(t *testing.T) {
	r := &Response{
		Links: map[string][]*Link{},
		Body: map[string]interface{}{
			"links": map[string]interface{}{
				"self": "/self",
			},
			"data": []interface{}{
				map[string]interface{}{
					"links": map[string]interface{}{
						"self": map[string]interface{}{
							"href": "/item",
						},
					},
				},
			},
		},
	}
	j := JSONAPIParser{}
	err := j.ParseLinks(r)
	assert.NoError(t, err)
	assert.Equal(t, r.Links["self"][0].URI, "/self")
	assert.Equal(t, r.Links["item"][0].URI, "/item")
}
