# Comparison to other tools

See how Restish compares to other tools below:

| Feature                                              | Restish | HTTPie        | cURL            |
| ---------------------------------------------------- | ------- | ------------- | --------------- |
| Implementation Language                              | Go      | Python        | C               |
| Fast native no-dependency binary                     | ✅      | ❌            | ✅              |
| HEAD/GET/POST/PUT/PATCH/DELETE/OPTIONS/etc           | ✅      | ✅            | ✅              |
| HTTPS by default                                     | ✅      | ❌            | ❌              |
| HTTP/2 by default                                    | ✅      | ❌            | ❌              |
| OAuth2.0 token fetching/caching                      | ✅      | 🟠 (plugin)   | ❌              |
| Authentication profiles                              | ✅      | 🟠 (sessions) | ❌              |
| Content negotiation by default                       | ✅      | 🟠 (encoding) | ❌              |
| gzip encoding                                        | ✅      | ✅            | ❌              |
| brotli encoding                                      | ✅      | ❌            | ❌              |
| CBOR & MessagePack binary format decoding            | ✅      | ❌            | ❌              |
| Local cache via `Cache-Control` or `Expires` headers | ✅      | ❌            | ❌              |
| Shorthand for structured data input                  | ✅      | ✅            | ❌              |
| Loading structured data fields from files via `@`    | ✅      | ✅            | ❌              |
| Raw input via stdin just works                       | ✅      | ✅            | 🟠 (via `-d@-`) |
| Per-domain configuration (e.g. default headers)      | ✅      | ❌            | ❌              |
| API nicknames, e.g. `github/users/repos`             | ✅      | ❌            | ❌              |
| OpenAPI 3 support                                    | ✅      | ❌            | ❌              |
| API documentation & examples via `--help`            | ✅      | ❌            | ❌              |
| Syntax highlighting                                  | ✅      | ✅            | ❌              |
| Pretty printing                                      | ✅      | ✅            | ❌              |
| Image response preview in terminal                   | ✅      | ❌            | ❌              |
| Structured response filtering                        | ✅      | ❌            | ❌              |
| Hypermedia link parsing                              | ✅      | ❌            | ❌              |
| Automatic pagination for `next` link relations       | ✅      | ❌            | ❌              |
| URL & command shell completion hints                 | ✅      | ❌            | ❌              |

# Performance Comparison

Test were run on a Macbook Pro and averages of several requests are reported as latency can and does vary. The general takeaway is that performance is better than HTTPie and very close to cURL but with many more features. Numbers below are in seconds.

| Test                           | Restish | Restish (cached) | HTTPie |  cURL |
| ------------------------------ | ------: | ---------------: | -----: | ----: |
| Github list repos              |   1.210 |            0.620 |  1.358 | 1.122 |
| Github list repo collaborators |   0.251 |            0.049 |  0.702 | 0.212 |
| Digital Ocean get account      |   0.512 | no cache headers |  0.786 | 0.526 |
| Get `httpbin.org/cache/60`     |   0.401 |            0.025 |  0.707 | 0.371 |

As the above shows, if caching is enabled at the API level then Restish can actually outperform `curl` in some scenarios. Imagine the following naive shell script where a single user might own many items and the `get-user` operation is cacheable:

```bash
for ITEM_ID in $(restish my-api list-items); do
  USER_ID=$(restish my-api get-item $ITEM_ID -f body.user_id)
  # The following call is going to be cached sometimes, saving us time!
  EMAIL=$(restish my-api get-user $USER_ID -f body.email)
  echo "$ITEM_ID is owned by $EMAIL"
done
```

This can be demonstrated in a very silly example with `zsh` showing how these small differences can easily compound to many second differences in how fast your scripts may run:

```bash
# curl total time: 3.968s
time (repeat 10 {curl https://httpbin.org/cache/60})

# HTTPie total time: 6.699s
time (repeat 10 {https https://httpbin.org/cache/60})

# Restish total time: 0.702s (first request is not cached)
time (repeat 10 {restish https://httpbin.org/cache/60})
```

# Detailed Comparisons

## Passing Headers & Query Params

This is how you pass parameters to the API.

cURL Example:

```bash
curl -H Header:value 'https://api.rest.sh/?a=1&b=true'
curl -H Header:value https://api.rest.sh/ -G -d a=1 -d b=true
```

HTTPie Example:

```bash
https Header:value 'api.rest.sh/?a=1&b=true'
https Header:value api.rest.sh/ a==1 b==true
```

Restish Example:

```bash
restish -H Header:value 'api.rest.sh/?a=1&b=true'
restish -H Header:value api.rest.sh/ -q a=1 -q b=true
```

## Input Shorthand

This is one mechanism to generate and pass a request body to the API.

cURL Example: n/a

HTTPie Example:

```bash
https post api.rest.sh \
  platform[name]=HTTPie \
  platform[about][mission]='Make APIs simple and intuitive' \
  platform[about][homepage]=httpie.io \
  platform[about][stars]:=54000 \
  platform[apps][]=Terminal \
  platform[apps][]=Desktop \
  platform[apps][]=Web \
  platform[apps][]=Mobile
```

Restish equivalent:

```bash
restish post api.rest.sh \
  platform.name: HTTPie, \
  platform.about.mission: Make APIs simple and intuitive, \
  platform.about.homepage: httpie.io, \
  platform.about.stars: 54000, \
  platform.apps: [Terminal, Desktop, Web, Mobile]
```

## Getting Header Values

How easy is it to read the output of a header in a shell environment?

cURL Exmaple:

```bash
curl https://api.rest.sh/ --head 2>/dev/null | grep -i Content-Length | cut -d':' -d' ' -f2
```

HTTPie Example:

```bash
https --headers api.rest.sh | grep Content-Length | cut -d':' -d' ' -f2
```

Restish Example:

```bash
restish api.rest.sh -f 'headers.Content-Length' -r
```
