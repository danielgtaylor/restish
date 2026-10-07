# Setup And Completions

## Summary

Restish v2 supports two shell-facing behaviors that make the CLI easier to use
interactively:

- shell completion scripts
- shell setup that installs the supported per-shell wrapper or alias needed to
  keep Restish input predictable

These are separate concerns on purpose: completion teaches the shell what
arguments exist, while setup teaches the shell not to eagerly expand Restish
input that is meant for Restish itself.

## Goals

- leverage standard shell completion support instead of inventing a custom
  mechanism
- preserve raw CLI input for shorthand and filter expressions
- support common interactive shells with minimal custom integration
- keep setup behavior inspectable, idempotent, and safe

## Non-Goals

- implementing one giant shell-integration system that mixes completion,
  aliases, and environment setup into one opaque step
- promising shell support where Restish cannot actually provide correct
  `noglob`-style protection

## Two Different Features

### Completion

Completion is about discovery:

- command names
- flags
- enum-backed values
- generated API commands

### Setup

Setup is about argument preservation:

- shorthand expressions
- bracket-heavy filters
- wildcard-like syntax that shells would otherwise expand first

Treating these as separate features lets users opt into one without assuming
they want the other.

## Completion Design

Completion generation is delegated to Cobra's built-in shell completion support.
That keeps Restish aligned with standard Go CLI behavior and automatically
covers both built-in commands and generated commands that are part of the Cobra
tree.

`shell completion <shell>` is the canonical v2 stdout generator so package
managers, shell frameworks, and advanced users can install the script using
their preferred mechanism. The hidden top-level `completion` command may remain
as a compatibility alias, but it is not part of the published v2 command
surface and should not appear in user-facing examples.

The v2 completion target is bash, zsh, fish, and PowerShell when PowerShell can
be supported through Cobra's standard generator without a large custom
integration. If PowerShell support turns out to require a separate shell setup
model, narrow `shell completion` to bash/zsh/fish for the initial release and
document PowerShell completion as future work.

Completion should reflect the real command tree, which means it can include:

- built-in commands
- generated API command groups
- plugin-contributed command roots when they are registered at startup

Where Restish has enough metadata, completion should also surface:

- enum-backed parameter values
- known output-format names
- profile names and similar finite sets
- API-aware URL paths from generated operation metadata

Parameter-level `x-cli-completion` is the explicit opt-in exception to offline
completion. It may call a declared `GET` or `HEAD` operation from the same
synced operation set to obtain values for a generated argument or flag. Static
enum metadata takes precedence.

The completion provider uses the normal profile, TLS, parameter serialization,
origin, and built-in authentication paths, but completion remains
non-interactive. It never retries, paginates, starts local plugins, runs command
secret sources, or prompts. Requests and decoded results have fixed time, body,
candidate-count, and candidate-length limits. Errors produce no candidates or
human diagnostics. A short, bounded cache avoids a request on repeated `Tab`
invocations for unauthenticated providers with the same API, profile, provider,
bindings, selectors, and typed prefix. Authenticated candidates are not
persisted.

For zsh, `shell setup zsh` writes the generated script under Restish's effective
config directory, not the cache directory, then adds a managed source block to
`~/.zshrc`. The script is generated state, but shell startup depends on it
being present; cache cleanup should not break completion. Fish is simpler:
`shell setup fish` writes the generated script to fish's user completions
directory, respecting `XDG_CONFIG_HOME` and falling back to `~/.config`.

### API-Aware URL Completion

Generated API commands are not the only interactive surface for operation
metadata. Users may also prefer generic URL requests such as:

```bash
restish example/images/<TAB>
restish get example/images/<TAB>
restish delete example/items/abc/<TAB>
```

URL completion uses cached OpenAPI operation metadata to suggest operation URL
templates for the active HTTP method. It must not perform network discovery
during shell completion. Local spec files and already-cached remote specs may
be parsed or upgraded into cached operation metadata, but missing remote specs
should produce no URL candidates until the user runs `api sync`.

The completion resolver preserves the form the user started with:

- API short-name paths complete as `example/...`
- full URLs complete as full URLs
- scheme-less URLs complete without adding `https://`

Path template variables are not inserted literally. Completion stops before an
unfilled path variable, so `example/formats/` stays ready for the user to enter
the value. When the path parameter has enum values, Restish offers those enum
values as the next completions. Path template variables are still carried
forward when the user has already typed a concrete segment value. For example,
`example/items/my-id` can complete to `example/items/my-id/tags/`. Candidate
descriptions come from the operation summary when available, and otherwise fall
back to `METHOD /path`.

URL completion follows the same profile, server-variable, and `operation_base`
selection rules as generated operations. Hidden generated operations are not
shown. Generic verb commands filter candidates by verb, while the root bare-URL
form uses `GET`.

## Setup Design

`shell setup <shell>` appends a shell snippet to the relevant rc file that
configures `restish` with the supported per-shell integration. For zsh this is
the shell's native `noglob` precommand. Other shells must use a shell-specific
wrapper that genuinely preserves Restish arguments, or they should not be
advertised as setup-supported. Fish has a managed function path and can be
paired with completion installation, but it should not be described as
zsh-style `noglob`.

The built-in setup currently supports:

- `zsh`
- `bash`
- `fish`

Shells without a supported wrapper or alias should not be advertised as
supported by `shell setup` just because they support completion.

## Rc File Selection

Rc file selection is part of the design, not an incidental implementation
detail.

Expected behavior:

- `zsh` -> `~/.zshrc`
- `bash` on macOS login-shell workflows -> `~/.bash_profile`
- `bash` on other Unix-like systems -> `~/.bashrc`
- `fish` -> `$XDG_CONFIG_HOME/fish/config.fish` or
  `~/.config/fish/config.fish`

This keeps `shell setup` aligned with where those shells actually load user
startup config in common environments.

## Idempotence And Confirmation

Setup should be idempotent:

- do not append the same alias repeatedly
- detect when the configured line already exists

Setup should also be operator-friendly:

- support dry-run behavior
- support explicit confirmation bypass for automation
- write helpful output telling the user what changed

## Shell Detection And Hints

Restish may offer first-run hints based on shell detection, but hints are not
the same as setup support.

The design rule is:

- hints may suggest a supported shell setup command
- unsupported shells may still get completion guidance
- detection heuristics such as `$SHELL` should be treated as best-effort hints,
  not as proof of the currently running shell

## Why `noglob` Protection Matters

Restish syntax often includes characters shells like to interpret:

- `[]`
- `*`
- `?`
- `{}` in some shell contexts

Without setup or careful quoting, the shell may transform those expressions
before Restish ever sees them. Setup exists to reduce that day-to-day friction.

## Examples

Generate a completion script:

```bash
restish shell completion zsh
restish shell completion bash
restish shell completion fish
restish shell completion powershell
```

Install zsh completion for a user-managed Restish install:

```bash
restish shell setup zsh
```

Install fish completion:

```bash
restish shell setup fish
```

Configure the shell alias:

```bash
restish shell setup zsh
```

Configure only the shell wrapper:

```bash
restish shell setup zsh --no-completion
```

which appends a line like:

```text
alias restish="noglob restish"
```

to the shell rc file for shells that support that alias style.

Generated command completions can also benefit from OpenAPI-derived enum values.

## Alternatives Considered

### Rely On Users To Disable Globbing Manually Every Time

Possible, but too error-prone for day-to-day use.

### Write Custom Completion Systems Per Shell

Too much maintenance burden for too little benefit.

### Fold Setup And Completion Into One Command

These solve related but distinct problems and should stay separate.

## Relationship To Other Designs

- Design 007 explains where generated commands come from.
- Design 017 defines truth-in-help and shell-facing CLI behavior.
- Design 001 defines runtime-owned prompting and file-write behavior.
