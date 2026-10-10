package plugin

import (
	"io"
	"time"
)

const (
	// CommandPluginProtocolVersion is the current command-plugin discovery
	// protocol version. A plugin that emits a larger version requires a newer
	// Restish host.
	CommandPluginProtocolVersion = 1

	// StartupFlagManifest asks a plugin to write its Manifest and exit.
	StartupFlagManifest = "--rsh-plugin-manifest"
	// StartupFlagCommands asks a command plugin to write its command list and exit.
	StartupFlagCommands = "--rsh-plugin-commands"
	// StartupFlagColor tells a command plugin whether host terminal color is enabled.
	StartupFlagColor = "--rsh-color"
	// StartupFlagStdoutTTY tells a command plugin whether host stdout is a TTY.
	StartupFlagStdoutTTY = "--rsh-stdout-tty"
	// StartupFlagStderrTTY tells a command plugin whether host stderr is a TTY.
	StartupFlagStderrTTY = "--rsh-stderr-tty"
	// StartupFlagTheme carries the host's configured terminal theme as JSON.
	StartupFlagTheme = "--rsh-theme"
)

const (
	// FeatureManifestRequiredFeatures means the host understands manifest
	// required_features validation.
	FeatureManifestRequiredFeatures = "manifest.required_features"
	// FeatureLoaderSourceMetadata means loader hooks receive content_type,
	// source_url, and local_path metadata when available.
	FeatureLoaderSourceMetadata = "loader.source_metadata"
	// FeatureRequestFinalBody means auth and request-middleware hooks may
	// receive the final request body bytes when Restish has them.
	FeatureRequestFinalBody = "request.final_body"
	// FeatureAuthOperationSecurity means auth resolver and auth hooks receive
	// the exact OpenAPI operation security alternative selected by Restish.
	FeatureAuthOperationSecurity = "auth.operation_security"
	// FeatureDPoPCredentialSource means the host supports provider-neutral,
	// proof-bound DPoP acquisition through credential-source hooks.
	FeatureDPoPCredentialSource = "auth.dpop_credential_source"
	// FeatureDPoPOperationScopes means credential-source hooks receive the
	// exact OpenAPI operation scopes required by the current request.
	FeatureDPoPOperationScopes = "auth.dpop_operation_scopes"
)

// Manifest is the metadata a plugin reports when called with
// --rsh-plugin-manifest. Plugin authors populate and write this with
// WriteManifest instead of manually marshalling CBOR.
type Manifest struct {
	// Name is the stable plugin identifier, without the "restish-" executable prefix.
	Name string `cbor:"name" json:"name"`
	// Version is a plugin-defined version string shown in plugin listings.
	Version string `cbor:"version,omitempty" json:"version,omitempty"`
	// Description is a short human-readable summary of the plugin.
	Description string `cbor:"description,omitempty" json:"description,omitempty"`
	// RestishAPIVersion is the minimum host/plugin protocol version required by
	// this plugin. Restish treats future protocol versions as backward
	// compatible unless RequiredFeatures asks for unsupported behavior.
	RestishAPIVersion int `cbor:"restish_api_version" json:"restish_api_version"`
	// Hooks lists plugin capabilities such as "command", "formatter", or "auth".
	Hooks []string `cbor:"hooks,omitempty" json:"hooks,omitempty"`
	// RequiredFeatures lists additive protocol features that must be supported
	// by the host before this plugin may run. Unknown optional manifest fields
	// are ignored, but unknown required features fail manifest loading.
	RequiredFeatures []string `cbor:"required_features,omitempty" json:"required_features,omitempty"`
	// FormatterNames lists the output format names this plugin registers when
	// the "formatter" hook is declared.
	FormatterNames []string `cbor:"formatter_names,omitempty" json:"formatter_names,omitempty"`
	// LoaderContentTypes lists the MIME types this plugin handles when the
	// "loader" hook is declared.
	LoaderContentTypes []string `cbor:"loader_content_types,omitempty" json:"loader_content_types,omitempty"`
	// AuthAPINames, when non-empty, restricts the "auth" hook to the listed
	// API names so the plugin is not invoked for every unrelated API.
	AuthAPINames []string `cbor:"auth_api_names,omitempty" json:"auth_api_names,omitempty"`
	// NeedsAuthSecrets, when true, tells Restish to forward secret auth
	// params (passwords, client secrets) and credential-bearing request headers
	// to this plugin. When false (the default), secret params are omitted and
	// Authorization, Cookie, and Proxy-Authorization request headers are sent
	// as "<redacted>" in auth and middleware hook payloads.
	NeedsAuthSecrets bool `cbor:"needs_auth_secrets,omitempty" json:"needs_auth_secrets,omitempty"`
	// HookTimeouts overrides the per-hook subprocess deadline. Keys are hook
	// names (e.g. "auth", "request-middleware"). The default is 30 s for all
	// hooks except "auth", which defaults to 5 minutes.
	HookTimeouts map[string]time.Duration `cbor:"hook_timeouts,omitempty" json:"hook_timeouts,omitempty"`
}

// CommandDecl describes one command that a command-plugin exposes.
// It is used in the response to --rsh-plugin-commands.
type CommandDecl struct {
	// Name is the top-level command name contributed by the plugin.
	Name string `cbor:"name" json:"name"`
	// Short is the one-line help text shown in command listings.
	Short string `cbor:"short,omitempty" json:"short,omitempty"`
	// Long is optional extended help text.
	Long string `cbor:"long,omitempty" json:"long,omitempty"`
	// PassthroughStdio asks the host to forward stdin frames to the plugin.
	PassthroughStdio bool `cbor:"passthrough_stdio,omitempty" json:"passthrough_stdio,omitempty"`
}

// CommandDiscoveryResponse is the response to StartupFlagCommands.
type CommandDiscoveryResponse struct {
	ProtocolVersion int           `cbor:"protocol_version,omitempty" json:"protocol_version,omitempty"`
	Commands        []CommandDecl `cbor:"commands" json:"commands"`
}

// WriteManifest serialises m as a CBOR data item and writes it to w.
// It is the canonical way to respond to --rsh-plugin-manifest.
//
//	case plugin.StartupFlagManifest:
//	    return plugin.WriteManifest(os.Stdout, m)
func WriteManifest(w io.Writer, m Manifest) error {
	return WriteMessage(w, m)
}

// WriteCommands serialises cmds as a CBOR map with a "commands" array and
// writes it to w. It is the canonical way to respond to --rsh-plugin-commands.
//
//	case plugin.StartupFlagCommands:
//	    return plugin.WriteCommands(os.Stdout, cmds)
func WriteCommands(w io.Writer, cmds []CommandDecl) error {
	return WriteMessage(w, CommandDiscoveryResponse{
		ProtocolVersion: CommandPluginProtocolVersion,
		Commands:        cmds,
	})
}
