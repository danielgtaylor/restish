# Configuration

There are two types of configuration in Restish:

1. Global configuration
2. API-specific configuration

## Global Configuration

Global configuration affects all commands and can be set in one of three ways, going from highest to lowest precedence:

1. Command line arguments
2. Environment variables
3. Configuration files (`/etc/restish/config.json` or `~/.restish/config.json`)

The global options in addition to `--help` and `--version` are:

| Argument                    | Env Var             | Example             | Description                                                                      |
| --------------------------- | ------------------- | ------------------- | -------------------------------------------------------------------------------- |
| `-f`, `--rsh-filter`        | `RSH_FILTER`        | `body.users[].id`   | [JMESPath Plus](https://github.com/danielgtaylor/go-jmespath-plus#readme) filter |
| `-H`, `--rsh-header`        | `RSH_HEADER`        | `Version:2020-05`   | Set a header name/value                                                          |
| `--rsh-insecure`            | `RSH_INSECURE`      |                     | Disable TLS certificate checks                                                   |
| `--rsh-client-cert`         | `RSH_CLIENT_CERT`   | `/etc/ssl/cert.pem` | Path to a PEM encoded client certificate                                         |
| `--rsh-client-key`          | `RSH_CLIENT_KEY`    | `/etc/ssl/key.pem`  | Path to a PEM encoded private key                                                |
| `--rsh-ca-cert`             | `RSH_CA_CERT`       | `/etc/ssl/ca.pem`   | Path to a PEM encoded CA certificate                                             |
| `--rsh-no-paginate`         | `RSH_NO_PAGINATE`   |                     | Disable automatic `next` link pagination                                         |
| `-o`, `--rsh-output-format` | `RSH_OUTPUT_FORMAT` | `json`              | [Output format](/output.md), defaults to `auto`                                  |
| `-p`, `--rsh-profile`       | `RSH_PROFILE`       | `testing`           | Auth profile name, defaults to `default`                                         |
| `-q`, `--rsh-query`         | `RSH_QUERY`         | `search=foo`        | Set a query parameter                                                            |
| `-r`, `--rsh-raw`           | `RSH_RAW`           |                     | Raw output for shell processing                                                  |
| `-s`, `--rsh-server`        | `RSH_SERVER`        | `https://foo.com`   | Override API server base URL                                                     |
| `-v`, `--rsh-verbose`       | `RSH_VERBOSE`       |                     | Enable verbose output                                                            |

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
# Configuration file
$ echo '{"rsh-verbose": true, "rsh-profile": "testing"}' > ~/.restish/config.json
$ restish api.rest.sh/images
```

Should TTY autodetection for colored output cause any problems, you can manually disable colored output via the `NOCOLOR=1` environment variable.

## API Configuration

### Adding an API

Adding or editing an API is possible via an interactive terminal UI:

```bash
$ restish api configure $NAME [$BASE_URI]
```

You should see something like the following, which enables you to create and edit profiles, headers, query params, and auth, eventually saving the data to `~/.restish/apis.json`:

<img alt="Screen Shot" src="https://user-images.githubusercontent.com/106826/83099522-79dd3200-a062-11ea-8a78-b03a2fecf030.png">

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
$ restish api configure $NAME
```

Output is in JSON by default. It can be displayed as a YAML by using `--rsh-output-format yaml` or `-o yaml`

### Syncing an API configuration

If the API endpoints changed, you can force-fetch the latest API description and update the local cache:

```bash
$ restish api sync $NAME
```

### Persistent Headers & Query Params

Follow the prompts to add or edit persistent headers or query params. These are values that get sent with **every request** when using that profile.

Use cases:

- API keys
- Additional parameters required by the API

If you **do not** want these values being applied to **all** requests, then consider the `-H` and `-q` options instead.

### API Auth

The following auth types are supported:

- HTTP Basic Auth
- API key
- OAuth 2.0 client credentials
- OAuth 2.0 authorization code

Each has its own set of parameters and setup. Any additional parameters beyond the default will get sent as additional request parameters when fetching tokens.

#### HTTP Basic Auth

HTTP Basic Auth is sent via an `Authorization` HTTP header and requires a `username` and `password` to be set.

#### API key

API keys are values given to you by the API operator that identify you as the caller. There is no explicit auth support for API keys because they are already handled by persistend headers or query params.

For example, if your API operator has given you a JWT of `abc123`, you might set a persistent header like `Authorization: bearer abc123` in the default profile.

#### OAuth 2.0 Client Credentials

[OAuth 2.0 Client Credentials](https://oauth.net/2/grant-types/client-credentials/) is typically used for scripts that are not initiated by a specific user. Machine-to-machine tokens is another term for them.

In order to set up a client credentials flow, you will need a client ID, client secret, and a token URL.

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

### Loading From Files

Sometimes an API won't provide a way to fetch its spec document, or a third-party will provide a spec for an existing public API, for example GitHub or Stripe.

In this case you can download the spec files to your machine and link to them in the API configuration. Use the `spec_files` array configuration directive for this in `~/.restish/apis.json`:

```json
{
  "my-api": {
    "base": "https://api.github.com",
    "spec_files": ["/path/to/github-openapi.yaml"]
  }
}
```

!> If more than one file path is specified, then the loaded APIs are merged in the order specified. You will get operations from both APIs, but there can only be a single API title or description so the first encountered non-zero value is used.
