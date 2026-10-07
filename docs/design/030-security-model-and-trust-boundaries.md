# Security Model And Trust Boundaries

## Summary

Restish is a local operator tool that reads untrusted network data, executes
trusted local plugins, stores credentials, and can be pointed at arbitrary
hosts. The design therefore needs explicit trust boundaries rather than a vague
"it's a CLI" assumption.

This document defines the safety rules that apply across the rest of the design
set.

## Threat Model

Restish is designed to defend against:

- malicious or buggy remote APIs
- malicious or surprising OpenAPI discovery endpoints
- accidental credential leakage through logs or output defaults
- stale or corrupted on-disk state
- subprocess leaks and hung plugins degrading the operator's environment

Restish is not designed to defend against:

- a malicious local user who can already read the operator's files
- a malicious plugin binary that the operator explicitly installed and allowed
- a fully compromised operating system

That distinction matters because plugins are an extension surface, not a
sandboxed execution environment.

## Trust Zones

The design assumes four trust zones:

### 1. Core Restish Process

Trusted application code running in the current process. It is responsible for
enforcing policy, propagating context, redacting logs, and deciding what
extension points plugins may use.

### 2. Local Configuration And Caches

Semi-trusted local data under the operator's control:

- `restish.json`
- token cache
- response cache
- spec cache
- plugin manifests

These files may be corrupted or stale, so they require validation and locking,
but they are not treated like hostile network input once successfully parsed.

### 3. Local Plugins

Locally installed executable code. Plugins are trusted code with limited
protocol-level authority, not untrusted content.

Restish should constrain what plugins can ask the host to do through protocol
contracts, but once a user installs a plugin, that plugin effectively has the
same local-user trust level as any other executable on the machine.

### 4. Remote Network Content

Untrusted data from:

- HTTP responses
- discovered specs
- OAuth metadata documents
- remote images and binary payloads

This zone gets the strictest validation and the narrowest defaults.

## Core Security Principles

### Secure By Default

Unsafe behavior must require an explicit user choice. Examples:

- TLS verification stays on unless `--rsh-insecure` is set
- cross-origin or private-address spec discovery is off by default
- credentials are redacted from logs
- document output should not silently dump binary data to a TTY

### Trust Explicit Operator Intent

Restish is a local operator tool, so explicit operator choices should generally
win over spec assumptions, safety heuristics, and extra ceremony. If a user
passes a flag, sets a config field, supplies a plugin source, or otherwise asks
Restish to do something directly, Restish should do it after applying the
minimum validation needed to keep behavior explainable.

This principle does not remove all prompts. Extra approval is appropriate when
dangerous behavior could happen later as a side effect of config or remote
metadata, especially when it executes local code or sends credentials. For
example, plugin install treats the source argument as the trust decision, while
external auth commands keep a one-time approval because they can run during
ordinary requests after config is loaded.

### No Silent Downgrade

When safety or correctness checks fail, Restish should error or warn clearly.
It should not silently fall back to looser behavior such as:

- using the wrong profile
- ignoring a spec build failure and pretending the API has no commands
- dropping back from refresh-token failure to a browser flow without context

### Narrow Trust Transfer

When data crosses a trust boundary, the representation should be narrow and
validated. Examples:

- plugin protocols use typed messages rather than raw shell snippets
- loaders return canonical OpenAPI content, not internal command trees
- response middleware can request specific follow-up actions, not arbitrary host
  behavior

### Project Config Trust

Restish discovers `.restish.json` from the current working directory or a parent
only when the user has not selected an explicit config with `--rsh-config` or
`RSH_CONFIG`. Discovered project config is not executed or layered until trust is
established. Interactive users can accept the first-run prompt; non-interactive
scripts must run `restish config trust` first or choose the file explicitly.

Trust is stored in user Restish state outside the repository and records the
canonical config path plus content hash. If the file changes, Restish ignores it
again until the user reviews and trusts the new content. This avoids surprise
trust transfer from a checked-out repository into normal requests.

The initial layered project-config surface is intentionally narrow: `apis` and
`theme` only. Global config remains the source for shared auth profiles,
plugins, cache settings, and writes. Project APIs are read-only through normal
mutation commands, and API-scoped caches/tokens use a project namespace outside
the repository. Explicit config selection still means the selected file is the
entire config source of truth.

Project config is allowed to be a normal committed repository file, so Unix
mode bits are not used as the safety boundary for `.restish.json`. The safety
boundary is content validation plus explicit trust: project config must not
contain inline secret values or executable secret sources. Secret-bearing auth
params must be omitted or written as `env:NAME` references, while non-secret
OAuth params such as `client_id`, `audience`, issuer URLs, token URLs, and
scopes remain valid shared project setup.

### Least Necessary Exposure

Restish should only expose sensitive data where it is required:

- stdout carries requested command output, not debug logs
- stderr diagnostics redact sensitive headers and query parameters
- explicit response filters, including `--rsh-headers` and `-f @`, are
  intentional data selection and may expose raw response headers
- plugin requests receive only the fields needed for their hook type
- cache files use restrictive permissions

Unix-like platforms enforce the permission diagnostic with mode-bit checks.
Windows ACL inspection is not implemented for the first v2 release; existing
config and token-cache files on Windows report permission status as unknown
rather than ok so users do not get a false safety signal.

## Spec Discovery Safety

Spec discovery is one of the most important remote-input attack surfaces because
it lets an API cause Restish to make more network requests.

The design requirements are:

- default allowlist of `http` and `https` only
- same-origin discovery by default
- explicit opt-in before following cross-origin links
- reject loopback, link-local, and RFC1918/private ranges by default during
  discovery hops unless the original configured base URL is itself in that
  trust class
- keep those private/local follow-target checks even when cross-origin
  discovery is explicitly enabled; direct `--spec`/`spec_url` remains the
  operator-visible escape hatch for private spec URLs
- bounded request timeout for every discovery probe
- response size limits before parsing
- cancellation through the CLI context

OpenAPI `x-cli-*` extensions are remote command-surface hints. They must not
execute local code, but they can still rename, hide, or remove generated
commands and parameters, and `x-cli-config` can pre-populate local API profile
defaults during `api connect`. Restish therefore treats them as visible remote
metadata rather than a separate trust prompt: `api connect` and `api sync`
summarize behavior-changing `x-cli-*` extension classes, while `doctor api`
reports the affected paths, operations, and parameters in more detail.

`x-cli-completion` is network-capable remote metadata. It can only reference a
declared `GET` or `HEAD` operation in the same operation set, and the existing
operation-origin policy remains authoritative. Completion requests have fixed
deadlines and response limits, never retry or paginate, and suppress errors.
They may use built-in non-interactive credentials, but do not prompt, run auth
hooks, start local auth or TLS plugins, or resolve command-based secret sources.
Only built-in response codecs and bounded field selectors run. Returned values
and descriptions are bounded and stripped of terminal control characters before
Cobra receives them.

These rules apply both to `Link`-header discovery and well-known-path probes.

## Sensitive Data Handling

Sensitive data includes at least:

- `Authorization`
- `Proxy-Authorization`
- `Cookie`
- `Set-Cookie`
- URL userinfo credentials (`https://user:password@example.com`)
- bearer tokens and API keys in query parameters
- OAuth client secrets
- refresh tokens
- PINs and passphrases

The design rules are:

- never print these values verbatim in verbose logs
- never include them verbatim in synthesized error messages from remote systems
- cap logged remote error body length
- preserve them in-memory only as long as required for the current operation
- bypass HTTP response-cache writes when a response carries credential-bearing
  headers; cached replay must not strip or alter headers

When a remote IdP returns a JSON error document, Restish should parse and
redact known token fields before surfacing the error.

## Output Safety

TTY output should not surprise users with raw binary. The output planner must
distinguish:

- structured documents
- printable text
- binary payloads
- image payloads with explicit TTY renderers

If Restish cannot safely render binary to a TTY, it should use a placeholder and
point users toward redirecting stdout.

Redirected stdout is different: when no filter, metadata shortcut, pagination
collection, or output format is set, `--rsh-print=auto` writes body bytes
directly because the user is saving or piping the response rather than asking
for a TTY presentation. This raw-download path bypasses response middleware so
installed plugins cannot silently rewrite saved files. Explicit filters,
formats, and print strings use the interpreted response path where response
middleware may modify, drop, or follow.

## Plugin Safety

Plugins are trusted local executables that run at the user's own risk. The host
still owns several safety responsibilities:

- manifest version compatibility checks
- duplicate-name detection
- bounded waits and subprocess cleanup
- clear stderr surfacing on plugin failures
- preventing plugin categories from using protocols they did not declare

Restish should not claim plugin sandboxing. The safety story is instead:

- discovery is explicit
- plugins are discovered only from the configured Restish plugin directory, not
  from `$PATH`
- allowlists can restrict which configured plugins are loaded if such a policy
  is added
- protocols are small and typed
- subprocess lifetime is bounded by host context and cleanup rules

Secret-bearing request fields require least exposure at plugin boundaries.
Headers such as `Authorization`, `Cookie`, and `Proxy-Authorization` are
redacted before hook payloads are sent to plugins unless the plugin declares a
capability that explicitly requires auth secrets.

External auth tools are also a trust transfer. Before using output from a new
external command, Restish should require an explicit approval or TOFU-style
record keyed by the command identity so a config edit does not silently start
executing a different credential helper.

## Persistence Safety

Files that store credentials or state must be protected against corruption and
concurrent writes.

That means:

- restrictive file permissions
- write-to-temp then fsync then rename
- cross-process file locking for config and token writes
- line-ending and comment preservation where possible
- versioned cache entries

When data cannot be preserved safely, Restish should refuse the mutation rather
than writing a best-effort corrupted structure.

## Cancellation And Resource Leaks

Hanging subprocesses and ignored cancellation are operational security issues as
well as UX issues. Long-lived leaked processes can:

- hold hardware token sessions open
- keep reading from TTY stdin
- pin network connections
- mask command completion

All long-lived operations therefore need:

- context-driven cancellation
- bounded waits
- close or kill fallback

## Compatibility Versus Safety

When v1 compatibility and safety conflict, safety wins unless there is a strong
operator workflow reason otherwise.

Examples:

- restoring `+json` media-type support is both compatible and safe
- restoring silent profile fallback is compatible with legacy behavior but not
  safe, so it should not return

## Review Checklist

Any new subsystem or plugin surface should answer these questions:

1. What trust zone does the input come from?
2. What validation happens before the data is used?
3. What context and timeout bounds the work?
4. What sensitive data could leak through logs or errors?
5. What resources must be closed on success, failure, or cancellation?
6. Does the failure mode silently downgrade security or correctness?

If a design cannot answer those questions, it is not ready.
