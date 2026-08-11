# Migrating an application to DistributionCenter

DistributionCenter is the release and artifact control plane for Solsynth
applications. This guide describes the migration from a legacy application
release flow—custom-app identity, CI secrets, and fixed R2 download paths—to a
publisher-owned product and versioned release flow.

The product-facing name used by Island is **Solsynth Express**. The service,
API routes, configuration, and service-discovery identity remain
**DistributionCenter**.

This guide is intentionally separate from the API reference in
[`MARKETPLACE_API.md`](MARKETPLACE_API.md). Use the API reference for complete
request and response schemas; use this document for the migration sequence.

## What changes

| Area | Legacy flow | DistributionCenter flow |
| --- | --- | --- |
| Application identity | Custom-app ID or project identity | DistributionCenter product UUID |
| Release identity | Git tag or object path | Product-scoped SemVer release |
| CI authentication | Shared storage/custom-app secret | Product-scoped upload API key |
| Artifact storage | Fixed R2 path or hand-built URL | Presigned upload plus immutable artifact metadata |
| Release publication | Implicit or external | Explicit publish operation |
| Client update check | GitHub Releases or fixed URL | `/api/products/{product_id}/update` |
| Download URL | Hard-coded storage URL | Direct public URL or signed GET URL from the release response |

Do not reuse a legacy custom-app ID as the DistributionCenter product UUID, and
do not compile an upload or publish credential into the application.

## Migration phases

1. Deploy and expose DistributionCenter.
2. Create the application's publisher product.
3. Create a product-scoped CI upload key.
4. Update the build workflow to upload versioned artifacts.
5. Publish a canary release and verify every artifact target.
6. Update the client to query DistributionCenter for releases and downloads.
7. Retire the legacy upload and download path after the supported client
   population has migrated.

## 1. Deploy and expose DistributionCenter

Configure the service dependencies first:

```toml
serviceName = "distribution"
version = "1.0.0"

[http]
port = "8080"

[database]
dsn = "postgres://..."

[auth]
target = "stargate:9090"

[sphere]
target = "sphere:9090"

[s3]
endpoint = "https://s3.example.com"
accessKey = "..."
secretKey = "..."
bucket = "distribution"
publicURL = "https://cdn.example.com/distribution" # optional
```

`publicURL` is optional. If it is empty, published S3 artifacts return a
signed GET URL instead of a public CDN URL.

The service registers its publisher API under `/api`. The public URL depends on
the reverse proxy:

```text
https://distribution.example.com/api
https://solian.app/api
https://solian.app/dist/api   # only when the proxy rewrites /dist away
```

`serviceName = "distribution"` is a service-discovery identity. It does not
create a `/distribution` HTTP path. See
[`HTTP_ROUTING.md`](HTTP_ROUTING.md) for ingress and path-prefix details.

Verify the deployment before creating products:

```sh
curl --fail -i https://distribution.example.com/health
curl --fail -i https://distribution.example.com/ready
```

## 2. Create the product

A publisher editor creates one DistributionCenter product for the application:

```http
POST {DISTRIBUTION_API_BASE_URL}/publishers/{publisher_name}/products
Authorization: Bearer <Sphere publisher token>
Content-Type: application/json

{
  "slug": "solian",
  "name": "Solian"
}
```

Record the returned product `id`. Configure that UUID as
`DISTRIBUTION_PRODUCT_ID` in CI and as the Flutter build define for the client.

The product UUID is the canonical application identity for DistributionCenter.
It is not the old custom-app ID.

## 3. Create CI credentials

Create a product-scoped upload key:

```http
POST {DISTRIBUTION_API_BASE_URL}/products/{product_id}/upload-api-keys
Authorization: Bearer <Sphere publisher token>
Content-Type: application/json

{"name":"Solian GitHub Actions"}
```

Store the returned plaintext `key` immediately as the CI secret
`DISTRIBUTION_UPLOAD_KEY`. It is only shown once.

Create or select a separate Sphere bearer token with publisher membership and
the `distribution.releases.publish` permission for the publish step. Store it
as `DISTRIBUTION_PUBLISH_TOKEN`.

The credentials have deliberately different scopes:

- `DISTRIBUTION_UPLOAD_KEY`: request upload URLs and attach artifacts.
- `DISTRIBUTION_PUBLISH_TOKEN`: authenticate as a publisher and publish the
  completed release.

The Sphere token used to create products, releases, channels, or upload keys
also needs the corresponding fine-grained permission from
[`MARKETPLACE_API.md`](MARKETPLACE_API.md#authentication-and-authorization).
An upload key cannot create, edit, publish, yank, or delete releases.

## 4. Migrate the build workflow

Configure the GitHub repository with:

### Repository variables

```text
DISTRIBUTION_API_BASE_URL=https://distribution.example.com/api
DISTRIBUTION_PRODUCT_ID=<product UUID>
```

### Repository secrets

```text
DISTRIBUTION_UPLOAD_KEY=<product-scoped upload key>
DISTRIBUTION_PUBLISH_TOKEN=<Sphere publisher bearer token>
```

Use the standalone action:

<https://github.com/Solsynth/SolsynthExpressUpload>

A minimal upload step is:

```yaml
- name: Upload Linux artifact to Solsynth Express
  id: upload_linux
  uses: Solsynth/SolsynthExpressUpload@v1
  with:
    api-base-url: ${{ vars.DISTRIBUTION_API_BASE_URL }}
    product-id: ${{ vars.DISTRIBUTION_PRODUCT_ID }}
    api-key: ${{ secrets.DISTRIBUTION_UPLOAD_KEY }}
    version: ${{ github.ref_name }}
    file: ./artifacts/solian-linux.zip
    platform: linux
    architecture: amd64
    mime-type: application/zip
```

The action normalizes a tag such as `v1.2.3` to the DistributionCenter version
`1.2.3`. If that version does not exist, the upload request creates a stable
draft automatically. Subsequent artifact uploads with the same version attach
to that draft.

For each artifact, provide the exact runtime target the client will query:

| Target | Platform | Architecture |
| --- | --- | --- |
| Windows x64 | `windows` | `amd64` |
| Linux x64 | `linux` | `amd64` |
| Android arm64 | `android` | `arm64` |
| Android ARMv7 | `android` | `armeabi-v7a` |
| Android x86_64 | `android` | `x86_64` |

The action computes SHA-256, uploads the file to the presigned S3 URL, and
attaches immutable size, MIME type, hash, platform, and architecture metadata.

### Publish after all uploads

Use the `release-id` output from one successful upload step. Publishing accepts
the release ID, not the version string:

```yaml
- name: Publish release in Solsynth Express
  if: success()
  env:
    API_BASE_URL: ${{ vars.DISTRIBUTION_API_BASE_URL }}
    PRODUCT_ID: ${{ vars.DISTRIBUTION_PRODUCT_ID }}
    RELEASE_ID: ${{ steps.upload_linux.outputs.release-id }}
    PUBLISH_TOKEN: ${{ secrets.DISTRIBUTION_PUBLISH_TOKEN }}
  run: |
    test -n "$RELEASE_ID"
    curl --fail-with-body --silent --show-error --request POST \
      --header "Authorization: Bearer $PUBLISH_TOKEN" \
      "$API_BASE_URL/products/$PRODUCT_ID/releases/$RELEASE_ID/publish"
```

Only publish after all required platform artifacts have been attached. A
published release must contain at least one complete artifact.

## 5. Migrate the client update check

The client must receive the public API base URL and product UUID at build time:

```sh
flutter build linux \
  --dart-define=DISTRIBUTION_API_BASE_URL="https://distribution.example.com/api" \
  --dart-define=DISTRIBUTION_PRODUCT_ID="<product UUID>"
```

The client update check should call:

```http
GET {DISTRIBUTION_API_BASE_URL}/products/{product_id}/update
  ?current_version=1.1.0
  &channel=stable
  &os=linux
  &architecture=amd64
```

The response contains the newest compatible published release and its matching
artifact `download_url`. The URL is either:

- A direct CDN URL when `s3.publicURL` is configured.
- A time-limited signed GET URL when `s3.publicURL` is unset.

Do not construct download URLs from object keys in the client. Always use the
`download_url` returned by DistributionCenter.

For settings or onboarding screens that display the newest release, use:

```http
GET {DISTRIBUTION_API_BASE_URL}/products/{product_id}/releases
  ?channel=stable
  &platform=linux
  &architecture=amd64
  &limit=1
```

A client that does not receive the two build defines should skip the
DistributionCenter check rather than making a request with an empty product
identity.

## 6. Canary migration

Keep old R2 objects and legacy release metadata during migration. Existing
client versions may still depend on them.

Create a version newer than the currently installed client:

```sh
git tag v1.2.3
git push origin v1.2.3
```

Then verify:

1. Every build artifact uploads successfully.
2. All artifacts reference the same release ID and version.
3. The release is published.
4. Each platform and architecture has a matching artifact.
5. The returned `download_url` downloads the expected file.
6. A client running the previous version receives the canary update.
7. Installation works on Windows, Linux, and each Android ABI.

Example update check:

```sh
curl --fail-with-body \
  "$DISTRIBUTION_API_BASE_URL/products/$DISTRIBUTION_PRODUCT_ID/update?current_version=1.0.0&channel=stable&os=linux&architecture=amd64"
```

The response should include `"update_available":true`, a newer release
version, and a Linux `amd64` artifact with a non-empty `download_url`.

## 7. Retire the legacy flow

After the canary is verified and the minimum supported client version uses
DistributionCenter:

- Remove old R2 upload steps from CI.
- Revoke old shared storage credentials.
- Keep old objects available for the remaining legacy clients, or provide a
  documented retention period before deletion.
- Remove hard-coded download URL construction from clients.
- Keep the DistributionCenter upload key separate from the publish token.

## Rollback

If the migration fails:

1. Stop publishing new DistributionCenter releases.
2. Keep the previous legacy workflow and download URLs available.
3. Revoke only the compromised DistributionCenter credential if the issue is
   authentication-related.
4. Fix the product, artifact target, or ingress configuration.
5. Publish a new canary version rather than mutating a failed release.

Do not delete a release or object as a first response to a client update issue;
first determine whether the problem is version selection, platform matching,
publication state, or URL routing.
