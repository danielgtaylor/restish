# Command Plugins

## Summary

Command plugins are long-lived subprocesses that contribute top-level CLI
commands and can converse with Restish over a multi-message CBOR protocol while
they run.

This plugin type is for workflow-oriented features that do not fit naturally
into a single request hook.

In practice that often means one plugin-defined top-level entry point, such as
`bulk` or `mcp`, with the plugin itself owning any nested subcommands and
workflow-specific flags behind that entry point.

## Problem

Some extensions need to:

- expose their own user-facing subcommands
- coordinate multiple HTTP requests
- stream progress or warnings while work is running
- optionally pass stdin/stdout through as part of an interactive session

Trying to model that as repeated hook invocations would be awkward and would
force the plugin to reinvent parts of the CLI lifecycle itself.

## Design

A command plugin declares the `command` hook in its manifest. Restish then
discovers contributed commands in a separate startup phase by invoking the
plugin with `--rsh-plugin-commands`.

That command returns a CBOR payload describing one or more commands, including:

- `name`
- `short`
- `long`
- `passthrough_stdio`

Each declaration becomes a Cobra command on the root CLI.

Restish deliberately disables host-side flag parsing for those commands before
launching the plugin. That lets the plugin receive raw arguments such as
`bulk init --url-template=...` and implement its own subcommand tree, flags,
and `--help` behavior without forcing the host to understand every plugin's UX.
After the plugin command name, short flags such as `-f` or `-v` are
plugin-owned even when they overlap Restish global shorthands. Explicit
Restish-owned long flags such as `--rsh-config` may still be stripped before
plugin launch if they were already consumed by the host, but shorthand aliases
must not be silently removed from plugin argv.

Restish also injects terminal context flags before plugin-owned args. These
include stdout/stderr TTY state, whether color is enabled, and the configured
terminal theme entries. Command plugins that render human output should use
that context so plugin output matches built-in command output and user themes.

Command-name collisions must be handled explicitly. If a plugin tries to add a
command that would shadow a built-in or another plugin command, Restish should
skip it with a warning or fail startup, but never silently shadow the existing
command.

When the user runs one of those commands, Restish starts the plugin and sends
an initial message:

```json
{
  "type": "init",
  "command": "<declared name>",
  "args": ["..."]
}
```

From there, the plugin and Restish exchange CBOR data items until the plugin
sends `done` or dies.

The host owns session lifetime. "Sent `done`" is not enough by itself if the
process refuses to exit; the host must still close stdin and use a bounded wait
before killing the process.
All command-plugin paths should share one wait result for the subprocess so
startup cleanup, cancellation, and normal shutdown cannot call `Wait` twice and
turn the real exit status into "wait was already called".

### Messages From Plugin To Restish

The current implementation handles these message types:

- `http-request` to ask Restish to perform an HTTP call
- `api-spec` to ask Restish to resolve a registered API spec
- `response` to ask Restish to format and print a normalized response
- `stdout-data` and `stderr-data` to write raw bytes directly
- `progress` to report structured or plain-text status on stderr
- `spinner` and `log` to print status text on stderr
- `warn` to print a warning-prefixed message
- `done` to terminate with an exit code

Prompt/confirm style interactions are also valid protocol concepts when a
plugin needs host-owned prompting behavior.

`progress` always includes `text` as a plain fallback. It may also include
`id`, `label`, `state`, `current`, `total`, `unit`, and `message`. When a streaming
formatter named `progress` is registered and the record has an `id` or `label`,
the host sends those structured fields through one formatter session for the
command. Otherwise it prints `text` as a normal stderr line. Formatter startup
or rendering failures warn once and fall back to `text`; they do not fail the
command. The host closes an active formatter session whenever the command ends.

The fields are additive to the v2 CBOR contract. New plugins must keep `text`
meaningful because older hosts ignore the structured fields, and installations
without a progress formatter use it directly. Plugins must not discover or
execute sibling plugins themselves.

### Messages From Restish To Plugin

Restish currently sends:

- `init` when the command starts
- `http-response` after handling an `http-request`
- `api-spec-response` after handling an `api-spec`
- `stdin-data` and `stdin-close` when `passthrough_stdio` is enabled

Long-lived request/response pairs use `request_id` correlation identifiers so
plugins can issue more than one delegated HTTP request at a time. The host
continues reading plugin messages while HTTP work runs, and replies include the
same `request_id` as the original request. The public `CommandClient.Do` helper
assigns request IDs when needed and routes replies back to the goroutine that
sent the request.

For `passthrough_stdio`, the public client dispatches `stdin-data` and
`stdin-close` through a bounded asynchronous queue. Slow stdin consumers must
not block reply delivery on the command-plugin read loop; if the queue fills,
the client fails pending requests with a clear queue-full error instead of
growing memory without bound.

### Delegated HTTP

The most important design choice is that command plugins delegate HTTP back to
Restish instead of constructing their own transport stack. When a plugin sends
`http-request`, Restish performs the request through the normal request path,
including:

- profile and API-base-URL resolution
- TLS signer resolution
- request middleware
- retries, caching, and output normalization

The reply sent back to the plugin is the normalized response shape, not a raw
`*http.Response`.

That keeps workflows consistent with the rest of the CLI and avoids creating a
second HTTP client implementation inside every plugin.

### Output Ownership

Command plugins have two output modes:

- ask Restish to print a normalized `response` using the current formatter and
  filter rules
- emit raw `stdout-data` or `stderr-data` bytes directly

This lets a workflow choose between "behave like a normal Restish command" and
"own the terminal output myself" on a message-by-message basis.

Public helper names should make these side effects obvious. Methods such as
`WriteStdout` and `WriteStderr` are preferable to names that look like simple
accessors, because they write directly to the user's output channels.

Plugins that opt into passthrough stdio must still coexist with the host's
session rules. The host must avoid leaking stdin readers that steal later shell
keystrokes after plugin exit.

### Terminal Context

Restish also passes simple terminal context flags on plugin startup:

- `--rsh-stdout-tty=<bool>`
- `--rsh-stderr-tty=<bool>`
- `--rsh-color=<bool>`

That gives plugins enough signal to adapt interactive behavior without coupling
them tightly to the terminal implementation.

These flags live in the host-injected argv prefix. Plugins must parse terminal
context only until the first non-startup argument and then treat the rest of
argv as user input. This prevents commands such as
`restish bulk init URL --rsh-plugin-manifest` from switching the plugin into
manifest discovery mode or overriding the terminal context.

Command discovery responses include a `protocol_version` field. Version `0` is
treated as the initial command-plugin discovery shape for compatibility with
older helpers, and versions greater than the host's current command-plugin
protocol are rejected before commands are registered.

## Session Termination Rules

The host ends a command-plugin session when:

- the plugin sends `done`
- the plugin exits
- the host context is canceled
- a protocol error occurs

Termination should include:

- closing stdin to the plugin
- bounded wait for clean exit
- force kill if needed
- surfacing non-zero exit after `done` when it indicates a late crash
- flushing any buffered plugin stderr line before returning

## Error Model

Protocol errors should identify:

- plugin name
- message type
- decode or semantic failure

The host should prefer typed protocol errors over generic "unexpected reply"
messages where possible.

Plugin-side helpers should report write, decode, and reply errors to stderr and
exit nonzero. Dropping protocol errors silently makes plugin failures look like
successful no-op commands.

## Alternatives Considered

### Run command plugins as ordinary child processes with no protocol

That would be simpler mechanically, but the plugin would lose access to
Restish-managed HTTP, config, and formatting. The protocol is what keeps the
workflow integrated.

### Let command plugins make HTTP requests directly

This would duplicate auth, retry, caching, TLS, and normalization logic across
plugins. Delegation back into Restish is a better fit for consistency and
maintainability.

### Model command plugins as many independent hook calls

That would make progress reporting, streamed stdin, and multi-step workflows
far more awkward than keeping one long-lived session.

## Notes

The current implementation is centered in
[`internal/cli/command_plugin.go`](../../internal/cli/command_plugin.go),
with end-to-end examples in
[`internal/cli/testdata/testplugin/main.go`](../../internal/cli/testdata/testplugin/main.go)
and tests in
[`internal/cli/command_plugin_test.go`](../../internal/cli/command_plugin_test.go).

One detail worth preserving is that command plugins add commands at root-command
construction time, not through late dynamic dispatch. That keeps help output,
completion, and command discovery aligned with built-in commands.
