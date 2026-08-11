package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/tags"

	"src.solsynth.dev/sosys/distribution/internal/config"
	"src.solsynth.dev/sosys/distribution/internal/service"
)

type S3Store struct {
	client    *minio.Client
	bucket    string
	publicURL string
}

func NewS3Store(cfg *config.Config) (*S3Store, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	endpoint := strings.TrimSpace(cfg.S3.Endpoint)
	accessKey := strings.TrimSpace(cfg.S3.AccessKey)
	secretKey := strings.TrimSpace(cfg.S3.SecretKey)
	bucket := strings.TrimSpace(cfg.S3.Bucket)
	region := strings.TrimSpace(cfg.S3.Region)
	secure := true
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		secure = parsed.Scheme == "https"
		endpoint = parsed.Host
	}
	if region == "" && strings.HasSuffix(strings.ToLower(endpoint), ".r2.cloudflarestorage.com") {
		region = "auto"
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: secure, Region: region})
	if err != nil {
		return nil, fmt.Errorf("create s3 client: %w", err)
	}
	return &S3Store{client: client, bucket: bucket, publicURL: strings.TrimRight(strings.TrimSpace(cfg.S3.PublicURL), "/")}, nil
}

func (s *S3Store) Head(ctx context.Context, objectKey string) (*service.ArtifactMetadata, error) {
	info, err := s.client.StatObject(ctx, s.bucket, objectKey, minio.StatObjectOptions{Checksum: true})
	if err != nil {
		return nil, err
	}
	hash := info.Metadata.Get("X-Amz-Meta-Sha256")
	if hash == "" {
		hash = info.Metadata.Get("Sha256")
	}
	return &service.ArtifactMetadata{ObjectKey: objectKey, FileName: path.Base(objectKey), MimeType: info.ContentType, Size: info.Size, Hash: hash}, nil
}
func (s *S3Store) PresignedUpload(ctx context.Context, objectKey, mimeType, sha256 string) (*url.URL, error) {
	headers := make(http.Header)
	if mimeType = strings.TrimSpace(mimeType); mimeType != "" {
		headers.Set("Content-Type", mimeType)
	}
	if sha256 = strings.TrimSpace(sha256); sha256 != "" {
		headers.Set("X-Amz-Meta-Sha256", sha256)
	}
	return s.client.PresignHeader(ctx, http.MethodPut, s.bucket, objectKey, 24*time.Hour, nil, headers)
}

func (s *S3Store) PresignedDownload(ctx context.Context, objectKey string) (*url.URL, error) {
	return s.client.PresignedGetObject(ctx, s.bucket, objectKey, 24*time.Hour, nil)
}

func (s *S3Store) SetPublic(ctx context.Context, objectKey string) error {
	objectTags, err := tags.NewTags(map[string]string{"public": "true"}, true)
	if err != nil {
		return err
	}
	return s.client.PutObjectTagging(ctx, s.bucket, objectKey, objectTags, minio.PutObjectTaggingOptions{})
}

func (s *S3Store) UnsetPublic(ctx context.Context, objectKey string) error {
	return s.client.RemoveObjectTagging(ctx, s.bucket, objectKey, minio.RemoveObjectTaggingOptions{})
}

func (s *S3Store) Delete(ctx context.Context, objectKey string) error {
	return s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{})
}

func (s *S3Store) PublicURL(objectKey string) string {
	if strings.TrimSpace(s.publicURL) == "" {
		return ""
	}
	return s.publicURL + "/" + strings.ReplaceAll(url.PathEscape(objectKey), "%2F", "/")
}
