# Implementation Contract

## Summary

This record captures the current implementation-level contract that cuts across
the narrower subsystem records. A clean-room implementation should be able to
reconstruct global flags, config shape, command precedence, plugin protocol
families, and output ownership from this document plus the specialized records.

## Global Flags And Environment

Command-line flags override environment variables. Environment variables
override built-in defaults only when the matching flag was not set.

| Flag | Short | Type | Env | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `--rsh-header` | `-H` | repeat `Name: Value` | `RSH_HEADER` | empty | Env is comma-separated, supports `\,` for literal commas, and is prepended. |
| `--rsh-query` | `-q` | repeat `key=value` | `RSH_QUERY` | empty | Env is comma-separated, supports `\,` for literal commas, and is prepended. |
| `--rsh-server` | `-s` | string | | empty | Overrides scheme/host; path prefixes request path. |
| `--rsh-output-format` | `-o` | string | `RSH_OUTPUT_FORMAT` | auto | Formats the rendered body/value selected by `--rsh-print=b`; `lines` for scalar line output; no `raw` format. |
| `--rsh-print` | | string | `RSH_PRINT` | auto | Chooses stdout parts: `H` request headers, `B` request body, `h` response status/headers, `b` rendered body, `p` pretty, `c` color. `auto` is `hbpc` on a terminal, body bytes for redirected unfiltered responses with no explicit output transform, and `bp` for filters, metadata shortcuts, and formatted/collected output. |
| `--rsh-silent` | `-S` | bool | | false | Suppress output. |
| `--rsh-columns` | | string | | empty | Table columns. |
| `--rsh-sort-by` | | string | | empty | Table sort column. |
| `--rsh-content-type` | `-c` | string | | empty | Empty means JSON default for bodies unless operation media type applies. |
| `--rsh-filter` | `-f` | string | `RSH_FILTER` | empty | Shorthand/jq auto-detected. |
| `--rsh-filter-lang` | | string | | auto | `shorthand` or `jq`. |
| `--rsh-headers` | | bool | | false | Shorthand for `-f headers`. |
| `--rsh-status` | | bool | | false | Shorthand for `-f status`. |
| `--rsh-verbose` | `-v` | count | | 0 | `-v` headers, `-vv` TLS details. |
| `--rsh-insecure` | | bool | `RSH_INSECURE` | false | Warns, then disables TLS verification. |
| `--rsh-client-cert` | | string | | empty | mTLS cert. |
| `--rsh-client-key` | | string | | empty | mTLS key. |
| `--rsh-tls-signer` | | string | | empty | TLS signer plugin name/path. |
| `--rsh-tls-signer-param` | | repeat `key=value` | | empty | Plugin params. |
| `--rsh-ca-cert` | | string | | empty | Extra trusted CA. |
| `--rsh-tls-min-version` | | string | | `TLS1.2` | `TLS1.2` or `TLS1.3`. |
| `--rsh-ignore-status-code` | | bool | | false | Suppresses status-derived non-zero exit. |
| `--rsh-timeout` | `-t` | duration | `RSH_TIMEOUT` | none | Bounded request lifetime; for streams, header wait timeout before switching to stream cancellation rules. |
| `--rsh-profile` | `-p` | string | `RSH_PROFILE` | `default` | Active API profile. |
| `--rsh-auth` | | string | `RSH_AUTH` | empty | Generated-operation credential alternative override, e.g. `UserOAuth+PartnerKey`. |
| `--rsh-no-cache` | | bool | `RSH_NO_CACHE` | false | Bypass reads and writes. |
| `--rsh-no-browser` | | bool | | false | OAuth auth-code browser suppression. |
| `--rsh-retry` | | int | `RSH_RETRY` | 2 | `0` disables retries. Internally, `-1` may be used as the unresolved-default sentinel. |
| `--rsh-retry-unsafe` | | bool | `RSH_RETRY_UNSAFE` | false | Replay POST/PUT/PATCH/DELETE on retryable failures. |
| `--rsh-retry-max-wait` | | duration | `RSH_RETRY_MAX_WAIT` | `5m` | Cap server-provided retry waits. |
| `--rsh-no-paginate` | | bool | | false | Disable automatic pagination. |
| `--rsh-collect` | | bool | | false | Collect pages before filtering. |
| `--rsh-max-pages` | | int | | 25 | `0` means unlimited. |
| `--rsh-max-items` | | int | | 0 | Paginated item or streamed event/line cap; `0` means unlimited. |
| `--rsh-max-body-size` | | int MiB | | formatter default | Bounded response cap. |
| `--rsh-config` | | string path | `RSH_CONFIG` | default config path | Selects one complete config file. Missing explicit files error. |

Config file location precedence is `--rsh-config`, `RSH_CONFIG`,
`RSH_CONFIG_DIR/restish.json`, `XDG_CONFIG_HOME/restish/restish.json`, then the
default config path. The default config path is
`~/.config/restish/restish.json` on macOS, Linux, and other Unix-like systems,
and `%APPDATA%\restish\restish.json` on Windows.
`--rsh-config` and `RSH_CONFIG` are source-of-truth selectors: Restish does not
merge them with the default config. Token and external-tool approval sidecars
live next to the selected explicit config. HTTP response and spec caches stay
under the cache root, with a namespace derived from the explicit config path.
When no explicit config file is selected, Restish may discover `.restish.json`
from the current directory or an ancestor. A discovered project config is used
only after trust is recorded with `restish config trust` or an interactive TTY
prompt. Trust is stored outside the repository in user Restish state and is
keyed by canonical project config path plus content hash, so a changed project
config must be trusted again.

Trusted project config is layered over the selected/global config for reads. The
first project layer honors only `apis` and `theme`: project APIs shadow global
APIs by name without deep-merging individual API definitions, and project theme
entries override global theme entries by key. Other top-level keys such as
`auth_profiles`, `plugins`, `cache`, and `theme_source` are invalid in
auto-discovered project config and produce a clear diagnostic when the project
config is otherwise trusted. Project config may use normal repository file
permissions, but it must not contain inline secret values; secret-bearing auth
params must be omitted or use `env:NAME` references. Non-interactive runs do not
prompt for trust; when they discover an untrusted project config, they continue
with the selected/global config and print a concise stderr warning naming the
file and `restish config trust`. Auto-discovered project config is read-only for
normal mutation commands; writes continue to target the selected/global config
unless `--rsh-config` or `RSH_CONFIG` explicitly selects the project file.

Project-local machine state is not written into the repository. OAuth/token
caches, HTTP response caches, and spec/generated-command caches for trusted
project APIs live in user Restish state/cache roots under a namespace derived
from the trusted project config path and content hash.

Windows ACL inspection is not implemented for the first v2 release. Existing
config and token-cache files on Windows therefore report permission diagnostics
as `unknown`, not `ok`. Startup remains non-blocking on Windows because Restish
cannot yet prove the ACL is insecure, but `doctor` must not imply that a
secret-bearing file was checked successfully. A future hardening pass may add
native ACL inspection and turn broad read access into the same warning/failure
path used for Unix mode bits.

## Config Schema

Top-level config is JSONC with strict decoded fields:

| Path | Type | Meaning |
| --- | --- | --- |
| `apis` | map | API registrations keyed by short name. |
| `auth_profiles` | map | Shared auth configs referenced by profile or credential `auth_ref`. |
| `cache.max_size` | string | Disk cache size such as `100MB`. |
| `theme` | map | Auto-output and terminal transcript style entries. |
| `plugins` | map | Raw per-plugin JSON config. |

HTTP response-cache entries are written with temp-file plus rename semantics,
and LRU eviction is guarded by an advisory sibling lock so separate Restish
processes can share the same cache directory.

API fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `base_url` | string | Default API URL prefix. |
| `spec_url` | string | Explicit spec URL. |
| `spec_files` | array | Ordered local/remote specs to merge. |
| `allow_cross_origin_spec` | bool | Permit safe cross-origin Link spec discovery. |
| `operation_base` | string | Absolute path prefix resolved against `base_url` for generated operations. |
| `command_layout` | string | `flat` or `tags`; empty means `flat`. |
| `server_variables` | map | Explicit OpenAPI server URL variable values used for generated operation paths. |
| `retry_max_wait` | string duration | API-local cap for `Retry-After`/`X-Retry-In` when no flag/env override is set. |
| `preserve_header_case` | bool | Opt-in HTTP/1.x compatibility mode for broken servers that treat request header names as case-sensitive. |
| `pagination.items_path` | string | Item extraction path. |
| `pagination.next_path` | string | Next URL extraction path. |
| `pagination.page_param` | string | Query parameter to increment for APIs without next links. |
| `profiles` | map | Profile configs keyed by name. |

Profile fields are `base_url`, `headers`, `query`, `tls_signer`,
`tls_signer_params`, `server_variables`, `auth`, `auth_ref`, and
`credentials`. Profile server variables override API-level server variables for
command generation. Inline `auth` and `auth_ref` are mutually exclusive.

Credential fields under `profiles.<name>.credentials.<id>` are `auth`,
`auth_ref`, and `satisfies`. Credential inline `auth` and `auth_ref` are also
mutually exclusive. Auth fields are `type` plus string `params`.

Config files are written private. On Unix-like systems, group/world-readable
config permissions are fatal because profiles and auth parameters can contain
secrets. Users should repair them with `chmod 600`.

`config set <patch> [patch...]` applies shorthand patch expressions to the
whole config object. `api set <name> <patch> [patch...]` applies the same
language rooted at `apis.<name>`. Both commands reject the unreleased pre-v2
`key value` form. Shorthand patch supports recursive object merge, scalar
replacement, array set/append/insert, `undefined` deletion, and `^` swap/move
operations. API-scoped patches cannot escape the selected API root.

Command-line config patching validates the final patched object in layers:
Huma-backed structural validation, typed config decode, `config.Validate`
semantic validation, then CLI/runtime checks such as registered auth handlers
and TLS signer plugins. Writes are atomic and are skipped entirely when any
validation layer fails.

## Command Surface And Precedence

Public built-ins own: `get`, `head`, `options`, `post`, `put`, `patch`,
`delete`, `api`, `cache`, `cert`, `config`, `doctor`, `edit`, `help`, `links`,
`plugin`, `shell`, and `version`.

The public completion generator is `shell completion <shell>`. A top-level
`completion` command may exist as a hidden compatibility alias, but design 037
owns the published command surface and user-facing docs should not advertise
the alias. There is no public `flags` command in v2; global flag discovery is
through command help and `--help-all`.

API short names must not collide with public built-ins or hidden compatibility
commands. Removed pre-release command names are not held in reserve unless an
actual hidden command remains. In particular, `completion` is reserved because
the hidden alias exists, while `content-types` and `flags` are available as API
short names.

`api auth logout` accepts either one API argument or `--auth-profile
<name>`. The API argument is required unless `--auth-profile` is supplied.
`--all-profiles` applies only to API-scoped cache clearing.

Generated API commands are registered under API short names when cached spec
metadata is available. Short-name generic fallback commands are registered for
APIs without generated command groups. Plugin commands are top-level commands
but must not collide with built-ins, generated APIs, configured API names, or
other plugin commands.

Bare URLs and registered API short names at root infer the generic request
method from body presence: no body sends `GET`; shorthand or stdin body input
sends `POST`.
Generated command startup uses a fast path that skips value-taking global flags
but does not consume bool/count flags such as `-v` or `--rsh-insecure`.

## Plugin Wire Protocol Families

All plugin messages use CBOR. The stable message families are:

| Family | Direction | Purpose |
| --- | --- | --- |
| Manifest/startup flags | host -> plugin process | Discover hooks, loaders, formatters, and commands. |
| Hook messages | host <-> short-lived plugin | Auth, request middleware, response middleware, loader, formatter hooks. |
| Command messages | host <-> long-lived plugin | Init, stdin, HTTP delegation, formatting delegation, stderr, done. |
| Config messages | plugin -> host | Read/list config and prompt/confirm/select where allowed. |
| Formatter messages | host <-> formatter plugin | Normalize host response and stream or document formatting. |
| TLS signer messages | host <-> signer plugin | Certificate discovery and signing for mTLS. |

Protocol changes that alter message meaning require a plugin API version bump or
explicit compatibility handling. Additive command interactions use manifest
`required_features`; interactive selection is negotiated as `command.select`.

## Output Ownership

Design 009 owns the normalized response schema and bounded response formatting
contract. Design 028 owns the planner that decides document vs record framing
across pagination, streaming, filters, and explicit formats.

| Concern | Owner |
| --- | --- |
| Decode body and preserve raw bytes | 009 |
| Normalize status, headers, links, body | 009 |
| Select default formatter for TTY/non-TTY | 009 and 028 |
| Decide document vs record execution | 028 |
| Paginated collection vs streaming behavior | 028 and 011 |
| SSE/NDJSON event rendering | 028 and 012 |

## Local Mutation Inventory

Commands that mutate local state must name what they changed and, for config
writes, print `Wrote config: <path>` in human output. This inventory is the
release contract for which commands touch disk and what they intentionally
preserve.

| Command | Local state touched | Safety contract |
| --- | --- | --- |
| `api connect` | `restish.json`, spec cache, generated-operation cache | Without `--replace`, refresh API-level metadata and cache state while preserving existing profiles, credentials, and explicit local defaults. With `--replace`, regenerate replaceable profiles and reapply contract-provided defaults while preserving values that cannot be rediscovered safely. |
| `api sync` | spec cache, generated-operation cache, sometimes `restish.json` API metadata | Refresh discovered API metadata and generated operations without replacing credential-containing profiles. |
| `api set` | one API section in `restish.json` | Patch only the requested API fields and preserve comments/formatting when possible. |
| `api remove` | `restish.json`, API-owned HTTP cache namespaces, API-scoped auth token cache entries | Remove the API and clean API-owned local state. Shared auth-profile tokens are removed only when no remaining API references that shared profile. |
| `api auth add` / `api auth remove` | profile credential entries in `restish.json` | Add or remove only the named credential binding. Empty additions are allowed as an easy escape hatch before filling details with `api set`. |
| `api auth logout` | auth token cache | Clear cached OAuth/auth tokens only; do not mutate config. |
| `config set` / `config edit` | `restish.json` | Validate runtime config before keeping changes. Preserve comments/formatting for targeted edits when possible. |
| `config theme set` / `config theme reset` | theme fields in `restish.json` | Remote theme sources require trust confirmation unless `--yes` is explicit. Reset removes only theme override fields. |
| `plugin install` / `plugin remove` | plugin directory and plugin manifest cache | Install only after manifest inspection and trust confirmation unless `--yes` is explicit. Remove only installed plugin files selected by name/path. |
| `shell setup` / `shell completion install` | shell rc files and completion files | Support dry-run, explicit confirmation, duplicate detection, and atomic writes where possible. |
| `cache clear` | HTTP response cache | Clear all HTTP entries, one API/namespace, or direct URL requests. Do not clear spec cache or auth token cache. |

## Intentional v2 Breaks

The v1 habit of whole-config editing through `api edit` is retired. v2 config
is edited through `restish.json`, `api connect`, `api set`, `config set`, and
`config edit`.
Legacy `x-cli-config.prompt` is not retired: `api connect <name> <url>`
prompts for those values while writing local config, then normal requests use
the saved config without extension-driven prompting.

The `restish-mcp --http` flag is not part of v2; MCP currently uses stdio as a
command plugin.

The v2 command surface is intentionally not preserving pre-release aliases such
as `api show`, `api edit`, `api clear-auth-cache`, `api content-types`, a
top-level `setup` command, or a direct `mcp <api...>` service invocation. Design
037 owns the exact accepted command tree and v1-to-v2 command move table.
