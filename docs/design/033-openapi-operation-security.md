# OpenAPI Operation Security

## Status

Accepted for the first Restish v2 release. This record owns the product and
configuration model for OpenAPI operation security; design 034 carries the
compact OpenAPI implementation matrix.

## Problem

OpenAPI security is operation-specific. A document can define several security
schemes, set a default security policy at the document level, override it per
operation, offer alternative requirements, combine schemes, and express OAuth
scopes.

Restish profiles currently describe how to attach credentials, but they do not
model which operation credential requirement an individual credential satisfies.
OpenAPI security schemes are the main producer of those requirements, but they
should not become the whole Restish configuration vocabulary. The current model
is good enough for APIs with one auth scheme applied everywhere. It becomes poor
UX for APIs where some operations use user OAuth, some use admin OAuth, some use
an API key, and some explicitly remove global auth. Users are forced to create
several profiles for the same environment and remember which profile matches
which endpoint.

The core UX problem is that profiles are doing two jobs:

- selecting the request context, such as environment, base URL, headers, TLS,
  and the account/persona the user intends to use;
- selecting a generated operation's credential requirement.

Those should be separate. A profile should remain the user's selected trust and
environment boundary, while generated commands choose the correct configured
credential inside that profile according to normalized operation requirements.

## Goals

- Keep the one-auth-scheme case easy to configure and easy to read.
- Support multiple generated-operation credential requirements inside one
  Restish profile.
- Preserve explicit profile selection as the user's consent boundary; do not
  silently switch profiles based on operation metadata.
- Keep `security: []` as a reliable no-auth default unless the user explicitly
  asks Restish to send a configured credential.
- Implement OpenAPI's OR and AND security semantics closely enough for correct
  request planning and useful diagnostics.
- Represent OAuth scopes, OpenAPI roles, and similar requirement values in
  operation metadata, setup UX, help, diagnostics, and local profile coverage
  checks.
- Keep startup command registration offline. Operation security matching must
  use cached operation metadata and local config.
- Allow a credential-owning auth plugin to satisfy exact operation security
  requirements without persisting a synthetic credential binding.
- Keep `x-cli-config` useful as setup guidance without letting it replace
  standard OpenAPI `securitySchemes` and operation `security` policy.
- Keep the Restish config model generic enough that another loader could produce
  the same credential-requirement metadata without pretending to be OpenAPI.

## Non-Goals

- Full OAuth token introspection across providers.
- Validating remote authorization server policy at command generation time.
- Inventing auth behavior that cannot be traced to config, profile selection, or
  the OpenAPI document.
- Automatically selecting another profile because that profile satisfies an
  operation.
- Repeating operation-level security policy inside `x-cli-config`.

## Current Behavior And Constraints

Restish request auth is profile-driven. Generic requests and generated commands
select a configured API/profile, then attach auth through the shared request
pipeline.

Generated commands always know their source operation and apply that
operation's normalized security policy. Generic URL requests first resolve the
selected API/profile from the API short name or URL. When the final method and
URL path unambiguously match cached operation metadata for that API/profile,
the generic request applies the same operation security policy. If the cache is
missing or the route match is ambiguous, the request falls back to ordinary
profile auth behavior.

Generated operations carry `NoAuth` when the OpenAPI operation declares
`security: []`. In that case request execution suppresses profile auth for that
operation by default, including matching generic URL requests. An explicit
`--rsh-auth` override is a deliberate user request and may still send a
configured credential.

An operation with no effective OpenAPI security requirement is different from
`security: []`. When the OpenAPI document does not declare operation auth and
there is no inherited top-level security requirement, Restish treats that as
"no declared requirement", not as a command to strip user-configured API/profile
auth. If the user configured auth for the selected API profile, the request may
still send it. This favors explicit user configuration over incomplete OpenAPI
metadata. Users can use `security: []` in the spec to suppress configured auth
by default, while `--rsh-auth` remains the explicit escape hatch when they know
auth should still be sent.

`api connect` can read standard OpenAPI security schemes and the custom
`x-cli-config` extension to pre-populate a profile. The current extension shape
handles the single-scheme case well, but it does not express several credentials
bound to several credential requirement IDs within one profile.

Startup command registration must stay offline. Any security matching must use
the generated operation cache, local config, and already-loaded plugin metadata.
It must not contact OAuth issuers, fetch specs, or prompt the user during shell
startup.

When configured credentials cannot satisfy an operation, Restish may ask
installed `auth-resolver` plugins about each complete security alternative in
OpenAPI order. A resolver receives the final rewritten request target and exact
requirements. It may claim the alternative but cannot edit it. More than one
claim is rejected as ambiguous. A selected plugin receives the same
requirements in its `auth` hook. Explicit configured credentials remain higher
priority, and public `security: []` operations never enter resolver selection.
Authentication selected through a resolver is request-bound: Restish invokes
the selected plugin again before each transport retry so proof-of-possession
headers are never replayed across network attempts.

Inspection remains offline and side-effect free. When no configured credential
can be inspected and an applicable resolver is installed, `api auth inspect`
reports resolver evaluation as deferred until the final request method and URL
are known. It does not claim that the operation is callable, and it does not
recommend a specific static credential before the resolver is evaluated. It may
retain a generic `auth add` fallback for a resolver that declines. Configured
credential errors remain authoritative and are not hidden by deferred resolver
status.

## OpenAPI Security Model

Restish should preserve the OpenAPI model in the neutral operation metadata.

Example OpenAPI document:

```yaml
openapi: 3.1.0
info:
  title: Example API
  version: 1.0.0

components:
  securitySchemes:
    UserOAuth:
      type: oauth2
      flows:
        authorizationCode:
          authorizationUrl: https://auth.example.com/oauth/authorize
          tokenUrl: https://auth.example.com/oauth/token
          scopes:
            items:read: Read items
            items:write: Create and update items

    AdminOAuth:
      type: oauth2
      flows:
        authorizationCode:
          authorizationUrl: https://auth.example.com/admin/oauth/authorize
          tokenUrl: https://auth.example.com/admin/oauth/token
          scopes:
            admin:read: Read administrative resources

    PartnerKey:
      type: apiKey
      in: header
      name: X-Partner-Key

security:
  - UserOAuth: [items:read]

paths:
  /items:
    get:
      operationId: listItems
      responses:
        "200":
          description: OK

  /admin/users:
    get:
      operationId: adminUsers
      security:
        - AdminOAuth: [admin:read]
      responses:
        "200":
          description: OK

  /reports/partner:
    get:
      operationId: partnerReport
      security:
        - UserOAuth: [items:read]
        - PartnerKey: []
      responses:
        "200":
          description: OK

  /reports/signed:
    get:
      operationId: signedReport
      security:
        - UserOAuth: [items:read]
          PartnerKey: []
      responses:
        "200":
          description: OK

  /status:
    get:
      operationId: status
      security: []
      responses:
        "200":
          description: OK
```

The required semantics are:

- `components.securitySchemes` defines named schemes.
- top-level `security` is the default for operations;
- operation-level `security` replaces the top-level default;
- an omitted operation `security` inherits the document default;
- each requirement object inside `security` is an AND of schemes;
- the array of requirement objects is an OR of alternatives;
- `security: []` means no auth, even when document-level auth exists.

For the example above:

- `listItems` requires `UserOAuth` with `items:read`;
- `adminUsers` requires `AdminOAuth` with `admin:read`;
- `partnerReport` accepts either `UserOAuth` with `items:read` or
  `PartnerKey`;
- `signedReport` requires both `UserOAuth` with `items:read` and `PartnerKey`;
- `status` sends no auth.

## Restish Credential Requirement Model

OpenAPI should be normalized at load time into a Restish-native credential
requirement model. Generated command execution should not need to retain parser
objects or reason directly about every OpenAPI security-scheme detail.

Conceptually, the neutral model is:

```go
type CredentialRequirement struct {
	ID       string   // stable local ID, usually the OpenAPI scheme name
	Ref      string   // canonical source ref or URI, when available
	Kind     string   // oauth2, api-key, http-basic, http-bearer, openid, mtls, unknown
	Needs    []string // scopes, roles, or other named requirement values
	Source   string   // openapi, plugin, or another loader identity
	External bool     // true when the source used a URI-style scheme reference
}

type CredentialAlternative []CredentialRequirement
```

Each operation then has:

- `NoAuth`, for explicit forced no-auth;
- `OptionalAuth`, for anonymous access that can also accept credentials;
- `CredentialAlternatives`, the OR-list of AND requirements.

The OpenAPI loader owns:

- reading `components.securitySchemes` or URI security-scheme references;
- applying document-level and operation-level `security`;
- turning OpenAPI's OR/AND structure into `CredentialAlternatives`;
- translating security-scheme types into generic `Kind` values;
- translating OAuth scopes, OpenID Connect scopes, and non-OAuth role arrays
  into `Needs`;
- preserving source names and refs for diagnostics.

The core request pipeline owns:

- matching selected profile credentials to normalized requirements;
- matching OpenAPI `mutualTLS` requirements to the resolved TLS transport
  identity from flags or profile TLS settings;
- deciding which auth handlers to run;
- suppressing auth for forced no-auth operations;
- prompting and writing local config during setup;
- redaction, token storage, plugin boundaries, and request execution.

This split keeps OpenAPI as the primary producer of generated command auth
requirements without making Restish config a mirror of the OpenAPI object model.
The config still uses OpenAPI scheme names by default because those are stable
and visible to users, but the matching engine should treat them as credential
requirement IDs rather than as raw OpenAPI concepts.

`mutualTLS` is the exception to token-like credential binding. The OpenAPI
scheme still appears as a credential requirement so operations can express it in
OR/AND alternatives, but satisfying it uses the transport configuration:
`--rsh-client-cert`/`--rsh-client-key`, profile `client_cert`/`client_key`, or a
profile/flag TLS signer. It does not create a prompt-backed API key or bearer
credential.

## Config Model

Profiles should stay the environment and consent boundary. A profile may still
hold simple profile-level auth for generic requests and simple APIs, but complex
generated commands need credential bindings.

Add profile-level `credentials`, keyed by stable credential requirement ID. For
OpenAPI-generated commands this ID normally defaults to the security scheme
name:

```jsonc
{
  "auth_profiles": {
    "work-user-oauth": {
      "type": "oauth-authorization-code",
      "params": {
        "client_id": "env:USER_CLIENT_ID",
        "token_url": "https://auth.example.com/oauth/token",
        "authorize_url": "https://auth.example.com/oauth/authorize",
        "scopes": "items:read items:write"
      }
    },
    "work-admin-oauth": {
      "type": "oauth-authorization-code",
      "params": {
        "client_id": "env:ADMIN_CLIENT_ID",
        "token_url": "https://auth.example.com/admin/oauth/token",
        "authorize_url": "https://auth.example.com/admin/oauth/authorize",
        "scopes": "admin:read"
      }
    }
  },
  "apis": {
    "example": {
      "base_url": "https://api.example.com",
      "profiles": {
        "prod": {
          "credentials": {
            "UserOAuth": {
              "auth_ref": "work-user-oauth",
              "satisfies": ["items:read", "items:write"]
            },
            "AdminOAuth": {
              "auth_ref": "work-admin-oauth",
              "satisfies": ["admin:read"]
            },
            "PartnerKey": {
              "auth": {
                "type": "api-key",
                "params": {
                  "in": "header",
                  "name": "X-Partner-Key",
                  "value": "env:PARTNER_KEY"
                }
              }
            }
          }
        }
      }
    }
  }
}
```

Each binding may contain:

- `auth`, an inline `AuthConfig`;
- `auth_ref`, a reference to top-level `auth_profiles`;
- `satisfies`, the local declaration of requirement values this binding is
  intended to satisfy.

Inline `auth` and `auth_ref` remain mutually exclusive. `satisfies` is a local
coverage declaration used for matching and diagnostics. For OAuth and OpenID
Connect it normally contains scopes. For non-OAuth schemes it can contain roles
or other requirement values from the source description. OAuth auth handlers may
also need provider request parameters such as `params.scopes`, `audience`, or
`resource`; those remain auth-handler inputs, not the matching model.

Profile-level `auth` and `auth_ref` remain valid for:

- generic URL requests;
- hand-authored configs for simple APIs;
- compatibility with existing v2 config;
- generated commands when the OpenAPI policy can be unambiguously mapped to the
  profile-level auth, as described below.

### First-Class API Key Auth

API keys should become first-class auth configs instead of only being persisted
as headers or query parameters:

```jsonc
{
  "type": "api-key",
  "params": {
    "in": "query",
    "name": "api_key",
    "value": "env:API_KEY"
  }
}
```

Header and query storage remains useful as a low-level profile feature, but it
cannot reliably answer "which credential requirement does this satisfy?"
First-class API-key auth improves matching, no-auth suppression, redaction,
diagnostics, and setup.

## Request-Time Behavior

Generated commands and unambiguous cached-operation URL matches should evaluate
the operation security policy before auth callbacks are attached.

The algorithm is:

1. If the operation has explicit `security: []` and no explicit auth override,
   send no auth. Do not run built-in auth, auth hooks, API-key auth, or profile
   credential headers/query that are known to be sensitive.
2. Resolve the effective credential alternatives from operation metadata.
3. If there is no effective security policy, use ordinary selected-profile auth
   behavior for compatibility with generic requests and specs that omit auth.
4. For each non-empty credential alternative in source order, check whether the
   selected profile satisfies every requirement in that alternative.
5. Pick the first satisfied alternative unless the user supplied an explicit
   auth override.
6. Attach only the auth bindings required by the selected alternative.
7. If no credential alternative is satisfied but anonymous access is allowed,
   send the request without auth.
8. If no alternative is satisfied and anonymous access is not allowed, fail
   before sending the request.

For generic URL requests, operation matching is intentionally offline and
conservative: Restish uses cached operation metadata, matches by HTTP method and
path template, prefers more specific static paths over templates, and applies
operation auth only when one route wins. This keeps manual URL calls consistent
with generated commands without fetching specs or guessing between overlapping
templates at request time.

A binding satisfies a requirement when:

- the selected profile has `credentials.<requirement-id>`;
- or the profile has legacy/profile-level auth and the operation policy has one
  effective requirement that can be unambiguously mapped to it;
- and required `Needs` values are covered by the binding's declared
  `satisfies` values.

The conservative v2 rule for requirement values should be: if an operation
requires scopes, roles, or other `Needs` values and the candidate binding does
not declare matching `satisfies` values, fail before the request with an
actionable diagnostic. This avoids treating an unknown token or key as
authorized. Users can declare broad local coverage intentionally when they know
their provider credential contains the needed scopes or roles.

OpenAPI-derived setup should seed `satisfies` from the credential Restish is
actually configured to request. If a user accepts default OAuth scopes, those
defaults can become the binding's `satisfies` values. If the user supplies a
narrower `params.scopes` value, `satisfies` must reflect that narrower value so
coverage and generated-command preflight checks do not overstate what the
credential can satisfy.

Diagnostics must name the operation, selected profile, required alternatives,
configured credential IDs, and missing credentials or requirement values. They
must not print tokens, API keys, command output from secret sources, or raw
Authorization headers.

Example diagnostic:

```text
operation example admin-users requires one of:
  - AdminOAuth scopes: admin:read

profile prod provides:
  - UserOAuth satisfies: items:read, items:write
  - PartnerKey

Configure AdminOAuth for this profile or choose another profile.
Run: restish --rsh-profile prod api auth add example AdminOAuth
```

## Explicit Security Override

Some operations offer alternatives where the user may want a specific credential.
Generated commands should support a global override such as:

```bash
restish --rsh-profile prod example partner-report --rsh-auth PartnerKey
restish --rsh-profile prod example signed-report --rsh-auth UserOAuth+PartnerKey
```

The override selects one credential alternative by requirement ID set. Restish
should still verify that the selected profile satisfies that alternative. The
override should warn when the requested credential set is not one of the
operation's declared OpenAPI security alternatives, but it may still send the
explicitly requested configured credential set. This is intentional: OpenAPI
documents can be incomplete or stale, and a user who explicitly chooses an auth
credential may know something the source document does not capture. Restish
should preserve that escape hatch while making the mismatch visible.

The same escape-hatch model applies when an operation references an undeclared
security scheme. Restish should surface the malformed OpenAPI metadata during
connect/sync/readiness workflows and in generated-command auth errors, but a
user who knows which credential to send can configure it and select it with
`--rsh-auth <credential-id>`.

This override escape hatch applies even when the operation explicitly declares
`security: []`: the empty security array suppresses configured auth by default,
but it does not overrule an explicit user request to send a configured
credential.

OpenAPI's empty requirement object has different semantics from `security: []`.
For Restish:

- `security: []` means no auth by default;
- `security: [{}]` means anonymous-only access;
- `security: [{}, {UserOAuth: [read]}]` means optional auth.

When optional auth is available, Restish should prefer a satisfied non-empty
credential alternative over anonymous access. Anonymous should be the fallback,
not the first choice merely because `{}` appears first in the source document.

## `api connect` UX

The common case should remain compact. If an API has one effective supported
credential requirement, `api connect` should prompt much like it does today:

```text
Discovered Example API
Base URL: https://api.example.com
OpenAPI:  https://api.example.com/openapi.json

Auth:
  UserOAuth  oauth2 authorizationCode  needs items:read  used by 48 operations

Client ID: abc123
Satisfies [items:read items:write]:

Configured API "example" profile "default".
```

Restish may write the explicit `credentials` form even for one requirement. It
may also continue accepting shorter profile-level auth for hand-authored simple
configs.

For multiple requirements, setup should show the credential inventory and
coverage instead of forcing profile juggling:

```text
Discovered Example API
Base URL: https://api.example.com
OpenAPI:  https://api.example.com/openapi.json

This API declares 3 credential requirements:

  UserOAuth    oauth2 authorizationCode       needs items:read   42 operations, global default
  AdminOAuth   oauth2 authorizationCode       needs admin:read    6 operations
  PartnerKey   apiKey header X-Partner-Key                       3 operations

Configure auth for profile "prod".

Configure UserOAuth? [Y/n] y
Client ID: user-client
Satisfies [items:read items:write]:

Configure AdminOAuth? [y/N] y
Client ID: admin-client
Satisfies [admin:read]:

Configure PartnerKey? [y/N] n

Configured API "example" profile "prod".

Auth coverage for profile "prod":
  configured: UserOAuth, AdminOAuth
  skipped:    PartnerKey
  callable:   48/51 secured operations
```

Users should not be forced to configure every credential up front. Missing auth
should become actionable when needed:

```text
operation example partner-report requires:
  - PartnerKey

profile prod does not configure PartnerKey.
Run: restish --rsh-profile prod api auth add example PartnerKey
```

Add focused auth-management commands so users can update auth without rerunning
the full API registration flow:

```bash
restish --rsh-profile prod api auth inspect example
restish --rsh-profile prod api auth add example PartnerKey
restish --rsh-profile prod api auth remove example AdminOAuth
restish --rsh-profile prod api auth inspect example --operation signedReport
```

`api auth inspect` is the canonical auth debugging surface. Bare inspect should
answer "what can this profile call?" by showing configured credentials, missing
credentials, declared `satisfies` values, and operation coverage. If operations
reference a security requirement that is not declared in
`components.securitySchemes`, the command should report that as an OpenAPI
metadata issue next to the affected credential ID instead of silently presenting
the operation as a normal missing-auth case.

`api auth inspect` also replaces the v1/v2-draft header-only inspect behavior.
It should inspect the credential or operation auth selected from the active API
profile without sending the full application request. Unlike the old narrow
behavior, it must handle credentials that do not produce an `Authorization`
header, such as API keys in headers or query parameters, and combined
requirements that attach several credentials.

Default inspect output is human-oriented and shows computed auth values because
the user explicitly asked to inspect auth material. If the profile has several
configured credentials, bare inspect shows each of them; `--credential`
narrows the output when the user wants one credential:

```text
Credential: UserOAuth
Authorization: Bearer user-token

Credential: PartnerKey
X-Partner-Key: partner-key
```

For combined operation auth:

```text
Operation: signedReport
Credentials: UserOAuth, PartnerKey
Authorization: Bearer user-token
X-Partner-Key: partner-key
```

For shareable diagnostics, `api auth inspect` offers explicit redaction:

```bash
restish api auth inspect example --credential UserOAuth --redact
```

For script compatibility with the old narrow behavior and for curl
composition, `api auth get` prints one selected computed auth fragment:

```bash
restish api auth get example UserOAuth
restish api auth get example PartnerKey
restish api auth get example --operation signedReport
```

Header auth prints as `Name: value`; query auth prints as `?name=value`. If
the selected auth material does not collapse to exactly one header or query
fragment, the command fails and points users to `api auth inspect`.

Ambient request and response output remains redacted by default; auth inspection
is different because it is an explicit request to show the final auth material.

The old API-or-URI, Authorization-header-only inspect behavior should be
removed for v2 rather than kept as an alias. This is a release-window break
that keeps auth viewing, setup, and mutation under one API-auth command family.

For noninteractive setup, extend the existing preanswer/set-expression grammar
to address credential bindings:

```bash
restish --rsh-profile prod api connect example https://api.example.com \
  credentials.UserOAuth.auth.params.client_id: env:USER_CLIENT_ID \
  credentials.AdminOAuth.auth.params.client_id: env:ADMIN_CLIENT_ID \
  credentials.PartnerKey.auth.params.value: env:PARTNER_KEY
```

The exact grammar should follow existing `api connect` and `api set` behavior,
but values land under the selected profile's `credentials`.

When `api connect` is rerun for an existing API, local profiles are treated as
user-owned state. By default, Restish should refresh discovery/spec metadata and
may add newly discovered profile names, but it must not overwrite a profile that
already exists in local config. Users who want to recreate profile setup from
OpenAPI and `x-cli-config` hints must opt in with `--replace`.

This preservation boundary is intentionally profile-scoped. Profiles may contain
credentials or auth references that are hard to recover once overwritten.
API-level fields such as `base_url`, `spec_url`, `operation_base`, pagination,
server variables, `allowed_operation_origins`, and retry settings are not
protected by the profile-preservation rule. Reconnect may rebuild them from the
new connect run, and sync may refresh spec-derived metadata such as a newly
discovered `spec_url` or operation-server origins. Users should manage durable
local overrides with `api set`, `config edit`, or explicit setup expressions.

## `x-cli-config`

Standard OpenAPI remains the source of truth for auth schemes and operation
requirements:

- `components.securitySchemes` defines available scheme names and types;
- document and operation `security` define when each scheme is needed.

`x-cli-config` should only describe how Restish should configure local
credentials for those standard scheme names or normalized requirement IDs. It
should not restate per-operation security policy.

Restish must continue accepting every `x-cli-config` shape it already supports.
API providers have published specs that Restish users consume directly, and
Restish does not control those documents. New `x-cli-config` fields are
additive. They may improve multi-credential setup, but v2 must not require API
providers to update existing extension documents.

The compatibility contract includes:

- legacy top-level `x-cli-config.security`;
- legacy top-level `x-cli-config.headers`;
- legacy top-level `x-cli-config.prompt`;
- legacy top-level `x-cli-config.params`;
- profile-level `security`;
- profile-level `auth`;
- profile-level `headers`;
- profile-level `query`;
- profile-level `prompt`;
- profile-level `params`.

Internally, Restish may normalize those legacy shapes into `credentials`, but
the external extension format and its existing meaning remain valid.

New profile configuration may add a `credentials` map:

```yaml
x-cli-config:
  profiles:
    prod:
      credentials:
        UserOAuth:
          auth:
            type: oauth-authorization-code
            params:
              client_id: "{client_id}"
              scopes: "items:read items:write"
          satisfies:
            - items:read
            - items:write
          prompt:
            client_id:
              description: OAuth client ID

        AdminOAuth:
          auth:
            type: oauth-authorization-code
            params:
              client_id: "{admin_client_id}"
              scopes: "admin:read"
          satisfies:
            - admin:read
          prompt:
            admin_client_id:
              description: Admin OAuth client ID

        PartnerKey:
          auth:
            type: api-key
            params:
              in: header
              name: X-Partner-Key
              value: "{partner_key}"
          prompt:
            partner_key:
              description: Partner API key
```

API-level generated-command defaults remain outside profiles:

```yaml
x-cli-config:
  command_layout: tags
  profiles:
    default:
      credentials: {}
```

`command_layout` accepts `flat` or `tags`; other values are ignored with a
warning so a bad hint never blocks `api connect`. `api connect` applies it as a remote
default, while an existing non-empty local value remains operator-owned unless
the user reconnects with `--replace`. `api sync` does not overwrite it.

Existing single-scheme shapes remain supported:

```yaml
x-cli-config:
  profiles:
    default:
      security: UserOAuth
      auth:
        type: oauth-authorization-code
        params:
          client_id: "{client_id}"
      prompt:
        client_id:
          description: OAuth client ID
```

Restish may normalize that internally to:

```yaml
x-cli-config:
  profiles:
    default:
      credentials:
        UserOAuth:
          auth:
            type: oauth-authorization-code
            params:
              client_id: "{client_id}"
          prompt:
            client_id:
              description: OAuth client ID
```

When `x-cli-config` is absent, Restish should derive reasonable setup defaults
from standard OpenAPI security schemes:

- `http` `basic` becomes `http-basic`;
- `http` `bearer` should either become a first-class bearer/API-token auth type
  or a documented header auth fallback;
- `oauth2` authorization-code and client-credentials flows become existing
  OAuth auth types with endpoint params;
- `apiKey` header/query/cookie becomes first-class `api-key` auth when the
  location is supported;
- unsupported schemes appear in setup and coverage diagnostics as unsupported.

## Future OpenAPI 3.2 Support

OpenAPI 3.2 adds security features that are worth supporting directly, and they
reinforce the neutral credential-requirement model. These are future-looking
requirements for v2 rather than release-blocking scope for the first v2
implementation pass. The v2 design should avoid choices that make these
features hard to add later.

When implemented, OAuth2 Device Authorization flow should map to Restish
`oauth-device-code`:

```yaml
components:
  securitySchemes:
    DeviceOAuth:
      type: oauth2
      flows:
        deviceAuthorization:
          deviceAuthorizationUrl: https://auth.example.com/device
          tokenUrl: https://auth.example.com/token
          scopes:
            read: Read data
```

OAuth2 security schemes can also provide `oauth2MetadataUrl`, which points at
authorization server metadata. A future implementation should accept this as
discovery input and map it to either a dedicated auth param or the existing
issuer/metadata discovery path, after applying the same HTTPS and endpoint
validation rules as other OAuth discovery inputs.

Security Scheme Objects can be marked `deprecated: true`. Future setup and
inspection flows should show deprecated schemes but avoid selecting them by
default when a non-deprecated alternative is available.

OpenAPI 3.2 also allows security schemes to be referenced by URI rather than
only by component name. Internally, Restish should preserve both a display ID and
a canonical ref/URI when this support is added. Config keys should stay
human-friendly when possible, and commands such as `api auth add` should handle
URI-backed requirements so users do not need to hand-type awkward JSON paths.

Security Requirement arrays are not OAuth-only. For `oauth2` and
`openIdConnect`, array values are scopes. For other security-scheme types, the
array can contain role names or other required values. Restish should normalize
all of these into `CredentialRequirement.Needs` and match them against
credential binding `satisfies` values.

OpenAPI 4.0/Moonwalk work is not concrete enough to target directly, but the
direction argues for keeping Restish's generated-operation auth model neutral:
loaders produce credential alternatives, and the core request pipeline matches
those alternatives against selected profile credentials. If a future OpenAPI
version changes how auth is organized, only the loader normalization layer should
need significant changes.

## Compatibility And Migration

The current `security: []` default behavior is retained, with `--rsh-auth`
available as an explicit override.

Existing profile-level `auth` and `auth_ref` remain valid. For generated
commands:

- if an operation has explicit `security: []`, no auth is sent unless the user
  passes an explicit `--rsh-auth` credential override;
- if an API has exactly one effective supported credential requirement,
  profile-level auth may satisfy that requirement for compatibility;
- if a profile was created by `api connect` from a known OpenAPI scheme or
  normalized requirement ID, migration or reconfiguration should write the
  corresponding `credentials` binding;
- if an operation requires a credential and the selected profile has no
  unambiguous binding, Restish should fail before the request with an actionable
  diagnostic.

The old API-or-URI, Authorization-header-only inspect behavior is removed in
v2. Its replacements are `restish api auth inspect <api>` for diagnostics and
`restish api auth get <api> [credential-id]` for scripts that need one
curl-friendly auth fragment. This is an intentional v2 break because
operation-specific, non-Authorization, and query-based credentials make a
header-only command misleading.

Existing `x-cli-config` documents are not a breaking-change surface for v2. If a
legacy extension maps cleanly to one credential requirement, Restish should keep
today's setup UX and write equivalent local config. If a legacy extension is
ambiguous in a multi-credential API, Restish should still configure the legacy
or default credential where possible and explain remaining coverage gaps, rather
than rejecting the API registration.

This is an intentional v2 breaking change for ambiguous multi-scheme APIs. The
previous behavior of sending whatever profile auth happened to be selected for
every non-public generated operation hides policy mismatches and encourages
users to create profile workarounds.

Config validation must reject unknown fields, mutually exclusive `auth` and
`auth_ref` within a binding, malformed `satisfies` lists, and unsupported
API-key locations.

## Security And Failure Modes

No-auth operations must never run auth plugins or attach persistent profile
credentials.

Restish must not auto-select another profile. Profile selection is the user's
explicit choice of environment and credential boundary.

Diagnostics may print credential IDs, source scheme names, scheme types, API-key
parameter names, and requirement values such as scopes or roles. They must not
print tokens, passwords, API-key values, resolved secret-source values,
external-tool stdout, or raw Authorization headers.

An untrusted spec must not be able to force Restish to send credentials to a
different origin. Server-origin checks and operation URL planning remain the
request pipeline's responsibility.

If a spec declares a malicious or misleading scheme name, Restish may use that
name for matching and diagnostics, but it must not resolve secrets or execute
external tools until the selected profile contains an explicit binding for that
scheme.

## Testing Plan

- document-level default security applies to operations that omit `security`;
- operation security overrides document defaults;
- operation `security: []` suppresses built-in auth, auth hooks, profile auth,
  and sensitive credential headers/query unless the user passes an explicit
  auth override;
- alternative requirements accept any satisfied alternative in spec order;
- combined requirements require every credential requirement in the alternative;
- explicit `--rsh-auth` selects a declared alternative, or warns and sends a
  configured credential set that the user explicitly requested outside the
  operation's declared security metadata;
- OAuth scopes and non-OAuth requirement values appear in diagnostics and are
  checked against binding `satisfies` declarations;
- missing requirement values fail before request;
- profile-level auth satisfies the only effective credential requirement for
  compatibility;
- ambiguous profile-level auth does not satisfy multi-credential operations;
- generic URL requests apply operation security only for unambiguous cached
  method/path matches and otherwise use ordinary profile auth;
- first-class API-key auth supports header and query locations and redaction;
- optional anonymous auth prefers configured credentials before anonymous
  fallback;
- cached operation metadata preserves effective security policy for offline
  startup;
- `api connect` derives single-scheme setup from OpenAPI;
- `api connect` prompts and writes `credentials` for multi-credential APIs;
- `x-cli-config` single-scheme legacy shape normalizes to `credentials`;
- `x-cli-config.credentials` drives prompts and written config without
  redefining operation policy;
- `api auth add/remove` preserve comments and validate config paths;
- `api auth inspect` handles profile-level auth, credential-specific auth,
  operation-selected auth, non-Authorization credentials, combined credentials,
  operation coverage, readiness diagnostics, and explicit redaction;
- `api auth get` emits exactly one curl-friendly header or query fragment and
  points users to `api auth inspect` when selected auth material is ambiguous;
- the old API-or-URI, Authorization-header-only `api auth inspect` behavior is
  removed.

Future OpenAPI 3.2 coverage, not required for the first v2 implementation pass:

- device authorization maps to `oauth-device-code`;
- `oauth2MetadataUrl` participates in OAuth setup;
- deprecated security schemes are shown but de-prioritized during setup;
- URI-backed security scheme references remain matchable and diagnosable.

## Documentation Impact

Update user docs before release:

- authentication guide: explain profile-level auth versus credential-specific auth;
- OpenAPI CLI integration guide: document OpenAPI security support, alternatives,
  combined requirements, requirement values, optional anonymous auth, and
  `security: []`;
- config reference: add `profiles.<name>.credentials`, binding fields, and
  first-class `api-key` auth;
- profile reference: explain profiles as environment/consent boundary rather
  than per-endpoint auth selection;
- API management reference: document `api auth inspect/add/remove` and
  configure behavior;
- migration notes: document removal of the old header-only inspect behavior and
  the equivalent `api auth get <api>` flow;
- troubleshooting guide: add missing credential/requirement diagnostics.

## Decision

Restish v2 should implement credential bindings inside a selected profile,
rather than requiring users to create one profile per OpenAPI security scheme or
auto-switching profiles. Standard OpenAPI remains authoritative for operation
requirements when OpenAPI is the source, but the loader should normalize those
requirements into Restish credential alternatives before request planning.
`x-cli-config` remains an optional setup hint that maps standard scheme names or
normalized requirement IDs to Restish credential prompts and auth configs.
