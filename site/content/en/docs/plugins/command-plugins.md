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
- prompts or confirmations
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
err = c.Response(200, nil, map[string]any{"ok": ok, "apis": apis.APIs})
```

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

## Structured Progress

Use `Progress` for a plain status line. Use `ProgressUpdate` when a command can
report a stable identity, state, and optional step count:

```go
current, total := int64(2), int64(4)
err := c.ProgressUpdate(plugin.ProgressMsg{
  Text:    "Running step 2 of 4: fetch owner",
  ID:      "workflow",
  Label:   "Run workflow",
  State:   "running",
  Current: &current,
  Total:   &total,
  Message: "fetch owner",
})
```

`Text` is always the fallback. Restish prints it on stderr when no streaming
formatter named `progress` is installed. When that formatter is available,
Restish keeps one formatter session open and sends it the structured records.
Older Restish versions ignore the additional fields and continue printing
`Text`.

`current` and `total` are optional, but must be supplied together. Use the same
`id` for updates to one task, and send a terminal `state` such as `success` or
`failed` on its final update. Set `unit` when the count is not steps. Progress
remains on stderr and never changes the command's primary stdout result.

## Lifecycle

1. The plugin declares commands during startup discovery.
2. Restish starts the plugin when the user runs the contributed command.
3. The plugin sends requests such as `http-request`, `api-spec`, `prompt`, or `response`.
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
