# Generic Operation Browsing

Status: proposed.

## Problem

Restish can connect an OpenAPI-described API and expose generated commands, but
large APIs may intentionally hide routine resource operations from that command
tree with `x-cli-hidden`. Shell completion also omits hidden operations. The
generic HTTP commands can still call those resources, but users have no
command-shaped way to discover which registered paths are available.

Hypermedia links do not solve this problem. A link identifies a relation and
URI, but it does not communicate the HTTP method, operation summary,
`operationId`, or a Restish command that can be copied and completed.

## Goals

- Let a generic HTTP verb browse matching OpenAPI operations below a registered
  API path.
- Show command templates rather than bare URLs.
- Include `x-cli-hidden` operations because this browser is independent of the
  generated command surface.
- Preserve exact HTTP requests, request bodies, direct URLs, and APIs without
  cached OpenAPI metadata.
- Use the active profile when resolving operation paths.
- Keep browsing local and deterministic; it must not refresh a remote spec.

## Non-Goals

- Do not add another generated-command tree.
- Do not change shell completion visibility.
- Do not browse operations for arbitrary, unregistered URLs.
- Do not include operations removed with `x-cli-ignore`.
- Do not infer request bodies, query values, or optional parameter values.
- Do not replace `links`, which remains response-driven hypermedia navigation.

## User Workflow

After connecting an API, a user can browse all GET operations:

```text
restish get demo/
```

They can narrow the path and preserve concrete path values:

```text
restish get demo/users/42/
```

Example human output:

```text
COMMAND                                      SUMMARY       OPERATION ID
restish get demo/users/42/sessions           List sessions listUserSessions
restish get demo/users/42/sessions/{id}      Get session   getUserSession
```

The command value is a command template. Concrete values already present in
the browsed prefix remain concrete; unresolved OpenAPI path parameters remain
in `{parameter}` form for the user to replace.

The selected verb filters operations:

```text
restish post demo/
restish patch demo/users/42/
restish delete demo/users/42/
```

## Dispatch Rules

Browsing applies only when all of these conditions are true:

1. The invocation uses an explicit generic HTTP verb.
2. The first argument starts with a registered API short name.
3. The request has no body from positional arguments, stdin, or an internal
   body override.
4. Cached operation metadata is available without remote discovery.
5. No operation for that method exactly matches the requested path, or the
   short-name path has a trailing slash that explicitly requests parent
   browsing.
6. One or more operations for that method are descendants of the requested
   path.

When all conditions hold, Restish renders the matching operations and does not
send an HTTP request.

Exact operation matches without a trailing slash always execute the request,
including template matches such as `demo/users/42`. A trailing slash explicitly
requests descendants, so `demo/users` can execute a collection operation while
`demo/users/` browses operations below it. If there are no descendants, Restish
falls back to the exact request. A request with a body always executes. A direct
URL, unknown API name, missing operation cache, unmatched path, or method with
no descendants also follows the existing HTTP request path unchanged.

Query strings and fragments are not browsing prefixes. They retain normal
request behavior.

## Operation Selection

Browsing uses the same cached `OperationSet`, active profile base URL, and
`operation_base` resolution used by URL completion. It compares normalized path
segments:

- Literal OpenAPI segments must match literally.
- A path-template segment matches one concrete browsed segment.
- A descendant must contain at least one segment beyond the browsed prefix.
- A trailing slash turns an exact short-name operation into a parent browse
  only when matching descendants exist.

Results are filtered case-insensitively by HTTP method and sorted by command,
then `operationId`.

`x-cli-hidden` is deliberately ignored here. That extension controls generated
commands and shell completion, not whether generic HTTP users may discover an
operation. `x-cli-ignore` operations are absent from the `OperationSet` and
therefore remain undiscoverable.

## Output Contract

Each result contains stable fields:

```json
{
  "command": "restish get demo/users/{user-id}",
  "summary": "Get user",
  "operation_id": "getUser"
}
```

Normal Restish output selection applies. Interactive output presents a compact
table, while non-interactive output remains structured and explicit formats
such as `-o json` continue to work.

The browser writes only to stdout. It does not emit request traces because no
request is planned or sent.

## Compatibility

This is additive for parent paths backed by cached OpenAPI operations. The
precedence rules protect exact requests without a trailing slash and
body-bearing requests.
Users who intentionally need to send a request to a non-operation parent path
that has documented descendants can use the expanded full URL, which always
retains generic HTTP behavior.

## Validation

- Unit/CLI tests cover root and nested browsing, method filtering,
  `x-cli-hidden`, active profile and `operation_base`, deterministic structured
  output, exact-operation precedence, body precedence, and request fallback.
- Generic HTTP help and reference docs show the discovery workflow.
- The full unit, integration-tagged, build, generated-doc, and documentation
  checks run before the pull request is marked ready.
