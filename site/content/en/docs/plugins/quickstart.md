---
title: Plugin Quickstart
linkTitle: Quickstart
weight: 25
description: Build the smallest useful plugin and verify that Restish discovers it.
---

This author-focused quickstart is for plugin developers. Operators should start
with [Install and Use Plugins](../install-and-use/).

The quickest way to write a plugin is to start from a working first-party
plugin, verify the manifest and message flow with `plugin debug`, and then
change one capability at a time.

## Author Path

1. Choose the smallest plugin type for the job.
2. Build and install a known-good plugin locally.
3. Verify discovery with `plugin list`.
4. Inspect protocol messages with `plugin debug`.
5. Change the manifest and implementation.
6. Re-run the same discovery and behavior checks.

## Choose A Plugin Type

- Formatter hook: easiest way to add an output format.
- Loader hook: add support for an API description format or source that Restish
  does not load natively.
- Auth hook: integrate a local credential source that should participate in
  Restish's normal request pipeline.
- Request or response middleware: inspect or mutate prepared requests or
  interpreted responses.
- Command plugin: add a top-level workflow such as `bulk` or `mcp`.
- TLS signer: sign mTLS handshakes when the private key must stay outside
  Restish.

Choose a hook when the plugin should do one focused job inside a request. Choose
a command plugin when the plugin owns a multi-step workflow and needs host
messages such as delegated HTTP, prompts, selections, config reads, or formatted
responses.

## Build And Install

Use an existing first-party plugin as the template for your type. The CSV
formatter is the smallest practical hook example:

```bash
go build ./cmd/restish-csv
restish plugin install ./restish-csv
restish plugin list
```

Verify behavior:

```bash
restish api.rest.sh/images -o csv
```

## Check The Manifest

Every plugin must describe itself before Restish will load it:

```bash
./restish-csv --rsh-plugin-manifest
```

The manifest declares the plugin name, protocol version, capabilities, and any
registered formatter names, loader content types, auth API names, commands, or
required features. Restish ignores unknown optional manifest fields, but rejects
unknown `required_features`.

## Debug Protocol Messages

```bash
restish plugin debug ./restish-csv
```

Use debug mode whenever discovery works but behavior does not. It decodes the
startup exchange and runtime messages so you can tell whether the host, plugin,
or payload shape is failing.

Command plugins also expose command discovery:

```bash
./restish-bulk --rsh-plugin-commands
restish plugin debug ./restish-bulk -- status
```

Use `--` before plugin arguments that could otherwise be parsed as Restish
flags.

## Authoring Rules

- Keep stdout reserved for protocol messages unless the protocol says otherwise.
- Send human diagnostics to stderr.
- Redact secrets.
- Delegate HTTP to Restish from command plugins.
- Keep operator documentation separate from protocol details.
- Prefer host-provided request, response, config, prompt, selection, and output
  helpers to custom HTTP clients or custom terminal rendering.
- Keep hooks deterministic and bounded; Restish applies hook timeouts from the
  manifest or defaults.

## Good Starting Points

| Goal | Start with | Then read |
| --- | --- | --- |
| Add `-o myformat` | `cmd/restish-csv/main.go` | [Hook Plugins](../hook-plugins/) |
| Add a multi-step command | `cmd/restish-bulk/main.go` | [Command Plugins](../command-plugins/) |
| Expose APIs to another protocol | `cmd/restish-mcp/main.go` | [Command Plugins](../command-plugins/) and [MCP](../mcp/) |
| Sign mTLS with external key material | `cmd/restish-pkcs11/main.go` | [TLS Signer Plugins](../tls-signer-plugins/) |

Use the first-party section of [Example Plugins](../example-plugins/) as the map
from binary to source path.

## Compatibility

Plugins are trusted local executables. Restish validates the manifest and
protocol compatibility, but it does not sandbox plugin code. Preserve existing
message fields and meanings. Add optional fields when possible, and use
`required_features` only when an older host cannot safely run the plugin.

When publishing a plugin, include operator docs that explain installation,
configuration, verification, and troubleshooting without requiring users to
read the wire protocol reference.

## Related Pages

- [Hook Plugins](../hook-plugins/)
- [Command Plugins](../command-plugins/)
- [Example Plugins](../example-plugins/)
- [Plugin Manifest](/docs/reference/plugin-manifest/)
- [Plugin Messages](/docs/reference/plugin-messages/)
- [Plugin Command](/docs/reference/plugin-command/)
