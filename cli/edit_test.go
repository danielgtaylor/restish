package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/h2non/gock.v1"
)

func TestEditSuccess(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").
		Get("/items/foo").
		Reply(http.StatusOK).
		SetHeader("Etag", "abc123").
		JSON(map[string]interface{}{
			"foo": 123,
		})

	gock.New("http://example.com").
		Put("/items/foo").
		MatchHeader("If-Match", "abc123").
		BodyString(
			`{"foo": 123, "bar": 456}`,
		).
		Reply(http.StatusOK)

	os.Setenv("VISUAL", "")
	os.Setenv("EDITOR", "true") // dummy to just return
	edit("http://example.com/items/foo", []string{"bar:456"}, true, true, func(int) {}, json.Marshal, json.Unmarshal, "json")
}

func TestEditNonInteractiveArgsRequired(t *testing.T) {
	code := 999
	edit("http://example.com/items/foo", []string{}, false, true, func(c int) {
		code = c
	}, json.Marshal, json.Unmarshal, "json")

	assert.Equal(t, 1, code)
}

func TestEditInteractiveMissingEditor(t *testing.T) {
	os.Setenv("VISUAL", "")
	os.Setenv("EDITOR", "")
	code := 999
	edit("http://example.com/items/foo", []string{}, true, true, func(c int) {
		code = c
	}, json.Marshal, json.Unmarshal, "json")

	assert.Equal(t, 1, code)
}

func TestEditBadGet(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").
		Get("/items/foo").
		Reply(http.StatusInternalServerError)

	code := 999
	edit("http://example.com/items/foo", []string{"foo:123"}, false, true, func(c int) {
		code = c
	}, json.Marshal, json.Unmarshal, "json")

	assert.Equal(t, 1, code)
}

func TestEditNoChange(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").
		Get("/items/foo").
		Reply(http.StatusOK).
		SetHeader("Etag", "abc123").
		JSON(map[string]interface{}{
			"foo": 123,
		})

	code := 999
	edit("http://example.com/items/foo", []string{"foo:123"}, false, true, func(c int) {
		code = c
	}, json.Marshal, json.Unmarshal, "json")

	assert.Equal(t, 0, code)
}

func TestEditNotObject(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").
		Get("/items/foo").
		Reply(http.StatusOK).
		SetHeader("Etag", "abc123").
		JSON([]interface{}{
			123,
		})

	code := 999
	edit("http://example.com/items/foo", []string{"foo:123"}, false, true, func(c int) {
		code = c
	}, json.Marshal, json.Unmarshal, "json")

	assert.Equal(t, 1, code)
}
