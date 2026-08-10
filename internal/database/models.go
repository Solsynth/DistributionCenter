package database

import "time"

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

type Release struct {
	ID           string            `gorm:"primaryKey;size:36" json:"id"`
	AppID        string            `gorm:"size:36;index;uniqueIndex:idx_release_app_version_channel,priority:1" json:"app_id"`
	Version      string            `gorm:"size:128;uniqueIndex:idx_release_app_version_channel,priority:2" json:"version"`
	Channel      ReleaseChannel    `gorm:"size:16;uniqueIndex:idx_release_app_version_channel,priority:3;index:idx_release_app_channel_status,priority:2" json:"channel"`
	ReleaseNotes string            `json:"release_notes"`
	Status       ReleaseStatus     `gorm:"size:16;index;index:idx_release_app_channel_status,priority:3" json:"status"`
	PublishedAt  *time.Time        `json:"published_at"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Artifacts    []ReleaseArtifact `gorm:"foreignKey:ReleaseID;constraint:OnDelete:CASCADE" json:"artifacts"`
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
