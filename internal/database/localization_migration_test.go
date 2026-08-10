package database

import (
	"testing"
)

func TestAutoMigrateMovesLegacyLocalizedJSON(t *testing.T) {
	db, err := OpenDSN("file:localization-migration-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.DB.AutoMigrate(&Product{}, &Channel{}, &Release{}, &ReleaseArtifact{}, &ClientCheck{}, &Localization{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE products ADD COLUMN names JSON`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE products ADD COLUMN descriptions JSON`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO products (id, publisher_id, slug, name, description, names, descriptions) VALUES (?, ?, ?, ?, ?, ?, ?)`, "product-1", "publisher-1", "desktop", "Desktop", "A desktop app", `{"en-US":"Desktop","zh-CN":"桌面客户端"}`, `{"en-US":"A desktop app","zh-CN":"桌面应用"}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	var rows []Localization
	if err := db.Where("resource_type = ? AND resource_id = ?", "product", "product-1").Order("field ASC, locale ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0].Value == "" || rows[3].Value == "" {
		t.Fatalf("migrated localizations = %#v", rows)
	}
	var legacyValue string
	if err := db.Raw("SELECT names FROM products LIMIT 1").Scan(&legacyValue).Error; err == nil {
		t.Fatal("legacy product names column remains")
	}
	if err := db.Raw("SELECT descriptions FROM products LIMIT 1").Scan(&legacyValue).Error; err == nil {
		t.Fatal("legacy product descriptions column remains")
	}
}
