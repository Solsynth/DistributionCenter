package database

import (
	"fmt"
	"strings"

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
	return d.DB.AutoMigrate(&Channel{}, &Release{}, &ReleaseArtifact{}, &ClientCheck{})
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
