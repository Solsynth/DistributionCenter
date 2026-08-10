package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/distribution/internal/database"
	gen "src.solsynth.dev/sosys/go/proto"
)

type CreateProductInput struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *ReleaseService) CreateProduct(ctx context.Context, publisherID string, input CreateProductInput) (*database.Product, error) {
	if s == nil || s.db == nil || s.publishers == nil {
		return nil, fmt.Errorf("%w: publisher service unavailable", ErrDependency)
	}
	if err := validatePublisherID(publisherID); err != nil {
		return nil, err
	}
	if err := s.requirePublisher(ctx, publisherID); err != nil {
		return nil, err
	}
	slug, err := validateProductSlug(input.Slug)
	if err != nil {
		return nil, err
	}
	product := &database.Product{ID: uuid.NewString(), PublisherID: publisherID, Slug: slug, Name: strings.TrimSpace(input.Name), Description: input.Description}
	if product.Name == "" {
		product.Name = slug
	}
	if err := s.db.Create(product).Error; err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("%w: product slug already exists", ErrConflict)
		}
		return nil, fmt.Errorf("create product: %w", err)
	}
	return product, nil
}

func (s *ReleaseService) ListProducts(ctx context.Context, publisherID string) ([]database.Product, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("%w: database unavailable", ErrDependency)
	}
	if err := validatePublisherID(publisherID); err != nil {
		return nil, err
	}
	var products []database.Product
	if err := s.db.Where("publisher_id = ?", publisherID).Order("slug ASC").Find(&products).Error; err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return products, nil
}

func (s *ReleaseService) ProductPublisherID(productID string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("%w: database unavailable", ErrDependency)
	}
	var product database.Product
	if err := s.db.Select("publisher_id").Where("id = ?", productID).First(&product).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("%w: product", ErrNotFound)
		}
		return "", fmt.Errorf("load product: %w", err)
	}
	return product.PublisherID, nil
}

func (s *ReleaseService) GetPublicProduct(ctx context.Context, productID string) (*database.Product, *gen.DyPublisher, *database.Release, error) {
	if s == nil || s.db == nil || s.publishers == nil {
		return nil, nil, nil, fmt.Errorf("%w: publisher service unavailable", ErrDependency)
	}
	var product database.Product
	if err := s.db.Where("id = ?", productID).First(&product).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, fmt.Errorf("%w: product", ErrNotFound)
		}
		return nil, nil, nil, fmt.Errorf("load product: %w", err)
	}
	publisher, err := s.publishers.GetPublisher(ctx, product.PublisherID)
	if err != nil {
		return nil, nil, nil, dependencyError(err)
	}
	if publisher == nil {
		return nil, nil, nil, fmt.Errorf("%w: publisher", ErrNotFound)
	}
	latest, err := s.latest(product.ID, string(database.ReleaseChannelStable))
	if err != nil {
		return nil, nil, nil, err
	}
	return &product, publisher, latest, nil
}

func (s *ReleaseService) requirePublisher(ctx context.Context, publisherID string) error {
	publisher, err := s.publishers.GetPublisher(ctx, publisherID)
	if err != nil {
		return dependencyError(err)
	}
	if publisher == nil {
		return fmt.Errorf("%w: publisher", ErrNotFound)
	}
	accountID := AccountID(ctx)
	if accountID == "" {
		return ErrUnauthorized
	}
	valid, err := s.publishers.IsPublisherMember(ctx, publisherID, accountID, gen.DyPublisherMemberRole_DY_EDITOR)
	if err != nil {
		return dependencyError(err)
	}
	if !valid {
		return ErrForbidden
	}
	return nil
}

func validatePublisherID(value string) error {
	if _, err := uuid.Parse(strings.TrimSpace(value)); err != nil {
		return fmt.Errorf("%w: publisher_id must be a UUID", ErrValidation)
	}
	return nil
}

func validateProductSlug(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 64 {
		return "", fmt.Errorf("%w: product slug must be 1-64 characters", ErrValidation)
	}
	for i, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') || ((i == 0 || i == len(value)-1) && (r == '-' || r == '_')) {
			return "", fmt.Errorf("%w: product slug contains invalid characters", ErrValidation)
		}
	}
	return value, nil
}

type actorContextKey struct{}

func WithAccountID(ctx context.Context, accountID string) context.Context {
	return context.WithValue(ctx, actorContextKey{}, strings.TrimSpace(accountID))
}

func AccountID(ctx context.Context) string {
	value, _ := ctx.Value(actorContextKey{}).(string)
	return value
}
