# Controlling Output

## Caching

By default, Restish will cache responses with appropriate headers set. When fetching API service descriptions, a 24-hour cache is used if no cache headers are sent. The cached responses are stored in `~/.restish/responses`.

The easiest way to tell if a cached response has been used is to look at the `Date` header, which will not change from request to request if a cached response is returned.

You may wish to disable caching to force an updated fetch:

```bash
# Disable caching via arg
$ restish --rsh-no-cache get api.example.com/items

# Disable caching via env
$ RSH_NO_CACHE=1 restish get api.example.com/items
```

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

Unlike JSON, object property names have no strings and there are no commas.

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

The above is the same format used when setting the output format to something other than the default, e.g. JSON or YAML:

```bash
# Output a response as JSON
$ restish -o json api.example.com/items
```

## Filtering & Projection

Restish includes JMESPath Plus, which includes all of [JMESPath](https://jmespath.org/) plus some [additional enhancements](https://github.com/danielgtaylor/go-jmespath-plus#readme). If you've ever used the [AWS CLI](https://aws.amazon.com/cli/), then you've likely used JMESPath. It's a language for filtering and projecting the response value that's useful for massaging the response data for scripts.

The response format described above is used as the input, so don't forget the `body` prefix when accessing body members.

```bash
# Filter results to just their ID & versions
$ restish api.example.com/items -f "body[].{id, version}"

# Get all `id` fields recursively from a response that are a URL
$ restish api.example.com/items -f "..id|[?starts_with(@, 'http')]"

# Pivot data, e.g. group/sort item IDs by tags
$ restish api.example.com/items -f "pivot(body, &tags, &id)"
```

See the JMESPath documentation for more information and examples.

!> Warning: structured data from binary formats like CBOR may be converted to its JSON equivalent before applying JMESPath filters. For example, a byte slice and a date would both be treated as strings.
