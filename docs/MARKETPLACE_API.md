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
Mutation routes accept either `Authorization: Bearer <Sphere access token>` or
the `AuthToken` cookie used by the DysonNetwork web clients.
DistributionCenter sends that token to Stargate `DyAuthService.Authenticate`,
then checks the resulting account with Sphere
`DyPublisherService.IsPublisherMember` at editor role or higher. Superuser
accounts bypass the fine-grained permission-node check, but publisher
membership is still required for publisher-owned operations. Non-superuser
accounts also require the route's fine-grained Stargate permission node:

| Operation | Permission |
| --- | --- |
| Create product | `app.products.create` |
| Update product | `app.products.update` |
| Delete product | `app.products.delete` |
| Create, list, or revoke upload keys | `distribution.upload_keys.manage` |
| Create or edit releases | `distribution.releases.manage` |
| Publish or yank releases | `distribution.releases.publish` |
| Create, edit, or delete custom channels | `distribution.channels.manage` |
| Read publisher metrics | `distribution.metrics.read` |
| Upload URL or artifact attach with a Sphere token | `distribution.artifacts.upload` |

The permission check is independent from publisher membership: an account must
pass both checks. Stargate permission-service failures return `503`; a missing
permission returns `403`. Product-scoped upload keys remain intentionally
limited to upload URL and artifact attach endpoints and bypass the
Sphere-token permission check for those two operations only.

Publisher editors can create app-level upload keys for CI/CD. Upload keys are
stored as hashes and the plaintext is shown only once:

```http
POST /api/products/{product_id}/upload-api-keys
Authorization: Bearer <Sphere access token>
Content-Type: application/json

{"name":"GitHub Actions"}
```

`GET /api/products/{product_id}/upload-api-keys` and
`DELETE /api/products/{product_id}/upload-api-keys/{key_id}` use the same
`distribution.upload_keys.manage` permission. An upload key is scoped to its
product and is accepted only for the upload URL and artifact attach endpoints.
It cannot create, publish, edit, or delete releases.

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
release-artifact metadata. It does not delete already-uploaded S3 objects;
release retention cleanup is separate.

## Channel management

Publisher members can create and edit channels with
`distribution.channels.manage`. Custom channels can also be deleted:

```http
DELETE /api/products/{product_id}/channels/{channel_id}
Authorization: Bearer <Sphere access token>
```

Deleting a custom channel detaches it from releases and removes its localized
metadata. The built-in `stable`, `beta`, and `nightly` channels cannot be
deleted.

## Release workflow

Prepare an S3 upload URL for an artifact. Include the release version and,
when creating a new draft, the desired channel. If the version already exists,
the upload response references that release; otherwise DistributionCenter uses
the requested channel and defaults to `stable`:

```http
POST /api/products/{product_id}/artifacts/upload-url
Authorization: Bearer <Sphere access token or app upload key>
Content-Type: application/json

{"version":"1.4.0","channel":"beta","file_name":"client.tar.gz","mime_type":"application/gzip","sha256":"<lowercase SHA-256 digest>"}
```

The response includes `object_key`, `upload_url`, `release_id`, and `version`.
Upload the bytes to `upload_url` with the same `Content-Type` and
`x-amz-meta-sha256` values supplied in the preparation request, then attach the
object using the returned release ID or the version in the artifact endpoint.

Alternatively, create the release explicitly before uploading when you need
custom release metadata at creation time. Do not create the same version after
the versioned upload request has already auto-created its draft:

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
rechecks the object metadata and records the immutable file metadata. The
path segment may be the returned release ID or the release version:

```http
POST /api/products/{product_id}/releases/{release_id_or_version}/artifacts
Authorization: Bearer <Sphere access token or app upload key>
Content-Type: application/json

{"object_key":"artifacts/.../client.tar.gz","platform":"macos","architecture":"arm64"}
```

For artifacts hosted outside DistributionCenter, provide an absolute HTTP(S)
URL instead of `object_key`. The optional file metadata is stored as supplied:

```json
{"download_url":"https://downloads.example.com/client.tar.gz","file_name":"client.tar.gz","mime_type":"application/gzip","size":1234,"hash":"sha256...","platform":"macos","architecture":"arm64"}
```

Published releases return the direct URL for externally hosted artifacts. S3
artifacts remain private and return a time-limited signed GET URL generated by
DistributionCenter.

After all artifacts are attached, publish the release:

```http
POST /api/products/{product_id}/releases/{release_id}/publish
Authorization: Bearer <Sphere access token>
```

Publishing requires at least one complete artifact. A published release remains
editable through the release update endpoint; yanked releases remain locked.

Publisher editors can inspect unpublished releases through the authenticated
management listing:

```http
GET /api/products/{product_id}/releases/manage?channel=stable&limit=100&offset=0
Authorization: Bearer <Sphere access token>
```

This returns draft, published, and yanked releases. The public
`/releases` listing remains limited to published releases.

Draft releases can be permanently removed. Published releases must be yanked
instead:

```http
DELETE /api/products/{product_id}/releases/{release_id}
Authorization: Bearer <Sphere access token>
```

Deletion requires `distribution.releases.manage`, removes release metadata and
stored artifact objects when the configured artifact backend supports deletion,
and is accepted only for draft releases.

Deployments retain the newest three published releases' S3 artifacts per
product by default. Configure `releases.artifactRetention` in TOML or
`DISTRIBUTION_RELEASES_ARTIFACT_RETENTION`; set it to `0` to disable cleanup.
When an artifact is cleaned up, its release metadata remains available and the
artifact is returned with `expired: true` and no `download_url`. External
`download_url` artifacts are never removed by this cleanup.


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
```

`[eventbus] url` is optional and release events remain best effort. The
`distribution` capability advertises REST API revision 2.
