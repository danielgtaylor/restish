# Hypermedia

Restish uses a standardized internal representation of hypermedia links to make navigating and querying different APIs simpler. It looks like:

```json
{
  "rel": "next",
  "uri": "https://api.example.com/items?cursor=abc123"
}
```

The URI is always resolved so you don't need to worry about absolute or relative paths.

## Automatic Pagination

Restish uses these standardized links to automatically handle paginated collections, returning the full collection to you whenever possible.

This behavior can be disabled via the `--rsh-no-paginate` argument or `RSH_NO_PAGINATE=1` environment variable when needed. You may need to do this for large or slow collections.

```bash
# Automatic paginated response returns all pages
$ restish api.rest.sh/images
...
[
  {
    format: "jpeg"
    name: "Dragonfly macro"
    self: "/images/jpeg"
  }
  {
    format: "webp"
    name: "Origami under blacklight"
    self: "/images/webp"
  }
  {
    format: "gif"
    name: "Andy Warhol mural in Miami"
    self: "/images/gif"
  }
  {
    format: "png"
    name: "Station in Prague"
    self: "/images/png"
  }
  {
    format: "heic"
    name: "Chihuly glass in boats"
    self: "/images/heic"
  }
]
```

```bash
# Return a single page of results
$ restish --rsh-no-paginate api.rest.sh/images
Link: </images?cursor=abc123>; rel="next", </schemas/ImageItemList.json>; rel="describedby"
...
[
  {
    format: "jpeg"
    name: "Dragonfly macro"
    self: "/images/jpeg"
  }
  {
    format: "webp"
    name: "Origami under blacklight"
    self: "/images/webp"
  }
]
```

## Links Command

The links command provides a shorthand for displaying the available links. All links are normalized to include the full URL. Paginated responses may generate the same link multiple times.

```bash
# Display available links
$ restish links api.rest.sh/images
{
  "describedby": [
    {
      "rel": "describedby",
      "uri": "https://api.rest.sh/schemas/ImageItemList.json"
    },
    {
      "rel": "describedby",
      "uri": "https://api.rest.sh/schemas/ImageItemList.json"
    },
    {
      "rel": "describedby",
      "uri": "https://api.rest.sh/schemas/ImageItemList.json"
    }
  ],
  "next": [
    {
      "rel": "next",
      "uri": "https://api.rest.sh/images?cursor=abc123"
    },
    {
      "rel": "next",
      "uri": "https://api.rest.sh/images?cursor=def456"
    }
  ],
  "self-item": [
    {
      "rel": "self-item",
      "uri": "https://api.rest.sh/images/jpeg"
    },
    {
      "rel": "self-item",
      "uri": "https://api.rest.sh/images/webp"
    },
    {
      "rel": "self-item",
      "uri": "https://api.rest.sh/images/gif"
    },
    {
      "rel": "self-item",
      "uri": "https://api.rest.sh/images/png"
    },
    {
      "rel": "self-item",
      "uri": "https://api.rest.sh/images/heic"
    }
  ]
}
```

```bash
# Optionally filter to certain link relations
$ restish links api.rest.sh/images next
[
  {
    "rel": "next",
    "uri": "https://api.rest.sh/images?cursor=abc123"
  },
  {
    "rel": "next",
    "uri": "https://api.rest.sh/images?cursor=def456"
  }
]
```
