package cli

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/h2non/gock.v1"
)

func TestRequestPagination(t *testing.T) {
	defer gock.Off()

	gock.New("http://example.com").
		Get("/paginated").
		Reply(http.StatusOK).
		// Page 1 links to page 2
		SetHeader("Link", "</paginated2>; rel=\"next\"").
		SetHeader("Content-Length", "7").
		JSON([]interface{}{1, 2, 3})
	gock.New("http://example.com").
		Get("/paginated2").
		Reply(http.StatusOK).
		// Page 2 links to page 3
		SetHeader("Link", "</paginated3>; rel=\"next\"").
		SetHeader("Content-Length", "5").
		JSON([]interface{}{4, 5})
	gock.New("http://example.com").
		Get("/paginated3").
		Reply(http.StatusOK).
		SetHeader("Content-Length", "3").
		JSON([]interface{}{6})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/paginated", nil)
	resp, err := GetParsedResponse(req)

	assert.NoError(t, err)
	assert.Equal(t, resp.Status, http.StatusOK)

	// Content length should be the sum of all combined.
	assert.Equal(t, resp.Headers["Content-Length"], "15")

	// Response body should be a concatenation of all pages.
	assert.Equal(t, []interface{}{1.0, 2.0, 3.0, 4.0, 5.0, 6.0}, resp.Body)
}
