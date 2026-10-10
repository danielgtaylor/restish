---
title: Authentication
linkTitle: Authentication
weight: 20
description: Configure Restish auth with profiles, generated API setup, safe auth fixtures, OAuth, and external tools.
aliases:
  - /docs/recipes/send-an-api-key-in-a-header/
  - /docs/recipes/send-an-api-key-in-a-query-parameter/
  - /docs/recipes/inspect-the-auth-header/
---

Auth belongs with the request context. In Restish that usually means a profile,
so tokens, API keys, and environment-specific credentials do not get copied into
every command.

## Safe Auth Fixtures

The example API has endpoints that require auth and return only a safe summary.
They are useful for learning and testing.

### Bearer Token

{{< restish-example >}}
restish -H 'Authorization: Bearer docs-token' api.rest.sh/auth/bearer --rsh-print b
{{< /restish-example >}}

Representative output:

```text
authenticated: true
scheme: "bearer"
subject: "docs-token"
```

### API Key Header

{{< restish-example >}}
restish -H 'X-API-Key: docs-key' api.rest.sh/auth/api-key-header --rsh-print b
{{< /restish-example >}}

### API Key Query Param

{{< restish-example >}}
restish 'api.rest.sh/auth/api-key-query?api_key=docs-key' --rsh-print b
{{< /restish-example >}}

### Basic Auth

{{< restish-example >}}
restish -H 'Authorization: Basic YWxpY2U6c2VjcmV0' api.rest.sh/auth/basic --rsh-print b
{{< /restish-example >}}

For real APIs, put these values in a profile or use an auth method instead of
repeating headers in shell history.

## Configure Auth In A Profile

Use `api set` to create or update profile auth without opening the config file:

```bash
restish api set example \
  'profiles.token.auth: {type: bearer, params: {token: env:RESTISH_DOCS_TOKEN}}'
```

That writes the same shape you would see in `restish.json`:

```jsonc
{
  "apis": {
    "example": {
      "base_url": "https://api.rest.sh",
      "profiles": {
        "token": {
          "auth": {
            "type": "bearer",
            "params": {
              "token": "env:RESTISH_DOCS_TOKEN"
            }
          }
        }
      }
    }
  }
}
```

Then call the API:

```bash
RESTISH_DOCS_TOKEN=docs-token restish -p token example get-echo
```

## Prompt-Driven Setup

When an OpenAPI spec declares security schemes, `api connect` can prompt for
values or accept preanswers:

```bash
restish api connect example api.rest.sh 'prompt.api_key: env:DOCS_API_KEY'
```

Generated OpenAPI commands use the operation's effective `security` policy.
Generic URL requests use the same policy when their method and URL path
unambiguously match one cached operation for the selected API/profile. For one
global scheme, profile-level `auth` remains enough. For APIs with several
schemes, configure named credential bindings under the active profile:

```jsonc
{
  "profiles": {
    "default": {
      "credentials": {
        "UserOAuth": {
          "auth_ref": "work-user-oauth",
          "satisfies": ["items:read"]
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
```

`security: []` operations are public and suppress profile auth, auth hooks, and
sensitive credential headers/query values, including for matching generic URL
requests. Optional anonymous alternatives use configured credentials when
available and anonymous access as the fallback.

For an API that defines several allowed auth alternatives, choose one with
`--rsh-auth`:

```bash
restish myapi partner-report --rsh-auth PartnerKey
restish myapi signed-report --rsh-auth UserOAuth+PartnerKey
```

By default, generated commands only use credentials declared by the operation's
OpenAPI security requirements. If a provider accepts a configured credential
that its spec forgot to list, `--rsh-auth <credential-id>` can explicitly select
that credential; Restish warns that the override is outside the operation's
declared requirements.

These commands use placeholder operation names because the public example API
does not expose a multi-scheme partner-auth fixture.

## DPoP Credential Sources

When an OpenAPI security scheme uses `type: http` with `scheme: DPoP`, or an
OAuth 2.0/OpenID Connect scheme declares `x-dpop-required: true`, Restish can
own the proof key and token lifecycle while an installed plugin supplies
provider-specific token acquisition. First obtain an opaque source reference
from that provider, then bind it to the exact OpenAPI credential ID:

```bash
restish api auth add myapi ResourceDPoP \
  --source provider-plugin \
  --reference https://identity.example/resources/123
```

The source and reference are configuration, not access tokens. Restish creates
and stores the P-256 private key, asks the source for short-lived tokens, signs
each target request, renews expired credentials, and forces one renewal after
a `401`. `api auth logout` deletes the cached token and key; `api auth remove`
removes the local OpenAPI credential binding.

## Inspect Auth Material

For configured API auth, inspect the computed auth material without making the
full request:

```bash
restish api auth inspect myapi
restish api auth inspect myapi --credential PartnerKey
restish api auth inspect myapi --credential UserBearer --redact
restish api auth get myapi UserBearer
```

Replace `myapi`, credential IDs, and operation names with values from your
registered API. Use bare `api auth inspect` first when you are unsure which credential
bindings exist for the selected profile.

When a profile has exactly one configured credential, `inspect` selects it by
default. When a profile has several credentials, `inspect` prints each
configured credential's computed auth material; pass `--credential` to
narrow the output. `inspect` shows the computed values; use `--redact` for
shareable output. `--redact` is safe to include even when no secrets are
configured, which makes it a good default for bug reports. Use
`api auth get` when a script or curl command needs a single auth header or
query-string fragment.

When an installed `auth-resolver` owns the credential, inspection does not run
the plugin without a final request target. It reports how many secured
operations will evaluate a resolver at request time and labels the affected
requirements as unconfigured rather than missing. Invoke the generated
operation to determine whether the resolver can satisfy its exact URL and
security requirements.

For curl, pass a header fragment to `-H` or append a query fragment to the URL:

```bash
curl -H "$(restish api auth get myapi UserBearer)" https://api.rest.sh/items
curl "https://api.rest.sh/items$(restish api auth get myapi PartnerKey)"
```

Use verbose mode when the question is about the whole request:

```bash
restish -v -p token api.rest.sh/auth/bearer
```

## OAuth

Restish supports OAuth client credentials, authorization code with PKCE, and
device code auth. OAuth setup is provider-specific, so keep the exact flow,
redirect URI, scopes, and provider parameters in the focused
[OAuth guide](../oauth/). The reference page lists every supported auth type
and param.

For remote terminals, use `--rsh-no-browser` with authorization code auth when
you need to copy the authorization URL and paste the returned code manually.
Use `oauth-device-code` when the provider documents device authorization for
CLI use. Restish does not automatically switch between those OAuth flows.

The most common profile-level shape uses an OAuth auth type plus provider
params:

```jsonc
{
  "auth": {
    "type": "oauth-authorization-code",
    "params": {
      "issuer_url": "https://issuer.test",
      "client_id": "env:CLIENT_ID",
      "scopes": "read:items offline_access",
      "redirect_path": "/callback"
    }
  }
}
```

OAuth tokens are cached separately from HTTP responses. When an OAuth-backed API
request returns `401 Unauthorized`, Restish forces fresh auth and retries that
request once. Use `restish api auth logout` when a grant is revoked, consent
changes, or you want the next request to perform a fresh sign-in.

## External Tool Auth

Use `external-tool` when another program owns credentials or signing:

```jsonc
{
  "auth": {
    "type": "external-tool",
    "params": {
      "commandline": "./scripts/sign-request"
    }
  }
}
```

Restish records approved command hashes so a changed executable must be trusted
again.

External-tool and `command:` secret sources run through `cmd /c` on Windows and
`/bin/sh -c` on other platforms. Keep snippets portable, avoid relying on your
interactive `$SHELL`, and move complex logic into a script.

## Related Pages

- [Profiles](/docs/reference/profiles/)
- [Config](/docs/reference/config/)
- [OAuth](../oauth/)
- [Auth Reference](/docs/reference/auth/)
- [API Management](/docs/reference/api-management/)
- [Use External-Tool Auth](/docs/recipes/use-external-tool-auth/)
- [Security Design](/docs/contributing/design-records/)
