package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// API represents an abstracted API description used to build CLI commands
// around available resources, operations, and links. An API is produced by
// a Loader and cached by the CLI in-between runs when possible.
type API struct {
	RestishVersion string      `json:"restish_version" yaml:"restish_version"`
	Short          string      `json:"short" yaml:"short"`
	Long           string      `json:"long,omitempty" yaml:"long,omitempty"`
	Operations     []Operation `json:"operations,omitempty" yaml:"operations,omitempty"`
	Auth           []APIAuth   `json:"auth,omitempty" yaml:"auth,omitempty"`
	AutoConfig     AutoConfig  `json:"auto_config,omitempty" yaml:"auto_config,omitempty"`
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
		if op.Group != "" && !root.ContainsGroup(op.Group) {
			groupName := fmt.Sprintf("%s Commands:", cases.Title(language.Und, cases.NoLower).String(op.Group))
			group := &cobra.Group{ID: op.Group, Title: groupName}
			root.AddGroup(group)
		}
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
	filename := filepath.Join(getCacheDir(), name+".cbor")
	if err := os.WriteFile(filename, b, 0o600); err != nil {
		LogError("Could not write API cache %s", err)
	}
}

// Load will hydrate the command tree for an API, possibly refreshing the
// API spec if the cache is out of date.
func Load(entrypoint string, root *cobra.Command) (API, error) {
	start := time.Now()
	defer func() {
		LogDebug("API loading took %s", time.Since(start))
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
		filename := filepath.Join(getCacheDir(), name+".cbor")
		if data, err := os.ReadFile(filename); err == nil {
			if err := cbor.Unmarshal(data, &cached); err == nil {
				if cached.RestishVersion == root.Version {
					setupRootFromAPI(root, &cached)
					return cached, nil
				}
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
			return io.ReadAll(resp.Body)
		} else {
			return os.ReadFile(os.ExpandEnv(uri))
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

			// No need to check error, it was checked above in `fromFileOrUrl`.
			uriSpec, _ := url.Parse(filename)

			for _, l := range loaders {
				// Reset the body
				resp.Body = io.NopCloser(bytes.NewReader(body))

				if l.Detect(resp) {
					found = true
					resp.Body = io.NopCloser(bytes.NewReader(body))
					tmp, err := load(root, *uri, *uriSpec, resp, name, l)
					if err != nil {
						return API{}, err
					}
					desc.Merge(tmp)
					break
				}
			}
		}

		if found {
			desc.RestishVersion = root.Version
			cacheAPI(name, &desc)
			return desc, nil
		}
	}

	LogDebug("Checking API entrypoint %s", entrypoint)
	req, err := http.NewRequest(http.MethodGet, entrypoint, nil)
	if err != nil {
		return API{}, err
	}

	// We already cache the parsed API specs, no need to cache the
	// server response.
	// We will almost never be in a situation where we don't want to use
	// the parsed API cache, but do want to use a cached response from
	// the server.
	client := &http.Client{Transport: InvalidateCachedTransport()}
	httpResp, err := MakeRequest(req, WithClient(client), IgnoreCLIParams())
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

		resp, err := MakeRequest(req, WithClient(client), IgnoreCLIParams())
		if err != nil {
			return API{}, err
		}
		if err := DecodeResponse(resp); err != nil {
			return API{}, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return API{}, err
		}

		for _, l := range loaders {
			// Reset the body
			resp.Body = io.NopCloser(bytes.NewReader(body))

			if l.Detect(resp) {
				resp.Body = io.NopCloser(bytes.NewReader(body))

				// Override the operation base path if requested, otherwise
				// default to the API entrypoint.
				opsBase := uri
				if config.OperationBase != "" {
					opsBase = uri.ResolveReference(&url.URL{Path: config.OperationBase})
				}
				api, err := load(root, *opsBase, *resolved, resp, name, l)
				if err == nil {
					cacheAPI(name, &api)
				}
				return api, err
			}
		}
	}

	return API{}, fmt.Errorf("could not detect API type: %s", entrypoint)
}
