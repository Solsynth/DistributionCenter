package integration

import (
	"context"
	"fmt"
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
	secure := true
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		secure = parsed.Scheme == "https"
		endpoint = parsed.Host
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.S3.AccessKey, cfg.S3.SecretKey, ""), Secure: secure, Region: cfg.S3.Region})
	if err != nil {
		return nil, fmt.Errorf("create s3 client: %w", err)
	}
	return &S3Store{client: client, bucket: cfg.S3.Bucket, publicURL: strings.TrimRight(cfg.S3.PublicURL, "/")}, nil
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

func (s *S3Store) PresignedUpload(ctx context.Context, objectKey, mimeType string) (*url.URL, error) {
	return s.client.PresignedPutObject(ctx, s.bucket, objectKey, 24*time.Hour)
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

func (s *S3Store) PublicURL(objectKey string) string {
	return s.publicURL + "/" + strings.ReplaceAll(url.PathEscape(objectKey), "%2F", "/")
}

var _ service.ArtifactStore = (*S3Store)(nil)
