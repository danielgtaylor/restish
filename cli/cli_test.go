package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"gopkg.in/h2non/gock.v1"
)

func run(cmd string) string {
	viper.Set("nocolor", true)
	Init("test", "1.0.0'")
	AddContentType("application/json", 1, JSON{})

	capture := &strings.Builder{}
	Stdout = capture
	Root.ParseFlags(strings.Split(cmd, " "))
	args := Root.Flags().Args()
	Root.Run(Root, args)

	return capture.String()
}

func expectJSON(t *testing.T, cmd string, expected interface{}) {
	captured := run("-o json " + cmd)

	var actual map[string]interface{}
	err := json.Unmarshal([]byte(captured), &actual)
	assert.NoError(t, err)
	assert.Equal(t, expected, actual["body"])
}

func TestGetURI(t *testing.T) {
	defer gock.Off()

	// TODO: set up gock, make a request
	gock.New("http://example.com").Get("/foo").Reply(200).JSON(map[string]interface{}{
		"Hello": "World",
	})

	expectJSON(t, "http://example.com/foo", map[string]interface{}{
		"Hello": "World",
	})
}
