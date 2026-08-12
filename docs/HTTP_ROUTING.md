# HTTP routing and public API base URLs

This document defines how to expose DistributionCenter through a public host.
It is separate from the marketplace API contract because it describes ingress,
service discovery, and URL composition rather than product or release behavior.

## Route contract

DistributionCenter registers its publisher API under `/api`:

```text
GET  /api/marketplace/apps
GET  /api/publishers/{publisher_name}/apps
GET  /api/products/{product_id}
GET  /api/products/{product_id}/releases
GET  /api/products/{product_id}/update
POST /api/products/{product_id}/artifacts/upload-url
POST /api/products/{product_id}/releases/{release_id_or_version}/artifacts
POST /api/products/{product_id}/releases/{release_id}/publish
```

The process also exposes operational routes at the service root:

```text
GET /       service metadata
GET /health liveness
GET /alive  liveness alias
GET /ready  readiness
```

`serviceName = "distribution"` does not add `/distribution` to the HTTP
routes. It identifies the service in Blade discovery and in the root metadata
response.

## `DISTRIBUTION_API_BASE_URL`

Clients compose API paths by appending `/products/...` to
`DISTRIBUTION_API_BASE_URL`. Therefore the configured value must include the
public `/api` prefix and must not include a trailing product path:

```text
DISTRIBUTION_API_BASE_URL + /products/{product_id}/update
```

Use the same base URL in GitHub Actions and in Island's Flutter build defines.

## Public host layouts

### Dedicated host

If the public host routes directly to DistributionCenter:

```text
Public base:  https://distribution.example.com/api
Request:      https://distribution.example.com/api/products/{product_id}/update
```

Configuration:

```text
DISTRIBUTION_API_BASE_URL=https://distribution.example.com/api
```

### Root host

If `solian.app` routes its root HTTP traffic directly to DistributionCenter:

```text
Public base:  https://solian.app/api
Request:      https://solian.app/api/products/{product_id}/update
```

Configuration:

```text
DISTRIBUTION_API_BASE_URL=https://solian.app/api
```

The service name remains `distribution`; it does not need to appear in the
public URL.

### Path-mounted host

If an ingress intentionally mounts DistributionCenter at `/dist`, the public
base is:

```text
https://solian.app/dist/api
```

The ingress must remove `/dist` before forwarding to the DistributionCenter
HTTP server:

```text
Public request:   /dist/api/products/{product_id}/update
Forwarded request: /api/products/{product_id}/update
```

Configuration:

```text
DISTRIBUTION_API_BASE_URL=https://solian.app/dist/api
```

A path prefix alone is not enough. DistributionCenter does not register
`/dist/api` internally; the reverse proxy must either rewrite the prefix or
route the external prefix to a backend that performs that rewrite.

Do not use only `https://solian.app/dist` unless the proxy separately maps
`/dist/products/...` to the backend's `/api/products/...` route. The standard
configuration includes `/api` explicitly.

## Reverse-proxy requirements

The public proxy should:

1. Forward HTTP methods, request bodies, query strings, and authorization
   headers unchanged.
2. Rewrite an optional public path prefix away before forwarding.
3. Preserve `Content-Type` for JSON requests and presigned-upload responses.
4. Allow the presigned S3 `PUT` request to go directly to the URL returned by
   DistributionCenter. The API proxy does not proxy artifact bytes.
5. Expose `/health`, `/alive`, and `/ready` for service probes, either at the
   public root or through an internal-only upstream.
6. Use HTTPS for public traffic.

Example Nginx layout for a `/dist` mount:

```nginx
location /dist/ {
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header Authorization $http_authorization;
    proxy_pass http://distribution:8080/;
}
```

The trailing slash on `proxy_pass` is significant: it removes `/dist/` before
forwarding the request. With this layout, the external API base remains:

```text
https://solian.app/dist/api
```

For a dedicated host, the proxy can forward without a path rewrite:

```nginx
server {
    server_name distribution.example.com;

    location / {
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Authorization $http_authorization;
        proxy_pass http://distribution:8080;
    }
}
```

The corresponding public API base is:

```text
https://distribution.example.com/api
```

## Service discovery versus public routing

The local configuration contains both HTTP and discovery settings:

```toml
serviceName = "distribution"

[http]
port = "8080"

[discovery]
enabled = true
service = "distribution"
httpEndpoint = "http://distribution:8080"
grpcEndpoint = "http://distribution:9090"
```

These have separate purposes:

- `serviceName`: root metadata and default discovery identity.
- `discovery.service`: Blade registration name.
- `discovery.httpEndpoint`: internal HTTP endpoint advertised to the service
  mesh.
- `http.port`: local listener port.
- Public DNS and `/dist` prefixes: ingress configuration outside the Go
  service.

Changing `serviceName` or `discovery.service` does not change the Gin HTTP
route prefix. Add a public path prefix only in the reverse proxy, then include
that prefix in `DISTRIBUTION_API_BASE_URL`.

## Verification

Check the upstream directly:

```sh
curl --fail -i http://127.0.0.1:8080/health
curl --fail -i http://127.0.0.1:8080/api/products/{product_id}
```

Check a public dedicated-host deployment:

```sh
curl --fail -i https://distribution.example.com/health
curl --fail -i https://distribution.example.com/api/products/{product_id}
```

Check a `/dist` deployment:

```sh
curl --fail -i https://solian.app/dist/health
curl --fail -i https://solian.app/dist/api/products/{product_id}
```

A `404` on `/dist/api/...` usually means the proxy did not strip `/dist`.
A `404` on `/api/...` usually means the public host is not routed to
DistributionCenter. A `401` or `403` on mutation routes indicates an
authentication or publisher-membership issue, not a URL-prefix issue.
