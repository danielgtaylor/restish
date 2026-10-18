---
title: Global Flags
linkTitle: Global Flags
weight: 12
description: Reference for flags shared across generic requests, generated API commands, utilities, and plugin-delegated requests.
---

Global flags apply across generic requests, generated API commands, utilities,
and many plugin-delegated requests. Ordinary command help shows common flags;
`--help-all` shows the full grouped reference:

```bash
restish get --help
restish get --help-all
```

## Generated Flag Reference

<!-- BEGIN GENERATED: restish-docgen global-flags -->
Generated from the current root persistent flags.

**`--help-all`**

Type: `bool`; default: `false`

Show all inherited Restish flags in help

**`--rsh-auth`**

Type: `string`; default: none

Generated operation auth override, e.g. "PartnerKey" or "UserOAuth+PartnerKey"

**`--rsh-ca-cert`**

Type: `string`; default: none

Path to a PEM encoded CA certificate to trust

**`--rsh-client-cert`**

Type: `string`; default: none

Path to a PEM encoded client certificate for mTLS

**`--rsh-client-key`**

Type: `string`; default: none

Path to a PEM encoded private key for mTLS

**`--rsh-collect`**

Type: `bool`; default: `false`

Collect paginated items before filtering (default: filter/render items as they arrive)

**`--rsh-columns`**

Type: `string`; default: none

Comma-separated column names for -o table (e.g. id,name,status)

**`--rsh-config`**

Type: `string`; default: none

Path to the restish config file (overrides RSH_CONFIG and the platform default)

**`--rsh-filter-lang`**

Type: `string`; default: none

Force filter language: shorthand or jq

**`--rsh-headers`**

Type: `bool`; default: `false`

Shorthand for -f headers

**`--rsh-ignore-status-code`**

Type: `bool`; default: `false`

Always exit 0 regardless of HTTP status

**`--rsh-insecure`**

Type: `bool`; default: `false`

Disable TLS certificate verification

**`--rsh-max-body-size`**

Type: `int`; default: `0`

Maximum response body size in MiB (0 = default 100 MiB)

**`--rsh-max-items`**

Type: `int`; default: `0`

Maximum number of paginated items or streamed events/lines to process (0 = unlimited)

**`--rsh-max-pages`**

Type: `int`; default: `25`

Maximum number of pages to fetch (0 = unlimited)

**`--rsh-no-browser`**

Type: `bool`; default: `false`

Disable automatic browser launch for interactive auth flows

**`--rsh-no-cache`**

Type: `bool`; default: `false`

Bypass the HTTP response cache (no read, no write)

**`--rsh-no-paginate`**

Type: `bool`; default: `false`

Disable automatic pagination (return only the first page)

**`--rsh-print`**

Type: `string`; default: `auto`

Output parts to print: auto or any of H=request headers, B=request body, h=response headers, b=rendered body, p=pretty, c=color

**`--rsh-prompt-secret`**

Type: `stringArray`; default: none

Request body path to fill from a hidden prompt (repeatable)

**`--rsh-retry-max-wait`**

Type: `string`; default: none

Maximum wait for Retry-After/X-Retry-In delays (default: 5m)

**`--rsh-retry-unsafe`**

Type: `bool`; default: `false`

Allow retries for POST, PUT, PATCH, and DELETE requests

**`--rsh-retry`**

Type: `int`; default: `2`

Maximum retry attempts for network errors and transient HTTP responses (0 = disable)

**`--rsh-sort-by`**

Type: `string`; default: none

Sort -o table rows by this column name

**`--rsh-status`**

Type: `bool`; default: `false`

Shorthand for -f status

**`--rsh-tls-min-version`**

Type: `string`; default: none

Minimum TLS version: TLS1.2 or TLS1.3 (default TLS1.2)

**`--rsh-tls-signer-param`**

Type: `stringArray`; default: none

TLS signer plugin parameter in "key=value" format (repeatable)

**`--rsh-tls-signer`**

Type: `string`; default: none

TLS signer plugin to use for mTLS client certificate signing

**`-H`, `--rsh-header`**

Type: `stringArray`; default: none

Request header in "Name: Value" format (repeatable)

**`-S`, `--rsh-silent`**

Type: `bool`; default: `false`

Suppress all output; only the exit code conveys success or failure

**`-c`, `--rsh-content-type`**

Type: `string`; default: none

Request body content type, e.g. json, yaml, cbor (default: json)

**`-f`, `--rsh-filter`**

Type: `string`; default: none

Filter/project the response using shorthand or jq (auto-detected)

**`-o`, `--rsh-output-format`**

Type: `string`; default: `auto`

Output format for rendered response bodies: auto, cbor, gron, image, json, lines, ndjson, table, toon, yaml (use -o lines for shell-friendly filtered values; see --rsh-columns, --rsh-sort-by for table)

**`-p`, `--rsh-profile`**

Type: `string`; default: none

API profile to use (overrides RSH_PROFILE env var; default: "default")

**`-q`, `--rsh-query`**

Type: `stringArray`; default: none

Query parameter in "key=value" format (repeatable)

**`-s`, `--rsh-server`**

Type: `string`; default: none

Override scheme://host for all requests (e.g. https://staging.example.com)

**`-t`, `--rsh-timeout`**

Type: `string`; default: none

Request timeout, e.g. 30s

**`-v`, `--rsh-verbose`**

Type: `count`; default: `0`

Verbose output: -v shows request/response headers, -vv adds TLS details
<!-- END GENERATED -->

## Request Construction

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `-H`, `--rsh-header` | repeatable `Name: Value` | none | Add request headers. Sensitive values are redacted in diagnostics. |
| `-q`, `--rsh-query` | repeatable `key=value` | none | Add query params without hand-editing the URL. |
| `-c`, `--rsh-content-type` | content alias or MIME | `json` | Request body encoder, such as `json`, `yaml`, `form`, or `multipart`. |
| `--rsh-prompt-secret` | repeatable body path | none | Fill a request-body path from a hidden terminal prompt. |
| `-s`, `--rsh-server` | URL | config/spec server | Override scheme and host for one request. |
| `-t`, `--rsh-timeout` | duration | transport default | Bound ordinary request lifetime. For SSE/NDJSON streams, bound the wait for response headers before stream rules take over. |
| `--rsh-max-body-size` | MiB | `100` when `0` | Maximum response body size. |
| `--rsh-ignore-status-code` | boolean | false | Exit zero even for HTTP error statuses. |

```bash
restish -H 'Accept: application/json' api.rest.sh/headers
restish -q api_key=docs-key api.rest.sh/auth/api-key-query
restish post -c form api.rest.sh/login 'username: alice' --rsh-prompt-secret password
restish --rsh-server https://staging.example.com example list-images
```

`RSH_HEADER` and `RSH_QUERY` use comma-separated entries, matching the
repeatable `-H` and `-q` flags. Escape a literal comma in an environment value
as `\,`:

```bash
RSH_HEADER='X-List: a\,b' restish api.rest.sh/headers
RSH_QUERY='list=a\,b' restish api.rest.sh/get
```

Generated operation commands may also expose `--rsh-generate-body` when the
OpenAPI operation has a request body schema. That flag prints an example body
for the generated operation instead of sending the request.

`--rsh-validate` is opt-in and applies to generated JSON request bodies. It
checks the assembled body against the operation's OpenAPI schema before sending
and leaves generic HTTP commands unchanged.

## Output And Filtering

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `-f`, `--rsh-filter` | expression | none | Filter with shorthand or jq. |
| `--rsh-filter-lang` | `shorthand` or `jq` | auto | Force one parser. |
| `-o`, `--rsh-output-format` | format | `auto` | `auto`, `json`, `yaml`, `cbor`, `table`, `ndjson`, `lines`, `gron`, `toon`, `image`, or plugin formats. |
| `--rsh-print` | `auto` or letters | `auto` | Print parts: `H` request headers, `B` request body, `h` response status and headers, `b` rendered body, `p` pretty, `c` color. |
| `--rsh-columns` | comma list | formatter default | Columns for `-o table`. |
| `--rsh-sort-by` | column name | none | Sort table rows by a column. |
| `--rsh-headers` | boolean | false | Shortcut for `-f headers`; selects raw response headers. |
| `--rsh-status` | boolean | false | Shortcut for `-f status`. |
| `-S`, `--rsh-silent` | boolean | false | Suppress request output, diagnostics, and request errors; use only exit status. |

```bash
restish api.rest.sh/images -f body.self -o lines
restish api.rest.sh/images -o table --rsh-columns name,format,self
restish api.rest.sh/types --rsh-print=b > types.json
restish api.rest.sh/ --rsh-headers
restish -S api.rest.sh/status/204
```

## Auth And Profiles

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `-p`, `--rsh-profile` | profile name | `default` or `RSH_PROFILE` | Select API profile. Command line wins over env. |
| `--rsh-auth` | auth requirement | operation default | Generated operation auth override, such as `PartnerKey` or `UserOAuth+PartnerKey`. |
| `--rsh-no-browser` | boolean | false | Disable automatic browser launch for interactive auth. |

```bash
restish -p json example list-images
restish myapi signed-report --rsh-auth UserOAuth+PartnerKey
restish --rsh-no-browser api connect example api.rest.sh 'prompt.api_key: docs-key'
```

## TLS

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `--rsh-ca-cert` | path | system trust | Additional PEM CA certificate. |
| `--rsh-client-cert` | path | none | PEM client certificate for mTLS. |
| `--rsh-client-key` | path | none | PEM private key for mTLS. |
| `--rsh-insecure` | boolean | false | Disable certificate verification as an explicit operator override. |
| `--rsh-tls-min-version` | `TLS1.2` or `TLS1.3` | `TLS1.2` | Minimum TLS version. |
| `--rsh-tls-signer` | plugin name | none | TLS signer plugin for external key signing. |
| `--rsh-tls-signer-param` | repeatable `key=value` | none | Parameters for the signer plugin. |

```bash
restish --rsh-ca-cert ./corp-ca.pem https://service.internal.test/items
restish --rsh-tls-min-version TLS1.3 api.rest.sh
```

## Pagination And Streaming

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `--rsh-no-paginate` | boolean | false | Return only the first page. |
| `--rsh-max-pages` | integer | `25` | Maximum pages, `0` means unlimited. |
| `--rsh-max-items` | integer | `0` | Maximum paginated items or streamed records, `0` means unlimited. |
| `--rsh-collect` | boolean | false | Collect all pages before filtering. |

```bash
restish api.rest.sh/images --rsh-no-paginate
restish api.rest.sh/images --rsh-max-pages 3
restish api.rest.sh/images --rsh-collect -f '.body | length'
restish api.rest.sh/events --rsh-max-items 3 -o ndjson
```

## Cache And Retry

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `--rsh-no-cache` | boolean | false | Bypass response cache reads and writes. |
| `--rsh-retry` | integer | `2` | Retry attempts for network errors and transient HTTP responses; `0` disables. |
| `--rsh-retry-max-wait` | duration | `5m` | Cap server-provided retry delays. |
| `--rsh-retry-unsafe` | boolean | false | Permit retries for POST, PUT, PATCH, and DELETE. |

```bash
restish 'api.rest.sh/flaky?failures=1&key=flags' --rsh-retry 2
restish post https://api.vendor.test/jobs 'name: demo' --rsh-retry 2 --rsh-retry-unsafe
restish api.rest.sh/cache --rsh-no-cache
```

## General

| Flag | Type | Default | Notes |
| --- | --- | --- | --- |
| `--rsh-config` | path | platform default | Active config file. Overrides `RSH_CONFIG` and default discovery. |
| `-v`, `--rsh-verbose` | count | `0` | `-v` shows request/response headers; `-vv` adds TLS details. |
| `--help-all` | boolean | false | Show all inherited Restish flags in help. |

```bash
restish -v api.rest.sh/headers
restish -vv api.rest.sh/headers
restish --rsh-config ./restish.json api list
restish --version
```

## Precedence

For request behavior, command-line flags win over environment variables, which
win over profile/API config defaults. Use explicit flags for one command; use
profiles when a choice should become routine.

## Related Pages

- [Requests](/docs/guides/requests/)
- [Output](/docs/guides/output/)
- [Config](../config/)
