package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIContentTypes(t *testing.T) {
	captured := run("api content-types")
	assert.Contains(t, captured, "application/json")
	assert.Contains(t, captured, "table")
	assert.Contains(t, captured, "readable")
}

func TestAPIShow(t *testing.T) {
	reset(false)
	configs["test"] = &APIConfig{
		name: "test",
		Base: "https://api.example.com",
	}
	captured := runNoReset("api show test")
	assert.Equal(t, captured, "\x1b[38;5;247m{\x1b[0m\n  \x1b[38;5;74m\"base\"\x1b[0m\x1b[38;5;247m:\x1b[0m \x1b[38;5;150m\"https://api.example.com\"\x1b[0m\n\x1b[38;5;247m}\x1b[0m\n")
}

func TestEditAPIsMissingEditor(t *testing.T) {
	os.Setenv("EDITOR", "")
	os.Setenv("VISUAL", "")
	exited := false
	editAPIs(func(code int) {
		exited = true
	})
	assert.True(t, exited)
}

func TestEditBadCommand(t *testing.T) {
	os.Setenv("EDITOR", "bad-command")
	os.Setenv("VISUAL", "")
	assert.Panics(t, func() {
		editAPIs(func(code int) {})
	})
}
