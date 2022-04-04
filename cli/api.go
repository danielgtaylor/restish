package cli

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
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

func setupRootFromAPI(root *cobra.Command, api *API) {
	if root.Short == "" {
		root.Short = api.Short
	}

	if root.Long == "" {
		root.Long = api.Long
	}

	for _, op := range api.Operations {
		root.AddCommand(op.command())
	}
}

func load(root *cobra.Command, entrypoint, spec url.URL, resp *http.Response, name string, loader Loader) (API, error) {
	api, err := loader.Load(entrypoint, spec, resp)
	if err != nil {
		return API{}, err
	}

	setupRootFromAPI(root, &api)
	return api, nil
}

func cacheAPI(name string, api *API) {
	if name == "" {
		return
	}

	Cache.Set(name+".expires", time.Now().Add(24*time.Hour))
	Cache.WriteConfig()

	b, err := cbor.Marshal(api)
	if err != nil {
		LogError("Could not marshal API cache %s", err)
	}
	filename := path.Join(viper.GetString("config-directory"), name+".cbor")
	if err := ioutil.WriteFile(filename, b, 0o600); err != nil {
		LogError("Could not write API cache %s", err)
	}
}

// Load will hydrate the command tree for an API, possibly refreshing the
// API spec if the cache is out of date.
func Load(entrypoint string, root *cobra.Command) (API, error) {
	start := time.Now()
	defer func() {
		LogDebug("API loading took %s", time.Now().Sub(start))
	}()
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

	// See if there is a cache we can quickly load.
	expires := Cache.GetTime(name + ".expires")
	if !viper.GetBool("rsh-no-cache") && !expires.IsZero() && expires.After(time.Now()) {
		var cached API
		filename := path.Join(viper.GetString("config-directory"), name+".cbor")
		if data, err := ioutil.ReadFile(filename); err == nil {
			if err := cbor.Unmarshal(data, &cached); err == nil {
				setupRootFromAPI(root, &cached)
				return cached, nil
			}
		}
	}

	fromFileOrUrl := func(uri string) ([]byte, error) {
		uriLower := strings.ToLower(uri)
		if strings.Index(uriLower, "http") == 0 {
			resp, err := http.Get(uri)
			if err != nil {
				return []byte{}, err
			}
			return ioutil.ReadAll(resp.Body)
		} else {
			return ioutil.ReadFile(os.ExpandEnv(uri))
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
					desc.Merge(tmp)
					break
				}
			}
		}

		if found {
			cacheAPI(name, &desc)
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

	httpResp, err := MakeRequest(req, WithClient(client))
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

		req, err := http.NewRequest(http.MethodGet, resolved.String(), nil)
		if err != nil {
			return API{}, err
		}

		resp, err := MakeRequest(req, WithClient(client))
		if err != nil {
			return API{}, err
		}
		if err := DecodeResponse(resp); err != nil {
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

				api, err := load(root, *uri, *resolved, resp, name, l)
				if err == nil {
					cacheAPI(name, &api)
				}
				return api, err
			}
		}
	}

	return API{}, fmt.Errorf("could not detect API type: %s", entrypoint)
}
