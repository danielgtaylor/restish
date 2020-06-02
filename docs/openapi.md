# OpenAPI 3

In general, OpenAPI 3 just works with Restish. There are a couple of things you can do to make sure your users can more easily use Restish with your API.

## Discoverability

Restish looks for link relation headers at the API base URI as a way to discover your API description and provide convenience operations. It looks for:

- [RFC 8631](https://tools.ietf.org/html/rfc8631) `service-desc` link relation
- [RFC 5988](https://tools.ietf.org/html/rfc5988#section-6.2.2) `describedby` link relation

Example response for `https://api.example.com/`:

```readable
HTTP/2.0 204 No Content
Link: </openapi.json>; rel="service-desc"
```

If no such link relations are found, then the OpenAPI loader defaults to looking in:

- `/openapi.json`
- `/openapi.yaml`

If neither one of those returns an OpenAPI spec, then the loader gives up.

### Loading from Files

For local testing or an API you don't control or can't update, you can load from OpenAPI files. See [Configuration: Loading from Files](configuration.md#loading-from-files) for an example configuration.

## OpenAPI Extensions

Several extensions properties may be used to change the behavior of the CLI.

| Name                | Description                                   |
| ------------------- | --------------------------------------------- |
| `x-cli-aliases`     | Sets up command aliases for operations.       |
| `x-cli-description` | Provide an alternate description for the CLI. |
| `x-cli-ignore`      | Ignore this path, operation, or parameter.    |
| `x-cli-hidden`      | Hide this path, or operation.                 |
| `x-cli-name`        | Provide an alternate name for the CLI.        |

### Aliases

The following example shows how you would set up a command that can be invoked by either `list-items` or simply `ls`:

```yaml
paths:
  /items:
    get:
      operationId: ListItems
      x-cli-aliases:
        - ls
```

### Description

You can override the default description for the API, operations, and parameters easily:

```yaml
paths:
  /items:
    description: Some info talking about HTTP headers.
    x-cli-description: Some info talking about command line arguments.
```

### Exclusion

It is possible to exclude paths, operations, and/or parameters from the generated CLI. No code will be generated as they will be completely skipped.

```yaml
paths:
  /included:
    description: I will get included in the CLI.
  /excluded:
    x-cli-ignore: true
    description: I will not be in the CLI :-(
```

Alternatively, you can have the path or operation exist in the UI but be hidden from the standard help list. Specific help is still available via `restish my-api my-hidden-operation --help`:

```yaml
paths:
  /hidden:
    x-cli-hidden: true
```

### Name

You can override the default name for the API, operations, and params:

```yaml
info:
  x-cli-name: foo
paths:
  /items:
    operationId: myOperation
    x-cli-name: my-op
    parameters:
      - name: id
        x-cli-name: item-id
        in: query
```

With the above, you would be able to call `restish my-api my-op --item-id=12`.

## Compatible Frameworks

The following work out of the box with Restish:

- [Huma](https://huma.rocks/)
- [FastAPI](https://fastapi.tiangolo.com/)
