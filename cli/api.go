package cli

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/peterhellberg/link"
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
	if name != "" && len(config.SpecFiles) > 0 {
		// Load the local files
		for _, filename := range config.SpecFiles {
			resp := &http.Response{
				Proto:      "HTTP/1.1",
				StatusCode: 200,
			}

			body, err := ioutil.ReadFile(filename)
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

	// For fetching specs, we apply a 24-hour cache time if no cache headers
	// are set. So APIs can opt-in to caching if they want control, otherwise
	// we try and do the right thing and not hit them too often.
	client := MinCachedTransport(24 * time.Hour).Client()
	if viper.GetBool("rsh-no-cache") {
		client = &http.Client{Transport: InvalidateCachedTransport()}
	}

	LogDebug("Checking %s", entrypoint)
	resp, err := client.Get(entrypoint)
	if err != nil {
		return API{}, err
	}
	defer resp.Body.Close()
	// Hack: read body even if empty to enable caching, due to a bug in httpcache
	// that only writes cache items after reaching EOF. Upstream lib is frozen
	// so needs to be forked and import paths fixed up.
	ioutil.ReadAll(resp.Body)

	links := link.ParseResponse(resp)
	if serviceDesc := links["service-desc"]; serviceDesc != nil {
		uris = append(uris, serviceDesc.URI)
	}

	// Try hints next
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
