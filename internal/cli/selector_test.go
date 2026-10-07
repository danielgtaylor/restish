package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	pluginwire "github.com/rest-sh/restish/v2/plugin"
)

func TestSelectOneNavigation(t *testing.T) {
	options := []pluginwire.SelectOption{{Label: "Mochi", Value: "42"}, {Label: "Pixel", Value: "7"}}
	tests := []struct {
		name, input, want string
	}{
		{name: "enter selects first", input: "\r", want: "42"},
		{name: "down selects second", input: "\x1b[B\r", want: "7"},
		{name: "up wraps to last", input: "\x1b[A\r", want: "7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			c := &CLI{Stderr: &stderr}
			c.hooks.PassReader = strings.NewReader(tt.input)
			got, err := c.selectOne(context.Background(), "Choose\npet", options)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("selectOne = %q, want %q", got, tt.want)
			}
			if output := stderr.String(); !strings.Contains(output, "Choose pet") || !strings.Contains(output, "Pixel") {
				t.Fatalf("selector output = %q", output)
			}
		})
	}
}

func TestSelectOneBoundaries(t *testing.T) {
	c := &CLI{Stdin: strings.NewReader("must not be read"), Stderr: &bytes.Buffer{}}
	if _, err := c.selectOne(context.Background(), "Choose", nil); err == nil {
		t.Fatal("empty options accepted")
	}
	if got, err := c.selectOne(context.Background(), "Choose", []pluginwire.SelectOption{{Label: "Only", Value: "one"}}); err != nil || got != "one" {
		t.Fatalf("single option = %q, %v", got, err)
	}
	if _, err := c.selectOne(context.Background(), "Choose", []pluginwire.SelectOption{{Label: "One"}, {Label: "Two"}}); err == nil {
		t.Fatal("non-interactive selection accepted")
	}
	c.hooks.PassReader = strings.NewReader("\x03")
	if _, err := c.selectOne(context.Background(), "Choose", []pluginwire.SelectOption{{Label: "One"}, {Label: "Two"}}); !errors.Is(err, errSelectCanceled) {
		t.Fatalf("Ctrl-C error = %v", err)
	}
}
