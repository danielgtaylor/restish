# Restish Plugin Quickstart

This guide is the shortest path from "I want to extend Restish" to a working
plugin binary.

If you want design rationale, read the records in
[`docs/design/`](./design/README.md). If you want the smallest practical path,
start here.

## Choose A Plugin Type

Use the smallest plugin shape that fits your job:

- Hook plugin: one request in, one reply out. Best for auth, request/response
  middleware, custom spec loaders, and output formatters.
- Command plugin: a long-lived top-level command such as `restish mcp serve ...` or
  `restish bulk ...` that can ask the host to make HTTP requests.
- TLS signer plugin: advanced mTLS use cases where the private key must stay
  outside the Restish process.

Start with a hook plugin unless you know you need a custom command lifecycle.

## The Wire Protocol

All messages between Restish and plugins — including manifest responses,
command declarations, hook inputs/outputs, and command-plugin runtime messages —
are plain CBOR data items written directly to stdin/stdout. CBOR is
self-delimiting, so no length prefix or other framing is needed. Any language
with a CBOR library can implement a plugin.

The startup flags (`--rsh-plugin-manifest`, `--rsh-plugin-commands`) use the
same format as runtime messages: write one CBOR map to stdout and exit. Command
discovery responses include `protocol_version`; Go plugins get that field from
`plugin.WriteCommands`.

## The Public Helper Package

Plugin authors in Go should build against the public
[`plugin`](../plugin/plugin.go) package.

The helpers you will use most often are:

- `plugin.WriteMessage` and `plugin.ReadMessage` for CBOR messages (one-shot);
  `plugin.NewDecoder` + `(*Decoder).ReadMessage` for streaming reads (command
  and TLS-signer plugins that receive multiple messages on the same stdin)
- `plugin.WriteManifest` and `plugin.WriteCommands` for startup responses
- `plugin.HandleStartupFlags` for `--rsh-plugin-manifest` and
  `--rsh-plugin-commands`
- `plugin.Run` for simple command plugins
- `plugin.CommandClient` for command plugins that delegate HTTP and terminal
  output back to Restish

## Smallest Formatter Plugin

Formatter plugins are a good first plugin because they only need to read
formatter messages from stdin and write final bytes to stdout.

```go
package main

import (
	"fmt"
	"os"

	"github.com/rest-sh/restish/v2/plugin"
)

func main() {
	manifest := plugin.Manifest{
		Name:              "hello-format",
		Version:           "0.1.0",
		Description:       "Example formatter plugin",
		RestishAPIVersion: 2,
		Hooks:             []string{"formatter"},
		FormatterNames:    []string{"hello"},
	}
	if plugin.HandleStartupFlags(os.Stdout, manifest, nil) {
		return
	}

	dec := plugin.NewDecoder(os.Stdin)
	var req plugin.FormatterRequest
	if err := dec.ReadMessage(&req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for {
		switch req.Event {
		case "start":
			if req.Response.Body != nil {
				fmt.Fprintf(os.Stdout, "hello: %#v\n", req.Response.Body)
			}
		case "item":
			fmt.Fprintf(os.Stdout, "hello: %#v\n", req.Response.Body)
		case "end":
			return
		}
		if err := dec.ReadMessage(&req); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
```

Build it as `restish-hello-format`, install it into the Restish plugin
directory, then run:

```bash
restish get https://httpbin.org/json -o hello
```

The same `formatter` message type is used for ordinary responses, pagination,
and event streams. The sequence is always:

1. `event: "start"`
2. zero or more `event: "item"`
3. `event: "end"`

For a normal non-streaming response, Restish usually includes the full body on
the `start` message. The CSV plugin in `cmd/restish-csv/` is the reference
implementation for a stateful formatter.

## Smallest Command Plugin

Command plugins contribute top-level commands and can delegate authenticated
HTTP back to the host instead of building their own client stack.

```go
package main

import "github.com/rest-sh/restish/v2/plugin"

func main() {
	manifest := plugin.Manifest{
		Name:              "hello-cmd",
		Version:           "0.1.0",
		Description:       "Example command plugin",
		RestishAPIVersion: 2,
		Hooks:             []string{"command"},
	}
	commands := []plugin.CommandDecl{
		{Name: "hello", Short: "Print a greeting"},
	}

	plugin.Run(manifest, commands, func(command string, args []string, c *plugin.CommandClient) error {
		return c.WriteStdout([]byte("hello from plugin\n"))
	})
}
```

That binary will show up as:

```bash
restish hello
```

To make authenticated delegated requests from a command plugin:

```go
resp, err := c.Do(&plugin.HTTPRequestMsg{
	Method: "GET",
	URI:    "myapi/users",
})
```

Restish handles auth, TLS, retries, cache settings, and response normalization
before the reply comes back to your plugin.

Prefer `CommandClient` helpers over hand-written protocol messages for host
capabilities that wait for a reply:

```go
apis, err := c.ListAPIs()
profiles, err := c.ListProfiles("myapi")
cfg, err := c.ConfigRead("myapi", "default", "hello-cmd")
answer, err := c.Prompt("Label", false)
ok, err := c.Confirm("Continue?")
choice, err := c.Select("Choose an environment", []plugin.SelectOption{
	{Label: "Development", Value: "dev"},
	{Label: "Production", Value: "prod"},
})
err = c.Response(200, nil, map[string]any{"configured": cfg.PluginConfig != nil})
```

Plugins that call `Select` must include `plugin.FeatureCommandSelect` in the
manifest's `RequiredFeatures`. Restish keeps the picker on stderr and returns
the selected option's opaque value. Selection requires an interactive terminal
unless only one option is available.

The context-aware variants (`ListAPIsContext`, `ConfigReadContext`,
`PromptContext`, `SelectContext`, and friends) let plugins bound waits when the
host is busy or the user does not answer.

## Local Development Loop

1. Build the plugin binary with a `restish-` prefix.
2. Install it with `restish plugin install ./restish-name`.
3. Check startup output directly:

```bash
./restish-name --rsh-plugin-manifest
./restish-name --rsh-plugin-commands
```

4. Use `restish plugin list` to confirm discovery.
5. Use `restish plugin debug <name> ...` when you need to inspect CBOR traffic.
   Expected traces start with startup discovery (`manifest`, and `commands` for
   command plugins), then show hook messages such as auth, request middleware,
   response middleware, formatter, loader, or TLS signer sign/shutdown frames
   depending on the plugin's manifest.

## Good Reference Implementations

These are the best examples in the repo:

- [`cmd/restish-csv/main.go`](../cmd/restish-csv/main.go)
  for a small formatter hook plugin
- [`cmd/restish-mcp/main.go`](../cmd/restish-mcp/main.go)
  for a real command plugin
- [`internal/cli/testdata/testplugin/main.go`](../internal/cli/testdata/testplugin/main.go)
  for tiny command, hook, and TLS signer test fixtures
- [`docs/design/019-hook-plugins.md`](./design/019-hook-plugins.md)
  for hook payload shapes
- [`docs/design/020-command-plugins.md`](./design/020-command-plugins.md)
  for command-plugin protocol details

## Plugin Ideas That Should Stay Out Of Core For Now

Some useful features fit better as plugins or wrappers than as built-in Restish
behavior:

- Page/count pagination strategies for APIs that do not expose standard links.
  A response-middleware plugin can inspect the first response and ask Restish to
  follow calculated page URLs while the host still owns auth, retries, TLS, and
  output.
- Swagger/OpenAPI 2.0 loading. A loader plugin can convert Swagger 2.0 into
  OpenAPI 3.x and return that document to Restish, keeping generated command
  creation on the canonical OpenAPI path.
- Rate-limit experiments or light load-test workflows. A command plugin can
  own pacing, concurrency, and reporting while delegating each request to the
  host.

Use these patterns when the behavior is valuable but provider-specific, still
experimental, or too workflow-shaped for a generic request flag.

## Common Pitfalls

- Plugin executables must be named `restish-<name>`.
- All messages — startup responses and runtime messages alike — are plain CBOR
  data items. Use `plugin.WriteMessage` for all writes. For reads, use
  `plugin.ReadMessage` for one-shot hook plugin reads; for command and
  TLS-signer plugins that loop over messages, create a `plugin.NewDecoder` once
  and call `ReadMessage` on it throughout.
- Formatter plugins write final bytes to stdout directly; they do not send a
  CBOR reply envelope.
- Formatter plugins receive a sequence of `formatter` messages with
  `start`/`item`/`end` events, not a single one-shot request.
- If you start a subprocess or long-lived goroutine inside a plugin, make sure
  it exits cleanly when stdin closes.
