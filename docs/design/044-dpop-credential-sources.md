# DPoP Credentials And Token Sources

## Status

Accepted for the v2 development line. The design separates proof-capable
request authentication from provider-specific token acquisition.

## Problem

An RFC 9449 DPoP credential is not a static header. The client must retain one
asymmetric key, obtain an access token bound to that key, and create a unique
proof for every protected-resource request. Some authorization servers expose
ordinary OAuth token and refresh endpoints. Other deployments broker token
issuance through a local identity service that must authenticate the caller and
relay a proof to another authorization server.

The existing `auth` and `auth-resolver` plugin hooks let a plugin mutate every
target request. That makes the plugin, rather than Restish, the owner of the
target key, token cache, retry behavior, and final request authentication. It
also prevents `api auth add`, `inspect`, and `logout` from representing the real
credential lifecycle.

## Decision

Restish owns every configured target DPoP credential:

- the P-256 private key and its persistent cache entry;
- the current access token, token type, expiry, Resource indicator, and scopes;
- the `Authorization: DPoP` header and fresh proof on each target request;
- forced reacquisition after a rejected token;
- deletion through the existing auth-cache lifecycle.

Provider-specific code may implement the narrow `credential-source` plugin
hook. A source never receives the DPoP private key and never mutates the target
request. Token acquisition is a two-message operation:

1. `describe` resolves an opaque source reference to a proof method and URI,
   Resource indicator, and authorized scopes.
2. Restish creates a proof for that exact method and URI and sends `issue` with
   only the opaque reference and signed proof. The source returns an OAuth-like
   short-lived credential response.

The source response is transported only over the bounded plugin CBOR channel.
Restish never prints token material. A manifest must explicitly declare the
`credential-source` hook and the additive `auth.dpop_credential_source` and
`auth.dpop_operation_scopes` required features.

The built-in auth type is `dpop`. Its required configuration parameters are:

- `source`: the manifest name of one installed credential-source plugin;
- `reference`: an opaque, non-secret provider reference.

`restish api auth add <api> <credential-id> --source <plugin>
--reference <reference>` creates the named OpenAPI credential binding. The
credential ID remains the local binding to an OpenAPI Security Requirement; it
is not a token or server-side grant identifier.

For every `describe` and `issue` call, Restish sends the exact scopes required
by the matched OpenAPI operation. Sources may use that request-scoped set to
select or acquire an offer without persisting scopes in the credential binding.

Restish maps both `type: http`, `scheme: DPoP` and OAuth 2.0/OpenID Connect
security schemes marked `x-dpop-required: true` to this auth type. The latter
preserves the provider's OAuth/OIDC discovery metadata while declaring that a
plain bearer credential is not acceptable.

## Security Invariants

- Only Restish persists the target private key.
- Plugins receive public metadata and signed proofs, never private key bytes.
- `describe` output is validated before signing: the proof URI is absolute,
  contains no user information or fragment, and uses HTTPS except for loopback
  HTTP development targets.
- `issue` must return `token_type=DPoP`, a non-empty token, a future expiry, the
  same Resource indicator, and scopes covering the current operation.
- Cache identity includes the API, profile, credential binding, source, and
  reference. A cached entry whose source metadata differs is not reused.
- Credential acquisition is serialized per cache identity. Provider network
  or interactive work does not hold the shared token-cache file lock, so an
  acquisition waiting on one identity does not block unrelated credentials.
- Restish sends a token only when the final target has the same origin as its
  Resource indicator and falls on that indicator's path boundary.
- Every protected-resource request gets a new proof bound to its actual method,
  URI without query or fragment, and access-token hash.
- A Resource Server `DPoP-Nonce` challenge reuses the still-valid token and key
  but retries once with the nonce bound into a fresh proof.
- An authorization-server `use_dpop_nonce` challenge is returned by the source
  as a structured `dpop-nonce` result. Restish retries credential issuance once
  with the same key and a fresh proof, and persists a nonce returned with a
  successful credential for the next issuance request.
- Cross-origin redirects strip both the authorization and DPoP headers.
  Transport retries and same-origin redirects regenerate the proof for the
  exact next request instead of replaying it.
- Token values, proofs, and private keys are treated as secret fields by
  inspection, plugin diagnostics, and error redaction.

## Compatibility

The new hook and auth type are additive. Operation scopes are an optional wire
field and plugins that require them declare `auth.dpop_operation_scopes`.
Existing bearer, OAuth, external-tool, auth hook, and auth-resolver behavior
remains available. Providers migrate by
returning a safe source receipt from their authorization workflow and declaring
`credential-source`; once all of their target APIs use explicit `dpop`
credential bindings they should stop claiming those target operations through
`auth-resolver` and stop mutating them through `auth`.

The first implementation does not change a provider's HTTP credential-offer
contract. A provider adapter may continue redeeming its existing opaque offer
internally. Standard OAuth DPoP acquisition can later implement the same
inward token-source contract without a plugin.

## Rejected Alternatives

- **Pass a raw token to `auth add`:** exposes credentials through argv, shell
  history, process inspection, or agent transcripts and cannot establish key
  binding safely.
- **Let the plugin keep signing target requests:** preserves the ownership
  problem and leaves Restish auth state inaccurate.
- **Put a provider-specific offer format in Restish core:** couples the CLI to
  one identity service and is unsuitable for upstream.
- **Give the plugin the target private key:** collapses the custody boundary and
  makes provider code capable of impersonating every target request.

## Verification

Behavioral proof must cover:

- P-256 key creation, persistence, and reuse across access-token renewal;
- RFC 9449 proof claims and raw ES256 JWS encoding;
- source discovery, two-phase acquisition, and response validation;
- per-request target signing and forced reacquisition after rejection;
- cache separation by credential source reference;
- absence of target private keys and request mutation in the provider plugin;
- CLI creation and inspection of one explicit DPoP credential binding.
