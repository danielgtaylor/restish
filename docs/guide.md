# Guide

This guide will help you to get started installing and using Restish.

## Installation

Grab a [release](https://github.com/danielgtaylor/restish/releases) for your platform, otherwise if you have Go installed:

```bash
# Download / build / install
$ go get -u github.com/danielgtaylor/restish
```

You can confirm the installation worked by trying to run Restish:

```bash
# Basic test to confirm everything is installed
$ restish --version
```

## Basic Usage

Generic HTTP verbs require no setup and are easy to use. If no verb is supplied then a GET is assumed. The `https://` is also optional as it is the default.

```bash
# Perform an HTTP GET request
$ restish jsonplaceholder.typicode.com/users/1

# Above is equivalent to:
$ restish get https://jsonplaceholder.typicode.com/users/1
```

You will see a response like:

```readable
HTTP/2.0 200 OK
Content-Encoding: br
Content-Type: application/json; charset=utf-8
Date: Wed, 20 May 2020 05:50:52 GMT

{
  address: {
    city: "Gwenborough"
    geo: {
      lat: -37.3159
      lng: 81.1496
    }
    street: "Kulas Light"
    suite: "Apt. 556"
    zipcode: "92998-3874"
  }
  company: {
    bs: "harness real-time e-markets"
    catchPhrase: "Multi-layered client-server neural-net"
    name: "Romaguera-Crona"
  }
  email: "Sincere@april.biz"
  id: 1
  name: "Leanne Graham"
  phone: "1-770-736-8031 x56442"
  username: "Bret"
  website: "https://www.hildegard.org/"
}
```

?> Note that the output above is **not** JSON! By default, Restish outputs an HTTP+JSON-like format meant to be more readable. See [output](/output.md) for more info.

Various inputs can be passed in as needed:

```bash
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

```bash
# Set via env vars
$ export RSH_HEADER=header1:value1,header2:value2
$ restish example.com/users
```

If you have persistent headers or query params you'd like to set, then consider registering the API endpoint with Restish rather than exporting environment variables. Read on to find out how.

## API Operation Commands

APIs can be registered in order to provide API description auto-discovery with convenience commands and authentication. APIs are registered with a short nickname. For example the GitHub v3 API might be called `github` or the Digital Ocean API might be called `do`.

Each registered API can have a number of named profiles which can be selected via the `-p` or `--rsh-profile` argument. The default profile is called `default`.

Each profile can have a number of preset headers or query params, a type of auth, and any auth-specific params.

Getting started registering an API is easy and uses an interactive prompt to set up profiles, auth, etc. At a minimum you must provide a short nickname, and you'll be asked for the base domain of the API (e.g `https://api.example.com/`):

```bash
# Register a new API called `example`
$ restish api configure example
```

Once registered, use the short nickname to access the API:

```bash
# If an OpenAPI or other API description document was found, this will show
# you all available commands.
$ restish example --help

# Call an API operation
$ restish example list-items -q search=active

# Call the same operation, re-using the same headers & auth
$ restish api.example.com/items?search=active
```

If you configured multiple profiles:

```bash
# Make a request with a non-default profile
$ restish -p my-profile example list-items
```

You can even use the short nickname in place of the full API domain, so for example this works:

```bash
# Use the API nickname instead of the domain
$ restish example/items?search=active
```

That's it for the guide! Hopefully this gave you a quick overview of what is possible with Restish. See the more in-depth topics in the side navigation bar to go deep on how all the above works and is used. Thanks for reading! :tada:
