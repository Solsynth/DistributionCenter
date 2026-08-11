package integration

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"src.solsynth.dev/sosys/distribution/internal/config"
)

func TestS3StorePresignedUploadSignsUploadHeaders(t *testing.T) {
	cfg := config.Default()
	cfg.S3.Endpoint = "https://account.r2.cloudflarestorage.com"
	cfg.S3.AccessKey = "access-key"
	cfg.S3.SecretKey = "secret-key"
	cfg.S3.Bucket = "artifacts"
	cfg.S3.Region = ""

	store, err := NewS3Store(cfg)
	if err != nil {
		t.Fatal(err)
	}
	presigned, err := store.PresignedUpload(context.Background(), "artifacts/product/file.tar.gz", "application/gzip", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}

	query := presigned.Query()
	signedHeaders := query.Get("X-Amz-SignedHeaders")
	if !strings.Contains(signedHeaders, "content-type") {
		t.Fatalf("signed headers %q do not include content-type", signedHeaders)
	}
	if !strings.Contains(signedHeaders, "x-amz-meta-sha256") {
		t.Fatalf("signed headers %q do not include x-amz-meta-sha256", signedHeaders)
	}
	credential := query.Get("X-Amz-Credential")
	if credential == "" {
		t.Fatalf("presigned URL %s has no credential", redactURL(presigned))
	}
	if !strings.Contains(credential, "/auto/") {
		t.Fatalf("credential %q does not use the R2 region", credential)
	}
}
func TestS3StoreTrimsCredentialAndEndpointWhitespace(t *testing.T) {
	cfg := config.Default()
	cfg.S3.Endpoint = " 0a70a6d1b7128888c823359d0008f4e1.r2.cloudflarestorage.com "
	cfg.S3.AccessKey = " access-key "
	cfg.S3.SecretKey = " secret-key\n"
	cfg.S3.Bucket = " solar-network-express "
	cfg.S3.Region = " auto "

	store, err := NewS3Store(cfg)
	if err != nil {
		t.Fatal(err)
	}
	presigned, err := store.PresignedUpload(context.Background(), "product/file.bin", "application/octet-stream", "")
	if err != nil {
		t.Fatal(err)
	}

	if presigned.Scheme != "https" {
		t.Fatalf("presigned scheme = %q, want https", presigned.Scheme)
	}
	if presigned.Host != "0a70a6d1b7128888c823359d0008f4e1.r2.cloudflarestorage.com" {
		t.Fatalf("presigned host = %q", presigned.Host)
	}
	credential := presigned.Query().Get("X-Amz-Credential")
	if !strings.HasPrefix(credential, "access-key/") {
		t.Fatalf("credential %q was not trimmed", credential)
	}
	if !strings.Contains(presigned.Path, "/solar-network-express/product/file.bin") {
		t.Fatalf("presigned path %q does not contain the trimmed bucket", presigned.Path)
	}
}

func redactURL(value *url.URL) string {
	copy := *value
	query := copy.Query()
	query.Set("X-Amz-Signature", "redacted")
	copy.RawQuery = query.Encode()
	return copy.String()
}
