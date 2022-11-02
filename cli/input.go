package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/danielgtaylor/shorthand"
	yaml "gopkg.in/yaml.v2"
)

// Stdin represents the command input, which defaults to os.Stdin.
var Stdin interface {
	Stat() (fs.FileInfo, error)
	io.Reader
} = os.Stdin

// GetBody returns the request body if one was passed either as shorthand
// arguments or via stdin.
func GetBody(mediaType string, args []string) (string, error) {
	var body string

	if info, err := Stdin.Stat(); err == nil {
		if len(args) == 0 && (info.Mode()&os.ModeCharDevice) == 0 {
			// There are no args but there is data on stdin. Just read it and
			// pass it through as it may not be structured data we can parse or
			// could be binary (e.g. file uploads).
			b, err := io.ReadAll(Stdin)
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
	}

	input, err := shorthand.GetInput(args)
	if err != nil {
		return "", err
	}

	if input != nil {
		if strings.Contains(mediaType, "json") {
			marshalled, err := json.Marshal(input)
			if err != nil {
				return "", err
			}
			body = string(marshalled)
		} else if strings.Contains(mediaType, "yaml") {
			marshalled, err := yaml.Marshal(input)
			if err != nil {
				return "", err
			}
			body = string(marshalled)
		} else {
			return "", fmt.Errorf("not sure how to marshal %s", mediaType)
		}
	}

	return body, nil
}
