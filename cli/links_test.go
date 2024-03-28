package cli

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

type errorLinkParser struct{}

var base *url.URL

func (p errorLinkParser) ParseLinks(base *url.URL, r *Response) error {
	return fmt.Errorf("error parsing links")
}

func TestLinkParserFailure(t *testing.T) {
	AddLinkParser(errorLinkParser{})
	u, _ := url.Parse("https://example.com/test")
	r := &Response{
		Links:   Links{},
		Headers: map[string]string{},
		Body:    nil,
	}
	err := ParseLinks(u, r)
	assert.Error(t, err)
}

func TestLinkHeaderParser(t *testing.T) {
	r := &Response{
		Links: Links{},
		Headers: map[string]string{
			"Link": `</self>; rel="self", </foo>; rel="item", </bar>; rel="item"`,
		},
	}

	p := LinkHeaderParser{}
	err := p.ParseLinks(base, r)
	assert.NoError(t, err)
	assert.Equal(t, r.Links["self"][0].URI, "/self")
	assert.Equal(t, r.Links["item"][0].URI, "/foo")
	assert.Equal(t, r.Links["item"][1].URI, "/bar")

	// Test a bad link header
	r.Headers["Link"] = "bad value"
	err = p.ParseLinks(base, r)
	assert.Error(t, err)
}

func TestHALParser(t *testing.T) {
	r := &Response{
		Links: Links{},
		Body: map[string]interface{}{
			"_links": map[string]interface{}{
				"curies": nil,
				"self": map[string]interface{}{
					"href": "/self",
				},
				"item": map[string]interface{}{
					"href": "/item",
				},
			},
		},
	}

	p := HALParser{}
	err := p.ParseLinks(base, r)
	assert.NoError(t, err)
	assert.Equal(t, r.Links["self"][0].URI, "/self")
	assert.Equal(t, r.Links["item"][0].URI, "/item")
}

func TestHALParserArray(t *testing.T) {
	r := &Response{
		Links: Links{},
		Body: []interface{}{
			map[string]interface{}{
				"_links": map[string]interface{}{
					"self": map[string]interface{}{
						"href": "/one",
					},
				},
			},
			map[string]interface{}{
				"_links": map[string]interface{}{
					"self": map[string]interface{}{
						"href": "/two",
					},
				},
			},
		},
	}

	p := HALParser{}
	err := p.ParseLinks(base, r)
	assert.NoError(t, err)
	assert.Equal(t, r.Links["self"][0].URI, "/one")
	assert.Equal(t, r.Links["self"][1].URI, "/two")
}

func TestTerrificallySimpleJSONParser(t *testing.T) {
	r := &Response{
		Links: Links{},
		Body: map[string]interface{}{
			"self": "/self",
			"things": []interface{}{
				map[string]interface{}{
					"self": "/foo",
					"name": "Foo",
				},
				map[string]interface{}{
					"self": "/bar",
					"name": "Bar",
				},
				// Weird object with int keys instead of strings? Possible with binary
				// formats but not JSON itself.
				&map[int]interface{}{
					5: map[string]interface{}{
						"self": "/weird",
					},
				},
			},
			"other": map[string]interface{}{
				"self": map[string]interface{}{
					"foo": "bar",
				},
			},
		},
	}

	p := TerrificallySimpleJSONParser{}
	err := p.ParseLinks(base, r)
	assert.NoError(t, err)
	assert.Equal(t, r.Links["self"][0].URI, "/self")
	assert.Equal(t, r.Links["things-item"][0].URI, "/foo")
	assert.Equal(t, r.Links["things-item"][1].URI, "/bar")
	assert.Equal(t, r.Links["5"][0].URI, "/weird")
	assert.NotContains(t, r.Links, "other")
	assert.NotContains(t, r.Links, "foo")
}

func TestSirenParser(t *testing.T) {
	r := &Response{
		Links: Links{},
		Body: map[string]interface{}{
			"links": []map[string]interface{}{
				{"rel": []string{"self"}, "href": "/self"},
				{"rel": []string{"one", "two"}, "href": "/multi"},
				{"rel": []string{"invalid"}},
			},
		},
	}

	s := SirenParser{}
	err := s.ParseLinks(base, r)
	assert.NoError(t, err)
	assert.Equal(t, r.Links["self"][0].URI, "/self")
	assert.Equal(t, r.Links["one"][0].URI, "/multi")
	assert.Equal(t, r.Links["two"][0].URI, "/multi")
}

func TestJSONAPIParser(t *testing.T) {
	r := &Response{
		Links: Links{},
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
	err := j.ParseLinks(base, r)
	assert.NoError(t, err)
	assert.Equal(t, r.Links["self"][0].URI, "/self")
	assert.Equal(t, r.Links["item"][0].URI, "/item")
}
