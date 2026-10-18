// Package input builds HTTP request bodies from CLI arguments and/or stdin.
package input

import (
	"fmt"
	"io"
	"strings"

	"github.com/danielgtaylor/shorthand/v2"
)

const MaxStdinBodyBytes = 16 << 20

// BodyOptions controls request-body parsing.
type BodyOptions struct {
	Warnf        func(string, ...any)
	PromptValues []PromptValue
}

// PromptValue is a body path and exact string value read from a hidden prompt.
type PromptValue struct {
	Path  string
	Value string
}

// BodyInfo describes which CLI input sources contributed to the returned body.
type BodyInfo struct {
	UsedStdin  bool
	UsedArgs   bool
	UsedPrompt bool
}

// Body parses positional CLI args (shorthand syntax) and/or stdin into a
// request body. The rules are:
//
//  1. No args, stdin is a TTY → nil body (no Content-Type sent).
//  2. No args, data on stdin → parsed as shorthand/JSON/YAML and returned
//     as a Go value so the content registry can marshal it correctly.
//  3. Args only → joined with spaces (matches how the shell splits tokens)
//     and parsed as shorthand.
//  4. Stdin + args → stdin document is the base; args are applied as a
//     shorthand patch on top.
//  5. Prompt values are applied last without reparsing their secret values.
//
// The returned body is a Go value (map/slice/scalar) ready to be marshalled
// by the content registry, or nil if there is no body.
// stdinReader is cli.Stdin; pass strings.NewReader("") with stdinIsTTY=true
// in tests to simulate an empty terminal.
func Body(stdinReader io.Reader, stdinIsTTY bool, args []string, contentType string, bodyOpts BodyOptions) (any, error) {
	body, _, err := BodyWithInfo(stdinReader, stdinIsTTY, args, contentType, bodyOpts)
	return body, err
}

// BodyWithInfo is like Body, but also reports which input sources contributed
// to the returned body.
func BodyWithInfo(stdinReader io.Reader, stdinIsTTY bool, args []string, contentType string, bodyOpts BodyOptions) (any, BodyInfo, error) {
	opts := shorthand.ParseOptions{
		EnableFileInput:       enableFileInput(contentType),
		EnableObjectDetection: true,
	}

	var base any
	var info BodyInfo
	hasPatches := len(args) > 0 || len(bodyOpts.PromptValues) > 0

	if !stdinIsTTY {
		data, err := io.ReadAll(io.LimitReader(stdinReader, MaxStdinBodyBytes+1))
		if err != nil {
			return nil, info, err
		}
		if len(data) > MaxStdinBodyBytes {
			return nil, info, fmt.Errorf("stdin body exceeds %d bytes; use @file input or reduce stdin size", MaxStdinBodyBytes)
		}
		if len(data) > 0 {
			info.UsedStdin = true
			parsed, serr := shorthand.Unmarshal(string(data), opts, nil)
			if serr != nil {
				// Not parseable as structured data — return raw string so the
				// content registry passes it through as the request body.
				if !hasPatches {
					return string(data), info, nil
				}
				if bodyOpts.Warnf != nil {
					bodyOpts.Warnf("stdin body could not be parsed as structured shorthand; applying body patches only")
				}
				info.UsedStdin = false
				// Can't patch non-structured stdin; ignore it as a base document.
			} else {
				if !hasPatches {
					return parsed, info, nil
				}
				if !isStructuredBody(parsed) {
					if bodyOpts.Warnf != nil {
						bodyOpts.Warnf("stdin body is not a structured object or array; applying body patches only")
					}
					parsed = nil
					info.UsedStdin = false
				}
				base = parsed
			}
		}
	}

	if !hasPatches {
		return nil, info, nil
	}

	result := base
	if len(args) > 0 {
		// Args are shell-split tokens; join with spaces to reconstruct the
		// full shorthand expression (e.g. ["name:", "Alice,", "age:", "30"]
		// → "name: Alice, age: 30").
		var err error
		result, err = shorthand.Unmarshal(strings.Join(args, " "), opts, result)
		if err != nil {
			return nil, info, err
		}
		info.UsedArgs = true
	}
	for _, prompted := range bodyOpts.PromptValues {
		doc := shorthand.NewDocument(opts)
		doc.Operations = []shorthand.Operation{{Kind: shorthand.OpSet, Path: prompted.Path, Value: prompted.Value}}
		var err error
		result, err = doc.Apply(result)
		if err != nil {
			return nil, info, err
		}
		info.UsedPrompt = true
	}
	return result, info, nil
}

func isStructuredBody(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

func enableFileInput(contentType string) bool {
	switch strings.ToLower(contentType) {
	case "form", "multipart", "application/x-www-form-urlencoded", "multipart/form-data":
		return false
	default:
		return true
	}
}
