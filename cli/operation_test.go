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

	gock.New("http://example.com").Get("/test/id1").MatchParam("search", "foo").Reply(200).JSON(map[string]interface{}{
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
		},
	}

	cmd := op.command()

	viper.Reset()
	Init("test", "1.0.0")
	Defaults()
	viper.Set("nocolor", true)
	capture := &strings.Builder{}
	Stdout = capture
	Stderr = capture
	cmd.Flags().Parse([]string{"--search=foo"})
	cmd.Run(cmd, []string{"id1"})

	assert.Equal(t, "HTTP/1.1 200 OK\nContent-Type: application/json\n\n{\n  hello: \"world\"\n}\n", capture.String())
}
