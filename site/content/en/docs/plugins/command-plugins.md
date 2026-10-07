---
title: Command Plugins
linkTitle: Command Plugins
weight: 40
description: Author top-level Restish workflows that exchange messages with the host.
---

Command plugins add root commands such as `bulk` and `mcp`. They can perform
multi-step workflows while delegating HTTP, config, prompts, and output back to
Restish.

## When To Use One

Use a command plugin when a feature needs:

- a top-level command
- multiple HTTP requests
- progress messages
- prompts, confirmations, or interactive selection
- access to registered APIs and profiles

Use a hook plugin for one request/response/auth/formatting task.

## Delegated HTTP

Go command plugins should use `plugin.CommandClient` helpers instead of
hand-writing CBOR messages. Delegated HTTP uses `Do`:

```go
resp, err := c.Do(&plugin.HTTPRequestMsg{
  Method: "GET",
  URI:    "https://api.rest.sh/items",
})
```

That preserves host profiles, auth, TLS signer behavior, retries, cache,
and output normalization. Each delegated `http-request` returns one normalized
response. If a command plugin wants pagination, it should send follow-up
requests itself.

To inspect a registered API, call `FetchAPISpec` or `FetchAPISpecContext` with
the API name and, when needed, the profile whose server variables should be
used:

```go
spec, err := c.FetchAPISpecContext(ctx, "example", "staging")
```

Other host-owned workflows have helpers too:

```go
apis, err := c.ListAPIs()
profiles, err := c.ListProfiles("example")
cfg, err := c.ConfigRead("example", "default", "my-plugin")
answer, err := c.Prompt("Label", false)
ok, err := c.Confirm("Continue?")
choice, err := c.Select("Choose an environment", []plugin.SelectOption{
  {Label: "Development", Value: "dev"},
  {Label: "Production", Value: "prod"},
})
err = c.Response(200, nil, map[string]any{"ok": ok, "apis": apis.APIs})
```

`Select` renders a host-owned picker on stderr. Up and Down move through the
options, Enter returns the selected value, and Ctrl-C cancels the picker.
Labels are for display; values remain opaque to the host. Multiple options
require an interactive terminal and cannot be used by commands with
`passthrough_stdio`.

Plugins that call `Select` must declare the additive feature in their manifest:

```go
RequiredFeatures: []string{plugin.FeatureCommandSelect},
```

This makes older Restish hosts reject the plugin during discovery instead of
hanging on an unknown request.

Wait for each host interaction before starting another, and avoid writing
terminal output while a picker is active.

Non-Go plugins can send the same message families directly:

```json
{
  "type": "http-request",
  "method": "GET",
  "uri": "https://api.rest.sh/items"
}
```

```json
{
  "type": "api-spec",
  "name": "example",
  "profile": "staging"
}
```

## Lifecycle

1. The plugin declares commands during startup discovery.
2. Restish starts the plugin when the user runs the contributed command.
3. The plugin sends requests such as `http-request`, `api-spec`, `select`, or `response`.
4. The plugin sends `done` with an exit code.

## Real Examples

- `restish-bulk` manages API collections as local files.
- `restish-mcp` exposes registered API operations as MCP tools.

## Related Pages

- [Plugin Quickstart](../quickstart/)
- [Plugin Messages](/docs/reference/plugin-messages/)
- [Plugin Manifest](/docs/reference/plugin-manifest/)
- [Bulk Management](/docs/plugins/bulk-management/)
- [MCP](/docs/plugins/mcp/)
