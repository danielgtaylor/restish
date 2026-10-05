# Custom CLI Embedding Surface

Status: accepted.

## Problem

Restish can already be embedded in a branded Go binary. An embedder can set the
command name, install default config, register auth handlers, loaders,
formatters, content types, encodings, and link parsers, then call `Run`.

That is enough to build a custom binary, but not enough to make a polished
single-API CLI. A binary such as `acme` should feel like the Acme API CLI, not
like generic Restish with a different executable name:

```text
acme list-users
acme get-user 123
acme auth header
acme cache clear
```

The current stock shape keeps generated API operations under the configured API
name:

```text
acme api list-users
```

or, if the API is also named `acme`:

```text
acme acme list-users
```

That is a poor first impression for a shipped product CLI. At the same time,
removing every Restish support command is too strict: auth inspection, cache
cleanup, config display, shell completion, version output, and diagnostics are
useful for scripting, CI, support tickets, and user recovery.

## Goals

- Let an embedder promote one configured API's generated operations to the CLI
  root.
- Keep the promoted API's generated commands present in help and dispatch on
  first run by loading the configured spec source before commands are needed.
- Keep a small support surface that uses app vocabulary rather than leaking the
  Restish implementation name.
- Preserve commands contributed by plugins installed in the active config.
- Preserve scriptability for auth headers, cache cleanup, config inspection,
  diagnostics, completion, and version output.
- Keep the public API small enough to maintain permanently.

## Non-Goals

- Do not expose the Cobra root command as the primary embedding API.
- Do not add arbitrary custom Go command registration in the first pass.
- Do not add generated-operation invocation by operation ID from Go in the
  first pass.
- Do not add a public embedded OpenAPI bytes helper in the first pass.
- Do not add an immutable "never update OpenAPI from the network" embedded-spec
  mode in the first pass.
- Do not bundle out-of-process plugin executables into custom binaries.

## Current Constraints

The stock root command is built by the central `CLI` runtime. It owns global
flags, grouped help, completions, builtin command families, generated API
commands, plugin command discovery, request execution, auth, cache paths, and
diagnostics.

Generated OpenAPI commands are currently added as API command groups. The
runtime loads generated metadata from cache when possible, rebuilds operation
metadata from stale raw spec cache when needed, and can refresh stale remote
metadata on generated command use. Local spec files are authoritative and
invalidate stale metadata when changed.

Stock startup and help intentionally do not trigger remote spec discovery. They
load cached generated metadata, rebuild from raw cache, or reload local spec
files, but a missing remote cache leaves the generated command group absent
until the user runs `api sync` or invokes a generated-looking command under an
API short name. That is the right default for the generic `restish` binary, but
it is not enough for a branded promoted-root CLI: `acme --help` and
`acme list-users` cannot depend on the user knowing to run a Restish-flavored
sync command first.

The existing discovery layer already supports explicit `spec_url`, local
`spec_files`, and normal OpenAPI discovery from the API base URL. It writes the
raw spec cache and extracted operation metadata in the same cache entry. The
custom CLI work should reuse that machinery rather than add a new first-run
spec source before it is proven necessary.

The embedding API must not make embedders depend on internal packages or the
exact Cobra tree. Anything exported from the root `restish` package is a
long-term public promise.

## Proposed Public Shape

Names are placeholders until the implementation design is accepted. The desired
developer experience is:

```go
package main

import (
	"fmt"
	"os"

	restish "github.com/rest-sh/restish/v2"
)

func main() {
	cli := restish.New()
	cli.SetCommandName("acme")
	cli.SetCommandDescription("Acme API CLI", "")
	cli.SetDefaultConfig(&restish.Config{APIs: map[string]*restish.APIConfig{
		"api": {
			BaseURL: "https://api.acme.com",
			SpecURL: "https://api.acme.com/openapi.yaml",
		},
	}})
	cli.SetCommandSurface(restish.CommandSurface{
		PromotedAPI: "api",
	})

	if err := cli.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

The command surface options are intentionally a small public struct:

```go
type CommandSurface struct {
	PromotedAPI string

	SupportCommandNamespace string
	HideSupportCommands     bool
}
```

The design intent is:

- The zero value preserves the stock Restish surface.
- `PromotedAPI` promotes generated operations from the configured API to the
  root command.
- The promoted API is the primary API. There is no separate primary/default API
  concept in the first pass.
- Support commands, including plugin management, stay at root by default when
  an API is promoted.
- Commands contributed by command plugins installed in the active config stay
  at root. Other stock command families remain hidden.
- `SupportCommandNamespace` moves support commands under that command name, for
  example `acme cli cache clear`.
- `HideSupportCommands` removes support commands from the custom CLI surface.
- `SupportCommandNamespace` and `HideSupportCommands` are mutually exclusive.
- Support-command layout fields without `PromotedAPI` are invalid in the first
  version.
- Examples should configure `SpecURL` for predictable first-run behavior. A
  promoted API may also use local `SpecFiles` or the normal discovery flow from
  `BaseURL`, but a custom CLI that wants reliable help should provide an
  explicit spec URL when possible.
- There is no separate embedded spec API in the first version.

## User-Facing Command Surface

The default single-API surface is:

```text
acme <generated-operation> [...]
acme auth
acme cache
acme config
acme doctor
acme completion
acme plugin
acme version
acme <command-plugin-command> [...]
```

The default single-API surface hides stock Restish control-plane commands that
would distract from a branded API CLI:

```text
api
get|head|options|post|put|patch|delete
shell
links
cert
```

Embedders may choose a fuller surface, hide support commands, or move the
support commands under an embedder-owned namespace:

```text
acme cli cache clear
acme cli auth header
```

Examples should use `cli` as the generic namespace because it describes support
commands without exposing Restish as an implementation detail. Embedders may
choose names such as `admin`, `support`, or `system`.

## App-Shaped Support Commands

Support commands at root must use the branded command name and product language
in help text. They should not say "Restish" unless the text is explicitly about
diagnostics for maintainers.

### Auth

Root `auth` exists for scripting and troubleshooting auth for the promoted API.
It is a thin wrapper over existing `api auth` internals with the promoted API
name injected. It must not duplicate token resolution logic.

Supported root sugar:

```text
acme auth get [credential-id]
acme auth get [credential-id] --operation <operation> [--print-header]
acme auth header [credential-id] [--operation <operation>]
acme auth inspect [--operation <operation>] [--credential <id>] [--redact]
acme auth logout [--all-profiles] [--auth-profile <name>]
```

`auth header` is the preferred scripting primitive. It prints exactly:

```text
Name: value
```

and exits non-zero for query auth, cookie auth, missing auth, or multi-header
auth. Restish should not add `auth token` as the first root sugar because many
auth schemes are not bearer tokens.

Root `auth add` and `auth remove` are deferred. If a custom CLI needs
user-managed credential bindings before root sugar is expanded, the fuller
advanced auth tree can live under the support namespace.

### Cache

Root `cache` reuses existing `cache info` and `cache clear` behavior. Do not
change `cache clear` semantics silently: without an argument, it clears all HTTP
cache entries.

In the default single-API surface, generic HTTP and direct URL commands are
hidden, so clearing all HTTP cache entries is acceptable. If a fuller surface
keeps generic HTTP commands, `cache clear <api>` and `cache clear --direct`
retain their existing meaning.

### Config

Root `config` reuses `path`, `show`, `set`, `edit`, and theme commands where
retained. Help text must be app-shaped, for example "Manage local Acme CLI
configuration" rather than "Manage local Restish configuration."

`config show -o json` remains redacted and scriptable.

### Doctor

Root `doctor` diagnoses runtime paths, config, cache, auth cache permissions,
and the promoted API's generated operation status.

`doctor api` with no API argument diagnoses the promoted API. Full
`doctor api <name>` and `doctor plugin <name>` belong in fuller surfaces or the
support namespace.

TTY diagnostics should use app vocabulary. JSON diagnostics may include enough
Restish-specific detail for maintainers and support workflows.

### Completion And Version

`completion` and `version` remain available at root by default.

`--version` must keep working even when the `version` command is hidden or moved
under a namespace.

## Spec Freshness And First-Run Help

A custom single-API CLI should never show an empty operation list merely because
the user has not run a sync command.

For a promoted API, Restish should build the command tree from the best
available source:

1. fresh generated operation metadata
2. fresh raw spec cache that can rebuild operation metadata
3. the configured spec source, fetched or reloaded through the existing
   discovery path and cached with extracted operations
4. stale generated operation metadata or stale raw spec cache as last-known-good
   when refresh fails
5. a clear diagnostic if no source can produce generated operations

The fetch-on-demand behavior is specific to promoted custom CLIs. The stock
`restish` command should keep its current no-network startup and generic help
behavior.

Top-level help, generated operation help, and generated command execution for a
promoted API may trigger spec discovery when no fresh command metadata is
available. If stale metadata exists, Restish should attempt a bounded refresh
and fall back to the stale last-known-good tree when refresh fails. The initial
timeout should reuse the generated metadata refresh timeout, currently three
seconds, so promoted custom CLIs do not introduce a second freshness policy.

Completion should use the same metadata policy where practical, but shell
completion must remain responsive. If completion cannot refresh within the
bounded timeout, it should fall back to cached or stale command names rather
than hanging the shell.

Unknown-command handling for promoted roots must account for newly added remote
operations. If a token looks like a generated operation and the promoted API has
a refreshable spec source, Restish should try a bounded sync before returning an
unknown-command error.

`doctor` should report whether generated operations are fresh, stale, refreshed
on demand, refresh-failed with stale fallback, or unavailable.

## Command Collision Rules

Generated operations, app support commands, and the optional support namespace
must not silently shadow one another.

Command-plugin commands must not collide with promoted operations, app support
commands, or the support namespace. Such collisions fail startup with an
actionable configuration error.

If a promoted operation collides with a root support command such as `auth`,
`cache`, or `doctor`, startup should fail with an actionable error that suggests
moving support commands under a namespace or hiding them.

If the chosen support namespace collides with a promoted operation, startup
should fail with the same kind of actionable error.

The stock Restish surface remains unchanged unless the embedder opts into a
custom command surface.

## Command Tree Construction

The implementation should reuse the stock Restish command construction path
instead of maintaining a second root builder for custom CLIs:

- Build the normal Restish root command first.
- Load generated commands through the existing generated-command path.
- For promoted APIs, ensure generated metadata is available before root help,
  generated operation help, completion, or command dispatch needs the promoted
  command tree.
- Reuse existing spec discovery and cache writes for configured `SpecURL`,
  `SpecFiles`, and normal base-URL discovery.
- Transform the command tree for the selected surface instead of maintaining a
  separate root builder.
- Skip adding the promoted API's normal wrapper/short-name command at root.
- Move, hide, or retain builtin support commands according to
  `SupportCommandNamespace` and `HideSupportCommands`.
- Capture installed command-plugin commands from the stock root before
  transforming it, then re-add those existing command nodes without changing
  their help, flags, completion, grouping, or execution behavior.
- Add only the minimal app-shaped auth sugar described above.
- Reuse cache, config, and doctor behavior where possible, adjusting help text
  and primary-API defaults rather than duplicating implementation.

## Alternatives

### Promote Operations And Hide All Support Commands

This gives the ergonomic root promotion but removes useful operational tools
such as auth inspection, cache cleanup, config display, completion, version, and
diagnostics. It also pushes embedders toward rebuilding those tools in custom
code.

### Keep API Operations Under A Namespace

This preserves current architecture but leaves custom CLIs with awkward command
shapes such as `acme api list-users` or `acme acme list-users`.

### Expose Raw Cobra Mutation

This gives embedders maximum flexibility, but it turns the internal command tree
into public API and makes help, completion, grouping, generated commands, and
compatibility harder to preserve.

### Add Full Custom Command Registration Now

Custom Go commands are useful, but they need their own design for flags, args,
I/O, global flags, completion, command grouping, and collision behavior. They
are future work, not required to make single-API promotion useful.

### Bundle OpenAPI Bytes In The First Pass

A public helper for `go:embed` OpenAPI bytes is useful for offline help,
pre-auth environments, and traditional CLIs that want command changes to follow
binary releases. It also adds public API, cache identity, freshness,
user-override, and diagnostics questions. Since a custom CLI that can reach the
API should usually be able to fetch that API's OpenAPI document, the first pass
should make configured spec fetch and cache behavior reliable before adding an
embedded-spec helper.

### Add Immutable Embedded Specs Now

A "never update OpenAPI from the network" mode is useful for traditional
release-driven CLIs, but it depends on the embedded-spec helper above and needs
explicit decisions about user config overrides, sync behavior, and diagnostics.

## Compatibility And Migration

The default `restish` command surface remains unchanged.

All new behavior is opt-in through the embedding API. A custom CLI that promotes
an API opts into bounded spec fetches before help, completion, or generated
command dispatch when cached metadata is missing or stale. Once exported,
embedding types and methods must be treated as public and maintained
additively.

The public API must not expose internal packages, cache paths, Cobra command
objects, or implementation-specific command grouping as stable contracts.

## Security And Privacy

OpenAPI documents may contain server URLs, examples, schemas, and operation
metadata. They should not contain secrets. Diagnostics should avoid printing
credential material or unredacted auth configuration.

Promoted-root help and completion can make network requests when metadata is
missing or stale. That behavior must stay bounded and must use the existing
discovery transport, auth, TLS, redaction, and cross-origin protections.

`auth header` prints credential material by design and must write only the
single header line to stdout on success. Errors and diagnostics go to stderr via
normal CLI error handling.

Config and doctor output must preserve existing redaction behavior.

## Testing Plan

Add focused tests for:

- promoted operation execution at root
- promoted operation help at root on first run from configured `SpecURL`
- promoted operation help when only stale cached metadata is available and
  refresh fails
- remote `spec_url` refresh populating raw spec and generated operation caches
- local `SpecFiles` reload for promoted root help and execution
- stale metadata fallback when refresh fails
- unknown promoted operation triggering bounded refresh before error
- collision with root support commands
- collision with support namespace
- support commands retained at root
- support commands moved under a namespace
- support commands hidden
- `auth get`, `auth header`, `auth inspect`, and `auth logout` for the promoted
  API
- `auth header` failures for query, cookie, missing, and multi-header auth
- cache clear semantics unchanged
- branded config and doctor help text
- completion and `--version` behavior
- compile-time embedding example using the root `restish` package

## Documentation Impact

When implemented, update:

- root package Go docs for embedding
- a custom CLI embedding guide on the docs site
- command reference/help generated regions if public command shape changes
- this design record with the accepted public API names

## Open Questions

- How should JSON doctor output represent generated operation metadata source,
  freshness, and the last refresh error?
- Should the first official custom CLI example live under `examples/`, the docs
  site, or both?
