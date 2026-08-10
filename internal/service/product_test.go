package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

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
