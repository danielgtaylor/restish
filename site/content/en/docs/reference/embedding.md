---
title: Embedding Restish in Go
linkTitle: Embedding
weight: 115
description: Build a custom Go CLI on top of Restish with custom auth, formats, and bundled API defaults.
aliases:
  - /docs/reference/embedding/
---

Restish can be used as a Go library when an organization wants to ship a
branded CLI while keeping Restish's request pipeline, OpenAPI command
generation, auth, output, and plugin behavior.

Use out-of-process plugins for the stock `restish` binary. Use embedding when
you own the binary and need in-process defaults or extensions.

## Minimal Custom CLI

```go
package main

import (
	"os"

	restish "github.com/rest-sh/restish/v2"
)

func main() {
	cli := restish.New()
	if err := cli.Run(os.Args); err != nil {
		cli.HandleError(err)
	}
}
```

`Run` installs SIGINT/SIGTERM handling by default so Ctrl-C cancels in-flight
requests in the stock CLI. If your application already owns process signal
handling, disable Restish's handler before running:

```go
cli := restish.New()
cli.SetSignalHandling(false)
```

## Custom Auth

Register an auth handler under the `auth.type` name your config will use:

```go
cli := restish.New()
cli.AddAuthHandler("corp-token", corpTokenHandler{})
```

The handler implements `restish.AuthHandler`. Use the root package aliases,
such as `restish.AuthParam` and `restish.AuthContext`, rather than importing an
auth subpackage:

```go
type corpTokenHandler struct{}

func (corpTokenHandler) Parameters() []restish.AuthParam {
	return []restish.AuthParam{{Name: "token", Required: true, Secret: true}}
}

func (corpTokenHandler) Authenticate(ctx context.Context, req *http.Request, ac restish.AuthContext) error {
	req.Header.Set("Authorization", "Bearer "+ac.Params["token"])
	return nil
}
```

The handler receives the request context, profile name, params, token store,
and HTTP client used by Restish auth flows.

## Response Cache Safety

The stock CLI partitions cached API responses by API and profile. For example,
requests made through the `billing` API's `prod` profile use a separate cache
namespace from the same API's `default` profile, so credentialed responses are
not reused across profiles.

Embedders get that behavior when they call `Run` or `FetchResponse`, because
Restish applies the configured API/profile before building the request
transport and cache namespace. `FetchResponse` is the public API for
programmatic HTTP access; it applies authentication and profile settings when
the URL matches a configured API or API short name, and returns the normalized
response without pagination, filtering, streaming, or writing output.

Disable caching with normal Restish config or flags for ad hoc credentialed raw
requests that do not have a stable profile boundary.

## Custom Content Or Output

Embedder-facing registration methods let custom CLIs add content types,
encodings, link parsers, loaders, and formatters before running the CLI:

```go
cli := restish.New()
cli.AddContentType(&restish.ContentType{
	Name:      "ion",
	MIMETypes: []string{"application/ion"},
	Marshal:   marshalIon,
	Unmarshal: unmarshalIon,
})
cli.AddFormatter("corp-table", corpFormatter)
```

Custom spec loaders implement `restish.Loader`, whose parse method receives
`restish.LoadOptions` so local paths, source URLs, request context, and
cross-origin reference policy stay available during loading:

```go
func (corpLoader) LoadWithOptions(body []byte, opts restish.LoadOptions) (*restish.APISpec, error) {
	// Parse body and use opts.SourceURL or opts.LocalPath for diagnostics/ref resolution.
}
```

Prefer plugins when the extension should also work with the stock `restish`
binary. Prefer in-process registration when the custom CLI must ship a
company-specific default without an external executable.

## Bundled Defaults

A custom CLI can install in-memory bundled config before calling `Run`:

```go
cli := restish.New()
cli.SetDefaultConfig(defaultConfig)
```

User config overrides bundled defaults with the same API or auth-profile name.
Config-writing commands such as `api connect` keep those bundled defaults in
the running `CLI` after the write completes. A custom CLI can also load or write
a Restish config before calling `Run`, or ship project templates that users
connect with:

```bash
mycorp api connect billing https://billing.example.com --spec ./openapi.yaml
```

Keep config explicit. `RSH_CONFIG_DIR`, `RSH_CONFIG`, and `--rsh-config` keep
the same precedence rules as the stock binary.

## Single-API CLI

Embedders that ship a CLI dedicated to one API can promote that API to the
root command so its generated operations appear as top-level subcommands.

For a complete starting point, see
[`examples/example-cli`](https://github.com/rest-sh/restish/tree/main/examples/example-cli).

```go
cli := restish.New()
cli.SetCommandName("mycli")
cli.SetDefaultConfig(&restish.Config{
    APIs: map[string]*restish.APIConfig{
        "api": {
            BaseURL: "https://api.example.com",
            SpecURL: "https://api.example.com/openapi.yaml",
        },
    },
})
cli.SetCommandSurface(restish.CommandSurface{
    PromotedAPI: "api",
})
```

With `SetCommandSurface`, generated operations appear directly under the CLI's
root command instead of being nested under the API name. Support commands such
as `auth`, `cache`, `config`, `doctor`, `completion`, and `version` remain at
root by default. Move them under a namespace when the API's operation names need
the entire root:

```go
cli.SetCommandSurface(restish.CommandSurface{
    PromotedAPI:             "api",
    SupportCommandNamespace: "cli",
})
```

Or hide the support commands entirely:

```go
cli.SetCommandSurface(restish.CommandSurface{
    PromotedAPI:         "api",
    HideSupportCommands: true,
})
```

Global Restish flags (`--rsh-profile`, `--rsh-output-format`, ...) remain
available. The promoted API must be configured and its spec loadable when
generated commands are needed. For a reliable first run, configure `SpecURL`.

### Plugins in promoted CLIs

The `plugin` command is a support command. By default, this command stays at
the command root. `SupportCommandNamespace` moves this command into the
specified namespace. `HideSupportCommands` removes this command.

```text
mycli support plugin install ./restish-example
```

Restish keeps commands from installed command plugins at the command root.
Users can run these commands with generated API commands. Generic HTTP commands
and API-management commands stay hidden.

Restish reports a configuration error when a plugin command has the same name
as a promoted operation, a support command, or the support namespace. Use a
separate configuration file for a custom CLI. The separate file isolates its
plugins from the stock `restish` CLI.

## Related Pages

- [API Setup and Discovery](/docs/guides/api-setup-and-discovery/)
- [Authentication](/docs/guides/authentication/)
- [Plugins](/docs/plugins/)
- [Plugin Quickstart](/docs/plugins/quickstart/)
