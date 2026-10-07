---
title: Plugin Manifest
linkTitle: Plugin Manifest
weight: 61
description: Reference for Restish plugin manifest fields.
---

A plugin manifest tells Restish what a plugin is, what protocol version it uses,
and which hooks or command surfaces it provides.

## Generated Schema

<!-- BEGIN GENERATED: restish-docgen plugin-manifest-schema -->
Generated from `plugin/manifest.go` and `plugin/messages.go`.

### `Manifest`

Manifest is the metadata a plugin reports when called with --rsh-plugin-manifest. Plugin authors populate and write this with WriteManifest instead of manually marshalling CBOR.

**`Name`**

CBOR: `name`; JSON: `name`; type: `string`; required: yes

Name is the stable plugin identifier, without the "restish-" executable prefix.

**`Version`**

CBOR: `version`; JSON: `version`; type: `string`; required: no

Version is a plugin-defined version string shown in plugin listings.

**`Description`**

CBOR: `description`; JSON: `description`; type: `string`; required: no

Description is a short human-readable summary of the plugin.

**`RestishAPIVersion`**

CBOR: `restish_api_version`; JSON: `restish_api_version`; type: `int`; required: yes

RestishAPIVersion is the minimum host/plugin protocol version required by this plugin. Restish treats future protocol versions as backward compatible unless RequiredFeatures asks for unsupported behavior.

**`Hooks`**

CBOR: `hooks`; JSON: `hooks`; type: `[]string`; required: no

Hooks lists plugin capabilities such as "command", "formatter", or "auth".

**`RequiredFeatures`**

CBOR: `required_features`; JSON: `required_features`; type: `[]string`; required: no

RequiredFeatures lists additive protocol features that must be supported by the host before this plugin may run. Unknown optional manifest fields are ignored, but unknown required features fail manifest loading.

**`FormatterNames`**

CBOR: `formatter_names`; JSON: `formatter_names`; type: `[]string`; required: no

FormatterNames lists the output format names this plugin registers when the "formatter" hook is declared.

**`LoaderContentTypes`**

CBOR: `loader_content_types`; JSON: `loader_content_types`; type: `[]string`; required: no

LoaderContentTypes lists the MIME types this plugin handles when the "loader" hook is declared.

**`AuthAPINames`**

CBOR: `auth_api_names`; JSON: `auth_api_names`; type: `[]string`; required: no

AuthAPINames, when non-empty, restricts the "auth" hook to the listed API names so the plugin is not invoked for every unrelated API.

**`NeedsAuthSecrets`**

CBOR: `needs_auth_secrets`; JSON: `needs_auth_secrets`; type: `bool`; required: no

NeedsAuthSecrets, when true, tells Restish to forward secret auth params (passwords, client secrets) and credential-bearing request headers to this plugin. When false (the default), secret params are omitted and Authorization, Cookie, and Proxy-Authorization request headers are sent as "<redacted>" in auth and middleware hook payloads.

**`HookTimeouts`**

CBOR: `hook_timeouts`; JSON: `hook_timeouts`; type: `map[string]time.Duration`; required: no

HookTimeouts overrides the per-hook subprocess deadline. Keys are hook names (e.g. "auth", "request-middleware"). The default is 30 s for all hooks except "auth", which defaults to 5 minutes.


### `CommandDecl`

CommandDecl describes one command that a command-plugin exposes. It is used in the response to --rsh-plugin-commands.

**`Name`**

CBOR: `name`; JSON: `name`; type: `string`; required: yes

Name is the top-level command name contributed by the plugin.

**`Short`**

CBOR: `short`; JSON: `short`; type: `string`; required: no

Short is the one-line help text shown in command listings.

**`Long`**

CBOR: `long`; JSON: `long`; type: `string`; required: no

Long is optional extended help text.

**`PassthroughStdio`**

CBOR: `passthrough_stdio`; JSON: `passthrough_stdio`; type: `bool`; required: no

PassthroughStdio asks the host to forward stdin frames to the plugin.


### `CommandDiscoveryResponse`

CommandDiscoveryResponse is the response to StartupFlagCommands.

**`ProtocolVersion`**

CBOR: `protocol_version`; JSON: `protocol_version`; type: `int`; required: no

**`Commands`**

CBOR: `commands`; JSON: `commands`; type: `[]CommandDecl`; required: yes
<!-- END GENERATED -->

## Core Fields

```json
{
  "name": "csv",
  "version": "0.1.0",
  "description": "Render array responses as CSV",
  "restish_api_version": 2,
  "hooks": ["formatter"],
  "formatter_names": ["csv"]
}
```

| Field | Meaning |
| --- | --- |
| `name` | Stable plugin name, without the `restish-` executable prefix. |
| `version` | Plugin version. |
| `description` | Human-facing summary. |
| `restish_api_version` | Minimum host/plugin protocol version required by this plugin. |
| `hooks` | Hook families such as `auth`, `request-middleware`, `response-middleware`, `loader`, `formatter`, `command`, or `tls-signer`. |
| `required_features` | Additive protocol features the host must support before the plugin can run. Unknown optional fields are ignored; unknown required features fail loading. |
| `formatter_names` | Required when `hooks` includes `formatter`; lists output format names. |
| `loader_content_types` | Required when `hooks` includes `loader`; lists source MIME types. |
| `auth_api_names` | Optional API-name allowlist for `auth` hooks, so the plugin is not invoked for unrelated APIs. |
| `needs_auth_secrets` | Forward secret auth params and credential-bearing request headers to the plugin. Defaults to `false`; secrets are otherwise omitted or redacted. |
| `hook_timeouts` | Optional map of hook name to duration. Defaults are 30 seconds for most hooks and 5 minutes for `auth`. |

Valid hook families are `auth`, `request-middleware`, `response-middleware`,
`loader`, `formatter`, `command`, and `tls-signer`.

Supported `required_features` values are:

| Feature | Meaning |
| --- | --- |
| `manifest.required_features` | Host understands manifest required-feature validation. |
| `loader.source_metadata` | Loader hooks may receive `content_type`, `source_url`, and `local_path`. |
| `request.final_body` | Auth and request-middleware hooks may receive final request body bytes when Restish has them. |
| `command.select` | Command plugins may request a host-owned single-choice picker. |

## Command Discovery

Command plugins also respond to `--rsh-plugin-commands` with command
declarations:

```json
{
  "protocol_version": 1,
  "commands": [
    {
      "name": "bulk",
      "short": "Manage API collections as local files",
      "long": "Optional longer help text.",
      "passthrough_stdio": false
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `protocol_version` | Command-plugin discovery protocol version. Restish v2 currently writes and accepts version `1`; omitted means the initial protocol. |
| `commands[].name` | Top-level command name contributed by the plugin. |
| `commands[].short` | One-line help text. |
| `commands[].long` | Optional extended help text. |
| `commands[].passthrough_stdio` | Ask the host to forward stdin frames to the plugin. |

## Guidance

- Keep names stable; config may refer to them.
- Use the narrowest hook set that solves the job.
- Use `restish_api_version` for the minimum compatible protocol, not the version
  you built with.
- Add `required_features` only when the plugin cannot operate without that
  host behavior.
- Rebuild v1 plugins for the v2 protocol.
- Keep operator docs separate from manifest internals.

## Related Pages

- [Plugin Messages](../plugin-messages/)
- [Hook Plugins](/docs/plugins/hook-plugins/)
- [Command Plugins](/docs/plugins/command-plugins/)
