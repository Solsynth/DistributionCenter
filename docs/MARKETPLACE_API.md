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
DistributionCenter sends that token to Stargate `DyAuthService.Authenticate`,
then checks the resulting account with Sphere
`DyPublisherService.IsPublisherMember` at editor role or higher. No
custom-app secret is accepted or persisted.

## Product routes

Create a product under a publisher:

```http
POST /api/publishers/{publisher_name}/products
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{"slug":"desktop-client","name":"Desktop Client","description":"...","icon":"file:icon.png","background_image":"file:hero.png","previews":["file:preview-1.png","file:preview-2.png"]}
```

List a publisher's products:

```http
GET /api/publishers/{publisher_name}/products
```

Public product metadata:

```http
GET /api/products/{product_id}
```

The response is `{product, publisher, latest}`. `latest` is the newest
published `stable` release or `null`.
Publisher members can replace product metadata with a full body and delete a
product:

```http
PUT /api/products/{product_id}
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{"slug":"desktop-client","name":"Desktop Client","description":"...","icon":"file:icon.png","background_image":"file:hero.png","previews":["file:preview-1.png"]}

DELETE /api/products/{product_id}
Authorization: Bearer <Sphere access token>
```

Deleting a product also removes its DistributionCenter release, channel, and
release-artifact metadata. It does not delete already-uploaded S3 objects.

## Release workflow

Prepare an S3 upload URL:

```http
POST /api/products/{product_id}/artifacts/upload-url
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{"file_name":"client.tar.gz","mime_type":"application/gzip"}
```

Upload the bytes to the returned URL with `x-amz-meta-sha256`, then create a
draft release:

```http
POST /api/products/{product_id}/releases
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{
  "version":"1.4.0",
  "channels":["stable"],
  "release_notes":"...",
  "descriptions":{
    "en-US":"Bug fixes and performance improvements.",
    "zh-CN":"错误修复和性能改进。"
  },
  "attachments":["file:release-banner.png","https://cdn.example/releases/1.4.0/notes.pdf"],
  "artifacts":[
    {"object_key":"artifacts/.../client.tar.gz","platform":"macos","architecture":"arm64"}
  ]
}
```

Publish or yank a release:

```http
POST /api/products/{product_id}/releases/{release_id}/publish
Authorization: Bearer <Sphere access token>

POST /api/products/{product_id}/releases/{release_id}/yank
Authorization: Bearer <Sphere access token>
```

Publication rechecks every object, requires non-empty hash metadata, tags all
objects `public=true`, and commits the release transition only after all tags
succeed. Tag failures compensate earlier tags best effort. Publish and yank are
idempotent when the release is already in the requested terminal state.

## Public release and update routes

```http
GET /api/products/{product_id}/channels
GET /api/products/{product_id}/releases?channel=stable&platform=macos&architecture=arm64&limit=20&offset=0
GET /api/products/{product_id}/update?current_version=1.3.0&channel=stable&platform=macos&architecture=arm64
```

Channel and release metadata keep the legacy `description`/`release_notes`
values and may additionally expose `descriptions`, keyed by BCP-47 locale
tags such as `en-US` and `zh-CN`. Clients choose a translation from that map.
The update endpoint accepts `locale` or the first `Accept-Language` value.
When installation telemetry is enabled, metrics include `by_locale`; checks
without a locale are counted under `und`.

The resolver filters the exact requested channel and artifact target, then
returns the highest published version strictly greater than `current_version`.
No compatible release returns HTTP 200 with `update_available:false` and
`release:null`. There is no implicit channel, platform, or architecture
fallback.

## Configuration

Production startup requires PostgreSQL, Stargate auth, Sphere publisher, and S3 settings:

```toml
[database]
dsn = "postgres://..."

[auth]
target = "stargate:9090"
useTLS = false
tlsSkipVerify = false

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
