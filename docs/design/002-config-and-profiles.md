# Config And Profiles

## Summary

Restish v2 uses a typed JSONC configuration model centered on one primary user
config file, `restish.json`. That file stores global settings, registered APIs,
profiles, and request-affecting defaults.

Profiles are Restish's main "environment" abstraction. They let one API
registration express per-environment differences without duplicating the entire
API definition.

This document defines both the data model and the persistence guarantees around
it.

## Goals

- one obvious place for users to inspect and edit configuration
- strict validation so typos fail early
- comments and formatting preserved when Restish edits the file
- deterministic layering between config, environment, and CLI flags
- safe writes under concurrent processes
- a real migration path from v1 locations and filenames

## Non-Goals

- supporting arbitrary config sources or many file formats
- creating a fully general environment-inheritance system shared across APIs
- silently accepting malformed or unknown fields for flexibility

## Persistent Layout

The primary persistent surfaces are:

- config file: `restish.json`
- token cache
- spec cache
- response cache
- plugin directory

Design 001 defines that the `CLI` runtime owns the resolved paths. This
document defines what those paths are for.

## Path Resolution

Restish should resolve config and cache locations in a developer-friendly,
predictable way:

- XDG-style locations on macOS, Linux, and other Unix-like systems
- native user config and cache locations on Windows
- explicit overrides when tests, scripts, projects, or embedders supply custom
  paths

The design intent is consistency and discoverability. Restish is a shell-native
developer tool, so Unix-like users should be able to find, edit, back up, and
sync configuration from the same `~/.config` tree they use for other CLI tools.
This is more useful for Restish than macOS's native
`~/Library/Application Support` default, which is appropriate for app bundles
but surprising for a terminal-first workflow. Config, token cache, spec cache,
and response cache should all come from one coherent path-resolution strategy
rather than several unrelated helper functions.

The plugin directory is part of this same path model. It should resolve beneath
the configured Restish config directory, so tests, XDG overrides, and embedded
CLIs do not accidentally use a different plugin trust root from the main
configuration.

User-facing config file selection follows this precedence:

1. `--rsh-config <file>`
2. `RSH_CONFIG=<file>`
3. `RSH_CONFIG_DIR=<dir>/restish.json`
4. `XDG_CONFIG_HOME/restish/restish.json`
5. the default config directory:
   - macOS, Linux, and other Unix-like systems: `~/.config/restish/restish.json`
   - Windows: `%APPDATA%\restish\restish.json`

`--rsh-config` and `RSH_CONFIG` select a complete config file, not an overlay.
When either is present, Restish reads and writes only that file. A missing
explicit config file is an error so operators do not accidentally run with the
global default after mistyping a project path. The ordinary platform-default
config path keeps the v1 migration and auto-create behavior described below.

When neither explicit config selector is present, Restish also walks from the
current directory toward the filesystem root looking for `.restish.json`.
Discovered project config is ignored until the user trusts that exact file
content with `restish config trust` or an interactive first-run prompt. Trust is
stored outside the repository in the user's Restish config directory and is keyed
by canonical project config path plus SHA-256 content hash, so a repository
cannot trust itself and changed project config requires renewed trust.

Trusted project config is layered over the global config, not written back into
it. Global APIs and auth profiles remain available. Project APIs override global
APIs with the same name at the API-entry boundary; individual API entries are
not deep-merged. The first version accepts only project `apis` and `theme`.
Project theme entries overlay the global theme so teams can make environment
context visible, while `auth_profiles`, `plugins`, `cache`, and `theme_source`
remain global-only. Normal config mutation commands still write the global
config and refuse to mutate APIs that came from trusted project config. Passing
`--rsh-config .restish.json` keeps explicit-config semantics: the selected file
is the complete config source of truth and sidecar state follows that explicit
config root.

Auto-discovered project config is intended to be a committed, shareable project
manifest. It may use normal repository file permissions, but it must not contain
inline secret material. Secret-bearing auth params such as API key values,
bearer tokens, HTTP Basic passwords, and OAuth client secrets must be omitted or
use `env:NAME` references. Non-secret OAuth parameters such as `client_id`,
`audience`, `resource`, `organization`, issuer URLs, token URLs, and scopes are
valid project config.

Sidecar state that belongs to the selected config trust root, such as OAuth
token caches and external-tool approval records, lives next to the explicit
config file. Response and spec caches remain under the cache root, but explicit
configs get a cache namespace derived from the config path so two project
configs do not reuse each other's cached HTTP responses or discovered specs.
In discovered layered mode, response cache, spec cache, and API-scoped OAuth
tokens for project APIs use a project namespace derived from the canonical
project config path and content hash. Tokens and caches stay in the user's
Restish state/cache roots, never in the repository.

Cache directory selection mirrors config selection with `RSH_CACHE_DIR`,
`XDG_CACHE_HOME/restish`, `~/.cache/restish` on Unix-like systems, and the
Windows user cache directory on Windows.

If Restish cannot determine a config directory from an explicit config file,
`RSH_CONFIG_DIR`, `XDG_CONFIG_HOME`, the platform user-config directory, or
`HOME`, it must fail with a clear setup error instead of falling back to a
relative `./.restish` directory. Cache state may fall back to
`os.TempDir()/restish` so one-off requests can still run, but that fallback is
diagnostic-only and should direct operators to set `RSH_CACHE_DIR`,
`XDG_CACHE_HOME`, or `HOME` for persistent cache state.

Config writes preserve permissions on an existing config directory. Missing
config directories are created with the restrictive default mode.
On Unix-like systems, Restish detects group/world-readable config and token
cache files as insecure. On Windows, ACL inspection is not implemented for the
first v2 release, so existing secret-bearing files report permission status as
unknown rather than ok.

Config writes should use the shared atomic-write helper used by the rest of
Restish's local state, while preserving config-specific locking and directory
permission behavior. The helper exists to keep temp-file creation, file modes,
sync/rename cleanup, and parent-directory sync consistent across config, token,
spec, HTTP cache, plugin manifest cache, and shell/completion setup writes.

Removing an API should remove its API-scoped local state at the same time:
the saved API entry, generated-command/spec cache, HTTP response cache
namespaces, and API-scoped auth token cache entries. Shared `auth_profiles`
remain configured globally, but cached tokens for a shared auth profile are
cleared when the removed API was the last remaining API that referenced that
auth profile. Restish should not keep a `--keep-cache` or `--keep-auth` escape
hatch unless real user demand appears before or after v2.

## Primary Config Shape

The primary top-level keys are:

- `apis`
- `openapi_extension_aliases`
- `auth_profiles`
- `cache`
- `theme`
- `theme_source`
- `plugins`

`auth_profiles` stores shared auth configurations that profile-level `auth_ref`
or credential-level `auth_ref` can point to. `cache` stores global cache
settings. `theme` stores human terminal output style overrides. `theme_source`
records the source last installed by `config theme set`; remote sources use this
for first-install confirmation, and official bundled themes use the
`official:<name>` source form. `plugins` stores raw per-plugin JSON config keyed
by plugin name so plugin-specific settings do not need to become core Restish
fields.

Each entry under `apis` is keyed by a short API name and contains the stable
registration for that API:

- `base_url`
- `spec_url`
- `spec_files`
- `allow_cross_origin_spec`
- `operation_base`
- `command_layout`
- `openapi_extension_aliases`
- `server_variables`
- `retry_max_wait`
- pagination configuration
- profile map

The exact field list may evolve, but the structural rule should remain:

- API-level keys describe the API as a whole
- profile-level keys describe environment-specific request behavior

## Profile Model

Profiles live under each API, not globally.

This is deliberate because a profile name such as `prod`, `staging`, or
`enterprise` only makes sense relative to one API registration.

Profiles may set or override:

- `base_url`
- `operation_base`
- persistent headers
- persistent query parameters
- profile-level auth configuration or `auth_ref`
- operation credential bindings
- CA certificate path
- client certificate and key paths
- TLS signer selection and parameters
- OpenAPI server variable values
- other request-affecting defaults added in future designs

The default selected profile name is `default` unless the user specifies
another profile through env vars or flags.

Profile-level `auth` and `auth_ref` are mutually exclusive. `auth` is an inline
auth config with `type` and string `params`; `auth_ref` points at one
top-level `auth_profiles` entry. A reference is not an overlay.

Generated operations may require named credential alternatives from OpenAPI.
Those bindings live under:

```jsonc
"profiles": {
  "prod": {
    "credentials": {
      "UserOAuth": {
        "auth_ref": "work-oauth",
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
```

Credential-level `auth` and `auth_ref` are mutually exclusive for the same
reason as profile-level auth. `satisfies` records scopes, roles, or other
requirement values the credential is intended to satisfy.

## Layering And Precedence

When Restish resolves a request through a registered API, precedence is:

1. built-in defaults
2. API registration defaults
3. selected profile values
4. environment-derived overrides
5. explicit CLI flags and command arguments

Two rules matter here:

- profiles provide durable defaults, not hard locks
- invalid explicit selection should error rather than silently falling back

That means a misspelled `--rsh-profile` should be treated as a real error.

## Header And Query Semantics

Persistent headers and query parameters are merged in a way that still lets the
user override them per invocation.

The desired behavior is:

- config contributes defaults
- command flags contribute explicit invocation values
- explicit invocation values win

The implementation detail may be prepend/append plus later overwrite, but the
user-visible contract is override-friendly layering.

## Strict Validation

The config format is intentionally strict:

- JSONC comments are allowed in source text
- after comment stripping or CST parsing, data is decoded into typed structs
- unknown fields are rejected
- invalid enums or malformed values should fail early with location-aware
  diagnostics

Strictness is a feature here. It keeps config drift from becoming runtime
mystery behavior.

Validation diagnostics should list the legal fields for the object that failed
when doing so is practical. A strict parser that says only "unsupported field"
without the valid alternatives makes config migration much harder to debug.

Size and duration values should surface parse errors. Falling back to a default
after a malformed cache size, timeout, or similar operator setting hides the
configuration mistake and makes behavior depend on accident.

## Command-Line Patching

`config set` and `api set` use shorthand patch syntax as their only v2 command
line patch language. Restish does not keep a second config-specific `key value`
assignment dialect from pre-release builds.

`config set` applies patch expressions to the whole config object:

```bash
restish config set \
  'apis.example.profiles.demo.auth: {type: bearer, params: {token: env:EXAMPLE_TOKEN}}'
```

`api set <name>` applies the same shorthand patch language with operations
rooted at `apis.<name>`:

```bash
restish api set example \
  'profiles.demo.auth: {type: http-basic, params: {username: demo, password: env:EXAMPLE_PASSWORD}}'
```

The two commands share shorthand behavior:

- objects merge recursively
- scalars replace
- arrays can be set, appended with `[]`, inserted with `[^index]`, and deleted
  with `undefined`
- object fields can be deleted with `undefined`
- values can be swapped or moved with shorthand `^` operations

`api set` must not escape its selected API root. Both sides of a shorthand swap
are interpreted under `apis.<name>` so an API-scoped command cannot mutate
global settings or another API.

Validation happens after shorthand has patched the current config object.
Structural validation runs first using the schema generated from the Go config
structs, so users can see unknown fields and type problems together when
possible. Typed decode and `config.Validate` then enforce semantic constraints
such as mutual exclusion between `auth` and `auth_ref`, valid `operation_base`
relationships, references to shared auth profiles, and duration parsing.
Runtime checks that need the active CLI registry, such as available auth
handlers or TLS signer plugins, stay in the CLI layer.

## Example

```jsonc
{
  "auth_profiles": {
    "github-token": {
      "type": "bearer",
      "params": {
        "token": "env:GITHUB_TOKEN"
      }
    }
  },
  "apis": {
    "github": {
      "base_url": "https://api.github.com",
      "spec_url": "https://api.github.com/openapi.json",
      "command_layout": "flat",
      "profiles": {
        "default": {
          "headers": ["Accept: application/json"],
          "auth_ref": "github-token"
        },
        "enterprise": {
          "base_url": "https://github.example.com/api/v3",
          "headers": ["Accept: application/json"],
          "query": ["per_page=100"],
        },
      },
    },
  },
}
```

For:

```bash
restish --rsh-profile enterprise get github/repos/octo/example -q per_page=50
```

the effective request should use:

- the `enterprise` profile's `base_url`
- the profile's persistent headers
- the CLI-supplied `per_page=50` value

## Write Guarantees

Config writes are safety-sensitive because the file is user-authored and often
manually curated.

The design requires:

- cross-process file locking during mutation
- temp-file write, fsync, then rename
- preserving existing line endings where possible
- preserving comments and nearby formatting when commands edit object paths
- refusing writes that would corrupt structure or silently change unrelated
  content

Design 027 covers the patching behavior in more detail.

## Migration From v1

Restish v2 needs an explicit migration path from known v1 config locations and
filenames.

Migration expectations:

- detect legacy locations on supported platforms
- migrate or import on first use when safe
- write v1 backups atomically, using `.bak.v1` or a numbered `.bak.v1.N`
  directory when an existing backup contains different data
- remove the migrated v1 `apis.json` and `config.json` after the v2
  `restish.json` has been written and parsed successfully
- preserve API registrations, profiles, and auth config
- preserve comments when practical
- emit a clear hint when automatic migration is not possible

Users should not experience "my APIs disappeared" simply because the config path
changed between major versions.

## Credentials

Today, some auth material may still live in the main config file. That is
acceptable for an initial implementation, but the long-term design direction is
to separate credentials from ordinary config more cleanly, either through:

- a dedicated credentials file with restrictive permissions, or
- OS keyring integration

That refactor is compatible with this design as long as profile auth references
remain stable from the operator's point of view.

## Concurrency And Multi-Process Use

Restish should assume users may run multiple instances concurrently:

- two shells editing config
- one command refreshing tokens while another reads them
- automation plus interactive sessions

That means in-memory mutexes are not sufficient for persistence safety.
Cross-process locking is part of the design, not an optimization.

## Public Embedder Surface

External Go programs (custom CLIs, automation, IDE plugins) that want to read
or write Restish config without depending on the full CLI rendering stack can
import the top-level `config` and `auth` packages directly. These packages
expose:

- `config.Config` and the on-disk schema structs (`APIConfig`, `ProfileConfig`,
  `AuthConfig`, `CacheConfig`, `PaginationConfig`, `CredentialConfig`)
- `config.Load`, `config.Save`, `config.ParseConfigBytes`, `config.DefaultPath`,
  `config.Validate`, `config.ApplyURLOverrides` for strict read/write of
  `restish.json`
- `config.ConvertLegacyAPI`, `config.ReadLegacyAPI`, `config.ReadLegacyAPIs`
  for reading a v1 `apis.json` (or a single entry) and converting it to the
  v2 shape in memory, without going through the CLI's automatic migration
- `config.NewPaths`, `Paths.ConfigFile`, `Paths.Cache`, `Paths.TokenCache` for
  XDG-aware path discovery
- `auth.NewTokenCache`, `auth.LoadTokenCache`, `auth.SaveTokenCache`,
  `auth.DefaultTokenCachePath` for sharing the OAuth token cache with the
  restish CLI

The file-locking and atomic-write helpers used internally are not part of the
public surface; they live under `internal/fileutil` because they are tied to
the internal config write path.

The bundled auth-handler implementations (api-key, basic, bearer, oauth-*) and
the external-tool approval flow are intentionally not in the public `auth`
package; they live under `internal/auth` because their field shapes are tied
to the restish auth registry. External embedders that need OAuth should use
`golang.org/x/oauth2` directly.

Embedders that need the resolved auth header for a profile without going
through the restish CLI's full request flow should use
`restish api auth get <api> --print-header`; this prints the single header as
`Name: value` on stdout and exits non-zero for any non-header auth. This is
the stable, parseable contract for shell scripts and external Go programs.

## Alternatives Considered

### Separate Files For APIs And Global Config

Possible, but it makes discovery and editing harder for most users.

### Global Profiles Shared Across All APIs

This weakens the connection between profile names and the API-specific behavior
they control.

### Loose Schema With Best-Effort Field Preservation

This improves short-term flexibility but hurts confidence and debuggability.

## Relationship To Other Designs

- Design 004 consumes profile auth configuration.
- Design 006 consumes spec-related config.
- Design 033 defines credential bindings for OpenAPI operation security.
- Design 017 defines how env vars and flags override config.
- Design 027 defines comment-preserving edit behavior.
- Design 031 defines migration expectations from v1.
