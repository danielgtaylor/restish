---
title: Config
linkTitle: Config
weight: 20
description: Reference for Restish configuration files, APIs, profiles, auth, cache, themes, plugins, and precedence.
aliases:
  - /docs/getting-started/your-first-config/
---

Restish config is the trust boundary for API base URLs, generated command
sources, profiles, auth, TLS, plugins, cache settings, and terminal-output
themes.

## Location And Selection

Default config lives in a Restish config directory as `restish.json`:

| Platform | Default path |
| --- | --- |
| macOS, Linux, and other Unix-like systems | `~/.config/restish/restish.json` |
| Windows | `%APPDATA%\restish\restish.json` |

Config path precedence:

1. `--rsh-config <file>`
2. `RSH_CONFIG=<file>`
3. `RSH_CONFIG_DIR=<dir>/restish.json`
4. `XDG_CONFIG_HOME/restish/restish.json`
5. platform default

```bash
restish --rsh-config ./restish.json api list
RSH_CONFIG=./restish.json restish api list
```

An explicit config file is the whole source of truth for that invocation. If it
is missing, Restish errors instead of falling back to user config.

If no default config directory can be determined, set `RSH_CONFIG` or
`RSH_CONFIG_DIR`. Restish does not create a relative `./.restish` directory.
Cache-only state may use a temporary directory until `RSH_CACHE_DIR` or
`XDG_CACHE_HOME` is available.

When no explicit config is selected, Restish searches the current directory and
parents for `.restish.json`. A discovered project config is ignored until you
trust it:

```bash
restish config trust
```

Trust is stored outside the repository and includes the file's content hash. If
`.restish.json` changes, run `restish config trust` again after reviewing it.
Trusted project config layers `apis` and `theme` over your global config. Project
APIs override global APIs with the same name, while unrelated global APIs remain
available. Normal config-writing commands still write the global config and
refuse to mutate APIs that came from trusted project config.

Project config is safe to commit when it contains only shared, non-secret setup.
Secret auth params such as API key values, bearer tokens, passwords, and OAuth
client secrets must be omitted or written as `env:NAME` references. Non-secret
values such as OAuth `client_id`, `audience`, issuer URLs, token URLs, and scopes
can live in `.restish.json`.

Use `--rsh-config ./.restish.json` or `RSH_CONFIG=./.restish.json` when a
command should use one complete project config file instead of layering trusted
project config over the global config. Explicit config selection can point to
any filename; `.restish.json` is only the auto-discovered project filename.

On Unix-like systems, Restish refuses group/world-readable config files because
profiles and auth settings may contain secrets:

```bash
chmod 600 ~/.config/restish/restish.json
```

Inspect the active config:

```bash
restish config path
restish config show
restish config show -o json
```

## Example Shape

```jsonc
{
  "apis": {},
  "auth_profiles": {},
  "cache": {},
  "theme_source": "https://example.com/theme.json",
  "theme": {},
  "plugins": {}
}
```

## API Entries

```jsonc
{
  "apis": {
    "example": {
      "base_url": "https://api.rest.sh",
      "spec_url": "https://api.rest.sh/openapi.json",
      "allow_cross_origin_spec": false,
      "spec_files": [],
      "operation_base": "/",
      "command_layout": "flat",
      "server_variables": {},
      "url_overrides": {
        "https://api.vendor.test/": "https://staging.vendor.test/"
      },
      "allowed_operation_origins": [],
      "retry_max_wait": "30s",
      "pagination": {
        "items_path": "data",
        "next_path": "links.next",
        "page_param": "page"
      },
      "profiles": {
        "default": {},
        "json": { "headers": ["Accept: application/json"] }
      }
    }
  }
}
```

API names may contain Unicode letters, Unicode numbers, combining marks, `-`,
and `_`, and must start with a letter or number. They cannot collide with
built-in commands such as `api`, `get`, or `post`, or hidden compatibility
commands such as `completion`. Removed commands are not reserved; `flags` is a
valid API name in v2.

## Fields

<!-- BEGIN GENERATED: restish-docgen config-schema -->
Generated from `config/config.go`.

### `Config`

Config is the top-level configuration for Restish, loaded from restish.json.

| JSON field | Go field | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `apis` | `APIs` | `map[string]*APIConfig` | no | APIs is a map of short API name to per-API configuration. |
| `openapi_extension_aliases` | `OpenAPIExtensionAliases` | `map[string]string` | no | OpenAPIExtensionAliases maps canonical Restish x-cli extension names to alternate names used by OpenAPI documents. API-level aliases override it. |
| `auth_profiles` | `AuthProfiles` | `map[string]*AuthConfig` | no | AuthProfiles holds named auth configurations that API profiles can reference with auth_ref. |
| `cache` | `Cache` | `CacheConfig` | no | Cache holds global cache settings. |
| `theme` | `Theme` | `map[string]string` | no | Theme customizes syntax highlighting for readable terminal output. Keys are Chroma token names or Restish theme aliases; values are Chroma style descriptors such as "#afd787" or "bold #ff5f87". |
| `theme_source` | `ThemeSource` | `string` | no | ThemeSource records the source URL last used by `config theme set`. |
| `plugins` | `Plugins` | `map[string]json.RawMessage` | no | Plugins holds per-plugin configuration keyed by plugin name (without the "restish-" prefix). Each value is stored as raw JSON so that restish itself does not need to know the shape of each plugin's config. Plugins can read their config via the "config-read" message. Example restish.json entry: "plugins": { "bulk": { "concurrency": 4, "retry": true } } |

### `APIConfig`

APIConfig holds per-API configuration.

| JSON field | Go field | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `base_url` | `BaseURL` | `string` | no | BaseURL is the base URL for all requests to this API. |
| `spec_url` | `SpecURL` | `string` | no | SpecURL is the URL of the OpenAPI spec for this API (optional). Mutually exclusive with SpecFiles; SpecFiles takes precedence when both are set. |
| `allow_cross_origin_spec` | `AllowCrossOriginSpec` | `bool` | no | AllowCrossOriginSpec permits discovery from Link-header spec URLs on hosts other than base_url. Private, loopback, link-local, and unspecified IP literal targets are still rejected. |
| `spec_files` | `SpecFiles` | `[]string` | no | SpecFiles is an ordered list of local file paths or URLs to load the API spec from. Multiple files are deep-merged in order (later entries win on conflict). When set, network spec discovery is skipped entirely. |
| `operation_base` | `OperationBase` | `string` | no | OperationBase, when set, is an absolute path resolved against base_url for paths generated from OpenAPI operations. Useful when operation paths should escape or replace a sub-path in base_url. |
| `command_layout` | `CommandLayout` | `string` | no | CommandLayout controls how generated operations are arranged under the API command. Empty or "flat" keeps one flat command namespace; "tags" groups operations under first-tag subcommands. |
| `openapi_extension_aliases` | `OpenAPIExtensionAliases` | `map[string]string` | no | OpenAPIExtensionAliases overrides global extension aliases for this API. |
| `server_variables` | `ServerVariables` | `map[string]string` | no | ServerVariables supplies explicit values for OpenAPI server URL variables. Values are used for generated operation path resolution; enum values from remote specs are never expanded eagerly. |
| `url_overrides` | `URLOverrides` | `map[string]string` | no | URLOverrides rewrites resolved request URL prefixes before execution. It is useful when an OpenAPI document names canonical servers but this profile should route requests to staging, local, or test endpoints. |
| `allowed_operation_origins` | `AllowedOperationOrigins` | `[]string` | no | AllowedOperationOrigins permits generated commands to use operation- or path-level OpenAPI servers on origins outside base_url. |
| `profiles` | `Profiles` | `map[string]*ProfileConfig` | no | Profiles is a map of profile name to profile configuration. |
| `pagination` | `Pagination` | `*PaginationConfig` | no | Pagination holds optional per-API pagination configuration. |
| `retry_max_wait` | `RetryMaxWait` | `string` | no | RetryMaxWait caps Retry-After/X-Retry-In delays for this API when no command-line or environment override is supplied. |
| `preserve_header_case` | `PreserveHeaderCase` | `bool` | no | PreserveHeaderCase sends user/API-supplied header names with their configured casing for broken HTTP/1.x servers that treat names as case-sensitive. It cannot affect HTTP/2, where header names are lowercase by protocol. |

### `PaginationConfig`

PaginationConfig holds per-API pagination settings.

| JSON field | Go field | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `items_path` | `ItemsPath` | `string` | no | ItemsPath is a filter expression that extracts the items array from the response body (e.g. "data" for JSON:API, "results" for some REST APIs). When empty, the body itself is used (if it is an array). |
| `next_path` | `NextPath` | `string` | no | NextPath is a filter expression that extracts the next-page URL from the response body (alternative to Link header rel="next"). |
| `page_param` | `PageParam` | `string` | no | PageParam is the URL query parameter that increments between pages when an API has no next links or next_path metadata. |

### `CacheConfig`

CacheConfig holds cache settings.

| JSON field | Go field | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `max_size` | `MaxSize` | `string` | no | MaxSize is the maximum cache size (e.g. "100MB"). Default: "100MB". |

### `AuthConfig`

AuthConfig holds authentication configuration for a profile.

| JSON field | Go field | Type | Required | Description |
| --- | --- | --- | --- | --- |
| `type` | `Type` | `string` | no | Type identifies the auth mechanism (e.g. "http-basic", "oauth-client-credentials"). |
| `params` | `Params` | `map[string]string` | no | Params holds handler-specific configuration, e.g. {"username": "alice"}. |
<!-- END GENERATED -->

## Profiles

Profiles hold request defaults under an API. See [Profiles](../profiles/) for
the full field reference.

```jsonc
{
  "profiles": {
    "default": {},
    "staging": {
      "base_url": "https://staging.example.com",
      "headers": ["Accept: application/json"],
      "query": ["trace=docs"],
      "auth": {
        "type": "bearer",
        "params": {
          "token": "env:EXAMPLE_TOKEN"
        }
      }
    }
  }
}
```

## Auth Profiles

Use top-level `auth_profiles` when several APIs or profiles should share one
credential configuration:

```jsonc
{
  "auth_profiles": {
    "work-user-oauth": {
      "type": "oauth-authorization-code",
      "params": {
        "authorize_url": "https://issuer.test/authorize",
        "token_url": "https://issuer.test/oauth/token",
        "client_id": "env:CLIENT_ID",
        "scopes": "read:items",
        "redirect_path": "/callback"
      }
    }
  }
}
```

Profiles can reference these with `auth_ref`.

Secret params may use `env:NAME` or `command:...`. Command secrets and
`external-tool` auth snippets run through `cmd /c` on Windows and `/bin/sh -c`
elsewhere.

## Cache

Cache settings control the HTTP response cache, not OAuth/auth token cache.
Use `restish cache info` to inspect runtime cache location, size, entry count,
oldest entry, largest cached hosts, and API/profile cache usage. Restish does
not write cache entries for responses that carry credential-bearing headers
such as `Set-Cookie` or common API-key headers.

```bash
restish config set 'cache.max_size: 250MB'
restish cache info
restish cache clear
```

Use `restish api auth logout` for cached auth tokens.

## Theme

Themes affect `auto` terminal output and printed HTTP transcript highlighting.
Theme files may be JSON or JSONC:

```bash
restish config theme list
restish config theme set one-dark-pro
restish config theme set restish-dark
restish config theme set ./theme.json
restish config theme set user/repo dark --yes
restish config theme reset
```

`theme_source` records where the theme came from. Local paths are stored as
absolute paths, and bundled official themes are stored as `official:<name>`.
`theme` stores the resolved highlighting values. Use `text` for the base text
color, `header_key` to color HTTP response header names differently from JSON
object keys, `heading` for interactive help headings, and `diagnostic_warn`,
`diagnostic_error`, `diagnostic_hint`, or `status_2xx` to customize human
status output. `reset` removes the saved theme and restores the built-in theme;
setting `restish-dark` does the same thing.

For GitHub shorthand with a theme name, Restish tries `themes/<name>.json`
first, then falls back to `<name>.json` at the repository root. Name-only
theme installation uses the official themes bundled with the Restish binary and
does not fetch from the network.

`theme list` shows the official theme names:
`catppuccin-latte`, `catppuccin-mocha`, `dracula`, `github-dark`,
`gruvbox-dark`, `gruvbox-light`, `houston`, `minimal`, `monokai-pro-dark`,
`monokai-pro-light`, `noctis`, `nord`, `one-dark-pro`, `restish-dark`,
`restish-light`, `solarized-dark`, `synthwave-84`, and `vscode-dark`.

## Plugins

The `plugins` object stores plugin-specific config keyed by plugin name
without the `restish-` prefix. Restish preserves each value as raw JSON and
plugins can read it with the `config-read` plugin message. Users normally
manage plugin installation separately with:

```bash
restish plugin list
restish plugin install rest-sh/restish csv
restish plugin remove restish-csv
```

Installed plugins are trusted local executables. Restish checks manifests and
declared capabilities, but it does not sandbox plugin code.

## Editing

Use `config edit` for larger changes:

```bash
restish config edit
```

Use `api set` for API-scoped patches:

```bash
restish api set example 'spec_url: https://api.rest.sh/openapi.json'
restish api set example \
  'profiles.demo.auth: {type: http-basic, params: {username: demo, password: env:EXAMPLE_PASSWORD}}'
```

Use `config set` for global patches:

```bash
restish config set \
  'apis.example.profiles.demo.auth: {type: http-basic, params: {username: demo, password: env:EXAMPLE_PASSWORD}}'
restish config set 'cache.max_size: 250MB'
```

`config set` and `api set` use shorthand patch expressions. Objects merge
recursively, scalars replace, `undefined` deletes fields or array items, `[]`
appends, `[^index]` inserts before an array index, and `^` moves or swaps
values. Restish validates the final config before writing.

## Precedence

Request behavior is layered from lower to higher precedence:

1. built-in defaults
2. API config
3. selected profile
4. environment variables
5. command-line flags

Explicit config selection is not layered. `--rsh-config` or `RSH_CONFIG`
selects one config file for the invocation. Project-local config is explicit;
Restish does not auto-discover config files from the current directory.

## Related Pages

- [Profiles](../profiles/)
- [Environment Variables](../environment-variables/)
- [API Management](../api-management/)
- [Shorthand](../shorthand/)
- [Security Design](/docs/contributing/design-records/)
