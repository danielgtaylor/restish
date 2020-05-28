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

## Links Command

The links command provides a shorthand for displaying the available links.

```bash
# Display available links
$ restish links api.example.com/items

# Optionally filter to certain link relations
$ restish links api.example.com/items next prev
```
