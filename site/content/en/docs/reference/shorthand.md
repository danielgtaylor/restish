---
title: Shorthand
linkTitle: Shorthand
weight: 34
description: Reference for Restish shorthand request bodies, config patches, stdin patches, file loading, arrays, and scalar values.
---

Shorthand is Restish's compact structured-input language. It builds JSON-shaped
values for request bodies, patches structured stdin, and powers `config set`,
`api set`, and `edit` patch arguments.

## Quick Shape

| Shorthand | Result |
| --- | --- |
| `name: Alice` | `{"name":"Alice"}` |
| `user.name: Alice` | `{"user":{"name":"Alice"}}` |
| `tags[]: docs` | `{"tags":["docs"]}` |
| `items[0].name: first` | `{"items":[{"name":"first"}]}` |
| `base{one: 1, two.three: 3}` | `{"base":{"one":1,"two":{"three":3}}}` |

Use quotes around an entire shorthand argument when it contains spaces,
brackets, `?`, `&`, `|`, or other shell-sensitive characters:

```bash
restish post api.rest.sh/post 'user.name: Alice, tags[]: docs'
```

## Assignments

An assignment is `path: value`. Paths use dots for nested object fields and
brackets for array positions.

```bash
restish post api.rest.sh/post 'name: Alice, enabled: true'
restish post api.rest.sh/post 'user.profile.email: alice@example.com'
restish post api.rest.sh/post 'items[0].name: first'
```

Multiple assignments must be comma-separated. Restish joins positional body
arguments with spaces and parses them as one shorthand expression, so a command
like `name: Alice enabled: true` is one `name` value, not two fields. Write
`name: Alice, enabled: true` instead. When quoting shorthand for the shell,
prefer one quoted argument that contains the complete comma-separated
expression.

## Scalars

Unquoted scalar values are parsed as logical values:

| Input | Value |
| --- | --- |
| `true`, `false` | booleans |
| `null` | null |
| `undefined` | delete marker when patching |
| `3`, `12.5` | numbers |
| `Alice` | string |

Quote a value when exact text matters:

```bash
restish post api.rest.sh/post 'enabled: "true", missing: "null", blank: ""'
```

Generated API commands may coerce request-body fields back to strings when the
OpenAPI schema declares those fields as `type: string`. Generic HTTP requests
keep the shorthand parser's normal scalar types.

## Arrays

Append with `[]`:

```bash
restish post api.rest.sh/post 'tags[]: docs, tags[]: cli'
```

Set by index with `[n]`:

```bash
restish post api.rest.sh/post 'items[0].name: first, items[1].name: second'
```

For config patches, `[^n]` inserts before an array index:

```bash
restish api set example 'profiles.default.headers[^0]: "X-Debug: true"'
```

Use `undefined` to delete an object field or array item while patching an
existing structure:

```bash
restish config set 'cache.max_size: undefined'
restish api set example 'profiles.default.headers[0]: undefined'
```

## Objects And Merging

Objects merge recursively when shorthand patches an existing structured value.
Scalar values replace. Fields not mentioned in the patch remain.

```bash
echo '{"name":"Alice","role":"user"}' |
  restish post api.rest.sh/post 'role: admin'
```

The request body keeps `name` and changes `role`.

This same merge model is used by `edit`, `config set`, and `api set`.

## Move And Swap

Config and structured patch surfaces support the shorthand `^` operator for
moving or swapping values by path:

```bash
restish config set 'apis.old ^ apis.new'
restish api set example 'profiles.staging ^ profiles.prod'
```

Both sides of `api set` are interpreted under the selected API, so an API-scoped
patch cannot escape into another API or global config.

## File Loading

`@path` loads a file as a value. Structured files are parsed when possible.
Plain text files become strings.

```bash
restish post api.rest.sh/post 'payload: @payload.json'
restish post api.rest.sh/post 'note: @message.txt'
```

Use `%path` when binary bytes should become a base64 string:

```bash
restish post api.rest.sh/post 'encoded: %photo.jpg'
```

For literal values that begin with `@` or `%`, quote or otherwise force string
semantics in the shell command. Multipart bodies treat `@path` as a file part
reference at encoding time; use `@@value` in multipart input to send a literal
text value that starts with `@`.

## Hidden Secret Values

Use `--rsh-prompt-secret <path>` to fill a shorthand body path from a hidden,
single-line terminal prompt. The flag is repeatable and works with generic and
generated commands:

```bash
restish post api.rest.sh/login 'username: alice' --rsh-prompt-secret password
```

Prompted values override the same paths from stdin or shorthand. They do not
appear in shell history or process arguments, and Restish redacts them from
request-body diagnostics even when their field names are not recognizably
sensitive. Use `@path` instead when a value contains newlines.

## Stdin

With no shorthand arguments, structured stdin becomes the request body:

```bash
cat payload.json | restish post api.rest.sh/post
```

With stdin plus shorthand arguments, stdin is the base document and shorthand
patches it:

```bash
echo '{"name":"Alice","role":"user"}' |
  restish post api.rest.sh/post 'role: admin'
```

When stdin is not JSON, YAML, or shorthand-shaped structured data, Restish sends
it as text unless body arguments force a structured patch workflow.

When shorthand is used as a response query, array slices use inclusive bounds:
`body[0:2]` selects indexes `0`, `1`, and `2`.

## Request vs Config Patches

Request shorthand builds the logical request body. The selected content type
then decides whether that value becomes JSON, YAML, CBOR, form fields,
multipart parts, text, or raw bytes.

Config shorthand patches an existing `restish.json` document. It preserves
comments where possible and validates the final config before writing.

| Surface | Root |
| --- | --- |
| `restish post <url> ...` | request body |
| `restish edit <url> ...` | fetched resource body |
| `restish config set ...` | full config object |
| `restish api set <name> ...` | `apis.<name>` |

## Related Pages

- [Input and Shorthand](/docs/guides/input/)
- [Query Syntax](../query-syntax/)
- [Config](../config/)
- [Content Types](../content-types/)
