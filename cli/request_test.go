package cli

import (
	"bytes"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"gopkg.in/h2non/gock.v1"
)

func TestFixAddress(t *testing.T) {
	assert.Equal(t, "https://example.com", fixAddress("example.com"))
	assert.Equal(t, "http://localhost:8000", fixAddress(":8000"))
	assert.Equal(t, "http://localhost:8000", fixAddress("localhost:8000"))

	configs["test"] = &APIConfig{
		Base: "https://example.com",
	}
	assert.Equal(t, "https://example.com/foo", fixAddress("test/foo"))
	delete(configs, "test")
}

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

type authHookFailure struct{}

func (a *authHookFailure) Parameters() []AuthParam {
	return []AuthParam{}
}

func (a *authHookFailure) OnRequest(req *http.Request, key string, params map[string]string) error {
	return errors.New("some-error")
}

func TestAuthHookFailure(t *testing.T) {
	configs["auth-hook-fail"] = &APIConfig{
		Profiles: map[string]*APIProfile{
			"default": {
				Auth: &APIAuth{
					Name: "hook-fail",
				},
			},
		},
	}

	authHandlers["hook-fail"] = &authHookFailure{}

	r, _ := http.NewRequest(http.MethodGet, "/test", nil)
	assert.PanicsWithError(t, "some-error", func() {
		MakeRequest(r)
	})
}

func TestGetStatus(t *testing.T) {
	defer gock.Off()

	reset(false)
	lastStatus = 0

	gock.New("http://example.com").
		Get("/").
		Reply(http.StatusOK)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	resp, err := MakeRequest(req)

	assert.NoError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)

	assert.Equal(t, http.StatusOK, GetLastStatus())
}

func TestIgnoreStatus(t *testing.T) {
	defer gock.Off()

	reset(false)
	lastStatus = 0

	gock.New("http://example.com").
		Get("/").
		Reply(http.StatusOK)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	resp, err := MakeRequest(req, IgnoreStatus())

	assert.NoError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)

	assert.Equal(t, 0, GetLastStatus())
}

func TestRequestRetryIn(t *testing.T) {
	defer gock.Off()

	reset(false)
	viper.Set("rsh-retry", 1)

	// Duration string value (with units)
	gock.New("http://example.com").
		Get("/").
		Times(1).
		Reply(http.StatusTooManyRequests).
		SetHeader("X-Retry-In", "1ms")

	gock.New("http://example.com").
		Get("/").
		Times(1).
		Reply(http.StatusOK)

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	resp, err := MakeRequest(req)

	assert.NoError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)
}

func TestRequestRetryAfter(t *testing.T) {
	defer gock.Off()

	reset(false)
	viper.Set("rsh-retry", 2)

	// Seconds value
	gock.New("http://example.com").
		Put("/").
		Times(1).
		Reply(http.StatusTooManyRequests).
		SetHeader("Retry-After", "0")

	// HTTP date value
	gock.New("http://example.com").
		Put("/").
		Times(1).
		Reply(http.StatusTooManyRequests).
		SetHeader("Retry-After", time.Now().Format(http.TimeFormat))

	gock.New("http://example.com").
		Put("/").
		Times(1).
		Reply(http.StatusOK)

	req, _ := http.NewRequest(http.MethodPut, "http://example.com/", bytes.NewReader([]byte("hello")))
	resp, err := MakeRequest(req)

	assert.NoError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)
}

func TestRequestRetryTimeout(t *testing.T) {
	defer gock.Off()

	reset(false)
	viper.Set("rsh-retry", 1)
	viper.Set("rsh-timeout", 1*time.Millisecond)

	// Duration string value (with units)
	gock.New("http://example.com").
		Get("/").
		Times(2).
		Reply(http.StatusOK).
		Delay(2 * time.Millisecond)
		// Note: delay seems to have a bug where subsequent requests without the
		// delay are still delayed... For now just have it reply twice.

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	_, err := MakeRequest(req)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "timed out")
}
