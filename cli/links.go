package cli

import (
	"net/url"

	link "github.com/tent/http-link-go"
)

// Link describes a hypermedia link to another resource.
type Link struct {
	Rel string `json:"rel"`
	URI string `json:"uri"`
}

// Links represents a list of linke relations.
type Links map[string][]*Link

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
func (l *LinkHeaderParser) ParseLinks(resp *Response) error {
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
