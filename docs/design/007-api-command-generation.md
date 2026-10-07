# API Command Generation

## Summary

Restish v2 turns registered APIs into first-class CLI surfaces. A configured API
becomes a command group, and operations from its API description become normal
Cobra commands with predictable names, arguments, flags, and help output.

The key design point is that generated commands should feel native while still
being traceable back to the source API description.

Design 034 is the detailed OpenAPI compatibility contract. This record explains
where generated commands fit in the CLI architecture; design 034 defines the
full OpenAPI 3.x feature matrix that tests and reimplementations should follow.

## Goals

- let users work with APIs by name instead of memorizing raw URLs
- preserve a close mapping from spec to CLI behavior
- generate commands deterministically from cached or local specs
- support CLI-shaping extensions without turning generation into ad-hoc code
- keep generated commands compatible with the core request pipeline

## Non-Goals

- perfect code generation for every OpenAPI edge case
- making the CLI mirror every spec quirk literally when that hurts usability
- requiring ahead-of-time build steps

## Command Generation Inputs

Generation depends on:

- API registration from config
- canonical loaded API description from design 006
- profile and pagination metadata from config
- CLI-specific extensions such as `x-cli-*`

The generator should consume a stable operation model rather than reaching
deeply into parser-library internals wherever possible.

## Command Tree Shape

Each registered API contributes one top-level command group named after the API
short name:

```text
restish <api> <operation> ...
```

Under that group, each included operation becomes a child command.

Built-in commands still take precedence over API short names. The generator does
not get to shadow core commands such as `api`, `cache`, `config`, or `shell`.
Hidden compatibility commands are reserved too; for example, the top-level
`completion` alias remains unavailable as an API short name even though the
canonical user-facing command is `shell completion`.

The set of reserved built-in command names should come from the actual root
command tree or a guard test that proves the reserved list is in sync. Removed
pre-release commands are not reserved unless they still exist as hidden
compatibility commands. For v2, `flags` is not a built-in command and may be
used as an API short name.

By default, operations live in one flat namespace under the API command. APIs
that benefit from tag hierarchy can opt into:

```jsonc
{
  "apis": {
    "example": {
      "command_layout": "tags"
    }
  }
}
```

In tag layout, operations with a first OpenAPI tag are nested under a tag
subcommand such as `restish example repos create-repo`. Untagged operations
remain directly under the API command. The default remains flat because tag
taxonomies are not always stable or ergonomic. There is no automatic layout
mode; API authors and users opt into tag layout explicitly when it helps.

## Operation Inclusion

An operation is eligible for generation when:

- it is not explicitly ignored
- the spec parsed successfully
- all required path variables can be mapped to declared parameters

If an operation cannot be generated safely, Restish should surface a clear
diagnostic rather than silently dropping it.

Empty or nil path items in an OpenAPI document are ignored safely. They should
not panic command generation, MCP tool generation, or any plugin-facing
operation export path.

Generated request commands are only produced for ordinary Path Item operations
using OpenAPI HTTP methods. Webhooks, callbacks, and response links are not
registered as ordinary request commands in v2.

An operation that explicitly declares `security: []` is generated as a no-auth
operation. Request execution should suppress configured profile auth for that
operation so public health, status, and discovery endpoints do not run external
auth tools or attach credentials unnecessarily.

## Naming

### Default Name

The preferred source of the command name is:

1. `x-cli-name`
2. `operationId`
3. fallback derived from HTTP method plus path

The fallback is important for compatibility. Operations without `operationId`
must still produce commands.

### Fallback Naming Rule

When no explicit name is provided, Restish should derive a stable kebab-case
name from the method and path, for example:

- `GET /users/{id}` -> `get-users-id`
- `POST /v1/invoices` -> `post-v1-invoices`

If `operation_base` is configured, fallback names are derived from the operation
path relative to that base. For example, with `operation_base: /api/rest`,
`GET /api/rest/foo` becomes `get-foo` rather than `get-api-rest-foo`.
Explicit `x-cli-name` and `operationId` values are not changed.

The exact normalization may evolve, but it must be deterministic and collision
aware.

### Aliases

Generated commands may have aliases from:

- `x-cli-aliases`
- compatibility aliases retained from v1 where useful

Alias collisions should be diagnosed rather than silently overwriting another
command.

## Hiding And Ignoring

CLI-shaping extensions should apply consistently across:

- operations
- paths
- parameters where applicable

That means `x-cli-ignore` and `x-cli-hidden` are not operation-only concepts in
the design, even if the current implementation still needs to catch up.

## Parameter Mapping

Parameters come from multiple OpenAPI scopes:

- path-item parameters
- operation-level parameters

The generator must merge these scopes according to OpenAPI rules before building
the command interface.

The merge key is `(in, name)`. Operation-level parameters override path-item
parameters with the same key, while same-named parameters in different
locations remain distinct.

### Positional Arguments

Required path parameters are positional arguments in path order.

Required query, header, and cookie parameters are also positional arguments,
after path parameters, in merged spec order. This keeps command-line flags and
options optional.

Parameter-level `x-cli-position` may assign a one-based slot to a required
argument. Unpositioned required arguments fill the remaining slots in the
default order. Non-integer, duplicate, or out-of-range positions and positions
on optional or authentication-supplied parameters fail generation for that
operation. Positioning changes only the command interface, not the parameter's
OpenAPI location or wire name.

Global and per-API `openapi_extension_aliases` config may map the recognized
`x-cli-position` semantic to an alternate OpenAPI extension name. Per-API
aliases override global aliases. The mapping changes which extension Restish
reads and does not inject or rewrite OpenAPI parameter metadata.

### Flags

Optional query, header, and cookie parameters become flags.

Parameters without a schema fall back to string handling. The original OpenAPI
parameter name is preserved on the wire even when the CLI-facing argument or
flag name is normalized or changed by `x-cli-name`.

Generated parameter flags must not shadow Cobra help flags, Restish global
flags, or generated-command local control flags such as `--help-all` and
`--rsh-generate-body`. Reserved-name collisions are disambiguated with the
parameter location, for example query parameter `help` becomes `--query-help`.
Duplicate normalized parameter names should keep readable wire-name hints:
`foo-bar` may remain `--foo-bar`, while `foo_bar` becomes
`--foo-underscore-bar`.

OpenAPI says header parameters named `Accept`, `Content-Type`, or
`Authorization` are ignored. Generated commands follow that rule so content
negotiation, request body encoding, and auth remain controlled by Restish
flags, profiles, and security handling instead of being modeled as ordinary
operation parameters.

### Missing Path Parameters

If the path template references `{petId}` but the operation does not declare a
matching path parameter after scope merge, generation should fail for that
operation with a diagnostic. Leaving the literal template token in the URL is
not acceptable.

### Serialization

Generated commands honor OpenAPI parameter serialization rules where practical:

- query `form`, `spaceDelimited`, `pipeDelimited`, `deepObject`, and
  `allowReserved`;
- path `simple`, `label`, and `matrix`;
- header `simple`;
- cookie `form`;
- JSON parameter `content`.

The generated CLI and `restish-mcp` must share the same location/style/explode
serializer for non-`content` parameters. Generated commands still own CLI input
parsing, shorthand object parsing, and JSON parameter `content` handling, while
MCP still owns model-facing value validation and may reject shapes outside its
supported subset before calling the shared serializer.

Query values are encoded byte-by-byte with RFC 3986 percent encoding. When
`allowReserved` is true, literal OpenAPI reserved characters may remain
unescaped, except `+` is still encoded as `%2B` so it cannot be confused with a
space by form-style decoders. Literal percent sequences in user input remain
literal input and are percent-encoded rather than treated as pre-escaped bytes.

Unsupported styles should warn or be visible in help and then use the closest
safe default for the parameter location. Required path substitutions must never
be corrupted by fallback behavior.

## Request Body Mapping

If the operation supports a request body, the generated command uses the same
body-construction model as the generic HTTP commands:

- shorthand positional assignments
- stdin merge or replacement
- content-type-aware encoding

Generated commands should not invent a separate body grammar.

Generated command request-body media support follows the content registry. JSON
and structured `+json` are preferred when no explicit content type is selected.
Form URL encoding, multipart forms, and raw binary bodies are also first-class
OpenAPI request-body modes when the operation advertises them. Multipart parts
may carry per-part content types from OpenAPI `encoding.contentType`, and array
file fields are encoded as repeated parts with the same field name.

Generated command body shorthand is schema-agnostic. A generated command and a
generic HTTP command must interpret the same body shorthand the same way:
`id: 123` is a number, while `id: "123"` is a string. OpenAPI schema metadata
can describe the expected shape in help and examples, but it must not silently
rewrite request-body values before they are sent. Unknown fields are still
accepted unless the user explicitly opts in to local validation.

OpenAPI schema constructs such as `oneOf`, `anyOf`, `allOf`, nullable/type
arrays, enum, const, defaults, examples, read-only/write-only, additional
properties, and recursive references are used for help and bounded example
generation. They are not full request validators by default.

Generated operations may expose optional request-body validation through
`--rsh-validate`. Validation runs after body assembly, applies only to JSON
request bodies, reports field paths and schema expectations, and never coerces
input. The server remains the source of truth, so validation is not enabled by
default and generic HTTP commands remain schema-agnostic.

Generated operation commands also expose `--rsh-generate-body` for request-body
operations. The flag prints an example body derived from OpenAPI examples,
schema examples/defaults/enums, or bounded schema placeholders, then exits
before request execution. This keeps body generation explicit: users can save
the example, edit it, and pass it back through the normal request-body path.

## Server Resolution

The operation URL is not just `api base URL + path`. The design must account
for OpenAPI `servers` definitions at:

- document level
- path level
- operation level

The generator or request planner must honor those server blocks and merge them
with API registration rules such as `operation_base` or profile base URL
overrides.

In v2 config, `operation_base` is an absolute path prefix such as `/` or `/v1`,
not a full URL. The request planner resolves that path against `base_url` with
the same URL-reference behavior v1 used. For example, `base_url:
https://example.com/my-api/v2-beta1` plus `operation_base: /` makes operation
`/my-op` request `https://example.com/my-op`, not
`https://example.com/my-api/v2-beta1/my-op`. Relative paths such as `v1` and
full URLs are rejected.

When an OpenAPI server URL resolves outside the configured `base_url` path but
stays on the same origin, generated commands preserve API-profile selection by
expressing the operation path as a deliberate relative escape from `base_url`.
For example, `base_url: https://example.com/root` plus `servers:
[{url: /v1}]` generates an operation path equivalent to `../v1/<operation>`.
The short-name URL expansion cleans that path before execution, so the request
goes to `https://example.com/v1/<operation>` while still using the configured
API profile. If no OpenAPI server matches the configured origin, Restish falls
back to the configured API base URL.

OpenAPI server URL variables are resolved with one bounded value per variable.
Restish first uses explicit local config values from API-level
`server_variables`, then profile-level `server_variables` overrides, and finally
the OpenAPI variable `default`. Server variable `enum` values are advisory for
Restish: an out-of-enum local value warns because the spec and config disagree,
but the configured value is still used. A locally configured variable that is
not declared by any applicable OpenAPI server remains an error when the spec
declares server variables, because it cannot change URL expansion. Server
variables are never expanded into every possible URL. This is a security
boundary: an untrusted spec must not be able to allocate a Cartesian product
during command generation or silently redirect authenticated requests to another
origin.

Relative OpenAPI server URLs are resolved against the configured API
`base_url`. Absolute server URLs are used only when they match the configured
origin; otherwise Restish falls back to the configured base URL to avoid
spec-driven credential exfiltration.

## Help And Discoverability

Generated commands should feel like ordinary Cobra commands:

- summary and description come from the spec or CLI extensions
- examples may be surfaced when available
- required args and flags are visible in help
- shell completion works for generated commands too

This is why generated commands are registered into the normal root tree instead
of living behind a special sub-interpreter.

Generated operation help is operation-focused by default: it shows the operation
usage, local parameters, response media and header names, schemas, schema
constraints, and examples without repeating every inherited Restish flag.
Schema constraints shown in help include enum/default/example information and
bounded numeric annotations such as minimum, maximum, exclusive bounds, and
multiple-of constraints where present. `--help-all` on a generated operation
shows the full Cobra help, including global request, output, auth, TLS,
pagination, cache, and config flags.

Generated API root help should stay scannable even when the OpenAPI
`info.description` is README-length. Ordinary `restish <api> --help` uses a
bounded prefix of a very long root description plus a note that points to
`--help-all`. `restish <api> --help-all` shows the full provider description so
users can still access the original spec text from the CLI.

## Name-Collision Policy

Collisions can happen between:

- two generated operations
- generated commands and built-ins
- generated API short names and built-in or hidden compatibility commands
- generated commands and plugin commands

The design rule is:

- built-ins win over everything else
- hidden compatibility commands are reserved like public built-ins
- removed commands such as `flags` are not reserved
- duplicate generated names are reported
- skipped commands should produce warnings or errors during generation

Silent shadowing is not acceptable.

## Example

Given:

```jsonc
{
  "apis": {
    "petstore": {
      "base_url": "https://api.example.com"
    }
  }
}
```

and:

```yaml
paths:
  /pets/{petId}:
    parameters:
      - name: tenant
        in: header
        required: true
        schema:
          type: string
    get:
      operationId: getPet
      summary: Get a pet
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: string
        - name: include
          in: query
          schema:
            type: string
      x-cli-name: pet
```

Restish should generate a command roughly shaped like:

```bash
restish petstore pet <pet-id> acme --include owner
```

which resolves through the normal request pipeline to:

```text
GET https://api.example.com/pets/<pet-id>?include=owner
tenant: acme
```

## Startup Versus Execution

Generated command registration at startup uses cached or local spec data only.
Live network fetching belongs to explicit management commands, not routine root
tree construction.

For large OpenAPI documents, startup should prefer the cached operation model
from design 006 and avoid parser work. Benchmarks should cover cold extraction
and cached startup/execution with at least a 1,000-operation synthetic spec so
scripted shell loops remain practical.

## Compatibility

Because generated commands are one of the most visible v1-to-v2 behaviors, the
generator should actively restore low-cost v1 compatibility where it does not
conflict with safety or architecture, including:

- fallback naming without `operationId`
- useful aliases
- honoring `servers[]`
- preserving required-parameter semantics

## Alternatives Considered

### Generic HTTP Commands Only

Too weak; it gives up a major product advantage.

### Ahead-Of-Time Code Generation

Too heavy for the normal operator workflow.

### Separate Generated-Command Mode

Would make help, completion, and discovery less coherent.

## Relationship To Other Designs

- Design 006 defines the source spec/loading model.
- Design 034 defines the detailed OpenAPI feature and regression-test matrix.
- Design 008 defines request-body shorthand used by generated commands.
- Design 017 defines command resolution and completion expectations.
- Design 029 defines how generated commands enter the shared request pipeline.
- Design 031 defines compatibility expectations for user-visible naming changes.
- Design 037 defines the accepted top-level command surface that generated API
  names must not collide with.
