# Authentication

## Summary

Restish v2 treats authentication as profile-driven request preparation. Each API
profile may declare an auth configuration, and the request pipeline resolves
that configuration into a typed auth handler immediately before the request is
sent.

Auth is therefore:

- part of request planning, not transport magic
- selected by API and profile context
- cached and refreshed through explicit token-storage rules
- extensible through plugins without giving up built-in common flows

## Goals

- simple built-in support for common schemes
- OAuth flows that work for local, headless, and service-account scenarios
- safe token caching and refresh semantics
- prompting when secrets are intentionally omitted from config
- typed handler context rather than stringly typed request mutation

## Non-Goals

- supporting every auth standard in the core binary
- hiding auth behavior so completely that users cannot inspect or debug it
- forcing all auth through external commands or plugins

## Placement In The Pipeline

Authentication is a request-stage concern. By the time the HTTP client sends the
request, auth resolution has already happened.

That means auth sees:

- selected API
- selected profile
- auth parameters
- current request method, URL, and headers
- prompting and logging facilities
- token store
- cancellation context

This placement is important because it lets auth cooperate with:

- profile selection
- per-request overrides
- `api auth inspect`
- retries and one-shot 401 recovery

without teaching the transport layer about individual auth schemes.

## Auth Configuration Model

Auth lives under an API profile:

```jsonc
{
  "profiles": {
    "default": {
      "auth": {
        "type": "oauth-authorization-code",
        "params": {
          "issuer": "https://issuer.example.com"
        }
      }
    }
  }
}
```

The core pieces are:

- `type`
- `params`

`type` selects the handler implementation. `params` supplies handler-specific
configuration.

Unknown auth types are errors unless a matching auth plugin is available.

Shared auth can also live under top-level `auth_profiles` and be referenced
from API profiles with `auth_ref`:

```jsonc
{
  "auth_profiles": {
    "work": {
      "type": "oauth-client-credentials",
      "params": {
        "client_id": "ci",
        "client_secret": "env:RESTISH_CLIENT_SECRET",
        "issuer_url": "https://issuer.example.com"
      }
    }
  },
  "apis": {
    "one": {
      "profiles": {
        "default": {"auth_ref": "work"}
      }
    }
  }
}
```

Inline `auth` and `auth_ref` are mutually exclusive. A reference is a
source-of-truth pointer to one named auth profile, not an overlay. Named auth
profiles make the v2 config schema capable of sharing credentials and OAuth
tokens between API registrations without duplicating secret-bearing params.

## Built-In Auth Types

The built-in set should include:

- `api-key`
- `bearer`
- `http-basic`
- `oauth-client-credentials`
- `oauth-authorization-code`
- `external-tool`
- device-code flow when available as part of the OAuth family

The design explicitly leaves room for:

- auth plugins
- additional OAuth grant helpers

## Handler Contract

The long-term handler contract should be richer than "mutate request from a map
of strings." A handler should conceptually receive:

- `context.Context`
- outbound request
- typed auth configuration
- token store
- prompter
- logger
- execution mode such as "normal request", "`api auth inspect` inspection", or
  "force refresh on retry"

And it should return:

- request mutations to apply
- token-store updates if applicable
- optional retry/refresh hints

This is a design direction adopted from the review. The current implementation
may still use a narrower interface internally, but new auth work should move
toward the typed model.

## Prompting Rules

Prompting is allowed when secrets are omitted intentionally from config.

Prompting must:

- prefer `/dev/tty` or equivalent terminal access when stdin is not interactive
- honor defaults when the design says a default exists
- treat EOF carefully rather than silently accepting destructive behavior
- use the runtime-owned prompter rather than ad-hoc scanners

This keeps auth usable in both interactive shells and scripted contexts where
stdin may already be consumed by piped data.

## Token Storage

Token-bearing auth flows use a persistent token cache keyed by at least:

- API identity
- profile name
- any additional cache partition key required by the auth config

When a profile uses `auth_ref`, the token cache key is based on the named auth
profile and selected token-affecting params such as issuer, token endpoint,
client ID, scopes, audience, resource, or an explicit `cache_key` param. This
lets two APIs that reference the same named auth profile share a token when the
auth context is actually the same, while still partitioning tokens when the
named profile changes materially.

The token store must provide:

- atomic writes
- restrictive permissions
- cross-process locking
- reload behavior for long-running processes

Refresh semantics matter:

- if a refresh response omits `refresh_token`, Restish preserves the existing
  refresh token
- transient refresh failures should be surfaced clearly
- automatic fallback from refresh failure to a browser flow should only happen
  when the failure mode justifies it
- the fallback triggers on OAuth `invalid_grant` and on a small, explicit list
  of provider `error_code` values that mean the refresh token or session is
  gone (currently Supabase GoTrue's); unknown codes, network errors, and 5xx
  responses are surfaced instead

## OAuth Design

OAuth support in Restish should be modeled as one family with shared helpers
instead of several mostly independent implementations.

Common responsibilities include:

- OIDC discovery
- token endpoint communication
- auth-method selection
- token parsing and redaction
- refresh behavior
- cache integration

Shared OAuth helpers should also centralize endpoint validation, bounded
response reads, token-cache behavior, TLS client construction, and redaction.
Those rules are security boundaries, not grant-type details.

### OIDC Discovery

If the profile provides an issuer URL, Restish may resolve it through OIDC
discovery to obtain:

- authorization endpoint
- token endpoint
- device endpoint when available

Discovery must honor the security rules in design 030.

### Token Endpoint Authentication

OAuth clients should support at least:

- `client_secret_post`
- `client_secret_basic`

The auth method must be configurable because different providers require
different token-endpoint auth behavior.

Directly configured OAuth endpoint URLs must be validated before use. Restish
should reject endpoint URLs with embedded credentials, fragments, or existing
query strings, and should require HTTPS except for deliberate localhost or
loopback development flows. Discovery, token, and device-authorization response
bodies should be size-limited before parsing.

Token endpoint requests should send `Accept: application/json` and tolerate
provider-compatible token response variants where they are common in the wild.
In particular, `expires_in` may be either a JSON number or a numeric string.

OAuth HTTP clients should honor the same relevant TLS options as ordinary
requests, including custom CA roots, TLS minimum version, and explicit
insecure-skip settings.

OIDC issuer and endpoint validation is deliberately strict. Issuers and
discovered endpoints must use HTTPS, except for explicit `http://` loopback
development flows. Public `http://`, custom schemes, embedded credentials,
fragments, and preexisting query strings are rejected before any token or
authorization request is sent.

Manually configured OAuth endpoints may be path-relative when the selected API
has a `base_url`. `oauth2/token` resolves under the effective API/profile base
path, while `/oauth2/token` resolves at the same host root. Scheme-relative
values are rejected, and the resolved endpoint still goes through the same
HTTPS/loopback, credential, query, and fragment validation as absolute
endpoints. `issuer_url` remains absolute-only because it defines the OIDC
issuer identity.

Discovery and token requests inherit the CLI HTTP client's timeout and TLS
configuration. This keeps `--rsh-timeout`, CA roots, TLS minimum version, and
`--rsh-insecure` consistent across ordinary requests and auth setup. OAuth
token clients disable automatic redirects so provider errors and redirect
responses remain visible to Restish.

### Authorization Code Flow

Authorization code flow should support both:

- local-browser callback on loopback
- explicit headless/manual fallback for SSH/remote use

The local callback server must:

- validate path and state
- ignore irrelevant requests like `/favicon.ico`
- shut down cleanly on success, timeout, or cancellation
- default to the v1-compatible redirect URI with a trailing slash:
  `http://localhost:<port>/`
- allow a configured local callback path such as `/callback` when a provider
  strictly matches registered redirect URIs
- show only a neutral "authorization code received" page before token exchange,
  then show success or failure after the token endpoint response is known

Manual-code fallback must also shut down cleanly. If the callback, timeout, or
context cancellation wins the race, any goroutine waiting for manual input must
exit without leaking. The full authorization URL should only be printed when
browser launch fails or verbose mode is enabled.

Device authorization is a separate explicit OAuth type, not an automatic
fallback from authorization code. Restish should not silently switch a profile
from `oauth-authorization-code` to `oauth-device-code` just because issuer
discovery advertises a device endpoint. Flow choice affects provider client
registration, redirect URI policy, consent UX, token semantics, and debugging;
the operator should choose it in config.

Local HTTPS callbacks are supported for providers that reject HTTP redirect
URIs. The callback listener stays on loopback. `redirect_url` can select an
exact `localhost`, `.localhost`, loopback IP, or loopback-resolving hostname
and overrides the separate scheme, port, and path settings. The listener binds
every loopback address the host resolves to and succeeds if at least one binds,
so an unavailable address family (such as `::1` with IPv6 disabled) does not
break login. Manual code entry skips the hostname lookup because no listener
runs on this machine; non-loopback IP literals are still rejected. HTTPS requires
operator-supplied `redirect_cert` and `redirect_key` files. Restish does not
generate certificates, install trust roots, or broaden OAuth endpoint scheme
validation on the user's behalf.

### Client Credentials Flow

Client credentials flow should support provider-specific extra parameters such
as `audience`, `resource`, or `organization`. Restish should forward unknown
endpoint parameters rather than silently dropping them.

## Secret Sources

Auth params may use late-resolved secret sources:

- `env:NAME` reads an environment variable at request time
- `command:...` runs a local command and uses its stdout, trimmed of trailing
  newlines

Resolution happens after config loading and before the auth handler runs, so
commands such as `api inspect` do not need to print resolved secret values.
Command stderr is bounded and redacted when included in errors.

Secret commands use deterministic platform shell execution: `cmd /c` on
Windows and `/bin/sh -c` on other platforms. Restish intentionally does not use
arbitrary `$SHELL` values here because interactive shells such as fish or
Nushell do not necessarily support POSIX `-c` semantics. The command timeout is
currently a fixed 30 seconds.

## External Tool Auth

`external-tool` preserves the v1 JSON request-mutation protocol by default:
Restish sends request metadata as JSON on stdin, and the tool returns JSON
header or URI updates on stdout. A new opt-in output mode,
`params.output: bearer-token`, treats stdout as the bearer token and sets
`Authorization: Bearer <token>`.

External auth commands remain trusted local code. Restish requires command
approval, streams stderr to the user, captures a bounded stderr excerpt for
errors, and redacts common secret assignments in the returned diagnostic.
External-tool command snippets use the same deterministic shell execution as
secret commands: `cmd /c` on Windows and `/bin/sh -c` elsewhere. The default
external-tool timeout remains 30 seconds unless the auth handler is constructed
with an explicit timeout by an embedder.

OAuth token exchange for provider-specific systems should be modeled as an auth
hook/plugin pattern when the built-in flows are not enough. The core should keep
generic OAuth helpers, not vendor-specific token exchange parameters.

### Browserless OAuth

Remote and SSH users have two explicit browserless paths in v2:

- `oauth-device-code` when the provider supports device authorization or
  recommends it for CLIs; and
- `oauth-authorization-code` with `--rsh-no-browser`, which prints the
  authorization URL and prompts for a pasted authorization code.

Restish keeps these paths explicit instead of auto-detecting and switching
flows. That makes the configured auth profile match the provider registration
and keeps failures understandable when a provider supports one OAuth extension
but not another.

## 401 Retry Semantics

Token-bearing handlers may need a one-shot recovery path when a token was
technically unexpired but rejected by the server.

The design allows:

- invalidate cached credential
- force one refresh or re-auth attempt
- retry the request once

Anything beyond one controlled retry should be left to the normal retry design,
not embedded in auth handlers.

## Operation Credential Coverage

Generated operations may require credential IDs and requirement values such as
OAuth scopes. A profile credential binding may declare those values explicitly
with `satisfies`. Explicit `satisfies` always wins.

When `satisfies` is omitted and the resolved auth profile has
`params.scopes`, Restish derives the covered requirement values from that
space-delimited scope string at request-build time. This keeps shared
`auth_profiles` useful without forcing users to duplicate OAuth scopes in every
credential binding. If neither explicit `satisfies` nor derived scopes cover
the operation's requirements, the strict pre-flight error remains.

OAuth setup must not claim broader operation coverage than the configured
credential can request. If the user provides an explicit OAuth `scopes` value
during setup, generated credential bindings derive `satisfies` from that value
rather than from every scope mentioned by the OpenAPI document.

Authorization-code OAuth has two useful states: configured and usable for an
immediate request. Client ID, endpoints, and scopes mean Restish can attempt the
flow, but they are not a bearer token. Noninteractive execution must fail before
sending a secured request unless the token cache already has a non-expired
access token or a refresh-token-backed entry that can be refreshed;
interactive execution may start the browser or manual-code flow and then attach
the resulting token.

## Auth Inspection

`restish api auth inspect <api>` reuses the same auth resolution path as real
requests but stops after producing the auth mutations that would be applied.
The command lives under API management because v2 auth can be credential-specific
and operation-specific.

An auth resolver needs the final request method, URL, and complete security
alternative. When inspection does not have that final target, it reports that
resolver evaluation is deferred until request time instead of invoking the
plugin, reporting the credential as missing, or claiming the operation is
callable. Static credential configuration errors still fail inspection.

Default output is human-oriented and shows the computed auth values because the
user explicitly asked to inspect auth material. Ambient request diagnostics,
logs, plugin payloads, and config display remain redacted by default.
When several credentials are configured on the profile, bare inspect prints
each credential's computed auth material. `--redact` produces shareable
inspection output, and
`restish api auth get <api> [credential-id]` prints one curl-friendly header or
query fragment for scripts.

The v1/v2-draft header-only auth-inspection behavior is removed for v2. The
stable command keeps the `restish api auth inspect <api>` family, but it must
be credential-aware and operation-aware rather than assuming that "auth" means
one `Authorization` header.

## Security Rules

Auth behavior must follow design 030:

- redact secrets in logs
- do not print raw remote token bodies in errors
- keep secrets out of stdout unless the command explicitly asks for auth
  inspection or a selected header value
- prefer restrictive file permissions for caches

## Plugin Integration

Auth plugins are valid for schemes that do not belong in the core binary.

The plugin boundary should stay narrow:

- Restish owns request planning
- plugin returns request mutations or auth material
- token storage and prompting stay host-coordinated unless the plugin owns them
  by explicit contract

Auth plugins extend the system; they should not replace the entire auth model.

## Alternatives Considered

### Store Auth Outside Profiles

Rejected because auth usually varies with the same environment boundary as base
URLs and headers.

### Treat Auth As Static Headers Only

Too weak for OAuth, prompting, refresh, and inspection tools.

### Push All Auth Into Plugins

Too heavyweight for common API usage.

## Relationship To Other Designs

- Design 002 defines where auth config lives.
- Design 017 defines prompting and diagnostics rules.
- Design 029 defines where auth sits in the request pipeline.
- Design 030 defines secret-handling and OIDC discovery safety rules.
- Design 031 defines compatibility expectations such as restoring headless
  flows.
