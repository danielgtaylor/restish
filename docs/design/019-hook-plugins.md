# Hook Plugins

## Summary

Hook plugins are usually short-lived subprocesses that handle a single
extension point per invocation. Restish writes one CBOR data item to the
plugin's stdin, reads either one CBOR reply or raw formatter output, and then
the plugin exits.

Formatter plugins are the one exception: they run as a short formatter session
so they can maintain output state across paginated and event-stream renders.

This model is used for:

- `auth-resolver`
- `auth`
- `request-middleware`
- `response-middleware`
- `loader`
- `formatter`

## Problem

Some extensions need access to Restish's request and response model but do not
need to own an entire command lifecycle. Typical examples include:

- adding auth headers from external credentials
- injecting request headers
- mutating or suppressing normalized responses
- teaching Restish about a new API description content type
- adding a new output format

Those use cases should stay simple to implement and safe to reason about. Most
of them do not need a persistent session or multi-message protocol.

Formatter plugins turned out to be the boundary case. One-shot formatter hooks
work for ordinary responses, but they break down for outputs such as CSV when
Restish is streaming items from pagination or NDJSON/SSE:

- the formatter may need to emit a header once, then many records
- the host should not have to buffer the entire stream just to satisfy a plugin
- one-shot formatter invocations per item create inconsistent behavior between
  collected and streamed output

## Design

The default hook-plugin contract is intentionally one-shot:

1. Restish selects discovered plugins whose manifest declares a hook.
2. It builds a hook-specific input message.
3. It starts the plugin executable.
4. It writes one CBOR data item to stdin.
5. It reads one result from stdout.
6. The plugin exits.

Most hooks use a bounded request/reply helper with these design constraints:

- one typed request message in
- one typed reply message out
- bounded wait with a configurable timeout
- plugin stderr surfaced on failure when helpful
- non-zero exit is an error

The current implementation uses a generic helper with a default timeout. The
design now requires that timeout to be configurable by hook type and overridable
where a workflow genuinely needs more time.

## Typed Messages, Not Loose Unions

Each hook message type should have its own request and response schema. A single
"decode everything into one broad struct" approach is easy initially but makes
future protocol evolution fragile.

The design preference is:

- auth request/response types
- request middleware request/response types
- response middleware request/response types
- loader request/response types
- formatter session message types

That keeps unknown-field handling and future evolution much safer.

### Auth Hook

The `auth` hook runs during request preparation. Restish sends:

- API name
- profile name
- auth params from config
- the outbound request method, URI, and headers

The plugin replies with request updates, typically header changes. The reply is
merged into the outbound request before it is sent.

This keeps external auth providers inside the same request-hook stage as
built-in auth handlers.

When generated-operation auth selects multiple credential bindings for one
request, Restish applies every built-in credential first and then invokes auth
hook plugins once with the final prepared request. The hook input omits
per-credential `params` in that multi-credential case until the protocol grows
an explicit combined credential context; single-credential auth continues to
send the credential params with secrets redacted according to the plugin
manifest.

Provider-specific OAuth token exchange is a good fit for this hook/plugin
boundary when the built-in OAuth flows are too small. The plugin can own the
provider-specific token exchange and return ordinary request updates, while the
core keeps common OAuth helpers focused on standard client credentials,
authorization code, and device-code behavior.

### Auth Resolver Hook

The `auth-resolver` hook lets a credential-owning plugin satisfy operation
security without placeholder credentials in Restish config. Restish consults
it only after configured credentials fail. It sends one complete OpenAPI
security alternative (an AND set), the API/profile names, and the final request
method and URI. The plugin replies only with `handled: true` or `false`.

Restish tries alternatives in document order. Exactly one plugin may claim an
alternative; multiple claims are an error. Once selected, Restish invokes only
that plugin's `auth` hook and includes the same requirements, including exact
scope needs. The resolver cannot change the target, requirement set, or
profile, and `security: []` never invokes it. Plugins requiring this contract
declare the `auth.operation_security` feature.

Resolvers should claim only already-available credentials that cover the whole
alternative. Interactive authorization, refresh, or request signing belongs in
the selected `auth` hook. This keeps resolution deterministic and prevents a
resolver from widening authority while Restish is choosing credentials.

Auth inspection does not invoke a resolver without a final request target.
Instead, it distinguishes statically callable operations from operations whose
security will be evaluated by a resolver at request time. It must not report
those operations as missing static credentials or suggest adding a static
binding as the primary action merely because resolver selection is deferred.
The diagnostic may retain a generic static-configuration fallback for use when
the resolver declines.

### Request Middleware Hook

The `request-middleware` hook receives the prepared request and can return
header updates before the request goes out. The implementation currently only
applies header changes, even if a plugin also returns method or URI fields.

That is an intentional practical boundary: middleware runs after Restish has
already prepared the request object and transport options.

If a future middleware contract needs broader request mutation, that should be a
new explicit hook capability rather than an undocumented side effect.

Request-signing plugins may require the final request body. Restish includes a
SHA-256 hash of replayable request bodies in hook request metadata. Plugins that
declare the `request.final_body` required feature, or plugins that opt into
auth-secret forwarding, may also receive the final body bytes. Non-replayable
bodies are omitted because the host must not consume the stream before sending
the request.

### Response Middleware Hook

The `response-middleware` hook receives:

- the original request metadata
- the normalized response status, headers, and body

The plugin may:

- return `{"drop": true}` to suppress output entirely
- return `{"follow": {...}}` to tell Restish to make a follow-up request
- return `{"response": {...}}` to replace body fields or merge headers

Response middleware runs on the interpreted response path: interactive output,
explicit filters, metadata shortcuts, collection, explicit output formats, and
explicit `--rsh-print` values. It does not run for the automatic raw-download
path (`restish URL > file` with no filter, collection, metadata shortcut, or
output format), because that path preserves the original response body bytes for
files and shell pipelines.

The response update's `headers` object is a partial update: keys returned by the
plugin replace those individual response header values, while omitted inbound
headers remain unchanged.

The follow-up path is especially important: the plugin asks Restish to issue
the request, so auth, retries, TLS, credential stripping, and other core
behaviors still apply. A `follow` request carries:

- `method`, defaulting to `GET`
- `uri`
- optional `headers`
- optional `body`
- optional `content_type`, or a `Content-Type` header when a body is present

The host encodes the body through the content registry just like normal request
bodies. Response middleware does not run again for the follow-up request, which
prevents accidental loops.

### Loader Hook

Loader plugins let Restish recognize non-built-in spec formats. A loader plugin
declares `loader_content_types` in its manifest, which Restish turns into
registered `spec.Loader` instances at startup.

When a matching content type is detected, Restish sends the raw body to the
plugin and expects back:

- `content_type`: the detected source content type, when known
- `source_url` or `local_path`: source metadata, when known
- `body`: an OpenAPI document as bytes or a string
- optional response `content_type`

The plugin does not return Restish's internal API model directly. Instead, it
returns an OpenAPI document, and Restish parses that through the normal
OpenAPI-loading path. That keeps generated commands aligned with the rest of
the system.

This is an intentionally narrow trust boundary. Loader plugins transform one
document format into the host's canonical API-description path; they do not
directly define commands.

### Formatter Hook

Formatter plugins declare `formatter_names` in the manifest. Each declared name
becomes available through `-o <name>`.

Formatter hooks are slightly different from the other hook types because stdout
is treated as raw formatted bytes rather than a CBOR reply.

### Formatter Hook Session

Formatter plugins receive a stream of `formatter` messages on stdin. The host
starts one plugin process, then sends:

1. `formatter` with `event: "start"`
2. zero or more `formatter` messages with `event: "item"`
3. one final `formatter` message with `event: "end"`

The `start` message carries response metadata (`proto`, `status`, `headers`,
`links`) and may also include a full `body` for ordinary non-streaming
responses.

Each `item` message carries one body/sub-value to render. This is what lets the
same formatter handle:

- a normal one-shot body by reading `start.response.body`
- paginated output by rendering each `item`
- NDJSON/SSE output by rendering each `item`

The plugin writes raw formatted bytes to stdout as values arrive and exits after
the `end` message or EOF on stdin.

This model is intentionally narrow:

- only formatter hooks get the long-lived stream session
- the host still owns pagination, filtering, and event parsing
- plugins do not get to drive the HTTP loop or ask for additional pages

The practical goal is to support stateful output modes such as CSV without
forcing Restish to invent formatter-specific buffering behavior in the core.

## Failure And Timeout Model

Hook failures should be explicit.

If a hook:

- times out
- exits non-zero
- returns malformed data
- writes an invalid response

then Restish should surface a hook-specific error with plugin identity.

Whether the command continues depends on the hook category:

- auth and loader failures are usually fatal
- response middleware may choose to fail the request rather than emit
  inconsistent output
- formatter failures are fatal once output has started

## Relationship To Output Planning

Formatter hooks are renderers, not planners. The host still decides:

- document versus record semantics
- pagination behavior
- filter evaluation strategy
- stream parsing

This keeps plugins focused and prevents each formatter from needing to
re-implement the core data-flow model.

## Alternatives Considered

### Keep these extension points in-process only

That is simpler for the core binary, but it forces every custom formatter,
loader, or auth hook to ship as a custom Restish build.

### Let loader plugins return an internal command model directly

That would expose too much of Restish's internal representation. Requiring
loader plugins to hand back OpenAPI keeps the seam smaller and more stable.

### Keep formatter hooks one-shot forever

That keeps the protocol simpler, but it forces the host to choose between:

- buffering entire paginated or streaming result sets before formatting, or
- calling formatter plugins once per item and accepting inconsistent behavior

Neither is a good fit for stateful formats such as CSV.

### Use raw JSON everywhere instead of CBOR

JSON would be easier to inspect manually, but CBOR is more efficient for binary
payloads, maps naturally to byte-oriented plugin messages, and is self-delimiting
over a stream.

## Notes

The current implementation is centered in:

- `internal/cli/hooks.go` for auth and middleware integration
- `internal/spec/plugin_loader.go` for loader-backed spec loading
- `internal/output/plugin_formatter.go` for formatter-backed output
- `internal/cli/plugin_hook_test.go` for end-to-end examples of every hook type

One detail worth preserving is that hook plugins are called from well-defined
seams in the existing pipeline instead of becoming alternate pipelines. Even
with streaming formatter sessions, the goal is extension, not bypass.
