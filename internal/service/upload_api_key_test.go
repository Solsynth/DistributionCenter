package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/distribution/internal/database"
	gen "src.solsynth.dev/sosys/go/proto"
)

func TestUploadAPIKeyLifecycleAndExternalArtifact(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:upload-api-key-test-"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Product{}, &database.Channel{}, &database.Release{}, &database.ReleaseArtifact{}, &database.ClientCheck{}, &database.Localization{}, &database.UploadAPIKey{}); err != nil {
		t.Fatal(err)
	}
	publisherID, productID := uuid.NewString(), uuid.NewString()
	directory := &productPublisherDirectory{accountID: uuid.NewString(), publisher: &gen.DyPublisher{Id: publisherID, Name: "Example"}}
	if err := db.Create(&database.Product{ID: productID, PublisherID: publisherID, Slug: "desktop", Name: "Desktop"}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewPublisherReleaseService(db, directory, nil, nil)
	ctx := WithAccountID(context.Background(), directory.accountID)

	created, err := svc.CreateUploadAPIKey(ctx, productID, CreateUploadAPIKeyInput{Name: "GitHub Actions"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Key == "" || created.KeyPrefix == "" || created.Name != "GitHub Actions" {
		t.Fatalf("created key = %#v", created)
	}
	keys, err := svc.ListUploadAPIKeys(ctx, productID)
	if err != nil || len(keys) != 1 || keys[0].Name != "GitHub Actions" {
		t.Fatalf("listed keys = %#v, error = %v", keys, err)
	}
	if valid, err := svc.CheckUploadAPIKey(ctx, productID, created.Key); err != nil || !valid {
		t.Fatalf("valid key check = %v, error = %v", valid, err)
	}
	if valid, err := svc.CheckUploadAPIKey(ctx, uuid.NewString(), created.Key); err != nil || valid {
		t.Fatalf("cross-product key check = %v, error = %v", valid, err)
	}
	if err := svc.RevokeUploadAPIKey(ctx, productID, created.ID); err != nil {
		t.Fatal(err)
	}
	if valid, err := svc.CheckUploadAPIKey(ctx, productID, created.Key); err != nil || valid {
		t.Fatalf("revoked key check = %v, error = %v", valid, err)
	}

	release, err := svc.CreateRelease(ctx, productID, CreateReleaseInput{Version: "1.0.0", Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	release, err = svc.AddArtifact(ctx, productID, release.ID, ArtifactInput{
		DownloadURL:  "https://downloads.example.test/desktop.tar.gz",
		FileName:     "desktop.tar.gz",
		MimeType:     "application/gzip",
		Size:         42,
		Hash:         "sha256-value",
		Platform:     "macos",
		Architecture: "arm64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(ctx, productID, release.ID); err != nil {
		t.Fatal(err)
	}
	published, err := svc.GetRelease(productID, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifact := published.Artifacts[0]
	if artifact.DownloadURL == "" || artifact.ObjectKey != "" || artifact.Hash != "sha256-value" {
		t.Fatalf("external artifact = %#v", artifact)
	}
	if _, err := svc.AddArtifact(ctx, productID, release.ID, ArtifactInput{DownloadURL: "not-a-url", Platform: "macos", Architecture: "x86_64"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid external URL error = %v, want validation", err)
	}
}
