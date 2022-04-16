# Input

You can set headers, query parameters, and a body for each outgoing request.

## Request Parameters

Request headers and query parameters are set via arguments or in the URI itself:

```bash
# Pass a query param (either way)
$ restish example.com/items?search=foo
$ restish -q search=foo example.com/items

# Query params with an API short name
$ restish do/droplets?tag_name=prod

# Pass a header
$ restish -H MyHeader:value example.com

# Pass multiple
$ restish -H Header1:val1 -H Header2:val2 example.com
```

?> Note that query params use `=` as a delimiter while haders use `:`, just like with HTTP.

## Request Body

A request body can be set in two ways for requests that support bodies (e.g. `POST` / `PUT` / `PATCH`):

1. Standard input
2. CLI shorthand

### Standard Input

Any stream of data passed to standard input will be sent as the request body.

```bash
# Set body from file
$ restish put example.com/items <item.json

# Set body from piped command
$ echo '{"name": "hello"}' | restish put example.com/items
```

?> Don't forget to set the `Content-Type` header if needed. It will default to JSON if unset.

### CLI Shorthand

The [CLI Shorthand](shorthand.md) is a convenient way of providing structured data on the commandline. It is a JSON-like syntax that enables you to easily create nested structured data. For example:

```bash
$ restish post example.com/items foo.bar[].baz: 1, .hello: world
```

Will send the following request:

```http
POST /items HTTP/2.0
Content-Type: application/json
Host: example.com

{
  "foo": {
    "bar": [
      {
        "baz": 1,
        "hello": "world"
      }
    ]
  }
}
```

The shorthand supports nested objects, arrays, automatic type coercion, context-aware backreferences, and loading data from files. See the [CLI Shorthand Syntax](shorthand.md) for more info.

### Combined Body Input

It's also possible to use standard in as a template and replace or set values via commandline arguments, getting the best of both worlds. For example:

```bash
# Use both a file and override a value
$ restish post example.com/items <template.json id: test1
$ restish post example.com/items <template.json id: test2, tags[]: group1
```

If you have a known small set of fields that need to change between calls, this makes it easy to do so without large complex commands.
