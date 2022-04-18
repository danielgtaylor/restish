# Controlling Output

## Caching

By default, Restish will cache responses with appropriate [RFC 7234](https://tools.ietf.org/html/rfc7234) caching headers set. When fetching API service descriptions, a 24-hour cache is used if _no cache headers_ are sent by the API. This is to prevent hammering the API each time the CLI is run. The cached responses are stored in `~/.restish/responses`.

The easiest way to tell if a cached response has been used is to look at the `Date` header, which will not change from request to request if a cached response is returned.

You may wish to disable caching to force an updated fetch:

```bash
# Disable caching via arg
$ restish --rsh-no-cache get api.example.com/items

# Disable caching via env
$ RSH_NO_CACHE=1 restish get api.example.com/items
```

Even if caching is disabled, the local disk cache will get updated. The setting above prevents the _use_ of a cached response.

## Default Output

By default, Restish will output a custom format that is similar to JSON or YAML and meant to be easily consumed by humans while supporting both text and binary formats. Here is an example of how various types look:

```readable
HTTP/1.1 200 OK
Cache-Control: max-age=30
Content-Length: 100
Content-Type: application/cbor
Date: Thu, 28 May 2020 05:56:31 GMT

{
  binary: 0x00030402060a632a3016...
  created: 2020-05-27T05:41:19.603396Z
  id: "test"
  nested: {
    saved: true
    self: "https://example.com/nested"
  }
  pointer: null
  tags: ["one", "tw\"o", "three"]
  value: 123
}
```

Unlike JSON and similar to YAML, object property names have no quotes and there are no commas. This is powered by a custom [marshaller](https://github.com/danielgtaylor/restish/blob/main/cli/readable.go) and [lexer](https://github.com/danielgtaylor/restish/blob/main/cli/lexer.go) to enable syntax highlighting.

The following types are supported & syntax highlighted:

- `null`
- Booleans `true` and `false`
- Numbers including scientific notation
- Strings
  - Special formatting for links
  - Special formatting for dates
- Dates (ISO8601 / RFC3339)
- Binary data as hex, e.g. `0xdeadbeef...`
  - Why hex? It's easier to read for a human than string escape codes or base64.

If the output is _not_ structured data (JSON/YAML/CBOR/etc) then it is output as-is without formatting.

?> Keep in mind the default output format is meant for **human** consumption!

### Images

Basic image support is available using unicode half-blocks if your terminal supports these unicode characters and true color mode. For example:

<img alt="Screen Shot" src="https://user-images.githubusercontent.com/106826/83105045-c4fd4200-a06e-11ea-8902-fc681cd7c66e.png">

## Response Structure

Internally, the response is structured like this:

```json
{
  "proto": "HTTP/2.0",
  "status": 200,
  "headers": {
    "Content-Type": "application/json"
  },
  "links": {
    "next": [
      {
        "rel": "next",
        "uri": "https://api.example.com/items?cursor=abc123"
      }
    ]
  },
  "body": {
    "id": 123,
    "description": "This is the parsed structured data"
  }
}
```

The headers are canonicalized (so `Content-Type` rather than `content-type`), the links are [standardized](hypermedia.md) and resolved, and the body is parsed based on the incoming content type, abstracting away the need to worry about different formats, encodings, etc.

The above is the same structure used when setting the output format to something other than the default, e.g. JSON or YAML:

```bash
# Output a response as JSON
$ restish -o json api.example.com/items
```

## Filtering & Projection

Restish includes JMESPath Plus, which includes all of [JMESPath](https://jmespath.org/) plus some [additional enhancements](https://github.com/danielgtaylor/go-jmespath-plus#readme). If you've ever used the [AWS CLI](https://aws.amazon.com/cli/), then you've likely used JMESPath. It's a language for filtering and projecting the response value that's useful for massaging the response data for scripts.

The response format described above is used as the input, so don't forget the `body` prefix when accessing body members!

```bash
# Print out request headers
$ restish api.example.com/items -f "headers"

# Filter results to just their ID & versions
$ restish api.example.com/items -f "body[].{id, version}"

# Get all `id` fields recursively from a response that are a URL
$ restish api.example.com/items -f "..id|[?starts_with(@, 'http')]"

# Pivot data, e.g. group/sort item IDs by tags
$ restish api.example.com/items -f "pivot(body, &tags, &id)"
```

See the JMESPath documentation for more information and examples.

!> Warning: structured data from binary formats like CBOR may be converted to its JSON equivalent before applying JMESPath filters. For example, a byte slice and a date would both be treated as strings.

## Raw Mode

Raw mode, when enabled, will remove JSON formatting from the filtered output if the result matches one of the following:

- A string
- An array of scalars (null, bool, number, string)

For example:

```bash
# Normal mode
$ restish api.example.com/items -f "body[0].id"
"id1"

# Raw mode strips the quotes
$ restish api.example.com/items -f "body[0].id" -r
id1

# It also works with arrays
$ restish api.example.com/items -f "body[].id" -r
id1
id2
id3
...
```

If the filtered output result doesn't match one of the above types, then `-r` is a no-op.

This feature is mainly useful for shell scripting, where you don't want to have to parse the JSON and instead just want to loop through a list of IDs and run further commands.

## Downloading Files & Saving Responses

Raw mode in combination with redirected output can also be used to download files, and saving a structured data response (e.g. JSON, CBOR, YAML, etc) is simple as well:

```bash
# Save a zip file from the server.
$ restish api.example.com/file.zip -r >file.zip

# Save structured data
$ restish api.example.com/items -f body >items.json
```
