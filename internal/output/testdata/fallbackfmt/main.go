package main

import (
	"fmt"
	"os"

	"github.com/rest-sh/restish/v2/plugin"
)

func main() {
	manifest := plugin.Manifest{
		Name:              "test-fallback-fmt",
		Version:           "1.0.0",
		Description:       "Test host formatter fallback",
		RestishAPIVersion: 2,
		Hooks:             []string{"formatter"},
		RequiredFeatures:  []string{plugin.FeatureFormatterHostFallback},
		FormatterNames:    []string{"testfallback"},
	}
	if plugin.HandleStartupFlags(os.Stdout, manifest, nil) {
		return
	}

	dec := plugin.NewDecoder(os.Stdin)
	for {
		var req plugin.FormatterRequest
		if err := dec.ReadMessage(&req); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := plugin.WriteMessage(os.Stdout, plugin.FormatterOutput{
			Type:      plugin.MsgTypeFormatterOutput,
			RequestID: req.RequestID,
			Fallback:  req.Response.Body != nil,
			Complete:  true,
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if req.Event == "end" {
			return
		}
	}
}
