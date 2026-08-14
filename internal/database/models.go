package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// LocalizedText is the API representation of translations. Persistence uses
// Localization rows so locale and resource fields can be indexed.

type LocalizedText map[string]string

// JSONMap stores arbitrary publisher-defined metadata as a JSON object.
type JSONMap map[string]any

func (JSONMap) GormDataType() string {
	return "json"
}

func (value JSONMap) Value() (driver.Value, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode json map: %w", err)
	}
	return encoded, nil
}

func (value *JSONMap) Scan(src any) error {
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
		return fmt.Errorf("scan json map from %T", src)
	}
	return json.Unmarshal(encoded, value)
}

func (value JSONMap) MarshalJSON() ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(map[string]any(value))
}

func (value *JSONMap) UnmarshalJSON(encoded []byte) error {
	if string(encoded) == "null" {
		*value = nil
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return err
	}
	*value = result
	return nil
}

// CloudFileReference mirrors Stargate's cached cloud-file profile reference.
// The object keeps immutable file metadata locally, avoiding a file-service
// lookup when rendering products and releases.
type CloudFileReference struct {
	Id              string         `json:"id"`
	Name            string         `json:"name,omitempty"`
	FileMeta        map[string]any `json:"file_meta"`
	UserMeta        map[string]any `json:"user_meta"`
	SensitiveMarks  []int          `json:"sensitive_marks,omitempty"`
	MimeType        string         `json:"mime_type,omitempty"`
	Hash            string         `json:"hash,omitempty"`
	Size            int64          `json:"size,omitempty"`
	HasCompression  bool           `json:"has_compression,omitempty"`
	Url             string         `json:"url,omitempty"`
	Width           *int32         `json:"width,omitempty"`
	Height          *int32         `json:"height,omitempty"`
	Blurhash        string         `json:"blurhash,omitempty"`
	Usage           string         `json:"usage,omitempty"`
	ApplicationType string         `json:"application_type,omitempty"`
	CreatedAt       *time.Time     `json:"created_at,omitempty"`
	UpdatedAt       *time.Time     `json:"updated_at,omitempty"`
	DeletedAt       *time.Time     `json:"deleted_at,omitempty"`
}

type CloudFileReferenceList []CloudFileReference

func (CloudFileReference) GormDataType() string {
	return "json"
}

func (value CloudFileReference) MarshalJSON() ([]byte, error) {
	type alias CloudFileReference
	if value.FileMeta == nil {
		value.FileMeta = map[string]any{}
	}
	if value.UserMeta == nil {
		value.UserMeta = map[string]any{}
	}
	return json.Marshal(alias(value))
}
func (value *CloudFileReference) UnmarshalJSON(encoded []byte) error {
	var id string
	if err := json.Unmarshal(encoded, &id); err == nil {
		value.Id = id
		return nil
	}
	type alias CloudFileReference
	return json.Unmarshal(encoded, (*alias)(value))
}

func (value CloudFileReference) Value() (driver.Value, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode cloud file reference: %w", err)
	}
	return encoded, nil
}

func (value *CloudFileReference) Scan(src any) error {
	if src == nil {
		*value = CloudFileReference{}
		return nil
	}
	var encoded []byte
	switch data := src.(type) {
	case []byte:
		encoded = data
	case string:
		encoded = []byte(data)
	default:
		return fmt.Errorf("decode cloud file reference from %T", src)
	}
	if len(encoded) == 0 {
		*value = CloudFileReference{}
		return nil
	}
	type alias CloudFileReference
	if err := json.Unmarshal(encoded, (*alias)(value)); err != nil {
		return fmt.Errorf("decode cloud file reference: %w", err)
	}
	return nil
}

func (CloudFileReferenceList) GormDataType() string {
	return "json"
}

func (value CloudFileReferenceList) Value() (driver.Value, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode cloud file references: %w", err)
	}
	return encoded, nil
}

func (value *CloudFileReferenceList) Scan(src any) error {
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
		return fmt.Errorf("decode cloud file references from %T", src)
	}
	if len(encoded) == 0 {
		*value = nil
		return nil
	}
	var decoded CloudFileReferenceList
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return fmt.Errorf("decode cloud file references: %w", err)
	}
	*value = decoded
	return nil
}

type Product struct {
	ID           string                 `gorm:"primaryKey;size:36" json:"id"`
	PublisherID  string                 `gorm:"size:36;index;uniqueIndex:idx_product_publisher_slug,priority:1" json:"publisher_id"`
	Slug         string                 `gorm:"size:64;uniqueIndex:idx_product_publisher_slug,priority:2" json:"slug"`
	Name         string                 `gorm:"size:128" json:"name"`
	Names        LocalizedText          `gorm:"-" json:"names,omitempty"`
	Description  string                 `json:"description"`
	Descriptions LocalizedText          `gorm:"-" json:"descriptions,omitempty"`
	Icon         *CloudFileReference    `gorm:"type:json" json:"icon,omitempty"`
	Background   *CloudFileReference    `gorm:"type:json" json:"background,omitempty"`
	Previews     CloudFileReferenceList `gorm:"type:json" json:"previews,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}
type UploadAPIKey struct {
	ID         string     `gorm:"primaryKey;size:36" json:"id"`
	ProductID  string     `gorm:"size:36;index" json:"product_id"`
	Name       string     `gorm:"size:128" json:"name"`
	KeyPrefix  string     `gorm:"size:16" json:"key_prefix"`
	SecretHash string     `gorm:"size:64;uniqueIndex" json:"-"`
	CreatedBy  string     `gorm:"size:36;index" json:"created_by"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (UploadAPIKey) TableName() string {
	return "upload_api_keys"
}

type ReleaseChannel string

const (
	ReleaseChannelStable  ReleaseChannel = "stable"
	ReleaseChannelBeta    ReleaseChannel = "beta"
	ReleaseChannelNightly ReleaseChannel = "nightly"
	ReleaseChannelRolling ReleaseChannel = "rolling"
)

type ReleaseStatus string

const (
	ReleaseStatusDraft     ReleaseStatus = "draft"
	ReleaseStatusPublished ReleaseStatus = "published"
	ReleaseStatusYanked    ReleaseStatus = "yanked"
)

type Channel struct {
	ID                string        `gorm:"primaryKey;size:36" json:"id"`
	AppID             string        `gorm:"size:36;index;uniqueIndex:idx_channel_app_name,priority:1" json:"app_id"`
	Name              string        `gorm:"size:64;uniqueIndex:idx_channel_app_name,priority:2" json:"name"`
	DisplayName       string        `gorm:"size:128" json:"display_name"`
	DisplayNames      LocalizedText `gorm:"-" json:"display_names,omitempty"`
	Description       string        `json:"description"`
	Descriptions      LocalizedText `gorm:"-" json:"descriptions,omitempty"`
	ArtifactRetention *int          `json:"artifact_retention,omitempty"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

type Release struct {
	ID             string                 `gorm:"primaryKey;size:36" json:"id"`
	AppID          string                 `gorm:"size:36;index;uniqueIndex:idx_release_app_version,priority:1" json:"app_id"`
	Version        string                 `gorm:"size:128;uniqueIndex:idx_release_app_version,priority:2" json:"version"`
	UploadAPIKeyID string                 `gorm:"size:36;index" json:"-"`
	ReleaseNotes   string                 `json:"release_notes"`
	Title          string                 `json:"title"`
	Metadata       JSONMap                `gorm:"type:json" json:"metadata,omitempty"`
	ForceUpdate    bool                   `json:"force_update"`
	Descriptions   LocalizedText          `gorm:"-" json:"descriptions,omitempty"`
	Titles         LocalizedText          `gorm:"-" json:"titles,omitempty"`
	Attachments    CloudFileReferenceList `gorm:"type:json" json:"attachments,omitempty"`
	Status         ReleaseStatus          `gorm:"size:16;index;index:idx_release_app_status,priority:3" json:"status"`
	PublishedAt    *time.Time             `json:"published_at"`
	DownloadCount  int64                  `gorm:"not null;default:0" json:"-"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	Channels       []Channel              `gorm:"many2many:release_channels;" json:"channels"`
	Artifacts      []ReleaseArtifact      `gorm:"foreignKey:ReleaseID;constraint:OnDelete:CASCADE" json:"artifacts"`

	// Channel is a compatibility view of the first channel. New callers must
	// use Channels because one release may belong to many channels.
	Channel ReleaseChannel `gorm:"-" json:"channel,omitempty"`
}

type ReleaseArtifact struct {
	ID            string     `gorm:"primaryKey;size:36" json:"id"`
	ReleaseID     string     `gorm:"size:36;index" json:"release_id"`
	ObjectKey     string     `gorm:"size:512;index" json:"object_key"`
	DownloadURL   string     `gorm:"size:2048" json:"download_url,omitempty"`
	Platform      string     `gorm:"size:32" json:"platform"`
	Architecture  string     `gorm:"size:32" json:"architecture"`
	FileName      string     `gorm:"size:255" json:"file_name"`
	MimeType      string     `gorm:"size:255" json:"mime_type"`
	Size          int64      `json:"size"`
	Hash          string     `gorm:"size:255" json:"hash"`
	DownloadCount int64      `gorm:"not null;default:0" json:"-"`
	ExpiredAt     *time.Time `json:"-"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
type Localization struct {
	ID           string    `gorm:"primaryKey;size:36" json:"id"`
	ResourceType string    `gorm:"size:32;uniqueIndex:idx_localization_resource_field_locale,priority:1;index:idx_localization_type_field,priority:1" json:"resource_type"`
	ResourceID   string    `gorm:"size:36;uniqueIndex:idx_localization_resource_field_locale,priority:2" json:"resource_id"`
	Field        string    `gorm:"size:32;uniqueIndex:idx_localization_resource_field_locale,priority:3;index:idx_localization_type_field,priority:2" json:"field"`
	Locale       string    `gorm:"size:32;uniqueIndex:idx_localization_resource_field_locale,priority:4;index:idx_localization_locale_value,priority:1" json:"locale"`
	Value        string    `gorm:"type:text;index:idx_localization_locale_value,priority:2" json:"value"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ClientCheck struct {
	ID            string    `gorm:"primaryKey;size:36" json:"id"`
	AppID         string    `gorm:"size:36;index" json:"app_id"`
	VisitorHash   string    `gorm:"size:64;index" json:"-"`
	Version       string    `gorm:"size:128;index" json:"version"`
	Channel       string    `gorm:"size:64;index" json:"channel"`
	Platform      string    `gorm:"size:32;index" json:"platform"`
	Architecture  string    `gorm:"size:32;index" json:"architecture"`
	Locale        string    `gorm:"size:32;index" json:"locale"`
	OSVersion     string    `gorm:"size:128" json:"os_version"`
	ClientVersion string    `gorm:"size:128" json:"client_version"`
	CheckedAt     time.Time `gorm:"index" json:"checked_at"`
}
