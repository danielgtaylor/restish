package cli

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

type overrideLoader struct {
	detect        func(resp *http.Response) bool
	load          func(entrypoint, spec url.URL, resp *http.Response) (API, error)
	locationHints func() []string
}

func (l *overrideLoader) Detect(resp *http.Response) bool {
	if l.detect != nil {
		return l.detect(resp)
	}
	return true
}

func (l *overrideLoader) Load(entrypoint url.URL, spec url.URL, resp *http.Response) (API, error) {
	if l.load != nil {
		return l.load(entrypoint, spec, resp)
	}
	return API{}, nil
}
func (l *overrideLoader) LocationHints() []string {
	if l.locationHints != nil {
		return l.locationHints()
	}
	return []string{}
}

func TestLoadFromFile(t *testing.T) {
	reset(false)
	viper.Set("rsh-no-cache", true)
	AddLoader(&overrideLoader{
		load: func(entrypoint, spec url.URL, resp *http.Response) (API, error) {
			assert.Equal(t, "testdata/petstore.json", spec.String())
			return API{}, nil
		},
	})

	configs["file-load-test"] = &APIConfig{
		Base:      "https://api.example.com",
		SpecFiles: []string{"testdata/petstore.json"},
	}

	_, err := Load("https://api.example.com", &cobra.Command{})

	assert.NoError(t, err)
}

func TestBadSpecURL(t *testing.T) {
	reset(false)
	viper.Set("rsh-no-cache", true)
	AddLoader(&overrideLoader{
		load: func(entrypoint, spec url.URL, resp *http.Response) (API, error) {
			assert.Equal(t, "testdata/petstore.json", spec.String())
			return API{}, nil
		},
	})

	configs["bad-spec-url-test"] = &APIConfig{
		Base:      "https://api.example.com",
		SpecFiles: []string{"http://abc{def@ghi}"},
	}

	_, err := Load("https://api.example.com", &cobra.Command{})
	assert.Error(t, err)
}
