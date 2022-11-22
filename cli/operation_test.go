package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"gopkg.in/h2non/gock.v1"
)

func TestOperation(t *testing.T) {
	defer gock.Off()

	gock.
		New("http://example2.com").
		Get("/prefix/test/id1").
		MatchParam("search", "foo").
		MatchParam("def3", "abc").
		MatchHeader("Accept", "application/json").
		Reply(200).
		JSON(map[string]interface{}{
			"hello": "world",
		})

	op := Operation{
		Name:        "test",
		Short:       "short",
		Long:        "long",
		Method:      http.MethodGet,
		URITemplate: "http://example.com/test/{id}",
		PathParams: []*Param{
			{
				Type:        "string",
				Name:        "id",
				DisplayName: "id",
				Description: "desc",
			},
		},
		QueryParams: []*Param{
			{
				Type:        "string",
				Name:        "search",
				DisplayName: "search",
				Description: "desc",
			},
			{
				Type:        "string",
				Name:        "def",
				DisplayName: "def",
				Description: "desc",
			},
			{
				Type:        "string",
				Name:        "def2",
				DisplayName: "def2",
				Description: "desc",
				Default:     "",
			},
			{
				Type:        "string",
				Name:        "def3",
				DisplayName: "def3",
				Description: "desc",
				Default:     "abc",
			},
		},
		HeaderParams: []*Param{
			{
				Type:        "string",
				Name:        "Accept",
				DisplayName: "Accept",
				Description: "desc",
				Default:     "application/json",
			},
			{
				Type:        "string",
				Name:        "Accept-Encoding",
				DisplayName: "Accept-Encoding",
				Description: "desc",
				Default:     "gz",
			},
		},
	}

	cmd := op.command()

	viper.Reset()
	viper.Set("nocolor", true)
	viper.Set("tty", true)
	Init("test", "1.0.0")
	Defaults()
	capture := &strings.Builder{}
	Stdout = capture
	Stderr = capture
	cmd.SetOutput(Stdout)
	viper.Set("rsh-server", "http://example2.com/prefix")
	cmd.Flags().Parse([]string{"--search=foo", "--def-3=abc", "--accept=application/json"})
	cmd.Run(cmd, []string{"id1"})

	assert.Equal(t, "HTTP/1.1 200 OK\nContent-Type: application/json\n\n{\n  hello: \"world\"\n}\n", capture.String())
}
