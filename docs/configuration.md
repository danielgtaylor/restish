# Configuration

There are two types of configuration in Restish:

1. Global configuration
2. API-specific configuration

## Global configuration

Global configuration affects all commands and can be set in one of three ways, going from highest to lowest precedence:

1. Command line arguments
2. Environment variables
3. Configuration files

Configuration file locations are operating-system dependent:

| OS      | Path                                                |
| ------- | --------------------------------------------------- |
| Mac     | `~/Library/Application Support/restish/config.json` |
| Windows | `%AppData%\restish\config.json`                     |
| Linux   | `~/.config/restish/config.json`                     |

You can quickly determine which is being used via `restish localhost -v 2>&1 | grep config-directory`.

The global options in addition to `--help` and `--version` are:

| Argument                    | Env Var             | Example             | Description                                                                                |
| --------------------------- | ------------------- | ------------------- | ------------------------------------------------------------------------------------------ |
| `-f`, `--rsh-filter`        | `RSH_FILTER`        | `body.users[].id`   | Filter response via [Shorthand query](https://github.com/danielgtaylor/shorthand#querying) |
| `-H`, `--rsh-header`        | `RSH_HEADER`        | `Version:2020-05`   | Set a header name/value                                                                    |
| `--rsh-insecure`            | `RSH_INSECURE`      |                     | Disable TLS certificate checks                                                             |
| `--rsh-client-cert`         | `RSH_CLIENT_CERT`   | `/etc/ssl/cert.pem` | Path to a PEM encoded client certificate                                                   |
| `--rsh-client-key`          | `RSH_CLIENT_KEY`    | `/etc/ssl/key.pem`  | Path to a PEM encoded private key                                                          |
| `--rsh-ca-cert`             | `RSH_CA_CERT`       | `/etc/ssl/ca.pem`   | Path to a PEM encoded CA certificate                                                       |
| `--rsh-no-paginate`         | `RSH_NO_PAGINATE`   |                     | Disable automatic `next` link pagination                                                   |
| `-o`, `--rsh-output-format` | `RSH_OUTPUT_FORMAT` | `json`              | [Output format](/output.md), defaults to `auto`                                            |
| `-p`, `--rsh-profile`       | `RSH_PROFILE`       | `testing`           | Auth profile name, defaults to `default`                                                   |
| `-q`, `--rsh-query`         | `RSH_QUERY`         | `search=foo`        | Set a query parameter                                                                      |
| `-r`, `--rsh-raw`           | `RSH_RAW`           |                     | Raw output for shell processing                                                            |
| `-s`, `--rsh-server`        | `RSH_SERVER`        | `https://foo.com`   | Override API server base URL                                                               |
| `-v`, `--rsh-verbose`       | `RSH_VERBOSE`       |                     | Enable verbose output                                                                      |

Configuration file keys are the same as long-form arguments without the `--` prefix.

The following three would be equivalent ways to configure restish:

```bash
# CLI arguments
$ restish -v -p testing api.rest.sh/images
```

```bash
# Environment variables
$ RSH_VERBOSE=1 RSH_PROFILE=testing restish api.rest.sh/images
```

```bash
# Configuration file (Linux example)
$ echo '{"rsh-verbose": true, "rsh-profile": "testing"}' > ~/.config/restish/config.json
$ restish api.rest.sh/images
```

Should TTY autodetection for colored output cause any problems, you can manually disable colored output via the `NOCOLOR=1` environment variable.

## API configuration

### Adding an API

Adding or editing an API is possible via an interactive terminal UI:

```bash
$ restish api configure $NAME [$BASE_URI]
```

You should see something like the following, which enables you to create and edit profiles, headers, query parameters, and auth:

<img alt="Screen Shot" src="https://user-images.githubusercontent.com/106826/83099522-79dd3200-a062-11ea-8a78-b03a2fecf030.png">

Eventually the data is saved to one of the following:

| OS      | Path                                              |
| ------- | ------------------------------------------------- |
| Mac     | `~/Library/Application Support/restish/apis.json` |
| Windows | `%AppData%\restish\apis.json`                     |
| Linux   | `~/.config/restish/apis.json`                     |

If the API offers autoconfiguration data (e.g. through the [`x-cli-config` OpenAPI extension](/openapi.md#AutoConfiguration)) then you may be prompted for other values and some settings may already be configured for you.

Once an API is configured, you can start using it by using its short name. For example, given an API named `example`:

```bash
# If it has an API service description, call an operation:
$ restish example list-images

# If there is no API description you can still use persistent headers, auth,
# and the API short-name in URLs:
$ restish example/images

# It also works for full URIs, e.g. auth will be applied to:
$ restish https://api.rest.sh/images
```

Read on the learn more about the available API options.

### Showing an API configuration

Showing an API is possible via the following command:

```bash
$ restish api show $NAME
```

Output is in JSON by default. It can be displayed as a YAML by using `--rsh-output-format yaml` or `-o yaml`

### Updating an API configuration

The `configure` command used to create an API configuration can also be used to update an existing one.

```bash
$ restish api configure $NAME
```

### Syncing an API configuration

If the API endpoints changed, you can force-fetch the latest API description and update the local cache:

```bash
$ restish api sync $NAME
```

?> This is usually not necessary, as Restish will update the API description every 24 hours. Use this if you want to force an update sooner!

### Persistent headers & query parameters

Follow the prompts to add or edit persistent headers or query parameters. These are values that get sent with **every request** when using that profile.

Use cases:

- API keys
- Additional parameters required by the API

If you **do not** want these values being applied to **all** requests, then consider the `-H` and `-q` options instead.

Example:

```json
{
  "my-api": {
    "base": "https://api.company.com",
    "profiles": {
      "default": {
        "query": {
          "api_key": "some-secret-here"
        },
        "headers": {
          "X-API-KEY": "some-secret-here"
        }
      }
    }
  }
}
```

### API auth

The following auth types are supported:

- [HTTP Basic Auth](#http-basic-auth)
- [API key](#api-key)
- [OAuth 2.0 client credentials](#oauth-20-client-credentials)
- [OAuth 2.0 authorization code](#oauth-20-authorization-code)
- [External tool](#external-tool)

Each has its own set of parameters and setup. Any additional parameters beyond the default will get sent as additional request parameters when fetching tokens.

#### HTTP Basic Auth

HTTP Basic Auth is sent via an `Authorization` HTTP header and requires a `username` to be set. Setting `password` is optional, and if unset you will be prompted every time.

```json
{
  "my-api": {
    "base": "https://api.company.com",
    "profiles": {
      "default": {
        "auth": {
          "name": "http-basic",
          "params": {
            "username": "foo",
            "password": "bar"
          }
        }
      }
    }
  }
}
```

#### API key

API keys are values given to you by the API operator that identify you as the caller. There is no explicit auth support for API keys because they are already handled by persistent headers or query parameters.

For example, if your API operator has given you a JWT of `abc123`, you might set a persistent header like `Authorization: bearer abc123` in the default profile.

```json
{
  "my-api": {
    "base": "https://api.company.com",
    "profiles": {
      "default": {
        "headers": {
          "Authorization": "Bearer ..."
        }
      }
    }
  }
}
```

#### OAuth 2.0 Client Credentials

[OAuth 2.0 Client Credentials](https://oauth.net/2/grant-types/client-credentials/) is typically used for scripts that are not initiated by a specific user. Machine-to-machine tokens is another term for them.

In order to set up a client credentials flow, you will need a client ID, client secret, and a token URL.

For example, to integrate with a third-party service like [Auth0](https://auth0.com/), you might use a configuration like:

```json
{
  "my-api": {
    "base": "https://api.company.com",
    "profiles": {
      "default": {
        "auth": {
          "name": "oauth-client-credentials",
          "params": {
            "audience": "audience-name",
            "client_id": "abc123",
            "client_secret": "...",
            "scopes": "",
            "token_url": "https://company.auth0.com/oauth/token"
          }
        }
      }
    }
  }
}
```

#### OAuth 2.0 Authorization Code

[OAuth 2.0 Authorization Code](https://oauth.net/2/grant-types/authorization-code/) is used by users to log in without giving their password to Restish. An authorization code with PKCE is generated and exchanged for a token after the user logs in through a browser.

This mode starts a web server on port `8484` to automatically get the redirected token. If the user cannot open a browser on the machine running the CLI, then doing it on another machine and pasting the returned token will work.

If offline mode is enabled (e.g. via scopes) and a refresh token is returned, then once the token expires the refresh token is used and the user does not need to log in via the browser again.

In order to set up the authorization code flow, you will need a client ID, authorization URL, and a token URL.

For example, to integrate with a third-party service like [Auth0](https://auth0.com/), you might use a configuration like:

```json
{
  "my-api": {
    "base": "https://api.company.com",
    "profiles": {
      "default": {
        "auth": {
          "name": "oauth-authorization-code",
          "params": {
            "audience": "audience-name",
            "authorize_url": "https://company.auth0.com/authorize",
            "client_id": "abc123",
            "scopes": "offline_access",
            "token_url": "https://company.auth0.com/oauth/token"
          }
        }
      }
    }
  }
}
```

#### External tool

To allow interaction with APIs which have custom signature schemes, a
third-party tool or script can be used. The script will need to accept
a JSON representation of the API request on its standard input and
will reply with the necessary request modifications on standard
output.

Two parameters are accepted for this authentication method:

- `commandline`: A required string, pointing to the command to run.
- `omitbody`: Optional. When present and set to the string `"true"`,
  do not supply the request body to the helper script.

```json
{
  "my-api": {
    "base": "https://api.company.com",
    "profiles": {
      "default": {
        "auth": {
          "name": "external-tool",
          "params": {
            "commandline": "restish-custom-auth",
            "omitbody": "false"
          }
        }
      }
    }
  }
}
```

The serialized body will be supplied in the following form to the
helper commandline:

```json
{
  "method": "GET",
  "uri": "http://...",
  "headers": {
    "content-type": ["…"]
    // …
  },
  "body": "…"
}
```

The same shape is expected on the program's standard output. Two
parameters only will be considered:

- `headers`: Values present will be added to the outbound payload.
- `uri`: Will replace the destination URL entirely (allowing the
  addition of query arguments if needed).

### Loading from files or URLs

Sometimes an API won't provide a way to fetch its spec document, or a third-party will provide a spec for an existing public API, for example GitHub or Stripe.

In this case you can download the spec files to your machine and link to them (or provide a URL) in the API configuration. Use the `spec_files` array configuration directive for this in the [`apis.json` file](#/configuration?id=adding-an-api):

```json
{
  "my-api": {
    "base": "https://api.github.com",
    "spec_files": ["/path/to/github-openapi.yaml"]
  }
}
```

!> If more than one file path is specified, then the loaded APIs are merged in the order specified. You will get operations from both APIs, but there can only be a single API title or description so the first encountered non-zero value is used.
