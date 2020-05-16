# Restish CLI

Restish is a CLI for interacting with [REST](https://apisyouwonthate.com/blog/rest-and-hypermedia-in-2019)-ish HTTP APIs with some nice features built-in, like always having the latest API resources, fields, and operations available when they go live on the API without needing to install or update anything.

Features include:

- HTTP/2 ([RFC 7540](https://tools.ietf.org/html/rfc7540)) by _default_ with fallback to HTTP/1.1
- Generic head/get/post/put/patch/delete verbs like `curl` or [HTTPie](https://httpie.org/)
- Understands [RFC 8631](https://tools.ietf.org/html/rfc8631) `service-desc` and [RFC 5988](https://tools.ietf.org/html/rfc5988#section-6.2.2) `describedby` link relations to auto-discover API specs if available.
  - [OpenAPI 3](https://github.com/OAI/OpenAPI-Specification/blob/master/versions/3.0.3.md) and [JSON Schema](https://json-schema.org/)
- Automatic pagination of resource collections via [RFC 5988](https://tools.ietf.org/html/rfc5988) `prev` and `next` hypermedia links
- API endpoint-based auth built-in:
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
- CLI [shorthand](https://github.com/danielgtaylor/openapi-cli-generator/tree/master/shorthand#cli-shorthand-syntax) for structured data input (e.g. for JSON)
- [JMESPath Plus](https://github.com/danielgtaylor/go-jmespath-plus) response filtering & projection
- Colorized prettified readable output
- Fast native binary

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

Coming soon...
