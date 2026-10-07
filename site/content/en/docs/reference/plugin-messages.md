---
title: Plugin Messages
linkTitle: Plugin Messages
weight: 62
description: Reference for Restish host/plugin message families.
---

Restish v2 plugins communicate over structured messages. Operators usually need
`plugin debug`; authors need the message families and lifecycle rules.

All startup and runtime messages are CBOR data items on stdin/stdout. CBOR is
self-delimiting, so there is no length prefix or line framing. Startup discovery
writes a single message and exits. Command plugins and TLS signer plugins keep
one decoder open for the process lifetime because later messages can already be
buffered.

## Generated Message Schema

<!-- BEGIN GENERATED: restish-docgen plugin-message-schema -->
Generated from `plugin/messages.go`.

### Message Type Constants

| Constant | Value |
| --- | --- |
| `MsgTypeAPISpec` | `api-spec` |
| `MsgTypeAPISpecResponse` | `api-spec-response` |
| `MsgTypeConfigRead` | `config-read` |
| `MsgTypeConfigReadResponse` | `config-read-response` |
| `MsgTypeConfirm` | `confirm` |
| `MsgTypeConfirmResponse` | `confirm-response` |
| `MsgTypeDone` | `done` |
| `MsgTypeHTTPRequest` | `http-request` |
| `MsgTypeHTTPResponse` | `http-response` |
| `MsgTypeInit` | `init` |
| `MsgTypeListAPIs` | `list-apis` |
| `MsgTypeListAPIsResponse` | `list-apis-response` |
| `MsgTypeListProfiles` | `list-profiles` |
| `MsgTypeListProfilesResponse` | `list-profiles-response` |
| `MsgTypeLog` | `log` |
| `MsgTypeProgress` | `progress` |
| `MsgTypePrompt` | `prompt` |
| `MsgTypePromptResponse` | `prompt-response` |
| `MsgTypeResponse` | `response` |
| `MsgTypeSelect` | `select` |
| `MsgTypeSelectResponse` | `select-response` |
| `MsgTypeSpinner` | `spinner` |
| `MsgTypeStderrData` | `stderr-data` |
| `MsgTypeStdinClose` | `stdin-close` |
| `MsgTypeStdinData` | `stdin-data` |
| `MsgTypeStdoutData` | `stdout-data` |
| `MsgTypeTLSSignerReady` | `ready` |
| `MsgTypeTLSSignerShutdown` | `shutdown` |
| `MsgTypeTLSSignerSign` | `sign` |
| `MsgTypeWarn` | `warn` |

### `InitMsg`

InitMsg is the first message sent from the host to the plugin after startup. It carries the sub-command name and the raw CLI arguments.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Command`**

CBOR: `command`; type: `string`; required: yes

**`Args`**

CBOR: `args`; type: `[]string`; required: yes


### `HTTPRequestMsg`

HTTPRequestMsg asks the host to perform an HTTP request on behalf of the plugin and reply with an HTTPResponseMsg.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Method`**

CBOR: `method`; type: `string`; required: no

**`URI`**

CBOR: `uri`; type: `string`; required: yes

**`Headers`**

CBOR: `headers`; type: `map[string]string`; required: no

**`Body`**

CBOR: `body`; type: `any`; required: no

**`ContentType`**

CBOR: `content_type`; type: `string`; required: no

**`NoCache`**

CBOR: `no_cache`; type: `bool`; required: no

NoCache bypasses the response cache for this request.

**`CacheTTL`**

CBOR: `cache_ttl`; type: `int`; required: no

CacheTTL (seconds) injects Cache-Control: max-age=N on the request. Only entries fresher than this TTL are served from the cache.

**`Timeout`**

CBOR: `timeout`; type: `int`; required: no

Timeout (seconds) sets a per-request deadline. 0 means no deadline.

**`Filter`**

CBOR: `filter`; type: `string`; required: no

Filter is an optional shorthand or jq expression. When set, the host applies it to the full response document before sending it back.


### `HTTPResponseMsg`

HTTPResponseMsg is the host reply to an HTTPRequestMsg.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Status`**

CBOR: `status`; type: `int`; required: yes

**`Headers`**

CBOR: `headers`; type: `map[string][]string`; required: no

**`URL`**

CBOR: `url`; type: `string`; required: no

**`Links`**

CBOR: `links`; type: `map[string]any`; required: no

**`Body`**

CBOR: `body`; type: `any`; required: yes

**`Error`**

CBOR: `error`; type: `string`; required: no

Error is set when the HTTP request itself failed.


### `APISpecMsg`

APISpecMsg asks the host to load the OpenAPI spec for a registered API.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Name`**

CBOR: `name`; type: `string`; required: yes

**`Profile`**

CBOR: `profile`; type: `string`; required: no


### `APISpecResponseMsg`

APISpecResponseMsg is the host reply to an APISpecMsg.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Name`**

CBOR: `name`; type: `string`; required: yes

**`Profile`**

CBOR: `profile`; type: `string`; required: no

**`ContentType`**

CBOR: `content_type`; type: `string`; required: no

**`Raw`**

CBOR: `raw`; type: `[]byte`; required: no

**`Operations`**

CBOR: `operations`; type: `[]APIOperation`; required: no

**`Error`**

CBOR: `error`; type: `string`; required: no


### `APIOperation`

APIOperation is the host's resolved, config-aware representation of one OpenAPI HTTP operation. It mirrors the generated-command model so command plugins do not need to re-parse raw OpenAPI specs.

**`ID`**

CBOR: `id`; type: `string`; required: yes

**`Method`**

CBOR: `method`; type: `string`; required: yes

**`Path`**

CBOR: `path`; type: `string`; required: yes

**`Summary`**

CBOR: `summary`; type: `string`; required: no

**`Description`**

CBOR: `description`; type: `string`; required: no

**`Deprecated`**

CBOR: `deprecated`; type: `bool`; required: no

**`Parameters`**

CBOR: `parameters`; type: `[]APIParam`; required: no

**`HasBody`**

CBOR: `has_body`; type: `bool`; required: no

**`BodyRequired`**

CBOR: `body_required`; type: `bool`; required: no

**`RequestMediaType`**

CBOR: `request_media_type`; type: `string`; required: no

**`RequestSchema`**

CBOR: `request_schema`; type: `map[string]any`; required: no

**`RequestSchemaDialect`**

CBOR: `request_schema_dialect`; type: `string`; required: no

**`MCPIgnore`**

CBOR: `mcp_ignore`; type: `bool`; required: no


### `APIParam`

APIParam is a resolved operation parameter for APIOperation.

**`Name`**

CBOR: `name`; type: `string`; required: yes

**`In`**

CBOR: `in`; type: `string`; required: yes

**`Required`**

CBOR: `required`; type: `bool`; required: no

**`Description`**

CBOR: `description`; type: `string`; required: no

**`Type`**

CBOR: `type`; type: `string`; required: no

**`ItemType`**

CBOR: `item_type`; type: `string`; required: no

**`Style`**

CBOR: `style`; type: `string`; required: no

**`Explode`**

CBOR: `explode`; type: `*bool`; required: no

**`AllowReserved`**

CBOR: `allow_reserved`; type: `bool`; required: no

**`ContentMediaType`**

CBOR: `content_media_type`; type: `string`; required: no

**`Schema`**

CBOR: `schema`; type: `map[string]any`; required: no

**`SchemaDialect`**

CBOR: `schema_dialect`; type: `string`; required: no

**`Enum`**

CBOR: `enum`; type: `[]string`; required: no


### `ListAPIsMsg`

ListAPIsMsg asks the host for the list of configured API names.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no


### `ListAPIsResponseMsg`

ListAPIsResponseMsg is the host reply to a ListAPIsMsg.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`APIs`**

CBOR: `apis`; type: `[]string`; required: yes

**`Error`**

CBOR: `error`; type: `string`; required: no


### `ListProfilesMsg`

ListProfilesMsg asks the host for the profile names of a specific API.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`API`**

CBOR: `api`; type: `string`; required: yes


### `ListProfilesResponseMsg`

ListProfilesResponseMsg is the host reply to a ListProfilesMsg.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`API`**

CBOR: `api`; type: `string`; required: yes

**`Profiles`**

CBOR: `profiles`; type: `[]string`; required: yes

**`Error`**

CBOR: `error`; type: `string`; required: no


### `ConfigReadMsg`

ConfigReadMsg asks the host for the effective configuration of an API profile (base URL, persistent headers and query params), and/or the plugin-specific config stored under plugins[Plugin] in restish.json.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`API`**

CBOR: `api`; type: `string`; required: no

**`Profile`**

CBOR: `profile`; type: `string`; required: no

**`Plugin`**

CBOR: `plugin`; type: `string`; required: no

Plugin is the plugin's short name (without the "restish-" prefix). When set, the response includes PluginConfig populated from restish.json's plugins[Plugin] entry.


### `ConfigReadResponseMsg`

ConfigReadResponseMsg is the host reply to a ConfigReadMsg. Auth secrets are intentionally excluded.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`BaseURL`**

CBOR: `base_url`; type: `string`; required: no

**`Headers`**

CBOR: `headers`; type: `[]string`; required: no

**`Query`**

CBOR: `query`; type: `[]string`; required: no

**`Error`**

CBOR: `error`; type: `string`; required: no

**`PluginConfig`**

CBOR: `plugin_config`; type: `any`; required: no

PluginConfig holds the parsed plugins[name] entry from restish.json, or nil when no config is stored for this plugin.


### `PromptMsg`

PromptMsg asks the host to display a message and read one line from the user. When Hidden is true and stdin is a TTY, echo is suppressed (suitable for password entry).

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Message`**

CBOR: `message`; type: `string`; required: yes

**`Hidden`**

CBOR: `hidden`; type: `bool`; required: no


### `PromptResponseMsg`

PromptResponseMsg is the host reply to a PromptMsg.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Value`**

CBOR: `value`; type: `string`; required: no

**`Error`**

CBOR: `error`; type: `string`; required: no


### `ConfirmMsg`

ConfirmMsg asks the host to display a message and read a yes/no answer.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Message`**

CBOR: `message`; type: `string`; required: yes


### `ConfirmResponseMsg`

ConfirmResponseMsg is the host reply to a ConfirmMsg.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Value`**

CBOR: `value`; type: `bool`; required: yes

**`Error`**

CBOR: `error`; type: `string`; required: no


### `SelectOption`

SelectOption pairs a user-visible label with an opaque plugin-owned value.

**`Label`**

CBOR: `label`; type: `string`; required: yes

**`Value`**

CBOR: `value`; type: `string`; required: yes


### `SelectMsg`

SelectMsg asks the host to display an interactive single-choice picker.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Message`**

CBOR: `message`; type: `string`; required: yes

**`Options`**

CBOR: `options`; type: `[]SelectOption`; required: yes


### `SelectResponseMsg`

SelectResponseMsg is the host reply to a SelectMsg.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`RequestID`**

CBOR: `request_id`; type: `string`; required: no

**`Value`**

CBOR: `value`; type: `string`; required: no

**`Error`**

CBOR: `error`; type: `string`; required: no


### `ResponseMsg`

ResponseMsg asks the host to format and display a response using the configured output formatter (same as a regular API response).

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Status`**

CBOR: `status`; type: `int`; required: no

**`Headers`**

CBOR: `headers`; type: `map[string][]string`; required: no

**`Body`**

CBOR: `body`; type: `any`; required: no


### `DoneMsg`

DoneMsg signals that the plugin has finished. ExitCode 0 is success.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`ExitCode`**

CBOR: `exit_code`; type: `int`; required: no


### `StdoutDataMsg`

StdoutDataMsg sends a chunk of raw bytes to the host's stdout.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Data`**

CBOR: `data`; type: `[]byte`; required: yes


### `StderrDataMsg`

StderrDataMsg sends a chunk of raw bytes to the host's stderr.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Data`**

CBOR: `data`; type: `[]byte`; required: yes


### `WarnMsg`

WarnMsg prints a warning line (prefixed with "warning: ") on the host's stderr.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Text`**

CBOR: `text`; type: `string`; required: yes


### `ProgressMsg`

ProgressMsg prints an informational progress line on the host stderr.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Text`**

CBOR: `text`; type: `string`; required: yes


### `SpinnerMsg`

SpinnerMsg requests spinner-style status text on the host stderr.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Text`**

CBOR: `text`; type: `string`; required: yes


### `LogMsg`

LogMsg prints an informational log line on the host stderr.

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Text`**

CBOR: `text`; type: `string`; required: yes


### `StdinDataMsg`

StdinDataMsg carries a chunk of stdin bytes from the host to the plugin (passthrough_stdio mode).

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Data`**

CBOR: `data`; type: `[]byte`; required: yes


### `StdinCloseMsg`

StdinCloseMsg signals that the host's stdin has reached EOF.

**`Type`**

CBOR: `type`; type: `string`; required: yes


### `FormatterResponse`

FormatterResponse is the normalized response shape forwarded to formatter plugins. The host may include the full response body on "start" for a normal one-shot render, or send body values incrementally on subsequent "item" messages for paginated and event-stream output.

**`Proto`**

CBOR: `proto`; JSON: `proto`; type: `string`; required: no

**`Status`**

CBOR: `status`; JSON: `status`; type: `int`; required: no

**`Headers`**

CBOR: `headers`; JSON: `headers`; type: `map[string][]string`; required: no

**`Links`**

CBOR: `links`; JSON: `links`; type: `map[string]any`; required: no

**`Body`**

CBOR: `body`; JSON: `body`; type: `any`; required: no


### `FormatterRequest`

FormatterRequest is sent to formatter plugins. Type is always "formatter" and Event is one of "start", "item", or "end".

**`Type`**

CBOR: `type`; JSON: `type`; type: `string`; required: yes

**`Format`**

CBOR: `format`; JSON: `format`; type: `string`; required: yes

**`Color`**

CBOR: `color`; JSON: `color`; type: `bool`; required: no

**`Event`**

CBOR: `event`; JSON: `event`; type: `string`; required: yes

**`Response`**

CBOR: `response`; JSON: `response`; type: `FormatterResponse`; required: yes


### `LoaderRequest`

LoaderRequest is sent to plugins registered for the "loader" hook. Body contains the source document bytes. ContentType, SourceURL, and LocalPath are metadata from discovery or cache when the host has it.

**`Type`**

CBOR: `type`; JSON: `type`; type: `string`; required: yes

**`Body`**

CBOR: `body`; JSON: `body`; type: `[]byte`; required: yes

**`ContentType`**

CBOR: `content_type`; JSON: `content_type`; type: `string`; required: no

**`SourceURL`**

CBOR: `source_url`; JSON: `source_url`; type: `string`; required: no

**`LocalPath`**

CBOR: `local_path`; JSON: `local_path`; type: `string`; required: no


### `LoaderResponse`

LoaderResponse is returned by a "loader" hook plugin. Body must contain an OpenAPI document in JSON or YAML form. ContentType may describe that returned body when it differs from the input document's content type.

**`Body`**

CBOR: `body`; JSON: `body`; type: `any`; required: yes

Body is an OpenAPI document as []byte or string.

**`ContentType`**

CBOR: `content_type`; JSON: `content_type`; type: `string`; required: no


### `HookRequest`

HookRequest carries the current HTTP request state forwarded to hook plugins.

**`Method`**

CBOR: `method`; JSON: `method`; type: `string`; required: yes

**`URI`**

CBOR: `uri`; JSON: `uri`; type: `string`; required: yes

**`Headers`**

CBOR: `headers`; JSON: `headers`; type: `map[string][]string`; required: yes

**`Body`**

CBOR: `body`; JSON: `body`; type: `[]byte`; required: no

**`BodySHA256`**

CBOR: `body_sha256`; JSON: `body_sha256`; type: `string`; required: no


### `HookRequestHeaderUpdate`

HookRequestHeaderUpdate holds headers that a hook plugin wants to set or replace on the outgoing request. Only headers are applied; method and URI fields are intentionally absent because the request has already been prepared.

**`Headers`**

CBOR: `headers`; JSON: `headers`; type: `map[string]any`; required: no

Each value is either a string or []string.


### `AuthHookInput`

AuthHookInput is sent to plugins registered for the "auth" hook.

**`Type`**

CBOR: `type`; JSON: `type`; type: `string`; required: yes

**`API`**

CBOR: `api`; JSON: `api`; type: `string`; required: yes

**`Profile`**

CBOR: `profile`; JSON: `profile`; type: `string`; required: yes

**`Params`**

CBOR: `params`; JSON: `params`; type: `map[string]string`; required: yes

**`Request`**

CBOR: `request`; JSON: `request`; type: `HookRequest`; required: yes


### `AuthHookOutput`

AuthHookOutput is the reply from an "auth" hook plugin.

**`Request`**

CBOR: `request`; JSON: `request`; type: `*HookRequestHeaderUpdate`; required: no


### `RequestMiddlewareInput`

RequestMiddlewareInput is sent to plugins registered for the "request-middleware" hook.

**`Type`**

CBOR: `type`; JSON: `type`; type: `string`; required: yes

**`Request`**

CBOR: `request`; JSON: `request`; type: `HookRequest`; required: yes


### `RequestMiddlewareOutput`

RequestMiddlewareOutput is the reply from a "request-middleware" hook plugin.

**`Request`**

CBOR: `request`; JSON: `request`; type: `*HookRequestHeaderUpdate`; required: no


### `HookResponse`

HookResponse carries the current HTTP response state forwarded to hook plugins.

**`Status`**

CBOR: `status`; JSON: `status`; type: `int`; required: yes

**`Headers`**

CBOR: `headers`; JSON: `headers`; type: `map[string][]string`; required: yes

**`Body`**

CBOR: `body`; JSON: `body`; type: `any`; required: yes


### `ResponseMiddlewareInput`

ResponseMiddlewareInput is sent to plugins registered for the "response-middleware" hook.

**`Type`**

CBOR: `type`; JSON: `type`; type: `string`; required: yes

**`Request`**

CBOR: `request`; JSON: `request`; type: `HookRequest`; required: yes

**`Response`**

CBOR: `response`; JSON: `response`; type: `HookResponse`; required: yes


### `FollowRequest`

FollowRequest instructs the host to issue a follow-up HTTP request.

**`Method`**

CBOR: `method`; JSON: `method`; type: `string`; required: no

**`URI`**

CBOR: `uri`; JSON: `uri`; type: `string`; required: yes

**`Headers`**

CBOR: `headers`; JSON: `headers`; type: `map[string]string`; required: no

**`Body`**

CBOR: `body`; JSON: `body`; type: `any`; required: no

**`ContentType`**

CBOR: `content_type`; JSON: `content_type`; type: `string`; required: no


### `HookResponseUpdate`

HookResponseUpdate carries partial response modifications from a "response-middleware" plugin.

**`Body`**

CBOR: `body`; JSON: `body`; type: `any`; required: no

**`Headers`**

CBOR: `headers`; JSON: `headers`; type: `map[string]any`; required: no

Each value is either a string or []string.


### `ResponseMiddlewareOutput`

ResponseMiddlewareOutput is the reply from a "response-middleware" hook plugin.

**`Drop`**

CBOR: `drop`; JSON: `drop`; type: `bool`; required: no

**`Follow`**

CBOR: `follow`; JSON: `follow`; type: `*FollowRequest`; required: no

**`Response`**

CBOR: `response`; JSON: `response`; type: `*HookResponseUpdate`; required: no


### `TLSSignerInitMsg`

TLSSignerInitMsg is sent by the host to a tls-signer plugin at startup. The Type field is MsgTypeInit ("init").

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Params`**

CBOR: `params`; type: `map[string]string`; required: yes


### `TLSSignerReadyMsg`

TLSSignerReadyMsg is sent by the plugin when it has loaded its certificate and is ready to sign. Type is MsgTypeTLSSignerReady ("ready").

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Certificate`**

CBOR: `certificate`; type: `[]byte`; required: yes


### `TLSSignerSignMsg`

TLSSignerSignMsg is sent by the host to request a signature. Type is MsgTypeTLSSignerSign ("sign").

**`Type`**

CBOR: `type`; type: `string`; required: yes

**`Digest`**

CBOR: `digest`; type: `[]byte`; required: yes

**`Hash`**

CBOR: `hash`; type: `uint64`; required: no

Hash is the crypto.Hash value cast to uint64. 0 means no specific hash.

**`Padding`**

CBOR: `padding`; type: `string`; required: no

**`SaltLength`**

CBOR: `salt_length`; type: `int`; required: no


### `TLSSignerSignedMsg`

TLSSignerSignedMsg is the plugin's reply to a TLSSignerSignMsg. It carries either a Signature or an Error, never both. This message has no Type field.

**`Signature`**

CBOR: `signature`; type: `[]byte`; required: no

**`Error`**

CBOR: `error`; type: `string`; required: no


### `TLSSignerShutdownMsg`

TLSSignerShutdownMsg asks the plugin to release resources and exit.

**`Type`**

CBOR: `type`; type: `string`; required: yes
<!-- END GENERATED -->

## Startup Messages

`--rsh-plugin-manifest` writes one manifest map and exits.
`--rsh-plugin-commands` writes one command discovery map and exits. Command
discovery includes:

- `protocol_version`: command-plugin discovery protocol version
- `commands`: command declarations contributed by the plugin

Restish treats omitted `protocol_version` as the initial command-plugin
protocol, and rejects future versions that require a newer host.

Startup flags are only recognized in the host-injected argv prefix, before the
first user argument. For command plugins, later arguments such as
`--rsh-plugin-manifest`, `--rsh-color`, `--rsh-stdout-tty`, or
`--rsh-stderr-tty` remain user arguments and must not affect startup mode or
terminal context.

## Command Plugin Messages

Restish sends the command plugin an initial `init` message with:

- `type`: `init`
- `command`: contributed command name selected by the user
- `args`: remaining user arguments

Reply-bearing command messages may include `request_id`; hosts echo it in the
matching response. Go plugins should normally let `plugin.CommandClient`
generate and route those IDs.

### Plugin To Host

| Type | Important fields | Host reply |
| --- | --- | --- |
| `http-request` | `method`, `uri`, `headers`, `body`, `content_type`, `no_cache`, `cache_ttl`, `timeout`, `filter` | `http-response` with `status`, `headers`, `url`, `body`, optional `error` |
| `api-spec` | `name`, optional `profile` | `api-spec-response` with `raw`, `content_type`, resolved `operations`, optional `error` |
| `list-apis` | none beyond optional `request_id` | `list-apis-response` with `apis` |
| `list-profiles` | `api` | `list-profiles-response` with `profiles` |
| `config-read` | optional `api`, `profile`, `plugin` | `config-read-response` with `base_url`, `headers`, `query`, `plugin_config`; auth secrets are excluded |
| `prompt` | `message`, optional `hidden` | `prompt-response` with `value` or `error` |
| `confirm` | `message` | `confirm-response` with boolean `value` or `error` |
| `select` | `message`, `options` containing `label` and `value` | `select-response` with the selected opaque `value` or `error` |
| `response` | `status`, `headers`, `body` | none; host formats it like a normal API response |
| `stdout-data` / `stderr-data` | `data` bytes | none |
| `progress` / `spinner` / `log` / `warn` | `text` | none |
| `done` | optional `exit_code` | none; ends the command workflow |

`api-spec-response.operations` is Restish's resolved, config-aware operation
list. Each operation includes fields such as `id`, `method`, `path`, `summary`,
`description`, `deprecated`, `parameters`, `has_body`, `body_required`,
`request_media_type`, and `mcp_ignore`. Parameters include `name`, `in`,
`required`, `description`, `type`, `item_type`, OpenAPI `style` and `explode`,
`allow_reserved`, `content_media_type`, raw `schema`, and `enum`.

### Host To Plugin

- `http-response`
- `api-spec-response`
- `list-apis-response`
- `list-profiles-response`
- `config-read-response`
- `prompt-response`
- `confirm-response`
- `select-response`
- `stdin-data`
- `stdin-close`

`stdin-data` and `stdin-close` are used only for command plugins that opt into
passthrough stdio.

Interactive selection is available only to plugins that declare the
`command.select` required feature. Multiple options require a terminal and are
not available with `passthrough_stdio`. Restish renders labels on stderr and
returns values unchanged to the plugin.

## Hook Plugins

Hook plugins are usually short-lived. Auth, middleware, and loader hooks receive
one request and return one reply. Formatter hooks receive a stream and write the
rendered bytes to stdout.

### Common HTTP Shapes

`request` carries `method`, `uri`, `headers`, optional raw `body`, and optional
`body_sha256`. Header maps use header names with string arrays.

`response` carries normalized `status`, `headers`, and `body`.

Auth and request middleware replies can return:

```json
{
  "request": {
    "headers": {
      "Authorization": "Bearer token"
    }
  }
}
```

Header update values may be a string, an array of strings, or `null`. `null`
deletes the header.

Response middleware may return:

- `drop`: omit the response.
- `follow`: make a follow-up request with `method`, `uri`, optional `headers`,
  optional `body`, and optional `content_type`.
- `response`: update normalized response fields, currently `headers` and
  `body`.

Restish skips response middleware on the follow-up request to avoid loops.

Loader hooks receive `body`, optional `content_type`, optional `source_url`, and
optional `local_path`. They return an OpenAPI document in `body`, with optional
`content_type` when the output differs from the input.

Formatter hooks receive `formatter` messages with `format`, `color`, `event`,
and `response`. `event` is `start`, `item`, or `end`. For full-response renders,
`start` usually includes the whole normalized response body. For paginated or
event-stream output, Restish sends `start`, then one or more `item` messages,
then `end`.

## TLS Signer Messages

TLS signer plugins are long-lived for the lifetime of the mTLS request. The host
starts with:

```json
{
  "type": "init",
  "params": {}
}
```

The plugin replies with `ready` and a DER certificate in `certificate`. For each
signature operation, Restish sends `sign` with `digest`, optional numeric
`hash`, optional `padding`, and optional `salt_length`. The plugin replies with
either `signature` or `error`; that reply has no `type` field. On cleanup,
Restish sends `shutdown`.

Certificate and signature messages are size-limited by the host to protect the
request process.

## Debugging

```bash
restish plugin debug ./path/to/plugin
```

Use streaming debug output to confirm message type, payload shape, and whether
the plugin sent `done` for command workflows.

## Related Pages

- [Command Plugins](/docs/plugins/command-plugins/)
- [Hook Plugins](/docs/plugins/hook-plugins/)
- [Plugin Command](../plugin-command/)
