package service

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/distribution/internal/database"
)

const (
	localizationProduct     = "product"
	localizationChannel     = "channel"
	localizationRelease     = "release"
	localizationName        = "name"
	localizationDescription = "description"
)

func replaceLocalizations(tx *gorm.DB, resourceType, resourceID string, fields map[string]database.LocalizedText) error {
	if err := tx.Where("resource_type = ? AND resource_id = ?", resourceType, resourceID).Delete(&database.Localization{}).Error; err != nil {
		return fmt.Errorf("clear %s localizations: %w", resourceType, err)
	}
	rows := make([]database.Localization, 0)
	for field, values := range fields {
		for locale, value := range values {
			rows = append(rows, database.Localization{ID: uuid.NewString(), ResourceType: resourceType, ResourceID: resourceID, Field: field, Locale: locale, Value: value})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	if err := tx.Create(&rows).Error; err != nil {
		return fmt.Errorf("save %s localizations: %w", resourceType, err)
	}
	return nil
}

func loadLocalizations(db *gorm.DB, resourceType string, resourceIDs []string) (map[string]map[string]database.LocalizedText, error) {
	result := make(map[string]map[string]database.LocalizedText, len(resourceIDs))
	if len(resourceIDs) == 0 {
		return result, nil
	}
	var rows []database.Localization
	if err := db.Where("resource_type = ? AND resource_id IN ?", resourceType, resourceIDs).Order("locale ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load %s localizations: %w", resourceType, err)
	}
	for _, row := range rows {
		fields := result[row.ResourceID]
		if fields == nil {
			fields = make(map[string]database.LocalizedText)
			result[row.ResourceID] = fields
		}
		values := fields[row.Field]
		if values == nil {
			values = make(database.LocalizedText)
			fields[row.Field] = values
		}
		values[row.Locale] = row.Value
	}
	return result, nil
}

func hydrateProductLocalizations(db *gorm.DB, products []database.Product) error {
	ids := make([]string, 0, len(products))
	for _, product := range products {
		ids = append(ids, product.ID)
	}
	loaded, err := loadLocalizations(db, localizationProduct, ids)
	if err != nil {
		return err
	}
	for i := range products {
		fields := loaded[products[i].ID]
		products[i].Names = fields[localizationName]
		products[i].Descriptions = fields[localizationDescription]
	}
	return nil
}

func hydrateChannelLocalizations(db *gorm.DB, channels []*database.Channel) error {
	ids := make([]string, 0, len(channels))
	for _, channel := range channels {
		ids = append(ids, channel.ID)
	}
	loaded, err := loadLocalizations(db, localizationChannel, ids)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		fields := loaded[channel.ID]
		channel.Descriptions = fields[localizationDescription]
	}
	return nil
}

func hydrateReleaseLocalizations(db *gorm.DB, releases []*database.Release) error {
	ids := make([]string, 0, len(releases))
	for _, release := range releases {
		ids = append(ids, release.ID)
	}
	loaded, err := loadLocalizations(db, localizationRelease, ids)
	if err != nil {
		return err
	}
	for _, release := range releases {
		fields := loaded[release.ID]
		release.Descriptions = fields[localizationDescription]
	}
	return nil
}
