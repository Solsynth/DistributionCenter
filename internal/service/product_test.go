package service

import (
	"context"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"net/url"
	"testing"
	"time"

	"src.solsynth.dev/sosys/distribution/internal/database"
	gen "src.solsynth.dev/sosys/go/proto"
)

type productPublisherDirectory struct {
	accountID  string
	publisher  *gen.DyPublisher
	memberCall int
}

func (f *productPublisherDirectory) Authenticate(context.Context, string) (string, error) {
	return f.accountID, nil
}
func (f *productPublisherDirectory) GetPublisher(context.Context, string) (*gen.DyPublisher, error) {
	return f.publisher, nil
}
func (f *productPublisherDirectory) IsPublisherMember(context.Context, string, string, gen.DyPublisherMemberRole) (bool, error) {
	f.memberCall++
	return true, nil
}

type productArtifactStore struct {
	deleted []string
}

func (f *productArtifactStore) Head(context.Context, string) (*ArtifactMetadata, error) {
	return nil, nil
}
func (f *productArtifactStore) PresignedUpload(context.Context, string, string, string) (*url.URL, error) {
	return url.Parse("https://example.test/upload")
}
func (f *productArtifactStore) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}

func TestPublisherOwnedProductAuthorization(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:product-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Product{}, &database.Channel{}, &database.Release{}, &database.ReleaseArtifact{}, &database.ClientCheck{}, &database.Localization{}); err != nil {
		t.Fatal(err)
	}
	publisherID := uuid.NewString()
	directory := &productPublisherDirectory{accountID: uuid.NewString(), publisher: &gen.DyPublisher{Id: publisherID, Name: "Example"}}
	svc := NewPublisherReleaseService(db, directory, nil, nil)
	ctx := WithAccountID(context.Background(), directory.accountID)
	product, err := svc.CreateProduct(ctx, publisherID, CreateProductInput{Slug: "desktop-client", Name: "Desktop Client"})
	if err != nil {
		t.Fatal(err)
	}
	if product.PublisherID != publisherID || product.Slug != "desktop-client" {
		t.Fatalf("product = %#v", product)
	}
	if directory.memberCall != 1 {
		t.Fatalf("membership calls = %d, want 1", directory.memberCall)
	}
	if _, err := svc.CreateProduct(context.Background(), publisherID, CreateProductInput{Slug: "second"}); err != ErrUnauthorized {
		t.Fatalf("unauthenticated create error = %v, want unauthorized", err)
	}
}

func TestDeleteProductRemovesArtifactObjects(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Product{}, &database.Channel{}, &database.Release{}, &database.ReleaseArtifact{}, &database.ClientCheck{}, &database.Localization{}); err != nil {
		t.Fatal(err)
	}
	publisherID := uuid.NewString()
	directory := &productPublisherDirectory{
		accountID: uuid.NewString(),
		publisher: &gen.DyPublisher{Id: publisherID, Name: "Example"},
	}
	store := &productArtifactStore{}
	svc := NewPublisherReleaseService(db, directory, store, nil)
	ctx := WithAccountID(context.Background(), directory.accountID)
	product, err := svc.CreateProduct(ctx, publisherID, CreateProductInput{Slug: "desktop-client", Name: "Desktop Client"})
	if err != nil {
		t.Fatal(err)
	}
	releaseID := uuid.NewString()
	objectKey := "products/" + product.ID + "/desktop-client.zip"
	if err := db.Create(&database.Release{
		ID:      releaseID,
		AppID:   product.ID,
		Version: "1.0.0",
		Status:  database.ReleaseStatusDraft,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ReleaseArtifact{
		ID:        uuid.NewString(),
		ReleaseID: releaseID,
		ObjectKey: objectKey,
		FileName:  "desktop-client.zip",
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteProduct(ctx, product.ID); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != objectKey {
		t.Fatalf("deleted objects = %#v, want [%q]", store.deleted, objectKey)
	}
	var remaining int64
	if err := db.Model(&database.ReleaseArtifact{}).Where("release_id = ?", releaseID).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("release artifacts remaining = %d, want 0", remaining)
	}
}

func TestListMarketplaceProductsSortsByLatestRelease(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Product{}, &database.Channel{}, &database.Release{}, &database.ReleaseArtifact{}, &database.ClientCheck{}, &database.Localization{}); err != nil {
		t.Fatal(err)
	}
	publisherID := uuid.NewString()
	directory := &productPublisherDirectory{publisher: &gen.DyPublisher{Id: publisherID, Name: "Example"}}
	svc := NewPublisherReleaseService(db, directory, nil, nil)
	now := time.Now().UTC()
	products := []database.Product{
		{ID: uuid.NewString(), PublisherID: publisherID, Slug: "older", Name: "Older", CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-3 * time.Hour)},
		{ID: uuid.NewString(), PublisherID: publisherID, Slug: "newer", Name: "Newer", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: uuid.NewString(), PublisherID: publisherID, Slug: "unreleased", Name: "Unreleased", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
	}
	if err := db.Create(&products).Error; err != nil {
		t.Fatal(err)
	}
	for index, product := range products[:2] {
		channel := database.Channel{ID: uuid.NewString(), AppID: product.ID, Name: "stable", DisplayName: "Stable"}
		release := database.Release{ID: uuid.NewString(), AppID: product.ID, Version: "1.0.0", Status: database.ReleaseStatusPublished, UpdatedAt: now.Add(time.Duration(index) * time.Hour), Channels: []database.Channel{channel}}
		if err := db.Create(&release).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := svc.ListMarketplaceProducts(context.Background(), MarketplaceListQuery{Descending: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 3 || len(result.Data) != 3 {
		t.Fatalf("marketplace result = total %d, data %d", result.Total, len(result.Data))
	}
	if result.Data[0].Product.ID != products[1].ID || result.Data[1].Product.ID != products[0].ID || result.Data[2].Product.ID != products[2].ID {
		t.Fatalf("default ordering = %q, %q, %q", result.Data[0].Product.Slug, result.Data[1].Product.Slug, result.Data[2].Product.Slug)
	}
	nameResult, err := svc.ListMarketplaceProducts(context.Background(), MarketplaceListQuery{SortBy: "name", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if nameResult.Data[0].Product.Name != "Newer" || nameResult.Data[2].Product.Name != "Unreleased" {
		t.Fatalf("name ordering = %q, %q, %q", nameResult.Data[0].Product.Name, nameResult.Data[1].Product.Name, nameResult.Data[2].Product.Name)
	}
}
