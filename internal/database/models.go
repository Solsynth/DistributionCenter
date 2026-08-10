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

type Channel struct {
	ID          string    `gorm:"primaryKey;size:36" json:"id"`
	AppID       string    `gorm:"size:36;index;uniqueIndex:idx_channel_app_name,priority:1" json:"app_id"`
	Name        string    `gorm:"size:64;uniqueIndex:idx_channel_app_name,priority:2" json:"name"`
	DisplayName string    `gorm:"size:128" json:"display_name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Release struct {
	ID           string            `gorm:"primaryKey;size:36" json:"id"`
	AppID        string            `gorm:"size:36;index;uniqueIndex:idx_release_app_version,priority:1" json:"app_id"`
	Version      string            `gorm:"size:128;uniqueIndex:idx_release_app_version,priority:2" json:"version"`
	ReleaseNotes string            `json:"release_notes"`
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
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	AppID          string    `gorm:"size:36;index" json:"app_id"`
	VisitorHash    string    `gorm:"size:64;index" json:"-"`
	Channel        string    `gorm:"size:64;index" json:"channel"`
	Platform       string    `gorm:"size:32;index" json:"platform"`
	Architecture   string    `gorm:"size:32;index" json:"architecture"`
	OSVersion      string    `gorm:"size:128" json:"os_version"`
	ClientVersion  string    `gorm:"size:128" json:"client_version"`
	CheckedAt      time.Time `gorm:"index" json:"checked_at"`
}
