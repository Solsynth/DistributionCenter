# Distribution Marketplace API

This document describes the DistributionCenter control-plane workflow and the
client update flow. DistributionCenter stores release metadata in PostgreSQL,
uses Develop as the source of app identity and publisher metadata, and stores
release artifacts in the dedicated S3 bucket configured under `[s3]`.

## Model and invariants

- A release belongs to one Develop app, one semantic version, and one channel.
- Valid channels are `stable`, `beta`, and `nightly`.
- Versions use SemVer without a leading `v`, for example `1.4.0` or
  `2.0.0-rc.1`.
- A release may contain any number of artifacts. The developer chooses the
  supported `platform` and `architecture` values; DistributionCenter does not
  prescribe a platform matrix or fill missing values.
- Each release must have at least one artifact.
- A release cannot contain two artifacts with the same normalized
  `(platform, architecture)` target.
- Artifact metadata is read from S3 after upload. Clients cannot spoof file
  name, MIME type, size, or hash in the release request.
- `(app_id, version, channel)` is unique. The same version may therefore be
  released independently on multiple channels.
- Draft releases are private control-plane records. Published and yanked
  releases are immutable historical records.
- Public catalog and update endpoints never return draft or yanked releases.

Channel, platform, and architecture are independent dimensions. For example,
a developer can publish `1.4.0` on `stable` for `macos/arm64` and
`windows/x86_64`, while publishing `1.5.0-beta.1` on `beta` for a different
set of targets. The update resolver only selects a release in the exact
requested channel with an exact artifact target match.

## Authentication

Public catalog and update routes do not require authentication. Developer
mutation routes require exactly one bearer token:

```http
Authorization: Bearer <app-secret>
```

The secret is checked by Develop with `is_oidc=false`. DistributionCenter does
not persist or log the secret. Missing or invalid credentials return `401`.
Develop errors return `503`; a suspended app returns `403` for developer
mutations and is hidden from public routes.

## Control-plane release workflow

### 1. Prepare the app and S3 bucket

The app must exist in Develop. It must be moved to `DY_PRODUCTION` before a
release can be published or exposed publicly.

The S3 bucket must be private by default and configured so that an object is
publicly readable only when it has the object tag `public=true`. The configured
`[s3].publicURL` must be the externally reachable base URL for that bucket or
its CDN. DistributionCenter applies the `public=true` tag only after a
successful publish.

### 2. Request an upload URL

```http
POST /api/v1/apps/{app_id}/artifacts/upload-url
Authorization: Bearer <app-secret>
Content-Type: application/json

{
  "file_name": "acme-client.tar.gz",
  "mime_type": "application/gzip"
}
```

Response:

```json
{
  "object_key": "artifacts/2f6.../9a1.../acme-client.tar.gz",
  "upload_url": "https://s3.example/upload?..."
}
```

The upload URL is presigned for a direct S3 `PUT`. Upload the bytes directly
to S3; do not send the binary through DistributionCenter. Include the final
content type and the SHA-256 value as S3 user metadata so the control plane can
validate the object:

```http
PUT <upload_url>
Content-Type: application/gzip
x-amz-meta-sha256: sha256-...

<artifact bytes>
```

The returned `object_key` is scoped to the app and must be supplied unchanged
when creating the release. The object must exist and have a non-negative size
and non-empty SHA-256 metadata before it can be referenced.

### 3. Create a draft release

```http
POST /api/v1/apps/{app_id}/releases
Authorization: Bearer <app-secret>
Content-Type: application/json

{
  "version": "1.4.0",
  "channel": "stable",
  "release_notes": "Adds offline sync.",
  "artifacts": [
    {
      "object_key": "artifacts/2f6.../9a1.../acme-client-macos-arm64.tar.gz",
      "platform": "macos",
      "architecture": "arm64"
    },
    {
      "object_key": "artifacts/2f6.../2b4.../acme-client-windows-x86_64.zip",
      "platform": "windows",
      "architecture": "x86_64"
    }
  ]
}
```

The response is `201` and has `status: "draft"`. `channel`, `platform`, and
`architecture` are required. There are no preset platform or architecture
values. Unknown channels, invalid SemVer, missing objects, incomplete objects,
and duplicate target tuples are rejected. A duplicate version/channel returns
`409`.

### 4. Publish the draft

```http
POST /api/v1/apps/{app_id}/releases/{release_id}/publish
Authorization: Bearer <app-secret>
```

Publishing performs all of the following before changing the database state:

1. Rechecks the Develop app and requires `DY_PRODUCTION`.
2. Rechecks every referenced S3 object and its hash metadata.
3. Tags every artifact with `public=true`.
4. Compensates already-tagged objects if a later tag operation fails.
5. Changes the release to `published` and sets `published_at`.
6. Best-effort publishes `distribution.release.published.v1`.

Repeated publish requests for an already-published release return the same
release. Publishing a yanked release is rejected.

### 5. Yank a published release

```http
POST /api/v1/apps/{app_id}/releases/{release_id}/yank
Authorization: Bearer <app-secret>
```

Yanking removes the release from catalog and update resolution. It does not
rewrite release metadata. Repeated yank requests are idempotent. The event
`distribution.release.yanked.v1` is published best effort.

## Public API

All public routes are rooted at `/api/v1/apps/{app_id}`.

### App metadata

```http
GET /api/v1/apps/{app_id}
```

Response shape:

```json
{
  "app": { "id": "...", "name": "...", "status": 3 },
  "developer": { "id": "...", "publisher_name": "..." },
  "latest": null
}
```
The `app` and `developer` objects are Develop protobuf payloads. Enum fields
such as `status` use their protobuf numeric representation in JSON.

`latest` is the newest published `stable` release, or `null` when none exists.
Only production apps are visible.

### Release catalog

```http
GET /api/v1/apps/{app_id}/releases
  ?channel=beta
  &platform=macos
  &architecture=arm64
  &limit=20
  &offset=0
```

`channel` is required and must be `stable`, `beta`, or `nightly`. There is no
implicit channel default. `platform` and `architecture` are optional, but must
be supplied together when used. The pair is matched exactly against an
artifact target. Results contain published releases only and are sorted by
semantic version descending. `limit` is capped at `100`.

Response:

```json
{
  "data": [
    {
      "id": "...",
      "app_id": "...",
      "version": "1.4.0",
      "channel": "beta",
      "release_notes": "...",
      "status": "published",
      "published_at": "2026-08-10T00:00:00Z",
      "artifacts": [
        {
          "id": "...",
          "object_key": "artifacts/.../client.tar.gz",
          "platform": "macos",
          "architecture": "arm64",
          "file_name": "client.tar.gz",
          "mime_type": "application/gzip",
          "size": 123456,
          "hash": "sha256-...",
          "download_url": "https://cdn.example/artifacts/.../client.tar.gz"
        }
      ]
    }
  ],
  "total": 1,
  "limit": 20,
  "offset": 0
}
```

### Update resolution

```http
GET /api/v1/apps/{app_id}/update
  ?current_version=1.3.0
  &channel=beta
  &platform=macos
  &architecture=arm64
```

All four query parameters are required. The resolver:

1. Validates `current_version` as SemVer without `v`.
2. Filters to published releases in the requested channel.
3. Requires an exact artifact `platform` and `architecture` match.
4. Selects the highest release version strictly greater than the current
   version.

A compatible release is returned as:

```json
{
  "update_available": true,
  "current_version": "1.3.0",
  "release": {
    "version": "1.4.0",
    "channel": "beta",
    "artifacts": [
      {
        "platform": "macos",
        "architecture": "arm64",
        "download_url": "https://cdn.example/artifacts/.../client.tar.gz",
        "hash": "sha256-...",
        "size": 123456
      }
    ]
  }
}
```

When no compatible update exists, the normal response is `200`:

```json
{
  "update_available": false,
  "current_version": "1.4.0",
  "release": null
}
```

The server does not fall back from `beta` to `stable`, or from one platform or
architecture to another. If a product wants fallback behavior, the client
must explicitly issue another request for its chosen fallback channel and
record that policy locally.

## Client installation algorithm

1. Persist the client's selected channel, platform, and architecture. Do not
   assume `stable`, `macos`, `linux`, or any architecture unless the product
   explicitly chooses those defaults.
2. Send the current installed version and the selected dimensions to the
   update endpoint.
3. Treat `200` with `update_available=false` as a successful no-op.
4. If an update is available, select the artifact whose target exactly matches
   the request. Do not select the first artifact in the array.
5. Download `download_url` with an HTTP client supporting redirects and large
   responses. The URL points to S3/CDN; DistributionCenter does not proxy the
   bytes.
6. Validate the downloaded byte count against `size` and verify the content
   hash against `hash`.
7. Stage the artifact, perform any platform-specific signature or package
   verification required by the client, then atomically install it.
8. Persist the new version only after installation succeeds. On failure, keep
   the old version and retry the same channel/target later.
9. To change channels, make the change explicit in client settings and issue a
   new update request. A beta client should not silently consume stable or
   nightly releases.

## Error handling

Marketplace errors use the following shape:

```json
{"error":"description"}
```

Common statuses:

- `400`: invalid SemVer, missing query parameter, missing channel, invalid
  channel, invalid pagination, or incomplete target dimensions.
- `401`: missing or invalid bearer secret.
- `403`: suspended or unauthorized developer app state.
- `404`: unknown release/app or non-production public app.
- `409`: duplicate release, invalid lifecycle transition, duplicate target, or
  missing/incomplete S3 artifact.
- `503`: Develop, S3, or event-bus dependency failure.

Clients should not retry `400`, `401`, `403`, or `404` without changing the
request or credentials. Publish/yank requests may be retried because terminal
state transitions are idempotent.
