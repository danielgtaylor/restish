package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/danielgtaylor/shorthand"
	yaml "gopkg.in/yaml.v2"
)

// GetBody returns the request body if one was passed either as shorthand
// arguments or via stdin.
func GetBody(mediaType string, args []string) (string, error) {
	var body string

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
			return "", fmt.Errorf("Not sure how to marshal %s", mediaType)
		}
	}

	return body, nil
}
