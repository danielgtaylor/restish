# OpenAPI Implementation Contract

## Status

Accepted for the first Restish v2 release. This record captures the OpenAPI 3.x
behavior that Restish generated commands must preserve. It is intentionally
more reference-like than most design docs because it is meant to be usable as a
reimplementation checklist.

## Problem

OpenAPI is broad enough that "parse paths and generate commands" is not a
complete design. Real-world specifications use path-level parameters,
operation-specific servers, several auth schemes, external references, mixed
media types, vendor extensions, non-JSON parameters, and schema constructs that
can easily break CLI generation or startup performance.

Restish v2 must turn those documents into predictable commands without treating
the parser library as the product contract.

## Goals

- Support OpenAPI 3.0 and 3.1 documents commonly seen in public APIs.
- Generate commands deterministically from cached or local metadata.
- Preserve OpenAPI wire semantics where they affect URLs, headers, cookies,
  query strings, request bodies, and auth suppression.
- Keep startup fast enough for shell loops that invoke `restish` repeatedly.
- Bound ambiguous or expensive spec features instead of panicking, hanging, or
  allocating unbounded structures.
- Make unsupported features explicit in diagnostics, help, or documented
  release decisions.

## Non-Goals

- Full OpenAPI request or response validation by default.
- Code generation for callbacks, links, or webhooks as ordinary request
  commands.
- Automatic selection of different user profiles per operation.
- Guaranteeing that every possible OpenAPI schema can be converted into a
  perfect example body.

## Document Loading

The built-in OpenAPI loader accepts OpenAPI 3.0 and 3.1 documents encoded as
JSON or YAML. It should recognize conventional OpenAPI media types, plain
structured content types, and documents whose top-level `openapi` key appears
after other keys.

The loader receives origin metadata through a typed option structure:

- source URL, if fetched from the network;
- local path, if loaded from disk;
- request context;
- HTTP transport;
- cross-origin reference policy.

OpenAPI loading resolves references before command extraction. Supported
external reference forms include:

- relative local file references from local specs;
- full `file://` references from local specs;
- same-origin `http` and `https` references from remote specs;
- cross-origin `http` and `https` references only when explicitly allowed and
  permitted by the trust-boundary rules in design 030.

Remote reference fetches use the same context, timeout, redirect, size-limit,
and private-host safeguards as spec discovery. Remote specs must not be allowed
to read local relative or `file://` refs. Local reference resolution must stay
beneath the user-selected local source rules where possible and should surface
the path that failed when a file is missing or malformed.

External references are valid anywhere the parser accepts references that affect
command generation, including Path Items, parameters, request schemas, response
schemas, and shared component schemas.

Some real-world specs place documentation-tool objects such as
`description: {"$ref": "markdown/usecases.md"}` where OpenAPI requires a
string. Restish treats those docs-only description refs as an empty description
before reference resolution, and it does not fetch the referenced markdown. This
compatibility cleanup must not rewrite schema properties or component entries
that happen to be named `description`.

Webhooks-only documents, documents with no paths, and empty Path Items generate
no request commands and must not panic. Response links and callbacks are not
generated as commands in v2.

## Operation Extraction

Restish generates request commands for ordinary Path Item operations using the
standard HTTP methods:

- `GET`
- `POST`
- `PUT`
- `PATCH`
- `DELETE`
- `HEAD`
- `OPTIONS`
- `TRACE`

The neutral operation model must contain enough information for command
generation and request planning without retaining parser-library-specific
objects. At minimum it carries:

- command name, aliases, summary, description, tags, deprecation, and hidden
  state;
- HTTP method;
- operation path after server resolution;
- merged parameters;
- request body media alternatives and schemas;
- response media, schemas, and response header names for help;
- examples and schema-derived example-body data;
- explicit no-auth state and future security policy metadata;
- original source location or stable operation key for diagnostics.

Operation extraction is deterministic. If an operation cannot be represented
safely, Restish reports why instead of silently dropping it.

## Command Naming And Extensions

Command names use this priority order:

1. `x-cli-name`
2. `operationId`
3. method-and-path fallback, for example `get-users-id`

Names are normalized for CLI ergonomics, but the generated command remains
traceable to the source operation. Duplicate generated names are disambiguated
with deterministic method or numeric suffixes, and compatibility aliases may
preserve the original name when it is safe to do so. Alias collisions are
diagnosed and the colliding alias is skipped.

Supported CLI-shaping extensions are:

- `x-cli-name`
- `x-cli-aliases`
- `x-cli-description`
- `x-cli-ignore`
- `x-cli-hidden`
- `x-cli-completion`

The naming, alias, description, ignore, and hidden extensions apply to
operations. `x-cli-ignore` and `x-cli-hidden` also apply at path scope where
supported. Parameter-level `x-cli-name`,
`x-cli-description`, `x-cli-ignore`, and `x-cli-hidden` shape the corresponding
argument or flag without changing the wire name. Parameter-level
`x-cli-completion` references a read-only operation from the same operation set
and response selectors for API-backed shell completion.

Completion providers are resolved by exact OpenAPI `operationId`. The provider
must be unique, use `GET` or `HEAD`, and have no request body. Bindings use
original `(in, name)` parameter identities and must cover every required
provider parameter. Invalid providers are omitted with a deterministic warning;
they do not remove the generated operation. Typed provider metadata is stored in
the operation cache so command registration after `api sync` does not reparse
the OpenAPI document.

In tag layout, operations with a first tag are nested under that tag command;
untagged operations remain directly under the API command. Flat layout remains
the default.

## Parameters

Parameters are merged from path scope and operation scope according to OpenAPI
rules. The merge key is `(in, name)`, so a path parameter and a query parameter
with the same name remain distinct. Operation-level parameters override
path-level parameters with the same merge key.

Path template variables must have matching path parameters after merge.
Required path parameters become positional arguments in template order.
Duplicate template variables or missing declarations fail operation generation.

Required non-path parameters become positional arguments after path parameters.
Optional query, header, and cookie parameters become flags. Parameters without a
schema fall back to string handling. Deprecated parameters should be visible in
help.

Header parameters named `Accept`, `Content-Type`, or `Authorization` are ignored
per OpenAPI. They are controlled by Restish content negotiation, body encoding,
and auth policy instead of generated as ordinary parameters.

Generated commands derive their default `Accept` header from successful
OpenAPI response `content` entries. Restish should advertise every declared
response media type that the active content registry can decode, ordered by
registry preference. It should not speculate about undeclared efficient
transports such as CBOR or MessagePack for generated commands; users can still
override the header explicitly with `-H Accept: ...` or a profile header when
they know the server supports a better representation than the spec declares.

The original OpenAPI parameter name is preserved on the wire even when the CLI
flag name is normalized, slugified, or overridden. This matters for names such
as `$select`, `$filter`, `X-Stripe-Account`, and dotted vendor fields.
Generated parameter flag names must not shadow Cobra help flags, Restish global
flags, or generated-command local flags. Reserved-name collisions use a
location prefix, such as query parameter `help` becoming `--query-help`, while
duplicate normalized names prefer readable escaped wire-name hints such as
`--foo-underscore-bar`.

## Parameter Serialization

Restish serializes parameters according to OpenAPI `style`, `explode`,
`allowReserved`, and parameter `content` where practical.

Generated CLI commands and MCP tool requests share one implementation for
location/style/explode serialization of non-`content` parameters. Callers may
still perform surface-specific validation first: generated commands parse CLI
strings and shorthand object values, while MCP accepts JSON tool arguments and
rejects unsupported object shapes or array styles with an MCP error.

Supported query parameter styles:

- `form` with `explode: true` or `false` for scalars, arrays, and objects;
- `spaceDelimited` arrays;
- `pipeDelimited` arrays;
- `deepObject` objects;
- `allowReserved` values, preserving literal reserved characters that OpenAPI
  says may remain unescaped, while still encoding literal `+` as `%2B` and
  literal `%` as `%25` to avoid ambiguous or double-decoded query bytes.

Supported path parameter styles:

- `simple`;
- `label`;
- `matrix`.

Supported header parameter style:

- `simple`.

Supported cookie parameter style:

- `form`.

Parameter `content` is supported for JSON-compatible media types by encoding the
parameter value as a JSON value in its parameter location. Non-JSON parameter
content falls back to raw string representation unless a future content handler
defines richer semantics.

Unsupported styles should produce a warning or help note and fall back to the
closest safe default for that parameter location. They must not corrupt required
path substitutions.

GitHub-style `x-multi-segment` path semantics are not part of the OpenAPI
standard. Restish currently percent-escapes slashes in path parameters rather
than allowing a single argument to span multiple path segments.

## Request Bodies And Media Types

Generated commands use the same request-body construction model as generic HTTP
commands: stdin, shorthand, files, and explicit content-type flags all flow
through design 008 and design 003.

When the user does not explicitly choose a request content type, media
preference is:

1. `application/json` or another registered `+json` media type;
2. another registered structured type;
3. the first deterministic supported media type from the operation;
4. raw text or binary where the operation only supports opaque input.

Supported request body media behaviors include:

- JSON and structured `+json` bodies from shorthand or stdin;
- `application/x-www-form-urlencoded` object bodies, including nested object
  keys and repeated array values;
- `multipart/form-data` string fields, file fields, repeated file fields for
  arrays, and OpenAPI `encoding.contentType` as per-part content metadata;
- `application/octet-stream` and other binary bodies from raw input or files;
- XML request bodies from raw string or whole-body `@file` input, preserving
  bytes with the declared XML media type;
- NDJSON/JSON Lines request bodies from raw string or whole-body `@file` input,
  while structured arrays remain encodable as one JSON record per line;
- non-JSON structured content when a registered content type can marshal it.

GET, DELETE, and other operations with request bodies are allowed because
OpenAPI permits documenting them even when some servers or intermediaries
discourage them.

Generated operation commands expose `--rsh-generate-body` when the operation has
a request body. The generated body is best-effort and bounded. It may use
examples, schema examples, defaults, enum values, const values, formats,
patterns, numeric bounds, object properties, and array items. It must never
recurse indefinitely.

## Schema Handling

Restish uses schemas for help, completions, and example generation. It does not
reject unknown body fields by default, does not perform full validation before
requests, and does not silently coerce generated-command request-body values
based on schema metadata.

`--rsh-validate` is the explicit generated-command escape hatch for local JSON
request-body validation. It validates the assembled body against the selected
operation request schema before execution, reports user-facing field paths, and
does not rewrite input. Generic HTTP commands and default generated-command
execution stay schema-agnostic because servers remain the source of truth.

Schema support should cover OpenAPI 3.0 and 3.1 shapes including:

- `type` as a string or an array;
- `nullable`;
- `enum` and `const`;
- `default`, `example`, and `examples`;
- `format`, `pattern`, `minimum`, `maximum`, exclusive bounds, `multipleOf`,
  and related annotations;
- `readOnly` and `writeOnly`, interpreted relative to request or response use;
- object `properties`, `required`, and `additionalProperties`;
- array `items`;
- recursive `$ref`s with bounded traversal;
- `oneOf`, `anyOf`, and `allOf`.

`allOf` should be merged enough for help and example generation, with later
constraints combined rather than losing earlier object properties or numeric
annotations. `oneOf` and `anyOf` should be represented in help and use a
deterministic first viable branch for example generation. Discriminators should
be surfaced when useful, but lack of discriminator enforcement must not block
command generation.

Generated-command JSON bodies are schema-agnostic after shorthand parsing. A
generated command and a generic HTTP command should send the same JSON value for
the same shorthand expression. For example, `id: 123` remains a number even if
the OpenAPI schema says `id` is a string; users should write `id: "123"` when
they need string semantics.

## Servers And URLs

Server resolution honors OpenAPI server precedence:

1. operation-level `servers`
2. path-level `servers`
3. document-level `servers`
4. configured API `base_url`

Document-level servers contribute the generated operation path shape, while the
configured API/profile `base_url` supplies the default request origin. Path- and
operation-level servers are treated as explicit routing for that part of the
API.

`operation_base` overrides OpenAPI servers for generated operation paths. In v2
it is an absolute path prefix, not a full URL.

Server variables are resolved before URL reference resolution. Values come from
API-level `server_variables`, then profile-level overrides, then OpenAPI
defaults. `enum` values document accepted values and may produce warnings when
local config chooses a different value, but they do not override explicit local
operator intent. Variables configured locally that are not declared by any
applicable OpenAPI server remain errors when the document declares server
variables, because they cannot affect URL expansion and are usually typos.
Restish does not expand variables into a Cartesian product of commands or URLs.

Relative document server URLs resolve against the configured API `base_url`.
Absolute document server URLs on another origin keep their path prefix but use
the configured `base_url` origin. Absolute path- or operation-level server URLs
are used directly when they match the configured origin. A cross-origin path- or
operation-level server is represented in operation metadata but may only be used
when the API config explicitly allows its origin with
`allowed_operation_origins`, or when a configured `url_overrides` entry rewrites
the resolved URL before execution; otherwise the generated command fails with a
configuration hint. If a matching absolute or relative server resolves outside
the configured base path while staying on the same origin, Restish represents
the generated operation path as a relative escape so short-name expansion
reaches the intended path while keeping the selected API profile.

An untrusted spec must not be able to redirect authenticated generated commands
to another origin by declaring a different server URL. Once an operator has
allowed an operation server origin, generated requests to that origin still run
under the selected API profile and operation security policy; the allowlist is
the explicit trust boundary that permits those operation credentials to be
applied off the API base origin.
The origin comparison is shared with request redirects, external ref loading,
and pagination: scheme, hostname, and effective port must match. Unknown schemes
without explicit ports are not treated as same-origin.

## Security Policy

OpenAPI security is normalized during operation extraction into Restish
credential alternatives:

- operation-level `security` overrides document-level `security`;
- document-level security applies when an operation omits `security`;
- `security: []` means no profile auth, auth hooks, credential headers, or
  credential query params are sent for that generated command;
- an empty requirement object (`{}`) means anonymous access is allowed as an
  alternative, not forced no-auth;
- each non-empty Security Requirement object is one AND-set of credential
  requirements, and the surrounding array is an OR-list.

Generated command execution matches those alternatives against the active
profile. Generic URL requests apply the same operation security when the
request's API/profile, HTTP method, and URL path unambiguously match cached
operation metadata; otherwise they keep ordinary profile-auth behavior. A single
effective credential requirement may use profile-level `auth` or `auth_ref` for
compatibility. Multiple alternatives or combined requirements require explicit
`profiles.<name>.credentials.<id>` bindings. Requirement values such as OAuth
scopes or role strings are matched against a binding's `satisfies` list.

`--rsh-auth` selects one alternative by credential ID set, for example
`PartnerKey` or `UserOAuth+PartnerKey`. When the selected set matches a
declared alternative, Restish enforces that alternative. When the selected set
does not match the operation security metadata, including `security: []`,
Restish treats the flag as explicit operator intent, sends the configured
credential set if the active profile satisfies it, and emits a warning that the
request is outside the OpenAPI contract. Overrides are still rejected for
invalid syntax, missing credential bindings, conflicting credential mutations,
and profiles that do not satisfy the requested credential set.

As an escape hatch for provider specs that under-declare security, an explicit
`--rsh-auth <credential-id>` may select a configured credential that is not
listed in the operation's OpenAPI security requirements. The default remains
strict: undeclared credentials are not selected implicitly, and the override
emits a warning so the caller knows it is outside the spec contract.

When no `x-cli-config` extension exists, fallback `api connect` auth setup is
derived only from security schemes referenced by document-level or operation
security requirements. Declared but unused `components.securitySchemes` are not
converted into prompts or credential bindings.

## Startup Performance And Caching

Routine startup must not fetch the network or parse large OpenAPI documents when
usable operation metadata is cached. Startup command registration should use
local specs or the operation metadata cache described in design 006.

Operation-cache identity includes:

- API base URL;
- `operation_base`;
- effective server-variable values;
- raw spec hash;
- local spec file freshness;
- cache schema version;
- Restish version.

Benchmarks should cover at least a 1,000-operation synthetic OpenAPI document
for:

- cold explicit sync or extraction;
- cached root/help startup;
- cached execution of a generated command.

These benchmarks guard the shell-loop use case where scripts call `restish`
many times for individual requests.

## Testing Plan

OpenAPI tests should include focused fixtures for:

- path-level parameters merged into operation commands;
- operation-level parameter overrides;
- required path and non-path parameters as positional arguments;
- optional parameters as flags;
- parameters without schema;
- path, query, header, and cookie locations;
- parameter serialization styles and `allowReserved`;
- JSON parameter `content`;
- original wire names for normalized CLI flags;
- path, operation, and document server precedence;
- relative server URLs;
- same-origin path escapes;
- `operation_base` override;
- server variables and enum validation;
- request media selection across JSON, form, multipart, binary, and non-JSON
  bodies;
- multipart per-part `encoding.contentType`;
- response media and response header names in help, including operations with
  no body schema;
- `oneOf`, `anyOf`, `allOf`, nullable, type arrays, examples, defaults, enums,
  consts, numeric constraints, read/write-only fields, additional properties,
  and recursive refs;
- external local, `file://`, same-origin remote, and blocked cross-origin refs;
- external Path Item and parameter refs;
- multiple security schemes, `security: []`, OAuth scopes, alternatives, and
  combined requirements under the current and future policy;
- hidden and ignored operations, paths, and parameters;
- naming collisions and alias collisions;
- empty Path Items, no paths, webhooks, callbacks, and response links;
- cached startup paths and large-spec benchmarks.

## Documentation Impact

User-facing OpenAPI documentation should explain the common behaviors without
mirroring this entire matrix. This design doc remains the maintainer-facing
source for reimplementation and regression-test planning.

## Release Decisions

Unsupported parameter styles are visible in generated help when Restish can
detect them during operation extraction. If the unsupported parameter is used at
execution time, Restish should warn and fall back to the closest safe
location-specific serialization. Required path substitutions must still fail
rather than risk corrupting the URL.

Strict OpenAPI request validation is not part of the first v2 release. Generated
commands use schemas for help, completion, example generation, media selection,
and bounded coercion only. A future explicit validation mode may be added, but
default requests stay shell-native and permissive. This follows the design 030
"trust explicit operator intent" tenet: when a user supplies a body field,
query value, header value, or flag explicitly, Restish should send it and let
the server validate API-specific semantics. Specs are often stale or incomplete,
and generated commands need an easy escape hatch for real APIs that differ from
their descriptions.

Local validation is still appropriate when Restish cannot build a coherent
request at all, such as a missing required path value, an unreadable file input,
or an invalid local flag value. Those checks protect URL construction and CLI
explainability; they are not full schema enforcement.

Credential bindings without declared OAuth scopes or other requirement values
may be accepted during setup so users can register an API incrementally. A
scoped operation fails before request execution when the selected profile cannot
prove that its credential satisfies the required values.
