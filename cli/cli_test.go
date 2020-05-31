package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"gopkg.in/h2non/gock.v1"
)

func reset(color bool) {
	viper.Reset()

	if color {
		viper.Set("color", true)
	} else {
		viper.Set("nocolor", true)
	}

	Init("test", "1.0.0'")
	Defaults()
}

func run(cmd string, color ...bool) string {
	if len(color) == 0 || !color[0] {
		reset(false)
	} else {
		reset(true)
	}

	capture := &strings.Builder{}
	Stdout = capture
	Stderr = capture
	os.Args = strings.Split("restish "+cmd, " ")
	Run()

	return capture.String()
}

func expectJSON(t *testing.T, cmd string, expected string) {
	captured := run("-o json -f body " + cmd)
	assert.JSONEq(t, expected, captured)
}

func TestGetURI(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").Get("/foo").Reply(200).JSON(map[string]interface{}{
		"Hello": "World",
	})

	expectJSON(t, "http://example.com/foo", `{
		"Hello": "World"
	}`)
}

func TestPostURI(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").Post("/foo").Reply(200).JSON(map[string]interface{}{
		"id":    1,
		"value": 123,
	})

	expectJSON(t, "post http://example.com/foo value: 123", `{
		"id": 1,
		"value": 123
	}`)
}

func TestPutURI400(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").Put("/foo/1").Reply(400).JSON(map[string]interface{}{
		"detail": "Invalid input",
	})

	expectJSON(t, "put http://example.com/foo/1 value: 123", `{
		"detail": "Invalid input"
	}`)
}

func TestLinks(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").Get("/foo").Reply(204).SetHeader("Link", "</bar>; rel=\"item\"")

	captured := run("links http://example.com/foo")
	assert.JSONEq(t, `{
		"item": [
			{
				"rel": "item",
				"uri": "http://example.com/bar"
			}
		]
	}`, captured)
}

func TestDefaultOutput(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").Get("/foo").Reply(200).JSON(map[string]interface{}{
		"hello": "world",
	})

	captured := run("http://example.com/foo", true)
	assert.Equal(t, "\x1b[38;5;204mHTTP\x1b[0m/\x1b[38;5;172m1.1\x1b[0m \x1b[38;5;172m200\x1b[0m \x1b[38;5;74mOK\x1b[0m\n\x1b[38;5;74mContent-Type\x1b[0m: application/json\n\n\x1b[38;5;247m{\x1b[0m\n  \x1b[38;5;74mhello\x1b[0m\x1b[38;5;247m:\x1b[0m \x1b[38;5;150m\"world\"\x1b[0m\x1b[38;5;247m\n}\x1b[0m\n", captured)
}
