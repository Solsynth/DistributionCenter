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
	Slug         string                          `json:"slug"`
	Name         string                          `json:"name"`
	Names        map[string]string               `json:"names"`
	Description  string                          `json:"description"`
	Descriptions map[string]string               `json:"descriptions"`
	Icon         *database.CloudFileReference    `json:"icon"`
	Background   *database.CloudFileReference    `json:"background"`
	Previews     database.CloudFileReferenceList `json:"previews"`
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
	names, err := normalizeDescriptions(input.Names)
	if err != nil {
		return nil, err
	}
	descriptions, err := normalizeDescriptions(input.Descriptions)
	if err != nil {
		return nil, err
	}
	previews, err := normalizeCloudFiles(input.Previews)
	if err != nil {
		return nil, err
	}
	product := &database.Product{ID: uuid.NewString(), PublisherID: publisherID, Slug: slug, Name: strings.TrimSpace(input.Name), Names: names, Description: input.Description, Descriptions: descriptions, Icon: input.Icon, Background: input.Background, Previews: previews}
	if product.Name == "" {
		product.Name = slug
	}
	if err := s.db.Create(product).Error; err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("%w: product slug already exists", ErrConflict)
		}
		return nil, fmt.Errorf("create product: %w", err)
	}
	if err := replaceLocalizations(s.db, localizationProduct, product.ID, map[string]database.LocalizedText{localizationName: names, localizationDescription: descriptions}); err != nil {
		return nil, err
	}
	return product, nil
}
func (s *ReleaseService) UpdateProduct(ctx context.Context, productID string, input CreateProductInput) (*database.Product, error) {
	if s == nil || s.db == nil || s.publishers == nil {
		return nil, fmt.Errorf("%w: publisher service unavailable", ErrDependency)
	}
	product, err := s.loadProduct(productID)
	if err != nil {
		return nil, err
	}
	if err := s.requirePublisher(ctx, product.PublisherID); err != nil {
		return nil, err
	}
	slug, err := validateProductSlug(input.Slug)
	if err != nil {
		return nil, err
	}
	names, err := normalizeDescriptions(input.Names)
	if err != nil {
		return nil, err
	}
	descriptions, err := normalizeDescriptions(input.Descriptions)
	if err != nil {
		return nil, err
	}
	previews, err := normalizeCloudFiles(input.Previews)
	if err != nil {
		return nil, err
	}
	product.Slug = slug
	product.Name = strings.TrimSpace(input.Name)
	if product.Name == "" {
		product.Name = slug
	}
	product.Names = names
	product.Description = input.Description
	product.Descriptions = descriptions
	product.Icon = input.Icon
	product.Background = input.Background
	product.Previews = previews
	if err := s.db.Save(product).Error; err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("%w: product slug already exists", ErrConflict)
		}
		return nil, fmt.Errorf("update product: %w", err)
	}
	if err := replaceLocalizations(s.db, localizationProduct, product.ID, map[string]database.LocalizedText{localizationName: names, localizationDescription: descriptions}); err != nil {
		return nil, err
	}
	return product, nil

}
func (s *ReleaseService) DeleteProduct(ctx context.Context, productID string) error {
	if s == nil || s.db == nil || s.publishers == nil {
		return fmt.Errorf("%w: publisher service unavailable", ErrDependency)
	}
	product, err := s.loadProduct(productID)
	if err != nil {
		return err
	}
	if err := s.requirePublisher(ctx, product.PublisherID); err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var releases []database.Release
		if err := tx.Select("id").Where("app_id = ?", product.ID).Find(&releases).Error; err != nil {
			return fmt.Errorf("list product releases: %w", err)
		}
		if len(releases) > 0 {
			ids := make([]string, 0, len(releases))
			for _, release := range releases {
				ids = append(ids, release.ID)
			}
			if err := tx.Where("release_id IN ?", ids).Delete(&database.ReleaseArtifact{}).Error; err != nil {
				return fmt.Errorf("delete release artifacts: %w", err)
			}
			if err := tx.Exec("DELETE FROM release_channels WHERE release_id IN ?", ids).Error; err != nil {
				return fmt.Errorf("delete release channels: %w", err)
			}
			if err := tx.Where("app_id = ?", product.ID).Delete(&database.Release{}).Error; err != nil {
				return fmt.Errorf("delete releases: %w", err)
			}
		}
		var channels []database.Channel
		if err := tx.Select("id").Where("app_id = ?", product.ID).Find(&channels).Error; err != nil {
			return fmt.Errorf("list product channels: %w", err)
		}
		channelIDs := make([]string, 0, len(channels))
		for _, channel := range channels {
			channelIDs = append(channelIDs, channel.ID)
		}
		if len(channelIDs) > 0 {
			if err := tx.Where("resource_type = ? AND resource_id IN ?", localizationChannel, channelIDs).Delete(&database.Localization{}).Error; err != nil {
				return fmt.Errorf("delete channel localizations: %w", err)
			}
		}
		if len(releases) > 0 {
			releaseIDs := make([]string, 0, len(releases))
			for _, release := range releases {
				releaseIDs = append(releaseIDs, release.ID)
			}
			if err := tx.Where("resource_type = ? AND resource_id IN ?", localizationRelease, releaseIDs).Delete(&database.Localization{}).Error; err != nil {
				return fmt.Errorf("delete release localizations: %w", err)
			}
		}
		if err := tx.Where("resource_type = ? AND resource_id = ?", localizationProduct, product.ID).Delete(&database.Localization{}).Error; err != nil {
			return fmt.Errorf("delete product localizations: %w", err)
		}
		if err := tx.Where("app_id = ?", product.ID).Delete(&database.Channel{}).Error; err != nil {
			return fmt.Errorf("delete channels: %w", err)
		}
		if err := tx.Delete(&database.Product{}, "id = ?", product.ID).Error; err != nil {
			return fmt.Errorf("delete product: %w", err)
		}
		return nil
	})
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
	if err := hydrateProductLocalizations(s.db, products); err != nil {
		return nil, err
	}
	return products, nil
}

func (s *ReleaseService) loadProduct(productID string) (*database.Product, error) {
	productID = strings.TrimSpace(productID)
	if _, err := uuid.Parse(productID); err != nil {
		return nil, fmt.Errorf("%w: product_id must be a UUID", ErrValidation)
	}
	var product database.Product
	if err := s.db.Where("id = ?", productID).First(&product).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: product", ErrNotFound)
		}
		return nil, fmt.Errorf("load product: %w", err)
	}
	return &product, nil
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
	products := []database.Product{product}
	if err := hydrateProductLocalizations(s.db, products); err != nil {
		return nil, nil, nil, err
	}
	product = products[0]
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
