package cli

import (
	"fmt"
	"net/url"
	"reflect"

	link "github.com/tent/http-link-go"
)

// Link describes a hypermedia link to another resource.
type Link struct {
	Rel string `json:"rel"`
	URI string `json:"uri"`
}

// Links represents a list of linke relations.
type Links map[string][]*Link

// LinkParser parses link relationships in a response.
type LinkParser interface {
	ParseLinks(resp *Response) error
}

var linkParsers = []LinkParser{}

// AddLinkParser adds a new link parser to create standardized link relation
// objects on a parsed response.
func AddLinkParser(parser LinkParser) {
	linkParsers = append(linkParsers, parser)
}

// ParseLinks uses all registered LinkParsers to parse links for a response.
func ParseLinks(base *url.URL, resp *Response) error {
	for _, parser := range linkParsers {
		if err := parser.ParseLinks(resp); err != nil {
			return err
		}
	}

	for _, links := range resp.Links {
		for _, l := range links {
			p, err := url.Parse(l.URI)
			if err != nil {
				return err
			}

			resolved := base.ResolveReference(p)
			l.URI = resolved.String()
		}
	}

	return nil
}

// LinkHeaderParser parses RFC 5988 HTTP link relation headers.
type LinkHeaderParser struct{}

// ParseLinks processes the links in a parsed response.
func (l LinkHeaderParser) ParseLinks(resp *Response) error {
	if resp.Headers["Link"] != "" {
		links, err := link.Parse(resp.Headers["Link"])
		if err != nil {
			return err
		}

		for _, parsed := range links {

			if resp.Links == nil {
				resp.Links = map[string][]*Link{}
			}

			resp.Links[parsed.Rel] = append(resp.Links[parsed.Rel], &Link{
				Rel: parsed.Rel,
				URI: parsed.URI,
			})
		}
	}

	return nil
}

// HALParser parses HAL hypermedia links. Ignores curies.
type HALParser struct{}

// ParseLinks processes the links in a parsed response.
func (h HALParser) ParseLinks(resp *Response) error {
	// Convert map[interface{}]interface{} to map[string]interface{} for
	// easier traversal/querying.
	b := makeJSONSafe(resp.Body)

	if m, ok := b.(map[string]interface{}); ok {
		links := m["_links"]
		if links != nil {
			if linksmap, ok := links.(map[string]interface{}); ok {
				for rel, v := range linksmap {
					if rel == "curies" {
						continue
					}

					if vm, ok := v.(map[string]interface{}); ok {
						if _, ok := vm["href"].(string); !ok {
							continue
						}

						if resp.Links == nil {
							resp.Links = map[string][]*Link{}
						}

						resp.Links[rel] = append(resp.Links[rel], &Link{
							Rel: rel,
							URI: vm["href"].(string),
						})
					}
				}
			}
		}
	}
	return nil
}

// TerrificallySimpleJSONParser parses `self` links from JSON-like formats.
type TerrificallySimpleJSONParser struct{}

// ParseLinks processes the links in a parsed response.
func (t TerrificallySimpleJSONParser) ParseLinks(resp *Response) error {
	return t.walk(resp, "self", resp.Body)
}

// walk the response body recursively to find any `self` links.
func (t TerrificallySimpleJSONParser) walk(resp *Response, key string, value interface{}) error {
	v := reflect.ValueOf(value)

	switch v.Kind() {
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			t.walk(resp, key+"-item", v.Index(i).Interface())
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			kStr := ""
			if s, ok := k.Interface().(string); ok {
				kStr = s
				if s == "self" {
					if resp.Links == nil {
						resp.Links = map[string][]*Link{}
					}

					resp.Links[key] = append(resp.Links[key], &Link{
						Rel: key,
						URI: fmt.Sprintf("%v", v.MapIndex(k).Interface()),
					})
					continue
				}
			} else {
				kStr = fmt.Sprintf("%v", k)
			}

			t.walk(resp, kStr, v.MapIndex(k).Interface())
		}
	case reflect.Ptr:
		return t.walk(resp, key, v.Elem().Interface())
	}

	return nil
}
