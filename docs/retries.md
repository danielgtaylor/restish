# Retries & Timeouts

Restish has support for automatic retries for some types of requests, and also supports giving up after a certain amount of time. This is useful for APIs that are rate-limited or have intermittent issues.

## Automatic Retries

By default, Restish will retry the following responses two times:

- `408 Request Timeout`
- `425 Too Early`
- `429 Too Many Requests`
- `500 Internal Server Error`
- `502 Bad Gateway`
- `503 Service Unavailable`
- `504 Gateway Timeout`

This is configurable via the `--rsh-retry` parameter or `RSH_RETRY` environment variable, which should be a positive integer. Set to `0` to disable retries.

Here is an example of the default behavior:

```bash
# Trigger retries by generating a 429 response.
$ restish api.rest.sh/status/429
WARN: Got 429 Too Many Requests, retrying in 1s
WARN: Got 429 Too Many Requests, retrying in 1s
HTTP/2.0 429 Too Many Requests
Cache-Control: private
Cf-Cache-Status: MISS
Cf-Ray: 80e668787b3ec59c-SEA
Content-Length: 0
Date: Fri, 29 Sep 2023 18:49:47 GMT
Server: cloudflare
Vary: Accept-Encoding
X-Do-App-Origin: 18871cde-e6ba-11ec-b1dc-0c42a19a82a7
X-Do-Orig-Status: 429
X-Varied-Accept-Encoding: deflate, gzip, br
```

By default, Restish will wait 1 second between retries. If the server responds with one of the following headers, it will be parsed and used to determine the retry delay:

- `Retry-After` ([RFC 7231](https://tools.ietf.org/html/rfc7231#section-7.1.3))
- `X-Retry-In` (as set by e.g. [Traefik](https://doc.traefik.io/traefik/middlewares/http/ratelimit/) [rate limiting](https://github.com/traefik/traefik/blob/v2.10/pkg/middlewares/ratelimiter/rate_limiter.go#L176-L177))

For example:

```bash
# Trigger delayed retries with a 429 and Retry-After header.
$ restish api.rest.sh/status/429?retry-after=3
WARN: Got 429 Too Many Requests, retrying in 3s
WARN: Got 429 Too Many Requests, retrying in 3s
HTTP/2.0 429 Too Many Requests
Cache-Control: private
Cf-Cache-Status: MISS
Cf-Ray: 80e669fbb95a283d-SEA
Content-Length: 0
Date: Fri, 29 Sep 2023 18:50:49 GMT
Retry-After: 3
Server: cloudflare
Vary: Accept-Encoding
X-Do-App-Origin: 18871cde-e6ba-11ec-b1dc-0c42a19a82a7
X-Do-Orig-Status: 429
X-Varied-Accept-Encoding: br, deflate, gzip
```

## Request Timeouts

Restish has optional timeouts you can set on outgoing requests using the `--rsh-timeout` parameter or `RSH_TIMEOUT` environment variable. This should be a duration with suffix, e.g. `1s` or `500ms`. Set to `0` to disable timeouts (which is the default). Timeouts are retried since they are often due to intermittent network issues and subsequent requests may succeed.

Here is an example of a timeout:

```bash
# Trigger a timeout with a ridiculously low value.
$ restish api.rest.sh/ --rsh-timeout=10ms
WARN: Got request timeout after 10ms, retrying
WARN: Got request timeout after 10ms, retrying
ERROR: Caught error: Request timed out after 10ms: Get "https://api.rest.sh/": context deadline exceeded
```
