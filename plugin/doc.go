// Package plugin provides the public contract for writing Restish
// out-of-process plugins.
//
// Restish supports three plugin styles:
//
//   - hook plugins for auth, middleware, loader, and formatter hooks
//   - command plugins for top-level workflow commands
//   - TLS signer plugins for external mTLS signing
//
// # Startup contract
//
// Plugin binaries are discovered as executables named "restish-<name>".
// Restish starts them with one of these startup flags:
//
//   - StartupFlagManifest: write a CBOR Manifest to stdout
//   - StartupFlagCommands: write a CBOR command list to stdout (command plugins)
//
// Most plugins should use HandleStartupFlags or Run instead of manually
// handling startup flags.
//
// # Runtime transport
//
// All messages — startup responses and runtime messages alike — are plain
// CBOR data items written directly to stdin/stdout. CBOR is self-delimiting,
// so no length prefix or other framing is needed. Any language with a CBOR
// library can implement a plugin.
//
// Use WriteMessage and ReadMessage for runtime messages. For command plugins,
// prefer Run and CommandClient unless you need lower-level stream control.
// WriteManifest and WriteCommands are convenience wrappers for startup
// responses.
//
// For hook plugins that read a single message, ReadMessage(r, v) is sufficient.
// Command and TLS-signer plugins receive multiple messages on the same stdin,
// so they must create one Decoder with NewDecoder(os.Stdin) and call
// ReadMessage on it for every read. Discarding the Decoder between calls loses
// bytes that were already buffered internally.
//
// # Command-plugin messages
//
// Command plugins should prefer the typed message structs in messages.go:
//
//   - InitMsg for the initial host -> plugin command selection
//   - HTTPRequestMsg / HTTPResponseMsg for delegated HTTP
//   - APISpecMsg / APISpecResponseMsg for fetching registered API specs
//   - ListAPIsMsg / ListProfilesMsg for config discovery
//   - ConfigReadMsg for effective API/profile/plugin config
//   - PromptMsg / ConfirmMsg for user interaction
//   - ResponseMsg, StdoutDataMsg, StderrDataMsg, WarnMsg, ProgressMsg, and DoneMsg for output
//
// For simple command plugins, plugin.Run and CommandClient are usually enough.
//
// # Hook-plugin payload shapes
//
// Most hook plugins receive one CBOR map and, except for formatter hooks,
// return one CBOR reply map.
//
//   - auth:
//     request contains api_name, profile_name, params, and request metadata
//     reply typically returns request.header updates
//   - request-middleware:
//     request contains the outbound request metadata
//     reply can return updated request headers
//   - response-middleware:
//     request contains original request metadata plus normalized response fields
//     reply may set drop, follow, or response
//   - loader:
//     request contains content_type and raw body bytes
//     reply returns an OpenAPI document in body plus optional content_type
//   - formatter:
//     plugins receive a stream of formatter messages (`start`, `item`, `end`)
//     on stdin and write final formatted bytes directly to stdout. For ordinary
//     full-response renders the host usually sends `start` with the full
//     normalized response body followed by `end`. For paginated or event-stream
//     output the host sends `start`, then one or more `item` messages, then
//     `end`.
//
// See site/content/en/docs/plugins/quickstart.md for the fastest practical path
// to a working plugin, and docs/design/019-hook-plugins.md plus
// docs/design/020-command-plugins.md for the full protocol details.
package plugin
