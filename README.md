![Restish Logo](https://user-images.githubusercontent.com/106826/82109918-ec5b2300-96ee-11ea-9af0-8515329d5965.png)

[Restish](https://rest.sh/) is a CLI for interacting with [REST](https://apisyouwonthate.com/blog/rest-and-hypermedia-in-2019)-ish HTTP APIs with some nice features built-in, like always having the latest API resources, fields, and operations available when they go live on the API without needing to install or update anything.

Features include:

- HTTP/2 ([RFC 7540](https://tools.ietf.org/html/rfc7540)) with TLS by _default_ with fallback to HTTP/1.1
- Generic head/get/post/put/patch/delete verbs like `curl` or [HTTPie](https://httpie.org/)
- Generated commands for CLI operations, e.g. `restish my-api list-users`
  - Automatically discovers API descriptions
    - [RFC 8631](https://tools.ietf.org/html/rfc8631) `service-desc` link relation
    - [RFC 5988](https://tools.ietf.org/html/rfc5988#section-6.2.2) `describedby` link relation
  - Supported formats
    - [OpenAPI 3](https://github.com/OAI/OpenAPI-Specification/blob/master/versions/3.0.3.md) and [JSON Schema](https://json-schema.org/)
- Automatic pagination of resource collections via [RFC 5988](https://tools.ietf.org/html/rfc5988) `prev` and `next` hypermedia links
- API endpoint-based auth built-in with support for profiles:
  - HTTP Basic
  - API key via header or query param
  - OAuth2 client credentials flow (machine-to-machine, [RFC 6749](https://tools.ietf.org/html/rfc6749))
  - OAuth2 authorization code (with PKCE [RFC 7636](https://tools.ietf.org/html/rfc7636)) flow
- Content negotiation, decoding & unmarshalling built-in:
  - JSON ([RFC 8259](https://tools.ietf.org/html/rfc8259), https://www.json.org/)
  - YAML (https://yaml.org/)
  - CBOR ([RFC 7049](https://tools.ietf.org/html/rfc7049), http://cbor.io/)
  - MessagePack (https://msgpack.org/)
  - Gzip ([RFC 1952](https://tools.ietf.org/html/rfc1952)) and Brotli ([RFC 7932](https://tools.ietf.org/html/rfc7932)) content encoding
- Local caching that respects [RFC 7234](https://tools.ietf.org/html/rfc7234) `Cache-Control` and `Expires` headers
- CLI [shorthand](https://github.com/danielgtaylor/openapi-cli-generator/tree/master/shorthand#cli-shorthand-syntax) for structured data input (e.g. for JSON)
- [JMESPath Plus](https://github.com/danielgtaylor/go-jmespath-plus) response filtering & projection
- Colorized prettified readable output
- Fast native zero-dependency binary

Why use this?

Every API deserves a CLI for quick access and for power users to script against the service. Building CLIs from scratch is a pain. Restish provides one tool your users can install that just works for multiple APIs and is always up to date, because the interface is defined by the server.

This project started life as a fork of [OpenAPI CLI Generator](https://github.com/danielgtaylor/openapi-cli-generator).

## Installation

Grab a release, otherwise if you have Go installed:

```sh
# Download / build / install
$ go get -u github.com/danielgtaylor/restish
```

## Usage

Generic HTTP verbs require no setup and are easy to use. If no verb is supplied then a GET is assumed. The `https://` is also optional as it is the default.

```sh
# Perform an HTTP GET request
$ restish jsonplaceholder.typicode.com/users/1

# Above is equivalent to:
$ restish get https://jsonplaceholder.typicode.com/users/1
```

You will see a response like:

```http
HTTP/2.0 200 OK
Content-Encoding: br
Content-Type: application/json; charset=utf-8
Date: Wed, 20 May 2020 05:50:52 GMT

{
  "address": {
    "city": "Gwenborough",
    "geo": {
      "lat": "-37.3159",
      "lng": "81.1496"
    },
    "street": "Kulas Light",
    "suite": "Apt. 556",
    "zipcode": "92998-3874"
  },
  "company": {
    "bs": "harness real-time e-markets",
    "catchPhrase": "Multi-layered client-server neural-net",
    "name": "Romaguera-Crona"
  },
  "email": "Sincere@april.biz",
  "id": 1,
  "name": "Leanne Graham",
  "phone": "1-770-736-8031 x56442",
  "username": "Bret",
  "website": "hildegard.org"
}
```

Various inputs can be passed in as needed:

```sh
# Pass a query param (either way)
$ restish example.com?search=foo
$ restish -q search=foo example.com

# Pass a header
$ restish -H MyHeader:value example.com

# Pass in a body via a file
$ restish post example.com/users <user.json

# Pass in body via CLI shorthand
$ restish post example.com/users name: Kari, tags[]: admin
```

Headers and query params can also be set via environment variables, for example:

```sh
# Set via env vars
$ export RSH_HEADER=header1:value1,header2:value2
$ restish example.com/users
```

If you have persistent headers or query params you'd like to set, then consider registering the API endpoint with Restish rather than exporting environment variables. Read on to find out how.

### Registering an API Endpoint

APIs can be registered in order to provide API description auto-discovery with convenience commands and authentication.

Each registered API can have a number of named auth profiles which can be selected via the `-p` or `--rsh-profile` argument. The default profile is called `default`.

Each profile can have a number of preset headers or query params, a type of auth, and any auth-specific params. The following auth types are available:

| Type                       | Inputs                                              |
| -------------------------- | --------------------------------------------------- |
| `http-basic`               | `username`, `password`                              |
| `oauth-client-credentials` | `client_id`, `client_secret`, `token_url`, `scopes` |
| `oauth-authorization-code` | `client_id`, `authorize_url`, `token_url`, `scopes` |

Register a new API like this, which launches an interactive configuration interface to set up base URIs, headers & query params, and auth.

```sh
# Register a new API named `example`
$ restish api configure example

# Call the API with a specific profile
$ restish -p myProfile example list-items

# Set an environment variable for the profile
$ export RSH_PROFILE=myProfile
$ restish example list-items
```

Registered APIs are stored in `~/.restish/apis.json`. A very basic example config for local service testing with [Huma](https://huma.rocks/) or [FastAPI](https://fastapi.tiangolo.com/) and named `local` might look like:

```json
{
  "local": {
    "base": "http://localhost:8888"
  }
}
```

### API Endpoint Usage

The registered API short name can be used instead of the domain, for example:

```sh
# Supposed we have an API called `ex`. Show what the API can do:
$ restish ex --help

# If there is e.g. an OpenAPI spec linked, call one of the operations:
$ restish ex list-items -q search=active

# If not, you can still use the shorthand in URLs:
$ restish ex/items?search=active
```
