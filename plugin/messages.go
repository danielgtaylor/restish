package plugin

// Message type constants for the command plugin protocol.
// Use these instead of bare strings to avoid typos; a mismatched type string
// causes the host or plugin to silently ignore the message.
const (
	// Plugin → host requests.
	MsgTypeInit         = "init"
	MsgTypeHTTPRequest  = "http-request"
	MsgTypeAPISpec      = "api-spec"
	MsgTypeListAPIs     = "list-apis"
	MsgTypeListProfiles = "list-profiles"
	MsgTypeConfigRead   = "config-read"
	MsgTypePrompt       = "prompt"
	MsgTypeConfirm      = "confirm"
	MsgTypeSelect       = "select"
	MsgTypeResponse     = "response"
	MsgTypeDone         = "done"
	MsgTypeStdoutData   = "stdout-data"
	MsgTypeStderrData   = "stderr-data"
	MsgTypeWarn         = "warn"
	MsgTypeProgress     = "progress"
	MsgTypeSpinner      = "spinner"
	MsgTypeLog          = "log"

	// Host → plugin responses.
	MsgTypeHTTPResponse         = "http-response"
	MsgTypeAPISpecResponse      = "api-spec-response"
	MsgTypeListAPIsResponse     = "list-apis-response"
	MsgTypeListProfilesResponse = "list-profiles-response"
	MsgTypeConfigReadResponse   = "config-read-response"
	MsgTypePromptResponse       = "prompt-response"
	MsgTypeConfirmResponse      = "confirm-response"
	MsgTypeSelectResponse       = "select-response"

	// Host → plugin passthrough-stdio data.
	MsgTypeStdinData  = "stdin-data"
	MsgTypeStdinClose = "stdin-close"

	// TLS signer plugin protocol.
	MsgTypeTLSSignerSign     = "sign"     // host → plugin: sign request
	MsgTypeTLSSignerReady    = "ready"    // plugin → host: ready with certificate
	MsgTypeTLSSignerShutdown = "shutdown" // host → plugin: graceful shutdown request
)

// InitMsg is the first message sent from the host to the plugin after startup.
// It carries the sub-command name and the raw CLI arguments.
type InitMsg struct {
	Type    string   `cbor:"type"`
	Command string   `cbor:"command"`
	Args    []string `cbor:"args"`
}

// HTTPRequestMsg asks the host to perform an HTTP request on behalf of the
// plugin and reply with an HTTPResponseMsg.
type HTTPRequestMsg struct {
	Type        string            `cbor:"type"`
	RequestID   string            `cbor:"request_id,omitempty"`
	Method      string            `cbor:"method,omitempty"`
	URI         string            `cbor:"uri"`
	Headers     map[string]string `cbor:"headers,omitempty"`
	Body        any               `cbor:"body,omitempty"`
	ContentType string            `cbor:"content_type,omitempty"`
	// NoCache bypasses the response cache for this request.
	NoCache bool `cbor:"no_cache,omitempty"`
	// CacheTTL (seconds) injects Cache-Control: max-age=N on the request.
	// Only entries fresher than this TTL are served from the cache.
	CacheTTL int `cbor:"cache_ttl,omitempty"`
	// Timeout (seconds) sets a per-request deadline. 0 means no deadline.
	Timeout int `cbor:"timeout,omitempty"`
	// Filter is an optional shorthand or jq expression. When set, the host
	// applies it to the full response document before sending it back.
	Filter string `cbor:"filter,omitempty"`
}

// HTTPResponseMsg is the host reply to an HTTPRequestMsg.
type HTTPResponseMsg struct {
	Type      string              `cbor:"type"`
	RequestID string              `cbor:"request_id,omitempty"`
	Status    int                 `cbor:"status"`
	Headers   map[string][]string `cbor:"headers,omitempty"`
	URL       string              `cbor:"url,omitempty"`
	Links     map[string]any      `cbor:"links,omitempty"`
	Body      any                 `cbor:"body"`
	// Error is set when the HTTP request itself failed.
	Error string `cbor:"error,omitempty"`
}

// APISpecMsg asks the host to load the OpenAPI spec for a registered API.
type APISpecMsg struct {
	Type      string `cbor:"type"`
	RequestID string `cbor:"request_id,omitempty"`
	Name      string `cbor:"name"`
	Profile   string `cbor:"profile,omitempty"`
}

// APISpecResponseMsg is the host reply to an APISpecMsg.
type APISpecResponseMsg struct {
	Type        string         `cbor:"type"`
	RequestID   string         `cbor:"request_id,omitempty"`
	Name        string         `cbor:"name"`
	Profile     string         `cbor:"profile,omitempty"`
	ContentType string         `cbor:"content_type,omitempty"`
	Raw         []byte         `cbor:"raw,omitempty"`
	Operations  []APIOperation `cbor:"operations,omitempty"`
	Error       string         `cbor:"error,omitempty"`
}

// APIOperation is the host's resolved, config-aware representation of one
// OpenAPI HTTP operation. It mirrors the generated-command model so command
// plugins do not need to re-parse raw OpenAPI specs.
type APIOperation struct {
	ID                   string         `cbor:"id"`
	Method               string         `cbor:"method"`
	Path                 string         `cbor:"path"`
	Summary              string         `cbor:"summary,omitempty"`
	Description          string         `cbor:"description,omitempty"`
	Deprecated           bool           `cbor:"deprecated,omitempty"`
	Parameters           []APIParam     `cbor:"parameters,omitempty"`
	HasBody              bool           `cbor:"has_body,omitempty"`
	BodyRequired         bool           `cbor:"body_required,omitempty"`
	RequestMediaType     string         `cbor:"request_media_type,omitempty"`
	RequestSchema        map[string]any `cbor:"request_schema,omitempty"`
	RequestSchemaDialect string         `cbor:"request_schema_dialect,omitempty"`
	MCPIgnore            bool           `cbor:"mcp_ignore,omitempty"`
}

// APIParam is a resolved operation parameter for APIOperation.
type APIParam struct {
	Name             string         `cbor:"name"`
	In               string         `cbor:"in"`
	Required         bool           `cbor:"required,omitempty"`
	Description      string         `cbor:"description,omitempty"`
	Type             string         `cbor:"type,omitempty"`
	ItemType         string         `cbor:"item_type,omitempty"`
	Style            string         `cbor:"style,omitempty"`
	Explode          *bool          `cbor:"explode,omitempty"`
	AllowReserved    bool           `cbor:"allow_reserved,omitempty"`
	ContentMediaType string         `cbor:"content_media_type,omitempty"`
	Schema           map[string]any `cbor:"schema,omitempty"`
	SchemaDialect    string         `cbor:"schema_dialect,omitempty"`
	Enum             []string       `cbor:"enum,omitempty"`
}

// ListAPIsMsg asks the host for the list of configured API names.
type ListAPIsMsg struct {
	Type      string `cbor:"type"`
	RequestID string `cbor:"request_id,omitempty"`
}

// ListAPIsResponseMsg is the host reply to a ListAPIsMsg.
type ListAPIsResponseMsg struct {
	Type      string   `cbor:"type"`
	RequestID string   `cbor:"request_id,omitempty"`
	APIs      []string `cbor:"apis"`
	Error     string   `cbor:"error,omitempty"`
}

// ListProfilesMsg asks the host for the profile names of a specific API.
type ListProfilesMsg struct {
	Type      string `cbor:"type"`
	RequestID string `cbor:"request_id,omitempty"`
	API       string `cbor:"api"`
}

// ListProfilesResponseMsg is the host reply to a ListProfilesMsg.
type ListProfilesResponseMsg struct {
	Type      string   `cbor:"type"`
	RequestID string   `cbor:"request_id,omitempty"`
	API       string   `cbor:"api"`
	Profiles  []string `cbor:"profiles"`
	Error     string   `cbor:"error,omitempty"`
}

// ConfigReadMsg asks the host for the effective configuration of an API
// profile (base URL, persistent headers and query params), and/or the
// plugin-specific config stored under plugins[Plugin] in restish.json.
type ConfigReadMsg struct {
	Type      string `cbor:"type"`
	RequestID string `cbor:"request_id,omitempty"`
	API       string `cbor:"api,omitempty"`
	Profile   string `cbor:"profile,omitempty"`
	// Plugin is the plugin's short name (without the "restish-" prefix).
	// When set, the response includes PluginConfig populated from
	// restish.json's plugins[Plugin] entry.
	Plugin string `cbor:"plugin,omitempty"`
}

// ConfigReadResponseMsg is the host reply to a ConfigReadMsg.
// Auth secrets are intentionally excluded.
type ConfigReadResponseMsg struct {
	Type      string   `cbor:"type"`
	RequestID string   `cbor:"request_id,omitempty"`
	BaseURL   string   `cbor:"base_url,omitempty"`
	Headers   []string `cbor:"headers,omitempty"`
	Query     []string `cbor:"query,omitempty"`
	Error     string   `cbor:"error,omitempty"`
	// PluginConfig holds the parsed plugins[name] entry from restish.json,
	// or nil when no config is stored for this plugin.
	PluginConfig any `cbor:"plugin_config,omitempty"`
}

// PromptMsg asks the host to display a message and read one line from the
// user. When Hidden is true and stdin is a TTY, echo is suppressed (suitable
// for password entry).
type PromptMsg struct {
	Type      string `cbor:"type"`
	RequestID string `cbor:"request_id,omitempty"`
	Message   string `cbor:"message"`
	Hidden    bool   `cbor:"hidden,omitempty"`
}

// PromptResponseMsg is the host reply to a PromptMsg.
type PromptResponseMsg struct {
	Type      string `cbor:"type"`
	RequestID string `cbor:"request_id,omitempty"`
	Value     string `cbor:"value,omitempty"`
	Error     string `cbor:"error,omitempty"`
}

// ConfirmMsg asks the host to display a message and read a yes/no answer.
type ConfirmMsg struct {
	Type      string `cbor:"type"`
	RequestID string `cbor:"request_id,omitempty"`
	Message   string `cbor:"message"`
}

// ConfirmResponseMsg is the host reply to a ConfirmMsg.
type ConfirmResponseMsg struct {
	Type      string `cbor:"type"`
	RequestID string `cbor:"request_id,omitempty"`
	Value     bool   `cbor:"value"`
	Error     string `cbor:"error,omitempty"`
}

// SelectOption pairs a user-visible label with an opaque plugin-owned value.
type SelectOption struct {
	Label string `cbor:"label"`
	Value string `cbor:"value"`
}

// SelectMsg asks the host to display an interactive single-choice picker.
type SelectMsg struct {
	Type      string         `cbor:"type"`
	RequestID string         `cbor:"request_id,omitempty"`
	Message   string         `cbor:"message"`
	Options   []SelectOption `cbor:"options"`
}

// SelectResponseMsg is the host reply to a SelectMsg.
type SelectResponseMsg struct {
	Type      string `cbor:"type"`
	RequestID string `cbor:"request_id,omitempty"`
	Value     string `cbor:"value,omitempty"`
	Error     string `cbor:"error,omitempty"`
}

// FormatterResponse is the normalized response shape forwarded to formatter
// plugins. The host may include the full response body on "start" for a normal
// one-shot render, or send body values incrementally on subsequent "item"
// messages for paginated and event-stream output.
type FormatterResponse struct {
	Proto   string              `cbor:"proto,omitempty" json:"proto,omitempty"`
	Status  int                 `cbor:"status,omitempty" json:"status,omitempty"`
	Headers map[string][]string `cbor:"headers,omitempty" json:"headers,omitempty"`
	Links   map[string]any      `cbor:"links,omitempty" json:"links,omitempty"`
	Body    any                 `cbor:"body,omitempty" json:"body,omitempty"`
}

// FormatterRequest is sent to formatter plugins. Type is always "formatter"
// and Event is one of "start", "item", or "end".
type FormatterRequest struct {
	Type     string            `cbor:"type" json:"type"`
	Format   string            `cbor:"format" json:"format"`
	Color    bool              `cbor:"color,omitempty" json:"color,omitempty"`
	Event    string            `cbor:"event" json:"event"`
	Response FormatterResponse `cbor:"response" json:"response"`
}

// LoaderRequest is sent to plugins registered for the "loader" hook. Body
// contains the source document bytes. ContentType, SourceURL, and LocalPath are
// metadata from discovery or cache when the host has it.
type LoaderRequest struct {
	Type        string `cbor:"type" json:"type"`
	Body        []byte `cbor:"body" json:"body"`
	ContentType string `cbor:"content_type,omitempty" json:"content_type,omitempty"`
	SourceURL   string `cbor:"source_url,omitempty" json:"source_url,omitempty"`
	LocalPath   string `cbor:"local_path,omitempty" json:"local_path,omitempty"`
}

// LoaderResponse is returned by a "loader" hook plugin. Body must contain an
// OpenAPI document in JSON or YAML form. ContentType may describe that returned
// body when it differs from the input document's content type.
type LoaderResponse struct {
	// Body is an OpenAPI document as []byte or string.
	Body        any    `cbor:"body" json:"body"`
	ContentType string `cbor:"content_type,omitempty" json:"content_type,omitempty"`
}

// ResponseMsg asks the host to format and display a response using the
// configured output formatter (same as a regular API response).
type ResponseMsg struct {
	Type    string              `cbor:"type"`
	Status  int                 `cbor:"status,omitempty"`
	Headers map[string][]string `cbor:"headers,omitempty"`
	Body    any                 `cbor:"body,omitempty"`
}

// DoneMsg signals that the plugin has finished. ExitCode 0 is success.
type DoneMsg struct {
	Type     string `cbor:"type"`
	ExitCode int    `cbor:"exit_code,omitempty"`
}

// StdoutDataMsg sends a chunk of raw bytes to the host's stdout.
type StdoutDataMsg struct {
	Type string `cbor:"type"`
	Data []byte `cbor:"data"`
}

// StderrDataMsg sends a chunk of raw bytes to the host's stderr.
type StderrDataMsg struct {
	Type string `cbor:"type"`
	Data []byte `cbor:"data"`
}

// WarnMsg prints a warning line (prefixed with "warning: ") on the host's
// stderr.
type WarnMsg struct {
	Type string `cbor:"type"`
	Text string `cbor:"text"`
}

// ProgressMsg prints an informational progress line on the host stderr.
type ProgressMsg struct {
	Type string `cbor:"type"`
	Text string `cbor:"text"`
}

// SpinnerMsg requests spinner-style status text on the host stderr.
type SpinnerMsg struct {
	Type string `cbor:"type"`
	Text string `cbor:"text"`
}

// LogMsg prints an informational log line on the host stderr.
type LogMsg struct {
	Type string `cbor:"type"`
	Text string `cbor:"text"`
}

// StdinDataMsg carries a chunk of stdin bytes from the host to the plugin
// (passthrough_stdio mode).
type StdinDataMsg struct {
	Type string `cbor:"type"`
	Data []byte `cbor:"data"`
}

// StdinCloseMsg signals that the host's stdin has reached EOF.
type StdinCloseMsg struct {
	Type string `cbor:"type"`
}

// ─── TLS signer plugin protocol ──────────────────────────────────────────────

// TLSSignerInitMsg is sent by the host to a tls-signer plugin at startup.
// The Type field is MsgTypeInit ("init").
type TLSSignerInitMsg struct {
	Type   string            `cbor:"type"`
	Params map[string]string `cbor:"params"`
}

// TLSSignerReadyMsg is sent by the plugin when it has loaded its certificate
// and is ready to sign. Type is MsgTypeTLSSignerReady ("ready").
type TLSSignerReadyMsg struct {
	Type        string `cbor:"type"`
	Certificate []byte `cbor:"certificate"`
}

// TLSSignerSignMsg is sent by the host to request a signature.
// Type is MsgTypeTLSSignerSign ("sign").
type TLSSignerSignMsg struct {
	Type   string `cbor:"type"`
	Digest []byte `cbor:"digest"`
	// Hash is the crypto.Hash value cast to uint64. 0 means no specific hash.
	Hash       uint64 `cbor:"hash,omitempty"`
	Padding    string `cbor:"padding,omitempty"`
	SaltLength int    `cbor:"salt_length,omitempty"`
}

// TLSSignerSignedMsg is the plugin's reply to a TLSSignerSignMsg.
// It carries either a Signature or an Error, never both.
// This message has no Type field.
type TLSSignerSignedMsg struct {
	Signature []byte `cbor:"signature,omitempty"`
	Error     string `cbor:"error,omitempty"`
}

// TLSSignerShutdownMsg asks the plugin to release resources and exit.
type TLSSignerShutdownMsg struct {
	Type string `cbor:"type"`
}

// ─── Hook plugin protocol ─────────────────────────────────────────────────────

// HookRequest carries the current HTTP request state forwarded to hook plugins.
type HookRequest struct {
	Method     string              `cbor:"method" json:"method"`
	URI        string              `cbor:"uri" json:"uri"`
	Headers    map[string][]string `cbor:"headers" json:"headers"`
	Body       []byte              `cbor:"body,omitempty" json:"body,omitempty"`
	BodySHA256 string              `cbor:"body_sha256,omitempty" json:"body_sha256,omitempty"`
}

// HookRequestHeaderUpdate holds headers that a hook plugin wants to set or
// replace on the outgoing request. Only headers are applied; method and URI
// fields are intentionally absent because the request has already been prepared.
type HookRequestHeaderUpdate struct {
	// Each value is either a string or []string.
	Headers map[string]any `cbor:"headers,omitempty" json:"headers,omitempty"`
}

// AuthHookInput is sent to plugins registered for the "auth" hook.
type AuthHookInput struct {
	Type    string            `cbor:"type" json:"type"`
	API     string            `cbor:"api" json:"api"`
	Profile string            `cbor:"profile" json:"profile"`
	Params  map[string]string `cbor:"params" json:"params"`
	Request HookRequest       `cbor:"request" json:"request"`
}

// AuthHookOutput is the reply from an "auth" hook plugin.
type AuthHookOutput struct {
	Request *HookRequestHeaderUpdate `cbor:"request,omitempty" json:"request,omitempty"`
}

// RequestMiddlewareInput is sent to plugins registered for the
// "request-middleware" hook.
type RequestMiddlewareInput struct {
	Type    string      `cbor:"type" json:"type"`
	Request HookRequest `cbor:"request" json:"request"`
}

// RequestMiddlewareOutput is the reply from a "request-middleware" hook plugin.
type RequestMiddlewareOutput struct {
	Request *HookRequestHeaderUpdate `cbor:"request,omitempty" json:"request,omitempty"`
}

// HookResponse carries the current HTTP response state forwarded to hook plugins.
type HookResponse struct {
	Status  int                 `cbor:"status" json:"status"`
	Headers map[string][]string `cbor:"headers" json:"headers"`
	Body    any                 `cbor:"body" json:"body"`
}

// ResponseMiddlewareInput is sent to plugins registered for the
// "response-middleware" hook.
type ResponseMiddlewareInput struct {
	Type     string       `cbor:"type" json:"type"`
	Request  HookRequest  `cbor:"request" json:"request"`
	Response HookResponse `cbor:"response" json:"response"`
}

// FollowRequest instructs the host to issue a follow-up HTTP request.
type FollowRequest struct {
	Method      string            `cbor:"method,omitempty" json:"method,omitempty"`
	URI         string            `cbor:"uri" json:"uri"`
	Headers     map[string]string `cbor:"headers,omitempty" json:"headers,omitempty"`
	Body        any               `cbor:"body,omitempty" json:"body,omitempty"`
	ContentType string            `cbor:"content_type,omitempty" json:"content_type,omitempty"`
}

// HookResponseUpdate carries partial response modifications from a
// "response-middleware" plugin.
type HookResponseUpdate struct {
	Body any `cbor:"body,omitempty" json:"body,omitempty"`
	// Each value is either a string or []string.
	Headers map[string]any `cbor:"headers,omitempty" json:"headers,omitempty"`
}

// ResponseMiddlewareOutput is the reply from a "response-middleware" hook plugin.
type ResponseMiddlewareOutput struct {
	Drop     bool                `cbor:"drop,omitempty" json:"drop,omitempty"`
	Follow   *FollowRequest      `cbor:"follow,omitempty" json:"follow,omitempty"`
	Response *HookResponseUpdate `cbor:"response,omitempty" json:"response,omitempty"`
}
