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
- Artifacts are immutable S3 object references or publisher-supplied HTTPS/HTTP
  download links. DistributionCenter never proxies artifact bytes or accepts
  caller-supplied S3 size/hash metadata.
- Draft releases are private control-plane records. Published releases remain
  editable by publisher members; yanked releases are immutable historical records.

## Authentication and authorization

Public product, release, channel, and update routes are unauthenticated.
Mutation routes require `Authorization: Bearer <Sphere access token>`.
DistributionCenter sends that token to Stargate `DyAuthService.Authenticate`,
then checks the resulting account with Sphere
`DyPublisherService.IsPublisherMember` at editor role or higher.

Publisher editors can create app-level upload keys for CI/CD. Upload keys are
stored as hashes and the plaintext is shown only once:

```http
POST /api/products/{product_id}/upload-api-keys
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{"name":"GitHub Actions"}
```

The response contains `key`; store it as a CI secret. List and revoke keys with
`GET /api/products/{product_id}/upload-api-keys` and
`DELETE /api/products/{product_id}/upload-api-keys/{key_id}`. An upload key is
scoped to its product and is accepted only for the upload URL and artifact
attach endpoints. It cannot create, publish, edit, or delete releases.

The legacy custom-app secret is not accepted or persisted by the production
publisher surface.

## Product routes

Create a product under a publisher:

```http
POST /api/publishers/{publisher_name}/products
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{"slug":"desktop-client","name":"Desktop Client","names":{"en-US":"Desktop Client","zh-CN":"桌面客户端"},"description":"...","descriptions":{"en-US":"Desktop client","zh-CN":"桌面客户端"},"icon":{"id":"icon-file","name":"Icon","mime_type":"image/png","size":1234},"background":{"id":"hero-file","name":"Hero","mime_type":"image/png","size":5678},"previews":[{"id":"preview-file","name":"Preview","mime_type":"image/png","size":3456}]}
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

{"slug":"desktop-client","name":"Desktop Client","names":{"en-US":"Desktop Client","zh-CN":"桌面客户端"},"description":"...","descriptions":{"en-US":"Desktop client","zh-CN":"桌面客户端"},"icon":{"id":"icon-file","name":"Icon","mime_type":"image/png","size":1234},"background":{"id":"hero-file","name":"Hero","mime_type":"image/png","size":5678},"previews":[{"id":"preview-file","name":"Preview","mime_type":"image/png","size":3456}]}

DELETE /api/products/{product_id}
Authorization: Bearer <Sphere access token>
```

Deleting a product also removes its DistributionCenter release, channel, and
release-artifact metadata. It does not delete already-uploaded S3 objects.

## Release workflow

Prepare an S3 upload URL for each artifact:

```http
POST /api/products/{product_id}/artifacts/upload-url
Authorization: Bearer <Sphere access token or app upload key>
Content-Type: application/json

{"file_name":"client.tar.gz","mime_type":"application/gzip"}
```

Upload the bytes to each returned URL with `x-amz-meta-sha256`, then create
the draft release without artifact entries:

```http
POST /api/products/{product_id}/releases
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{
  "version":"1.4.0",
  "channels":["stable"],
  "title":"Maintenance release",
  "titles":{
    "en-US":"Maintenance release",
    "zh-CN":"维护版本"
  },
  "release_notes":"...",
  "metadata":{
    "minimum_os":"13.0",
    "rollout":"ring-a"
  },
  "force_update":false,
  "descriptions":{
    "en-US":"Bug fixes and performance improvements.",
    "zh-CN":"错误修复和性能改进。"
  },
  "attachments":[
    {"id":"release-banner","name":"Release banner","mime_type":"image/png","size":1234},
    {"url":"https://cdn.example/releases/1.4.0/notes.pdf","name":"Release notes","mime_type":"application/pdf"}
  ]
}
```

Attach each completed upload in a separate request. DistributionCenter
rechecks the object metadata and records the immutable file metadata:

```http
POST /api/products/{product_id}/releases/{release_id}/artifacts
Authorization: Bearer <Sphere access token or app upload key>
Content-Type: application/json

{"object_key":"artifacts/.../client.tar.gz","platform":"macos","architecture":"arm64"}
```

For artifacts hosted outside DistributionCenter, provide an absolute HTTP(S)
URL instead of `object_key`. The optional file metadata is stored as supplied:

```json
{"download_url":"https://downloads.example.com/client.tar.gz","file_name":"client.tar.gz","mime_type":"application/gzip","size":1234,"hash":"sha256...","platform":"macos","architecture":"arm64"}
```

Published releases return the direct URL for externally hosted artifacts. For
S3 artifacts, `download_url` is a public CDN URL when `s3.publicURL` is set;
otherwise it is a time-limited signed GET URL.

After all artifacts are attached, publish the release:

```http
POST /api/products/{product_id}/releases/{release_id}/publish
Authorization: Bearer <Sphere access token>
```

Publishing makes all attached objects public and requires at least one
complete artifact. A published release remains editable through the release
update endpoint; yanked releases remain locked.


## Public release and update routes


Clients may submit the same check as JSON, which also records installation
telemetry when `installation_id` is a UUID:

```http
POST /api/products/{product_id}/update/check
Content-Type: application/json

{
  "version":"1.3.0",
  "channel":"stable",
  "os":"macos",
  "architecture":"arm64",
  "installation_id":"<installation-uuid>",
  "os_version":"14.5",
  "client_version":"2.8.1",
  "locale":"en-US"
}
```

The response includes the newer `release`, including publisher-defined
`metadata` and `force_update`. `force_update:true` tells the client that the
published update must be applied rather than deferred. The server still only
returns a release newer than the submitted version.

`os` identifies the operating system and is matched against release artifact
`platform`; `architecture` selects the CPU artifact. `os_version`,
`client_version`, and `locale` are recorded for telemetry and do not change
artifact matching. The legacy `platform` field is accepted as an alias for
`os`.

```http
GET /api/products/{product_id}/channels
GET /api/products/{product_id}/releases?channel=stable&platform=macos&architecture=arm64&limit=20&offset=0
GET /api/products/{product_id}/update?current_version=1.3.0&os=macos&architecture=arm64&channel=stable
```

Release titles use `title` for the default text and `titles` for localized
BCP-47 values. Clients can select the matching locale from `titles`, with
`title` as the fallback.

Channel and release metadata keep the legacy `description`/`release_notes`
values and may additionally expose `descriptions`, keyed by BCP-47 locale
tags such as `en-US` and `zh-CN`. Clients choose a translation from that map.
The publisher metrics endpoint is `GET /api/products/{product_id}/metrics`
and includes `checks`, `dau`, `mau`, plus grouped counts such as
`by_version`, `by_channel`, `by_platform`, `by_architecture`,
`by_os_version`, `by_client_version`, and `by_locale`.
When installation telemetry is enabled, checks include `by_version`; checks
without a locale are counted under `und`.
Product icons, backgrounds, previews, and release attachments use cached
cloud-file reference objects. They retain file identity plus immutable metadata
such as name, MIME type, hash, size, dimensions, blurhash, usage, and timestamps.
An object may use `id` for a local cloud file or `url` for an external file.
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
publicURL = "https://cdn.example/distribution" # optional; unset uses signed GET URLs
```

`[eventbus] url` is optional and release events remain best effort. The
`distribution` capability advertises REST API revision 2.
