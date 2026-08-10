# Distribution Marketplace API

DistributionCenter is the release control plane for publisher-owned products. Sphere
owns accounts, publishers, publisher membership, and bearer-token authentication.
DistributionCenter stores products, immutable release metadata, channels, and
S3 object metadata. It never treats a Develop custom app as a product identity.

## Ownership model

- A `product` belongs to one Sphere `publisher_id`.
- A product has a DistributionCenter UUID and a publisher-scoped slug.
- Releases belong to a product and may target multiple channels.
- `stable`, `beta`, and `nightly` are built-in channels. Custom channels are
  product-scoped and must be created before use.
- Release versions are SemVer without a leading `v`.
- Artifacts are immutable S3 object references. DistributionCenter never proxies
  artifact bytes or accepts caller-supplied size/hash metadata.
- Draft releases are private control-plane records. Published and yanked
  releases are immutable historical records.

## Authentication and authorization

Public product, release, channel, and update routes are unauthenticated.
Mutation routes require `Authorization: Bearer <Sphere access token>`.
DistributionCenter sends that token to Sphere `DyAuthService.Authenticate`, then
checks the resulting account with `DyPublisherService.IsPublisherMember` at
editor role or higher. No custom-app secret is accepted or persisted.

## Product routes

Create a product under a publisher:

```http
POST /api/v1/publishers/{publisher_id}/products
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{"slug":"desktop-client","name":"Desktop Client","description":"..."}
```

List a publisher's products:

```http
GET /api/v1/publishers/{publisher_id}/products
```

Public product metadata:

```http
GET /api/v1/products/{product_id}
```

The response is `{product, publisher, latest}`. `latest` is the newest
published `stable` release or `null`.

## Release workflow

Prepare an S3 upload URL:

```http
POST /api/v1/products/{product_id}/artifacts/upload-url
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{"file_name":"client.tar.gz","mime_type":"application/gzip"}
```

Upload the bytes to the returned URL with `x-amz-meta-sha256`, then create a
draft release:

```http
POST /api/v1/products/{product_id}/releases
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{
  "version":"1.4.0",
  "channels":["stable"],
  "release_notes":"...",
  "artifacts":[
    {"object_key":"artifacts/.../client.tar.gz","platform":"macos","architecture":"arm64"}
  ]
}
```

Publish or yank a release:

```http
POST /api/v1/products/{product_id}/releases/{release_id}/publish
Authorization: Bearer <Sphere access token>

POST /api/v1/products/{product_id}/releases/{release_id}/yank
Authorization: Bearer <Sphere access token>
```

Publication rechecks every object, requires non-empty hash metadata, tags all
objects `public=true`, and commits the release transition only after all tags
succeed. Tag failures compensate earlier tags best effort. Publish and yank are
idempotent when the release is already in the requested terminal state.

## Public release and update routes

```http
GET /api/v1/products/{product_id}/channels
GET /api/v1/products/{product_id}/releases?channel=stable&platform=macos&architecture=arm64&limit=20&offset=0
GET /api/v1/products/{product_id}/update?current_version=1.3.0&channel=stable&platform=macos&architecture=arm64
```

The resolver filters the exact requested channel and artifact target, then
returns the highest published version strictly greater than `current_version`.
No compatible release returns HTTP 200 with `update_available:false` and
`release:null`. There is no implicit channel, platform, or architecture
fallback.

## Configuration

Production startup requires PostgreSQL, Sphere gRPC, and S3 settings:

```toml
[database]
dsn = "postgres://..."

[sphere]
target = "sphere:9090"
useTLS = false
tlsSkipVerify = false

[s3]
endpoint = "https://s3.example"
accessKey = "..."
secretKey = "..."
bucket = "distribution"
publicURL = "https://cdn.example/distribution"
```

`[eventbus] url` is optional and release events remain best effort. The
`distribution` capability advertises REST API revision 2.
