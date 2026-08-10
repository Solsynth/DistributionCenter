package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"src.solsynth.dev/sosys/distribution/internal/config"
)

type DB struct {
	*gorm.DB
}

func Open(cfg *config.Config) (*DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if strings.TrimSpace(cfg.Database.DSN) == "" {
		return nil, fmt.Errorf("database dsn is required")
	}
	return OpenDSN(cfg.Database.DSN)
}

func OpenDSN(dsn string) (*DB, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("database dsn is required")
	}
	gormConfig := &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)}
	var (
		db  *gorm.DB
		err error
	)
	if strings.HasPrefix(dsn, "sqlite://") {
		db, err = gorm.Open(sqlite.Open(strings.TrimPrefix(dsn, "sqlite://")), gormConfig)
	} else if dsn == ":memory:" || strings.HasPrefix(dsn, "file:") {
		db, err = gorm.Open(sqlite.Open(dsn), gormConfig)
	} else {
		db, err = gorm.Open(postgres.Open(dsn), gormConfig)
	}
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &DB{DB: db}, nil
}

func (d *DB) AutoMigrate() error {
	if d == nil || d.DB == nil {
		return fmt.Errorf("database is not open")
	}
	// Older builds used a single release channel and created this unique
	// index. Multi-channel releases are keyed by app and version instead.
	if d.DB.Migrator().HasIndex(&Release{}, "idx_release_app_version_channel") {
		_ = d.DB.Migrator().DropIndex(&Release{}, "idx_release_app_version_channel")
	}
	if err := d.DB.AutoMigrate(&Product{}, &UploadAPIKey{}, &Channel{}, &Release{}, &ReleaseArtifact{}, &ClientCheck{}, &Localization{}); err != nil {
		return err
	}
	if err := migrateLegacyLocalizations(d.DB); err != nil {
		return err
	}
	return nil
}
func hasDatabaseColumn(db *gorm.DB, table, column string) (bool, error) {
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return false, err
	}
	for _, candidate := range columns {
		if strings.EqualFold(candidate.Name(), column) {
			return true, nil
		}
	}
	return false, nil
}

func migrateLegacyLocalizations(db *gorm.DB) error {
	legacyColumns := []struct {
		table        string
		resourceType string
		field        string
		column       string
	}{
		{"products", "product", "name", "names"},
		{"products", "product", "description", "descriptions"},
		{"channels", "channel", "description", "descriptions"},
		{"releases", "release", "description", "descriptions"},
	}
	for _, legacy := range legacyColumns {
		hasColumn, err := hasDatabaseColumn(db, legacy.table, legacy.column)
		if err != nil {
			return fmt.Errorf("inspect legacy %s columns: %w", legacy.table, err)
		}
		if !hasColumn {
			continue
		}
		var rows []struct {
			ID    string
			Value []byte
		}
		if err := db.Table(legacy.table).Select("id, " + legacy.column + " AS value").Where(legacy.column + " IS NOT NULL").Scan(&rows).Error; err != nil {
			return fmt.Errorf("read legacy %s localizations: %w", legacy.table, err)
		}
		for _, row := range rows {
			var values map[string]string
			if err := json.Unmarshal(row.Value, &values); err != nil {
				return fmt.Errorf("decode legacy %s localizations: %w", legacy.table, err)
			}
			for locale, value := range values {
				locale = strings.TrimSpace(locale)
				if locale == "" {
					continue
				}
				var existing Localization
				err := db.Where("resource_type = ? AND resource_id = ? AND field = ? AND locale = ?", legacy.resourceType, row.ID, legacy.field, locale).First(&existing).Error
				if err == nil {
					continue
				}
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("check legacy %s localization: %w", legacy.table, err)
				}
				if err := db.Create(&Localization{ID: uuid.NewString(), ResourceType: legacy.resourceType, ResourceID: row.ID, Field: legacy.field, Locale: locale, Value: value}).Error; err != nil {
					return fmt.Errorf("save legacy %s localization: %w", legacy.table, err)
				}
			}
		}
		if err := db.Exec("ALTER TABLE " + legacy.table + " DROP COLUMN " + legacy.column).Error; err != nil {
			return fmt.Errorf("drop legacy %s column: %w", legacy.table, err)
		}
	}
	return nil
}

func (d *DB) Close() error {
	if d == nil || d.DB == nil {
		return nil
	}
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
