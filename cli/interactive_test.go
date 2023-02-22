package cli

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/h2non/gock.v1"
)

type mockAsker struct {
	t         *testing.T
	pos       int
	responses []string
}

func (a *mockAsker) askConfirm(message string, def bool, help string) bool {
	a.pos++
	a.t.Log("confirm", a.responses[a.pos-1])
	return a.responses[a.pos-1] == "y"
}

func (a *mockAsker) askInput(message string, def string, required bool, help string) string {
	a.pos++
	a.t.Log("input", a.responses[a.pos-1])
	return a.responses[a.pos-1]
}

func (a *mockAsker) askSelect(message string, options []string, def interface{}, help string) string {
	a.pos++
	a.t.Log("select", a.responses[a.pos-1])
	return a.responses[a.pos-1]
}

func TestInteractive(t *testing.T) {
	// Remove existing config if present...
	os.Remove(filepath.Join(userHomeDir(), ".test", "apis.json"))
	os.Remove(filepath.Join(userHomeDir(), ".test", "cache.json"))

	reset(false)

	defer gock.Off()

	gock.New("http://api.example.com").Get("/").Reply(200).JSON(map[string]interface{}{
		"Hello": "World",
	})

	gock.New("http://api.example.com").Get("/openapi.json").Reply(404)
	gock.New("http://api.example.com").Get("/openapi.yaml").Reply(404)

	mock := &mockAsker{
		t: t,
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

type testLoader struct {
	API API
}

func (l *testLoader) LocationHints() []string {
	return []string{"/openapi.json"}
}

func (l *testLoader) Detect(resp *http.Response) bool {
	return true
}

func (l *testLoader) Load(entrypoint, spec url.URL, resp *http.Response) (API, error) {
	LogInfo("Loading API %s", entrypoint.String())
	return l.API, nil
}

func TestInteractiveAutoConfig(t *testing.T) {
	// Remove existing config if present...
	os.Remove(filepath.Join(userHomeDir(), ".test", "apis.json"))
	os.Remove(filepath.Join(userHomeDir(), ".test", "cache.json"))

	reset(false)
	AddLoader(&testLoader{
		API: API{
			Short: "Swagger Petstore",
			Auth: []APIAuth{
				{
					Name: "oauth-authorization-code",
					Params: map[string]string{
						"client_id":     "",
						"authorize_url": "https://example.com/authorize",
						"token_url":     "https://example.com/token",
					},
				},
			},
			Operations: []Operation{
				{
					Name:         "createpets",
					Short:        "Create a pet",
					Long:         "\n## Response 201\n\nNull response\n\n## Response default (application/json)\n\nunexpected error\n\n```schema\n{\n  code*: (integer format:int32) \n  message*: (string) \n}\n```\n",
					Method:       "POST",
					URITemplate:  "http://api.example.com/pets",
					PathParams:   []*Param{},
					QueryParams:  []*Param{},
					HeaderParams: []*Param{},
				},
				{
					Name:        "listpets",
					Short:       "List all pets",
					Long:        "\n## Response 200 (application/json)\n\nA paged array of pets\n\n```schema\n[\n  {\n    id*: (integer format:int64) \n    name*: (string) \n    tag: (string) \n  }\n]\n```\n\n## Response default (application/json)\n\nunexpected error\n\n```schema\n{\n  code*: (integer format:int32) \n  message*: (string) \n}\n```\n",
					Method:      "GET",
					URITemplate: "http://api.example.com/pets",
					PathParams:  []*Param{},
					QueryParams: []*Param{
						{
							Type:        "integer",
							Name:        "limit",
							Description: "How many items to return at one time (max 100)",
						},
					},
					HeaderParams: []*Param{},
				},
				{
					Name:        "showpetbyid",
					Short:       "Info for a specific pet",
					Long:        "\n## Response 200 (application/json)\n\nExpected response to a valid request\n\n```schema\n{\n  id*: (integer format:int64) \n  name*: (string) \n  tag: (string) \n}\n```\n\n## Response default (application/json)\n\nunexpected error\n\n```schema\n{\n  code*: (integer format:int32) \n  message*: (string) \n}\n```\n",
					Method:      "GET",
					URITemplate: "http://api.example.com/pets/{petId}",
					PathParams: []*Param{
						{
							Type:        "string",
							Name:        "petId",
							Description: "The id of the pet to retrieve",
						},
					},
					QueryParams:  []*Param{},
					HeaderParams: []*Param{},
				},
			},
			AutoConfig: AutoConfig{
				Prompt: map[string]AutoConfigVar{
					"client_id": {
						Description: "Client identifier",
						Example:     "abc123",
					},
				},
				Auth: APIAuth{
					Name: "oauth-authorization-code",
					Params: map[string]string{
						"client_id":     "",
						"authorize_url": "https://example.com/authorize",
						"token_url":     "https://example.com/token",
					},
				},
			},
		},
	})
	defer reset(false)

	defer gock.Off()

	gock.New("http://api2.example.com").Get("/").Reply(200).JSON(map[string]interface{}{
		"Hello": "World",
	})

	gock.New("http://api2.example.com").Get("/openapi.json").Reply(200).BodyString("dummy")

	mock := &mockAsker{
		t: t,
		responses: []string{
			"foo",
			"Save and exit",
		},
	}

	askInitAPI(mock, Root, []string{"autoconfig", "http://api2.example.com"})
}
