package cli

// AutoConfigVar represents a variable given by the user when prompted during
// auto-configuration setup of an API.
type AutoConfigVar struct {
	Description string        `json:"description,omitempty" yaml:"description,omitempty"`
	Example     string        `json:"example,omitempty" yaml:"example,omitempty"`
	Default     interface{}   `json:"default,omitempty" yaml:"default,omitempty"`
	Enum        []interface{} `json:"enum,omitempty" yaml:"enum,omitempty"`

	// Exclude the value from being sent to the server. This essentially makes
	// it a value which is only used in param templates.
	Exclude bool `json:"exclude,omitempty"`
}

// AutoConfig holds an API's automatic configuration settings for the CLI. These
// are advertised via OpenAPI extension and picked up by the CLI to make it
// easier to get started using an API.
type AutoConfig struct {
	Headers map[string]string        `json:"headers,omitempty" yaml:"headers,omitempty"`
	Prompt  map[string]AutoConfigVar `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Auth    APIAuth                  `json:"auth,omitempty" yaml:"auth,omitempty"`
}
