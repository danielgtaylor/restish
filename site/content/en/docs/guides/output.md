---
title: Output
linkTitle: Output
weight: 40
description: Understand how Restish decodes, normalizes, filters, and renders responses.
aliases:
  - /docs/guides/output/
  - /docs/recipes/show-only-response-headers/
extra_js:
  - js/restish-docs-interactions.js
---

Restish output is built around one rule: stdout carries the selected HTTP
exchange parts, while stderr carries diagnostics, warnings, progress, and
verbose traces.

## Processing Model

{{< restish-output-pipeline >}}

## Choose A Format

{{< restish-example >}}
restish api.rest.sh/images
{{< /restish-example >}}

{{< restish-example >}}
restish api.rest.sh/images -o json
{{< /restish-example >}}

{{< restish-example >}}
restish api.rest.sh/images -o yaml
{{< /restish-example >}}

{{< restish-example >}}
restish api.rest.sh/images -o table --rsh-columns name,format,self
{{< /restish-example >}}

```bash
restish api.rest.sh/images -o ndjson -f body.self
```

Use shortcuts for common response metadata:

```bash
restish api.rest.sh/status/204 --rsh-status
restish api.rest.sh/ --rsh-headers
```

`auto` is the default output format. Output formats render the selected
body/value; `--rsh-print` controls whether request or response headers appear
around that rendered value. In an interactive terminal with no explicit filter,
Restish prints the response status line, headers, and formatted body to stdout.
Redirected output preserves raw response body bytes when there is no filter,
metadata shortcut, collection, or explicit `-o` format. That raw-download path
bypasses response middleware plugins so installed plugins cannot rewrite saved
files. When you do ask Restish to select or transform a value, redirected output
is pretty by default. Use `--rsh-print=b` for compact rendered output.
`json`, `yaml`, and `cbor` are document formats.
`ndjson` is a record format for structured streams, and `lines` is for
shell-friendly scalar values.

## Document vs Record Output

Use document output when the next program expects one complete value. Make
collection explicit when pagination should become one redirected document:

```bash
restish api.rest.sh/images --rsh-collect > images.json
```

Use record output when you want one item per line:

{{< restish-example >}}
restish api.rest.sh/images -o ndjson -f body.self
{{< /restish-example >}}

This distinction matters for pagination and live streams. A live stream may
never finish, so `-o ndjson` is the right shape for structured stream output.
Restish rejects plain `-o json` on stream responses with an error that points
to `-o ndjson`; if you explicitly want one JSON document, combine `-o json`
with `--rsh-collect` and a finite `--rsh-max-items`.

Output format does not change paginated filter scope. Without `--rsh-collect`,
Restish filters each item through a mini response wrapper where the current
item lives under `body`; document formats then render the filtered item results
as one complete document.

## Filters Change What Gets Rendered

{{< restish-example >}}
restish api.rest.sh/example -f body.basics.profiles
{{< /restish-example >}}

```bash
restish api.rest.sh/images --rsh-collect -f '.body[] | select(.format == "jpeg") | .name' -o lines
restish api.rest.sh/ -f headers.Content-Type
```

Explicit scalar filters print without JSON string quotes. Use `-o lines` when
the filtered value is an array or stream of scalar values and shell tools should
receive one value per line. Use `-o json` when a script needs the selected value
as JSON.

## Raw Bytes And Files

Unfiltered responses redirect as body bytes by default. This includes JSON,
CBOR, YAML, images, octet streams, zip files, text, and unknown payloads:

```bash
restish api.rest.sh/images/jpeg > dragonfly.jpg
restish api.rest.sh/bytes/64 > sample.bin
restish api.rest.sh/formats/cbor > response.cbor
```

Choose an output format when you want Restish to transform the decoded body:

```bash
restish api.rest.sh/formats/cbor -o json > response.json
```

Raw redirected output bypasses Restish's structured body decoding and
formatting for presentation, but it is still based on the body after HTTP
content-encoding decompression. It also does not make Restish ask the server
for a different representation: default `Accept` negotiation still prefers
JSON and other text-friendly structured formats unless you set `Accept`
yourself. `raw` is not an `-o` format. To save bytes unchanged, redirect stdout
without choosing a filter, metadata shortcut, collection, or explicit output
format. Response middleware plugins are skipped on this raw-download path; they
run when Restish renders, filters, collects, or prints an interpreted response.

When stderr is a terminal and a streaming formatter named `progress` is
installed, non-verbose bounded raw downloads report transferred bytes on
stderr. Known totals use the final response's `Content-Length`; chunked or
unknown-length responses report bytes without a percentage. Redirected stderr,
verbose requests, and installations without the formatter remain silent.

Control exactly what stdout contains with `--rsh-print`:

```bash
restish api.rest.sh/images --rsh-print hbpc
restish api.rest.sh/images --rsh-collect > images.json
restish api.rest.sh/images --rsh-print b > images.compact.json
restish post api.rest.sh/post 'name: Alice' --rsh-print HBhbp
```

The letters are `H` request headers, `B` request body, `h` response status and
headers, `b` rendered body, `p` pretty formatting, and `c` color. `-o` still
controls how the rendered body (`b`) is formatted. In `auto` mode, transformed
or filtered output includes `p`; pass `--rsh-print=b` to omit pretty formatting.

Verbose diagnostics go to stderr, so body redirects stay clean:

```bash
restish -v api.rest.sh/images/jpeg > dragonfly.jpg 2> dragonfly.headers.txt
```

Sensitive headers such as `Authorization`, `Cookie`, `Proxy-Authorization`,
`Set-Cookie`, and common API-key headers are redacted in verbose diagnostics and
printed request/response headers. Explicit filters such as `--rsh-headers`,
`-f headers.Set-Cookie`, and `-f @` select raw response data and can reveal
those values, which is useful for pipelines but risky in logs.

Response bodies are not treated as secrets. Echo and debugging services can
reflect request headers, form values, or tokens into the response body, and
verbose output may preview that reflected body. Avoid `-v` with real credentials
against echo services unless you are comfortable with those values appearing in
logs.

## Images In The Terminal

Image responses can render in capable terminals:

```bash
restish api.rest.sh/images/png
restish -H 'Accept: image/png' api.rest.sh/images/png
```

The `auto` default renders `image/*` responses on an interactive terminal.
Use `-o image` only when you need to force image rendering for an ambiguous
response. Redirect the response to save the image instead.

## Greppable Output

`gron` prints paths and values, which is useful when you do not know the shape:

```bash
restish api.rest.sh/example -o gron | grep -i github
```

## Token-Dense Output For Agents

`toon` encodes the response as [TOON](https://github.com/toon-format/spec), a
compact format that cuts token count when feeding API responses to LLM agents.
Pair it with a filter that projects to a uniform list of records for the
largest savings:

```bash
restish api.rest.sh/images -f 'body.{name, format}' -o toon
```

See [Output Formats](/docs/reference/output-formats/#toon-for-agents) for the
tradeoffs.

## Related Pages

- [Query Syntax](/docs/reference/query-syntax/)
- [Filtering](../filtering/)
- [Output Formats](/docs/reference/output-formats/)
- [Output Defaults](/docs/reference/output-defaults/)
