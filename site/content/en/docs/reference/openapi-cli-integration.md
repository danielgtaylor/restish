---
title: OpenAPI Extensions and Generated Commands
linkTitle: OpenAPI
weight: 16
description: Reference for shaping generated Restish commands from OpenAPI documents.
aliases:
  - /docs/guides/openapi-cli-integration/
---

Restish turns OpenAPI operations into CLI commands. This reference is for API
authors and maintainers who want the generated command surface to feel natural:
stable names, useful help, predictable parameters, auth setup, and documented
Restish extensions.

## Minimum Good Operation

```yaml
paths:
  /items/{item-id}:
    get:
      operationId: getItem
      summary: Get one item
      parameters:
        - name: item-id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: Item
```

This can become:

```bash
restish myapi get-item alpha
```

## Discoverability

Publish the spec at a predictable location such as `/openapi.json`, or expose a
`service-desc` or `describedby` link from the API root. Verify with:

```bash
restish api connect example api.rest.sh 'prompt.api_key: docs-key'
restish example --help
```

## Naming

Restish prefers stable operation IDs. Use extensions when the generated name
would not be good CLI vocabulary:

```yaml
x-cli-name: list-items
x-cli-aliases: [items]
x-cli-description: List items with optional filtering.
```

Operation-level `x-cli-name` replaces the generated command name.
`x-cli-aliases` adds command aliases. `x-cli-description` replaces the help
summary/description shown for the generated command.

When an operation has no `operationId`, Restish falls back to the HTTP method
and path. If the API config has `operation_base`, that base path is removed from
fallback names. For example, `operation_base: /api/rest` turns
`GET /api/rest/foo` into `get-foo`.

Parameters support their own command-shaping extensions:

```yaml
parameters:
  - name: item-id
    in: path
    required: true
    schema:
      type: string
    x-cli-name: item
    x-cli-description: Item identifier
  - name: debug
    in: query
    schema:
      type: boolean
    x-cli-hidden: true
```

Parameter-level `x-cli-name` changes the positional argument or flag name, while
the original OpenAPI parameter name is preserved on the wire.
`x-cli-description` changes generated help. Parameter-level `x-cli-ignore`
removes that parameter from the generated CLI; `x-cli-hidden` keeps it callable
but omits it from ordinary help.

## API-Backed Parameter Completion

Static OpenAPI `enum` values are completed locally. For values managed by the
API, a parameter can opt into a read-only completion provider:

```yaml
paths:
  /projects:
    get:
      operationId: listProjects
      parameters:
        - name: account
          in: query
          required: true
          schema: {type: string}
      responses:
        "200": {description: OK}

  /accounts/{account}/servers:
    get:
      operationId: listServers
      parameters:
        - name: account
          in: path
          required: true
          schema: {type: string}
        - name: project
          in: query
          schema: {type: string}
          x-cli-completion:
            operation_id: listProjects
            bindings:
              query.account: path.account
            value_path: body.items.id
            description_path: body.items.name
      responses:
        "200": {description: OK}
```

`operation_id` names an existing operation in the same synced operation set. It
must be a
`GET` or `HEAD` operation without a request body. Bindings map a provider
parameter to an already supplied parameter on the current command. Parameter
references use `<location>.<original-name>`, where location is `path`, `query`,
`header`, or `cookie`.

`value_path` and optional `description_path` are bounded, dot-separated field
paths against the normalized response roots `body`, `headers`, `headers_all`,
`status`, and `proto`. Field projection across response arrays is automatic.
Values must resolve to a scalar or list of scalars. Descriptions, when present,
must resolve to the same number of items.

Pressing `Tab` may make one authenticated provider request. Restish applies the
active profile, TLS settings, URL overrides, operation-origin policy, parameter
serialization, and built-in non-interactive authentication. It does not retry,
paginate, prompt, start local auth or TLS plugins, or run command-based secret
sources. Only built-in response codecs run. The request has a two-second
deadline and a 1 MiB response limit.
Candidates are sanitized and capped at 100. Unauthenticated candidate sets are
cached for 30 seconds in the private response-cache directory; authenticated
sets are not persisted. `--rsh-no-cache` bypasses this cache. Any failure returns
no candidates. A static `enum` always takes precedence and stays offline.

## Hide Or Ignore Operations

```yaml
x-cli-hidden: true
x-cli-ignore: true
x-mcp-ignore: true
```

`x-cli-ignore` and `x-cli-hidden` may be placed on an operation or a path item.
Hidden operations remain callable by exact name when supported. Ignored
operations are left out of the generated command surface. `x-mcp-ignore`
excludes an operation from MCP tool exposure.

## Inspect Extension Effects

When `api connect` or `api sync` sees behavior-changing `x-cli-*` extensions,
Restish prints a compact summary such as renamed operations, aliases,
hidden/ignored operations, hidden/ignored parameters, and `x-cli-config`.
This is informational only; Restish does not block connection or prompt for
extension approval.

For a detailed view after connecting, run:

```bash
restish doctor api myapi
```

The doctor report lists where behavior-changing `x-cli-*` extensions appear
and what each one changes. Plain `x-cli-description` help text overrides are
not reported because they do not change which commands or inputs are available.

## Query Parameter Serialization

Restish maps required parameters to positional arguments and optional
parameters to flags. Required path parameters appear first, followed by required
query, header, and cookie parameters in spec order:

```yaml
parameters:
  - name: item-id
    in: path
    required: true
    schema:
      type: string
  - name: account
    in: query
    required: true
    schema:
      type: string
  - name: page
    in: query
    schema:
      type: integer
```

This becomes:

```bash
restish myapi get-item alpha acct-123 --page 2
```

Path-level parameters are merged into each operation under that path, and an
operation-level parameter with the same `in` and `name` overrides the path-level
one. Parameters with the same name but different locations, such as a path
`id` and a query `id`, stay separate. Parameters without a schema are treated
as strings.

The generated flag name may be normalized for the shell, but Restish preserves
the original OpenAPI parameter name on the wire. That matters for parameters
such as `$select`, `$filter`, dotted vendor names, and case-sensitive headers.

Model repeatable optional flags as arrays:

```yaml
parameters:
  - name: tag
    in: query
    schema:
      type: array
      items:
        type: string
```

Then users can repeat the flag:

```bash
restish myapi list-items --tag red --tag sale
```

Restish supports the common OpenAPI parameter styles used by generated
commands:

- query: `form`, `spaceDelimited`, `pipeDelimited`, and `deepObject`
- path: `simple`, `label`, and `matrix`
- header: `simple`
- cookie: `form`
- JSON parameter `content` for `application/json` and `+json` media types

For query parameters with `allowReserved: true`, literal reserved URL
characters are kept unescaped in the generated query value where safe. Literal
`+` is still encoded as `%2B`, and literal `%` is encoded as `%25`, so plus
signs and pre-escaped-looking input are not confused with spaces or decoded
twice.

OpenAPI header parameters named `Accept`, `Content-Type`, or `Authorization`
are ignored by generated commands. Configure response negotiation, request body
content type, and authentication through Restish flags, profiles, or OpenAPI
security schemes instead.

Unsupported styles are not silently ignored. Generated command help calls them
out so the API author can choose a supported style or the Restish implementation
can grow deliberately.

## Request Bodies

Generated commands use the normal Restish body syntax. JSON and `+json` media
types use shorthand assignments, form bodies use form encoding, multipart
bodies can include fields, repeated file-array fields, and
`encoding.contentType` per-part metadata, and opaque body media types such as
`application/octet-stream`, XML, and NDJSON send raw string or file input:

```bash
restish myapi upload-item 'name: alice, file: @photo.jpg'
restish myapi put-blob @payload.bin
restish myapi webdav-operation @propfind.xml
restish myapi insert-json-line @events.ndjson
```

Generated help prefers `@file` for whole-body file input because it keeps the
body source attached to the command invocation. Redirected stdin works too, but
`@file` is easier to copy, review, and combine with required operation
arguments and flags.

OpenAPI allows request bodies on methods such as `GET` and `DELETE`. Restish
will send them when the spec defines a request body and the user supplies body
arguments, but many servers, proxies, and caches treat those requests
inconsistently. Prefer body-bearing methods such as `POST`, `PUT`, or `PATCH`
when you control the API design.

## Schemas And Generated Bodies

Restish uses OpenAPI schemas for help, completions, and example generation.
Generated command request bodies use the same shorthand semantics as generic
HTTP requests. For example, `id: 123` sends a number and `id: "123"` sends a
string, even when the OpenAPI schema says the field is a string.

Schemas are not full request validators by default. Restish trusts explicit
input and lets the server validate API semantics. Unknown body fields are
allowed, enum mismatches are not rejected locally, and schema constraints are
not enforced unless you opt in with `--rsh-validate` on a generated command.
That validation runs after body assembly, only for JSON request bodies, and
does not rewrite or coerce values. Schema constructs such as `oneOf`, `anyOf`,
`allOf`, `nullable`, `enum`, `const`, defaults, examples, numeric constraints,
read-only/write-only fields, additional properties, and recursive references
are used for help, bounded example bodies, and optional local validation.

Restish still fails locally when it cannot build a coherent request, such as a
missing required path argument, an unreadable `@file`, or an invalid Restish
flag. Those checks protect the CLI invocation itself rather than enforcing the
remote API's schema.

Use `--rsh-generate-body` on a generated command to print an example body and
exit:

```bash
restish myapi create-item --rsh-generate-body
```

Generated operation help also shows response media types and response header
names when the spec defines them. That makes pagination headers, rate-limit
headers, and empty-body responses visible without reading the raw OpenAPI
document.

## Server URLs

Restish honors OpenAPI `servers` at document, path, and operation level.
Document-level servers define the generated operation path shape, while the
configured `base_url` provides the request origin. Operation-level servers win
over path-level servers, and both are treated as explicit operation routing.
Server variables use local `server_variables` config values when provided, then
OpenAPI defaults:

```jsonc
{
  "apis": {
    "myapi": {
      "base_url": "https://api.vendor.test/root",
      "server_variables": {
        "version": "v2"
      }
    }
  }
}
```

Relative server URLs resolve against `base_url`. A server such as `v2` with the
base URL above sends generated operation requests under
`https://api.vendor.test/root/v2`.

If a document-level server is absolute and points at another origin, Restish
keeps its path prefix but uses the configured `base_url` origin. For example,
`base_url: https://staging.vendor.test` with a document server
`https://api.vendor.test/v1` sends generated requests under
`https://staging.vendor.test/v1`.

If a configured server variable value is outside the OpenAPI `enum`, Restish
warns and uses the configured value. Local config represents operator intent,
and specs are sometimes stale. If a configured variable is not declared by any
applicable OpenAPI server and the spec does declare server variables, Restish
fails because the value cannot affect URL expansion.

Path- or operation-level absolute server URLs on another origin are blocked
unless the API config lists the origin in `allowed_operation_origins`, or a
matching `url_overrides` entry rewrites that URL before the request is sent.
Without that opt-in, generated commands fail with an `allowed_operation_origins`
hint instead of silently sending profile credentials to another host.

Same-origin absolute server URLs are used when scheme, hostname, and effective
port all match.
If a same-origin server points outside the configured base path, Restish keeps
the selected API profile and resolves the generated operation to that path. If
no OpenAPI server matches the configured origin, Restish falls back to
`base_url` instead of sending configured credentials to another host.

Use `url_overrides` when a resolved URL should be rewritten for a profile, such
as sending an upload operation to a local test service. Source and destination
entries must be absolute `http` or `https` URL prefixes without userinfo, query,
or fragment parts. The longest matching source prefix wins, and profile-level
entries override or extend API-level entries:

```jsonc
{
  "apis": {
    "myapi": {
      "base_url": "https://api.vendor.test",
      "url_overrides": {
        "https://upload.vendor.test/": "http://localhost:8080/"
      }
    }
  }
}
```

The rewrite runs after Restish has selected the generated operation URL, so it
also covers path- or operation-level OpenAPI servers. It also runs for generic
API-aware requests such as `restish myapi/items`.

Use `operation_base` when the API registration needs an explicit request-path
override independent of the OpenAPI document:

```jsonc
{
  "apis": {
    "myapi": {
      "base_url": "https://api.vendor.test/root",
      "operation_base": "/"
    }
  }
}
```

With that config, generated operation paths are resolved from
`https://api.vendor.test/`.

## External References

Restish resolves external OpenAPI `$ref` documents during `api sync` and local
spec loading. Local `spec_files` may reference nearby files or full `file://`
URIs. Downloaded specs may reference same-origin HTTP(S) documents. Cross-origin
remote refs are blocked unless cross-origin spec discovery is explicitly
enabled and the target passes Restish's trust checks.

Generated operation metadata is cached after sync, so generated commands can
start from the operation cache without refetching secondary reference files.

## Auth Setup Hints

Prefer standard OpenAPI security schemes first. Restish derives basic auth,
API keys, and supported OAuth setup from the spec. Use `x-cli-config` only for
Restish-specific prompting and defaults.

Document-level `x-cli-config` pre-populates API profiles during `api connect`:

```yaml
x-cli-config:
  command_layout: tags
  profiles:
    default:
      headers:
        - "X-Client: restish"
      query:
        - "trace=docs"
      prompt:
        api_key:
          description: API key
          example: sk_live_...
      credentials:
        PartnerKey:
          auth:
            type: api-key
            params:
              in: header
              name: X-Partner-Key
              value: "{api_key}"
          satisfies: ["items:read"]
```

`command_layout` is an API-level default and accepts `flat` or `tags`; any
other value is ignored with a warning. An
existing non-empty local value is preserved when reconnecting; use `--replace`
to reapply the document default.

Supported profile fields are `headers`, `query`, `auth`, `credentials`,
`security`, `params`, and `prompt`. Credential entries support `auth`,
`auth_ref`, `satisfies`, `prompt`, and `params`. Prompt entries support
`description`, `example`, `default`, `enum`, and `exclude`. `exclude` keeps the
prompt answer out of auth params while still allowing template expansion in
headers, params, or explicit auth params.

Legacy top-level `x-cli-config` fields `security`, `headers`, `prompt`, and
`params` are normalized into the `default` profile. Server-provided
`x-cli-config` cannot configure `external-tool` auth; Restish skips that auth
type because a remote spec must not cause a local executable to run.

Generated commands honor operation-level security:

- `security: []` is public and sends no configured auth.
- A single effective requirement can use profile-level `auth` for compatibility.
- Multiple alternatives or combined requirements use
  `profiles.<name>.credentials.<credential-id>` bindings.
- `mutualTLS` requirements are satisfied by TLS settings, not prompt-backed
  credentials. Use `--rsh-client-cert` with `--rsh-client-key`, profile
  `client_cert`/`client_key`, or a profile/flag TLS signer.
- `--rsh-auth PartnerKey` or `--rsh-auth UserOAuth+PartnerKey` selects
  one allowed alternative for an operation.

OpenAPI scope and role arrays are matched against credential `satisfies` values.
When a required binding is missing, Restish fails before sending the request.

Never put secrets in the OpenAPI document. Use prompts, environment references,
or external tools.

## Command Layout

Flat layout is the default. Tag layout can help large APIs when the tags are
stable and useful. Set it locally:

```bash
restish api set myapi 'command_layout: tags'
```

API providers can also select the initial layout with
`x-cli-config.command_layout`.

Keep tags short and user-facing if you expect them to become command groups.

## Related Pages

- [Connect to an API](/docs/getting-started/connect-to-an-api/)
- [API Setup and Discovery](/docs/guides/api-setup-and-discovery/)
- [API Management](/docs/reference/api-management/)
- [MCP](/docs/plugins/mcp/)
