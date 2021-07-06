package cli

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// API represents an abstracted API description used to build CLI commands
// around available resources, operations, and links. An API is produced by
// a Loader and cached by the CLI in-between runs when possible.
type API struct {
	Short      string      `json:"short"`
	Long       string      `json:"long,omitempty"`
	Operations []Operation `json:"operations,omitempty"`
	Auth       []APIAuth   `json:"auth,omitempty"`
	AutoConfig AutoConfig  `json:"autoconfig,omitempty"`
}

// Merge two APIs together. Takes the description if none is set and merges
// operations. Ignores auth - if that differs then create another API instead.
func (a *API) Merge(other API) {
	if a.Short == "" {
		a.Short = other.Short
	}

	if a.Long == "" {
		a.Long = other.Long
	}

	a.Operations = append(a.Operations, other.Operations...)
}

var loaders []Loader

// Loader is used to detect and load an API spec, turning it into CLI commands.
type Loader interface {
	LocationHints() []string
	Detect(resp *http.Response) bool
	Load(entrypoint, spec url.URL, resp *http.Response) (API, error)
}

// AddLoader adds a new API spec loader to the CLI.
func AddLoader(loader Loader) {
	loaders = append(loaders, loader)
}

func load(root *cobra.Command, entrypoint, spec url.URL, resp *http.Response, name string, loader Loader) (API, error) {
	api, err := loader.Load(entrypoint, spec, resp)
	if err != nil {
		return API{}, err
	}

	if root.Short == "" {
		root.Short = api.Short
	}

	if root.Long == "" {
		root.Long = api.Long
	}

	for _, op := range api.Operations {
		root.AddCommand(op.command())
	}

	return api, nil
}

// Load will hydrate the command tree for an API, possibly refreshing the
// API spec if the cache is out of date.
func Load(entrypoint string, root *cobra.Command) (API, error) {
	uris := []string{}

	if !strings.HasSuffix(entrypoint, "/") {
		entrypoint += "/"
	}

	uri, err := url.Parse(entrypoint)
	if err != nil {
		return API{}, err
	}

	name, config := findAPI(entrypoint)
	desc := API{}
	found := false

	fromFileOrUrl := func(uri string) ([]byte, error) {
		uriLower := strings.ToLower(uri)
		if strings.Index(uriLower, "http") == 0 {
			resp, err := http.Get(uri)
			if err != nil {
				return []byte{}, err
			}
			return ioutil.ReadAll(resp.Body)
		} else {
			return ioutil.ReadFile(uri)
		}
	}
	if name != "" && len(config.SpecFiles) > 0 {
		// Load the local files
		for _, filename := range config.SpecFiles {
			resp := &http.Response{
				Proto:      "HTTP/1.1",
				StatusCode: 200,
			}

			body, err := fromFileOrUrl(filename)
			if err != nil {
				return API{}, err
			}

			for _, l := range loaders {
				// Reset the body
				resp.Body = ioutil.NopCloser(bytes.NewReader(body))

				if l.Detect(resp) {
					found = true
					resp.Body = ioutil.NopCloser(bytes.NewReader(body))
					tmp, err := load(root, *uri, *uri, resp, name, l)
					if err != nil {
						return API{}, err
					}
					LogDebug("Loaded %s", filename)
					desc.Merge(tmp)
					break
				}
			}
		}

		if found {
			return desc, nil
		}
	}

	LogDebug("Checking API entrypoint %s", entrypoint)
	req, err := http.NewRequest(http.MethodGet, entrypoint, nil)
	if err != nil {
		return API{}, err
	}

	// For fetching specs, we apply a 24-hour cache time if no cache headers
	// are set. So APIs can opt-in to caching if they want control, otherwise
	// we try and do the right thing and not hit them too often. Localhost
	// is never cached to make local development easier.
	client := MinCachedTransport(24 * time.Hour).Client()
	if viper.GetBool("rsh-no-cache") || req.URL.Hostname() == "localhost" {
		client = &http.Client{Transport: InvalidateCachedTransport()}
	}

	httpResp, err := MakeRequest(req, WithClient(client), WithoutLog())
	if err != nil {
		return API{}, err
	}
	defer httpResp.Body.Close()

	resp, err := ParseResponse(httpResp)
	if err != nil {
		return API{}, err
	}

	// Start with known link relations for API descriptions.
	for _, l := range resp.Links["service-desc"] {
		uris = append(uris, l.URI)
	}
	for _, l := range resp.Links["describedby"] {
		uris = append(uris, l.URI)
	}

	// Try hints from loaders next. These are likely places for API descriptions
	// to be on the server, like e.g. `/openapi.json`.
	for _, l := range loaders {
		uris = append(uris, l.LocationHints()...)
	}

	uris = append(uris, uri.String())

	for _, checkURI := range uris {
		parsed, err := url.Parse(checkURI)
		if err != nil {
			return API{}, err
		}
		resolved := uri.ResolveReference(parsed)
		LogDebug("Checking %s", resolved)

		resp, err := client.Get(resolved.String())
		if err != nil {
			return API{}, err
		}
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return API{}, err
		}

		for _, l := range loaders {
			// Reset the body
			resp.Body = ioutil.NopCloser(bytes.NewReader(body))

			if l.Detect(resp) {
				resp.Body = ioutil.NopCloser(bytes.NewReader(body))

				return load(root, *uri, *resolved, resp, name, l)
			}
		}
	}

	return API{}, fmt.Errorf("could not detect API type: %s", entrypoint)
}
