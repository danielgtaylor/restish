package cli

import (
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
