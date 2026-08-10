package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// LocalizedText stores a translation keyed by a BCP-47 locale tag.
// It is persisted as JSON so adding a translation does not require a schema
// migration.
type LocalizedText map[string]string

// StringList stores references to linked media or attachments as JSON.
type StringList []string

func (LocalizedText) GormDataType() string {
	return "json"
}

func (value LocalizedText) Value() (driver.Value, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode localized text: %w", err)
	}
	return encoded, nil
}

func (value *LocalizedText) Scan(src any) error {
	if src == nil {
		*value = nil
		return nil
	}
	var encoded []byte
	switch data := src.(type) {
	case []byte:
		encoded = data
	case string:
		encoded = []byte(data)
	default:
		return fmt.Errorf("decode localized text from %T", src)
	}
	if len(encoded) == 0 {
		*value = nil
		return nil
	}
	var decoded map[string]string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return fmt.Errorf("decode localized text: %w", err)
	}
	*value = decoded
	return nil
}
func (StringList) GormDataType() string {
	return "json"
}

func (value StringList) Value() (driver.Value, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode string list: %w", err)
	}
	return encoded, nil
}

func (value *StringList) Scan(src any) error {
	if src == nil {
		*value = nil
		return nil
	}
	var encoded []byte
	switch data := src.(type) {
	case []byte:
		encoded = data
	case string:
		encoded = []byte(data)
	default:
		return fmt.Errorf("decode string list from %T", src)
	}
	if len(encoded) == 0 {
		*value = nil
		return nil
	}
	var decoded []string
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return fmt.Errorf("decode string list: %w", err)
	}
	*value = decoded
	return nil
}

type Product struct {
	ID              string     `gorm:"primaryKey;size:36" json:"id"`
	PublisherID     string     `gorm:"size:36;index;uniqueIndex:idx_product_publisher_slug,priority:1" json:"publisher_id"`
	Slug            string     `gorm:"size:64;uniqueIndex:idx_product_publisher_slug,priority:2" json:"slug"`
	Name            string     `gorm:"size:128" json:"name"`
	Description     string     `json:"description"`
	Icon            string     `json:"icon,omitempty"`
	BackgroundImage string     `json:"background_image,omitempty"`
	Previews        StringList `gorm:"type:json" json:"previews,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type ReleaseChannel string

const (
	ReleaseChannelStable  ReleaseChannel = "stable"
	ReleaseChannelBeta    ReleaseChannel = "beta"
	ReleaseChannelNightly ReleaseChannel = "nightly"
)

type ReleaseStatus string

const (
	ReleaseStatusDraft     ReleaseStatus = "draft"
	ReleaseStatusPublished ReleaseStatus = "published"
	ReleaseStatusYanked    ReleaseStatus = "yanked"
)

type Channel struct {
	ID           string        `gorm:"primaryKey;size:36" json:"id"`
	AppID        string        `gorm:"size:36;index;uniqueIndex:idx_channel_app_name,priority:1" json:"app_id"`
	Name         string        `gorm:"size:64;uniqueIndex:idx_channel_app_name,priority:2" json:"name"`
	DisplayName  string        `gorm:"size:128" json:"display_name"`
	Description  string        `json:"description"`
	Descriptions LocalizedText `gorm:"type:json" json:"descriptions,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

type Release struct {
	ID           string            `gorm:"primaryKey;size:36" json:"id"`
	AppID        string            `gorm:"size:36;index;uniqueIndex:idx_release_app_version,priority:1" json:"app_id"`
	Version      string            `gorm:"size:128;uniqueIndex:idx_release_app_version,priority:2" json:"version"`
	ReleaseNotes string            `json:"release_notes"`
	Descriptions LocalizedText     `gorm:"type:json" json:"descriptions,omitempty"`
	Attachments  StringList        `gorm:"type:json" json:"attachments,omitempty"`
	Status       ReleaseStatus     `gorm:"size:16;index;index:idx_release_app_status,priority:3" json:"status"`
	PublishedAt  *time.Time        `json:"published_at"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Channels     []Channel         `gorm:"many2many:release_channels;" json:"channels"`
	Artifacts    []ReleaseArtifact `gorm:"foreignKey:ReleaseID;constraint:OnDelete:CASCADE" json:"artifacts"`

	// Channel is a compatibility view of the first channel. New callers must
	// use Channels because one release may belong to many channels.
	Channel ReleaseChannel `gorm:"-" json:"channel,omitempty"`
}

type ReleaseArtifact struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	ReleaseID    string    `gorm:"size:36;index" json:"release_id"`
	ObjectKey    string    `gorm:"size:512;index" json:"object_key"`
	Platform     string    `gorm:"size:32" json:"platform"`
	Architecture string    `gorm:"size:32" json:"architecture"`
	FileName     string    `gorm:"size:255" json:"file_name"`
	MimeType     string    `gorm:"size:255" json:"mime_type"`
	Size         int64     `json:"size"`
	Hash         string    `gorm:"size:255" json:"hash"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ClientCheck struct {
	ID            string    `gorm:"primaryKey;size:36" json:"id"`
	AppID         string    `gorm:"size:36;index" json:"app_id"`
	VisitorHash   string    `gorm:"size:64;index" json:"-"`
	Channel       string    `gorm:"size:64;index" json:"channel"`
	Platform      string    `gorm:"size:32;index" json:"platform"`
	Architecture  string    `gorm:"size:32;index" json:"architecture"`
	Locale        string    `gorm:"size:32;index" json:"locale"`
	OSVersion     string    `gorm:"size:128" json:"os_version"`
	ClientVersion string    `gorm:"size:128" json:"client_version"`
	CheckedAt     time.Time `gorm:"index" json:"checked_at"`
}
