package cli

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gbl08ma/httpcache"
	"github.com/gbl08ma/httpcache/diskcache"
)

// CachedTransport returns an HTTP transport with caching abilities.
func CachedTransport() *httpcache.Transport {
	t := httpcache.NewTransport(diskcache.New(path.Join(cacheDir(), "responses")))
	t.MarkCachedResponses = false
	return t
}

type minCachedTransport struct {
	min time.Duration
}

func (m minCachedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.Header.Get("expires") == "" && !strings.Contains(resp.Header.Get("cache-control"), "max-age") {
		// Add the minimum max-age.
		ma := fmt.Sprintf("max-age=%d", int(m.min.Seconds()))
		if cc := resp.Header.Get("cache-control"); cc != "" {
			resp.Header.Set("cache-control", cc+","+ma)
		} else {
			resp.Header.Set("cache-control", ma)
		}
	}

	// HACK: httpcache expects reads rather than close, so for now we special-case
	// the 204 response type and do a dummy read that immediately results in
	// an EOF.
	if resp.StatusCode == http.StatusNoContent {
		ioutil.ReadAll(resp.Body)
	}

	return resp, nil
}

// MinCachedTransport returns an HTTP transport with caching abilities and
// a minimum cache duration for any responses if no cache headers are set.
func MinCachedTransport(min time.Duration) *httpcache.Transport {
	t := CachedTransport()
	t.Transport = &minCachedTransport{min}
	return t
}
