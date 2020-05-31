package cli

import (
	"fmt"
	"os"
	"path"
	"testing"

	"gopkg.in/h2non/gock.v1"
)

type mockAsker struct {
	pos       int
	responses []string
}

func (a *mockAsker) askConfirm(message string, def bool, help string) bool {
	a.pos++
	fmt.Println("confirm", a.responses[a.pos-1])
	return a.responses[a.pos-1] == "y"
}

func (a *mockAsker) askInput(message string, def string, required bool, help string) string {
	a.pos++
	fmt.Println("input", a.responses[a.pos-1])
	return a.responses[a.pos-1]
}

func (a *mockAsker) askSelect(message string, options []string, def interface{}, help string) string {
	a.pos++
	fmt.Println("select", a.responses[a.pos-1])
	return a.responses[a.pos-1]
}

func TestInteractive(t *testing.T) {
	// Remove existing config if present...
	os.Remove(path.Join(userHomeDir(), ".test", "apis.json"))

	reset(false)

	defer gock.Off()

	gock.New("http://api.example.com").Get("/").Reply(200).JSON(map[string]interface{}{
		"Hello": "World",
	})

	gock.New("http://api.example.com").Get("/openapi.json").Reply(404)
	gock.New("http://api.example.com").Get("/openapi.yaml").Reply(404)

	mock := &mockAsker{
		responses: []string{
			// TODO: Add a bunch more responses for various code paths.
			"http://api.example.com",
			"Add header",
			"Foo",
			"bar",
			"Add query param",
			"search",
			"bar",
			"Setup auth",
			"http-basic",
			"u",
			"p",
			"n",
			"Finished with profile",
			"Edit profile default",
			"Delete header Foo",
			"y",
			"Finished with profile",
			"Change base URI",
			"http://api.example.com",
			"Save and exit",
		},
	}

	askInitAPI(mock, Root, []string{"example"})
}
