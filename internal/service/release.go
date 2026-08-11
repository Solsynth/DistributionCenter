package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/mod/semver"
	"golang.org/x/text/language"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/distribution/internal/database"
	gen "src.solsynth.dev/sosys/go/proto"
)

var (
	ErrValidation   = errors.New("validation error")
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("conflict")
	ErrDependency   = errors.New("dependency failure")
)

// AuthenticatedAccount is the account identity returned by the auth service.
// IsSuperuser mirrors the auth service's global permission bypass.
type AuthenticatedAccount struct {
	ID          string
	IsSuperuser bool
}

// AccountAuthenticator is an optional extension implemented by integrations
// that can return superuser status with the authenticated account.
type AccountAuthenticator interface {
	AuthenticateAccount(context.Context, string) (AuthenticatedAccount, error)
}

type PublisherDirectory interface {
	Authenticate(context.Context, string) (string, error)
	GetPublisher(context.Context, string) (*gen.DyPublisher, error)
	IsPublisherMember(context.Context, string, string, gen.DyPublisherMemberRole) (bool, error)
}

// AppDirectory is the revision-1 compatibility contract. New deployments use
// PublisherDirectory and do not call Develop.
type AppDirectory interface {
	GetCustomApp(context.Context, string) (*gen.DyCustomApp, error)
	GetAppDeveloper(context.Context, string) (*gen.DyGetAppDeveloperResponse, error)
	CheckCustomAppSecret(context.Context, string, string, bool) (bool, error)
}

type ArtifactMetadata struct {
	ObjectKey string
	FileName  string
	MimeType  string
	Size      int64
	Hash      string
}

type ArtifactStore interface {
	Head(context.Context, string) (*ArtifactMetadata, error)
	PresignedUpload(context.Context, string, string) (*url.URL, error)
	SetPublic(context.Context, string) error
	UnsetPublic(context.Context, string) error
	PublicURL(string) string
}
type ArtifactDownloadStore interface {
	PresignedDownload(context.Context, string) (*url.URL, error)
}

// ArtifactRetentionStore is implemented by artifact backends that can remove
// objects after they fall outside the configured release retention window.
type ArtifactRetentionStore interface {
	Delete(context.Context, string) error
}

type ReleaseEvent struct {
	EventID    string    `json:"event_id,omitempty"`
	Timestamp  time.Time `json:"timestamp,omitempty"`
	EventType  string    `json:"event_type,omitempty"`
	StreamName string    `json:"stream_name,omitempty"`
	ProductID  string    `json:"product_id"`
	ReleaseID  string    `json:"release_id"`
	Version    string    `json:"version"`
	Channels   []string  `json:"channels"`
	Channel    string    `json:"channel,omitempty"`
}

type ReleaseEventPublisher interface {
	PublishPublished(context.Context, ReleaseEvent) error
	PublishYanked(context.Context, ReleaseEvent) error
}
type CreateReleaseInput struct {
	Version      string
	Channel      string // compatibility alias for a one-channel release
	Channels     []string
	ReleaseNotes string
	Title        string
	Titles       map[string]string
	Metadata     database.JSONMap
	ForceUpdate  bool
	Descriptions map[string]string
	Attachments  database.CloudFileReferenceList
}

type UpdateReleaseInput struct {
	Version      string
	Channel      string
	Channels     []string
	ReleaseNotes string
	Title        string
	Titles       map[string]string
	Metadata     database.JSONMap
	ForceUpdate  bool
	Descriptions map[string]string
}

type CreateChannelInput struct {
	Name         string            `json:"name"`
	DisplayName  string            `json:"display_name"`
	DisplayNames map[string]string `json:"display_names"`
	Description  string            `json:"description"`
	Descriptions map[string]string `json:"descriptions"`
}

type UpdateChannelInput struct {
	DisplayName  string            `json:"display_name"`
	DisplayNames map[string]string `json:"display_names"`
	Description  string            `json:"description"`
	Descriptions map[string]string `json:"descriptions"`
}

type ChannelSummary struct {
	Channel *database.Channel
	Latest  *database.Release
}

type ArtifactInput struct {
	ObjectKey    string
	DownloadURL  string
	FileName     string
	MimeType     string
	Size         int64
	Hash         string
	Platform     string
	Architecture string
}

type ArtifactUploadInput struct {
	FileName string
	MimeType string
	Version  string
}

type ArtifactUpload struct {
	ObjectKey string `json:"object_key"`
	UploadURL string `json:"upload_url"`
	ReleaseID string `json:"release_id,omitempty"`
	Version   string `json:"version,omitempty"`
}

type ReleaseListQuery struct {
	Channel      string
	Platform     string
	Architecture string
	Limit        int
	Offset       int
}

type ReleaseListResult struct {
	Data   []*database.Release
	Total  int
	Limit  int
	Offset int
}

type UpdateQuery struct {
	CurrentVersion string
	Channel        string
	Platform       string
	Architecture   string
	InstallationID string
	OSVersion      string
	ClientVersion  string
	Locale         string
}

type UpdateResult struct {
	UpdateAvailable bool
	CurrentVersion  string
	Release         *database.Release
}

type UsageMetrics struct {
	From            time.Time        `json:"from"`
	To              time.Time        `json:"to"`
	Checks          int64            `json:"checks"`
	DAU             int64            `json:"dau"`
	MAU             int64            `json:"mau"`
	ByVersion       map[string]int64 `json:"by_version"`
	ByChannel       map[string]int64 `json:"by_channel"`
	ByPlatform      map[string]int64 `json:"by_platform"`
	ByArchitecture  map[string]int64 `json:"by_architecture"`
	ByOSVersion     map[string]int64 `json:"by_os_version"`
	ByClientVersion map[string]int64 `json:"by_client_version"`
	ByLocale        map[string]int64 `json:"by_locale"`
}

type ReleaseService struct {
	db                *gorm.DB
	apps              AppDirectory
	publishers        PublisherDirectory
	artifacts         ArtifactStore
	events            ReleaseEventPublisher
	permissionChecker PermissionChecker
	analyticsEnabled  bool
	analyticsSalt     string
	artifactRetention int
}

func NewPublisherReleaseService(db *gorm.DB, publishers PublisherDirectory, artifacts ArtifactStore, events ReleaseEventPublisher) *ReleaseService {
	return &ReleaseService{db: db, publishers: publishers, artifacts: artifacts, events: events, analyticsEnabled: true, artifactRetention: 3}
}

// NewReleaseService remains available to revision-1 unit fixtures. Production
// composition must use NewPublisherReleaseService.
func NewReleaseService(db *gorm.DB, apps AppDirectory, artifacts ArtifactStore, events ReleaseEventPublisher) *ReleaseService {
	return &ReleaseService{db: db, apps: apps, artifacts: artifacts, events: events, analyticsEnabled: true, artifactRetention: 3}
}

func (s *ReleaseService) ConfigureAnalytics(enabled bool, salt string) {
	s.analyticsEnabled = enabled
	s.analyticsSalt = salt
}

// ConfigureArtifactRetention sets the number of published releases whose
// object-backed artifacts remain in the artifact store. Zero disables cleanup.
func (s *ReleaseService) ConfigureArtifactRetention(limit int) {
	if limit < 0 {
		limit = 0
	}
	s.artifactRetention = limit
}

func (s *ReleaseService) PublicURL(objectKey string) string {
	if s == nil || s.artifacts == nil {
		return ""
	}
	return s.artifacts.PublicURL(objectKey)
}
func (s *ReleaseService) PresignedDownload(ctx context.Context, objectKey string) (*url.URL, error) {
	if s == nil || s.artifacts == nil {
		return nil, fmt.Errorf("%w: artifact store unavailable", ErrDependency)
	}
	downloader, ok := s.artifacts.(ArtifactDownloadStore)
	if !ok {
		return nil, fmt.Errorf("%w: artifact store does not support signed downloads", ErrDependency)
	}
	return downloader.PresignedDownload(ctx, objectKey)
}

func (s *ReleaseService) EnsureDraftRelease(ctx context.Context, appID, version string) (*database.Release, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	version = strings.TrimSpace(version)
	if !validVersion(version) {
		return nil, fmt.Errorf("%w: invalid version", ErrValidation)
	}
	release, err := s.findReleaseByVersion(appID, version)
	if err == nil {
		if release.Status == database.ReleaseStatusYanked {
			return nil, fmt.Errorf("%w: yanked releases cannot be edited", ErrConflict)
		}
		return release, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	release, err = s.CreateRelease(ctx, appID, CreateReleaseInput{Version: version, Channels: []string{string(database.ReleaseChannelStable)}})
	if err == nil {
		return release, nil
	}
	if !errors.Is(err, ErrConflict) {
		return nil, err
	}
	if existing, lookupErr := s.findReleaseByVersion(appID, version); lookupErr == nil {
		if existing.Status == database.ReleaseStatusYanked {
			return nil, fmt.Errorf("%w: yanked releases cannot be edited", ErrConflict)
		}
		return existing, nil
	}
	return nil, err
}

func (s *ReleaseService) PrepareArtifactUpload(ctx context.Context, appID string, input ArtifactUploadInput) (*ArtifactUpload, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	var release *database.Release
	if version := strings.TrimSpace(input.Version); version != "" {
		var err error
		release, err = s.EnsureDraftRelease(ctx, appID, version)
		if err != nil {
			return nil, err
		}
	} else if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(filepath.Base(input.FileName))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return nil, fmt.Errorf("%w: file_name is required", ErrValidation)
	}
	if s.artifacts == nil {
		return nil, fmt.Errorf("%w: artifact store unavailable", ErrDependency)
	}
	key := "artifacts/" + appID + "/" + uuid.NewString() + "/" + name
	uploadURL, err := s.artifacts.PresignedUpload(ctx, key, strings.TrimSpace(input.MimeType))
	if err != nil {
		return nil, dependencyError(err)
	}
	upload := &ArtifactUpload{ObjectKey: key, UploadURL: uploadURL.String()}
	if release != nil {
		upload.ReleaseID = release.ID
		upload.Version = release.Version
	}
	return upload, nil
}

func (s *ReleaseService) CreateRelease(ctx context.Context, appID string, input CreateReleaseInput) (*database.Release, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	version := strings.TrimSpace(input.Version)
	if !validVersion(version) {
		return nil, fmt.Errorf("%w: invalid version", ErrValidation)
	}
	descriptions, err := normalizeDescriptions(input.Descriptions)
	if err != nil {
		return nil, err
	}
	titles, err := normalizeTitles(input.Titles)
	if err != nil {
		return nil, err
	}
	attachments, err := normalizeCloudFiles(input.Attachments)
	if err != nil {
		return nil, err
	}
	channelNames, err := normalizeChannels(input.Channels, input.Channel)
	if err != nil {
		return nil, err
	}
	channelModels, err := s.ensureChannels(appID, channelNames)
	if err != nil {
		return nil, err
	}
	release := &database.Release{ID: uuid.NewString(), AppID: appID, Version: version, ReleaseNotes: input.ReleaseNotes, Title: strings.TrimSpace(input.Title), Metadata: input.Metadata, ForceUpdate: input.ForceUpdate, Descriptions: descriptions, Titles: titles, Attachments: attachments, Status: database.ReleaseStatusDraft, Channels: channelModels}
	if err := s.db.Create(release).Error; err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("%w: release version already exists", ErrConflict)
		}
		return nil, fmt.Errorf("create release: %w", err)
	}
	if err := replaceLocalizations(s.db, localizationRelease, release.ID, map[string]database.LocalizedText{localizationTitle: titles, localizationDescription: descriptions}); err != nil {
		return nil, err
	}
	return s.loadRelease(release.ID)
}

// AddArtifact attaches a completed S3 upload or an external download link to a
// release. Draft and published releases remain editable; newly attached S3
// artifacts are made public immediately when the release is already published.
func (s *ReleaseService) AddArtifact(ctx context.Context, appID, releaseID string, input ArtifactInput) (*database.Release, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	release, err := s.findRelease(appID, releaseID)
	if err != nil && errors.Is(err, ErrNotFound) && validVersion(strings.TrimSpace(releaseID)) {
		release, err = s.findReleaseByVersion(appID, releaseID)
	}
	if err != nil {
		return nil, err
	}
	releaseID = release.ID
	if release.Status == database.ReleaseStatusYanked {
		return nil, fmt.Errorf("%w: yanked releases cannot be edited", ErrConflict)
	}
	artifact, err := s.buildArtifact(ctx, appID, input)
	if err != nil {
		return nil, err
	}
	var existing database.ReleaseArtifact
	if err := s.db.Where("release_id = ? AND ((object_key <> '' AND object_key = ?) OR (platform = ? AND architecture = ?))", releaseID, artifact.ObjectKey, artifact.Platform, artifact.Architecture).First(&existing).Error; err == nil {
		return nil, fmt.Errorf("%w: artifact object or target already exists", ErrConflict)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check release artifact: %w", err)
	}

	if release.Status == database.ReleaseStatusPublished && artifact.DownloadURL == "" {
		if err := s.artifacts.SetPublic(ctx, artifact.ObjectKey); err != nil {
			return nil, fmt.Errorf("%w: make artifact public: %v", ErrConflict, err)
		}
	}
	artifact.ReleaseID = releaseID
	if err := s.db.Create(&artifact).Error; err != nil {
		if release.Status == database.ReleaseStatusPublished && artifact.DownloadURL == "" {
			_ = s.artifacts.UnsetPublic(ctx, artifact.ObjectKey)
		}
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("%w: artifact object or target already exists", ErrConflict)
		}
		return nil, fmt.Errorf("create release artifact: %w", err)
	}
	return s.loadRelease(releaseID)
}

func (s *ReleaseService) buildArtifact(ctx context.Context, appID string, input ArtifactInput) (database.ReleaseArtifact, error) {
	objectKey := strings.TrimSpace(input.ObjectKey)
	downloadURL := strings.TrimSpace(input.DownloadURL)
	platform := normalize(input.Platform)
	architecture := normalize(input.Architecture)
	if platform == "" || architecture == "" {
		return database.ReleaseArtifact{}, fmt.Errorf("%w: artifact platform and architecture are required", ErrValidation)
	}
	if downloadURL != "" {
		parsed, err := url.Parse(downloadURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return database.ReleaseArtifact{}, fmt.Errorf("%w: artifact download_url must be an absolute HTTP(S) URL", ErrValidation)
		}
		fileName := strings.TrimSpace(input.FileName)
		if fileName == "" {
			fileName = filepath.Base(parsed.Path)
		}
		if fileName == "" || fileName == "." || fileName == string(filepath.Separator) {
			fileName = "artifact"
		}
		if input.Size < 0 {
			return database.ReleaseArtifact{}, fmt.Errorf("%w: artifact size cannot be negative", ErrValidation)
		}
		return database.ReleaseArtifact{ID: uuid.NewString(), DownloadURL: downloadURL, Platform: platform, Architecture: architecture, FileName: fileName, MimeType: strings.TrimSpace(input.MimeType), Size: input.Size, Hash: strings.TrimSpace(input.Hash)}, nil
	}
	if objectKey == "" {
		return database.ReleaseArtifact{}, fmt.Errorf("%w: artifact object_key or download_url is required", ErrValidation)
	}
	if !strings.HasPrefix(objectKey, "artifacts/"+appID+"/") {
		return database.ReleaseArtifact{}, fmt.Errorf("%w: artifact does not belong to app", ErrConflict)
	}
	if s.artifacts == nil {
		return database.ReleaseArtifact{}, fmt.Errorf("%w: artifact store unavailable", ErrDependency)
	}
	metadata, err := s.artifacts.Head(ctx, objectKey)
	if err != nil || metadata == nil || metadata.Size < 0 || strings.TrimSpace(metadata.Hash) == "" {
		return database.ReleaseArtifact{}, fmt.Errorf("%w: artifact %s is missing or incomplete", ErrConflict, objectKey)
	}
	return database.ReleaseArtifact{ID: uuid.NewString(), ObjectKey: objectKey, Platform: platform, Architecture: architecture, FileName: metadata.FileName, MimeType: metadata.MimeType, Size: metadata.Size, Hash: metadata.Hash}, nil
}

func (s *ReleaseService) UpdateRelease(ctx context.Context, appID, releaseID string, input UpdateReleaseInput) (*database.Release, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	release, err := s.findRelease(appID, releaseID)
	if err != nil {
		return nil, err
	}
	if release.Status == database.ReleaseStatusYanked {
		return nil, fmt.Errorf("%w: yanked releases cannot be edited", ErrConflict)
	}
	version := strings.TrimSpace(input.Version)
	if !validVersion(version) {
		return nil, fmt.Errorf("%w: invalid version", ErrValidation)
	}
	descriptions, err := normalizeDescriptions(input.Descriptions)
	if err != nil {
		return nil, err
	}
	titles, err := normalizeTitles(input.Titles)
	if err != nil {
		return nil, err
	}
	channelNames, err := normalizeChannels(input.Channels, input.Channel)
	if err != nil {
		return nil, err
	}
	channelModels, err := s.ensureChannels(appID, channelNames)
	if err != nil {
		return nil, err
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&database.Release{}).
			Where("id = ? AND app_id = ? AND status IN ?", releaseID, appID, []database.ReleaseStatus{database.ReleaseStatusDraft, database.ReleaseStatusPublished}).
			Updates(map[string]any{
				"version":       version,
				"release_notes": input.ReleaseNotes,
				"title":         strings.TrimSpace(input.Title),
				"metadata":      input.Metadata,
				"force_update":  input.ForceUpdate,
				"updated_at":    time.Now().UTC(),
			})
		if result.Error != nil {
			if isUniqueConstraint(result.Error) {
				return fmt.Errorf("%w: release version already exists", ErrConflict)
			}
			return fmt.Errorf("update release: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w: release changed concurrently", ErrConflict)
		}
		if err := tx.Exec("DELETE FROM release_channels WHERE release_id = ?", releaseID).Error; err != nil {
			return fmt.Errorf("clear release channels: %w", err)
		}
		for _, channel := range channelModels {
			if err := tx.Exec("INSERT INTO release_channels (release_id, channel_id) VALUES (?, ?)", releaseID, channel.ID).Error; err != nil {
				return fmt.Errorf("save release channels: %w", err)
			}
		}
		return replaceLocalizations(tx, localizationRelease, releaseID, map[string]database.LocalizedText{
			localizationTitle:       titles,
			localizationDescription: descriptions,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.loadRelease(releaseID)
}

func (s *ReleaseService) Publish(ctx context.Context, appID, releaseID string) (*database.Release, error) {
	release, err := s.findRelease(appID, releaseID)
	if errors.Is(err, ErrNotFound) && validVersion(strings.TrimSpace(releaseID)) {
		release, err = s.findReleaseByVersion(appID, releaseID)
		if err == nil {
			releaseID = release.ID
		}
	}
	if err != nil {
		return nil, err
	}
	if release.Status == database.ReleaseStatusPublished {
		return release, nil
	}
	if release.Status != database.ReleaseStatusDraft {
		return nil, fmt.Errorf("%w: only draft releases can be published", ErrConflict)
	}
	app, err := s.requireApp(ctx, appID, false)
	if err != nil {
		return nil, err
	}
	if app.GetStatus() != gen.DyCustomAppStatus_DY_PRODUCTION {
		return nil, fmt.Errorf("%w: app is not in production", ErrForbidden)
	}
	if len(release.Artifacts) == 0 || len(release.Channels) == 0 {
		return nil, fmt.Errorf("%w: release has no channels or artifacts", ErrConflict)
	}
	for _, artifact := range release.Artifacts {
		if artifact.DownloadURL != "" {
			continue
		}
		if s.artifacts == nil {
			return nil, fmt.Errorf("%w: artifact store unavailable", ErrDependency)
		}
		metadata, err := s.artifacts.Head(ctx, artifact.ObjectKey)
		if err != nil || metadata == nil || metadata.Size < 0 || strings.TrimSpace(metadata.Hash) == "" {
			return nil, fmt.Errorf("%w: artifact %s is no longer complete", ErrConflict, artifact.ObjectKey)
		}
	}
	public := make([]string, 0, len(release.Artifacts))
	for _, artifact := range release.Artifacts {
		if artifact.DownloadURL != "" {
			continue
		}
		if err := s.artifacts.SetPublic(ctx, artifact.ObjectKey); err != nil {
			for _, objectKey := range public {
				_ = s.artifacts.UnsetPublic(ctx, objectKey)
			}
			return nil, fmt.Errorf("%w: make artifact public: %v", ErrConflict, err)
		}
		public = append(public, artifact.ObjectKey)
	}
	now := time.Now().UTC()
	result := s.db.Model(&database.Release{}).Where("id = ? AND app_id = ? AND status = ?", releaseID, appID, database.ReleaseStatusDraft).Updates(map[string]any{"status": database.ReleaseStatusPublished, "published_at": now, "updated_at": now})
	if result.Error != nil || result.RowsAffected != 1 {
		for _, objectKey := range public {
			_ = s.artifacts.UnsetPublic(ctx, objectKey)
		}
		if result.Error != nil {
			return nil, fmt.Errorf("publish release: %w", result.Error)
		}
		return nil, fmt.Errorf("%w: release changed concurrently", ErrConflict)
	}
	published, err := s.loadRelease(releaseID)
	if err != nil {
		return nil, err
	}
	s.publishEvent(ctx, true, published)
	s.cleanupArtifactRetention(ctx, appID)
	return published, nil
}

func (s *ReleaseService) cleanupArtifactRetention(ctx context.Context, appID string) {
	if s.artifactRetention <= 0 || s.artifacts == nil {
		return
	}
	retentionStore, ok := s.artifacts.(ArtifactRetentionStore)
	if !ok {
		slog.Warn("artifact retention is configured but the artifact store cannot delete objects", "product_id", appID)
		return
	}

	var staleReleases []database.Release
	if err := s.db.Preload("Artifacts").
		Where("app_id = ? AND status = ?", appID, database.ReleaseStatusPublished).
		Order("published_at DESC, created_at DESC, id DESC").
		Offset(s.artifactRetention).
		Find(&staleReleases).Error; err != nil {
		slog.Warn("load stale releases for artifact retention", "product_id", appID, "error", err)
		return
	}
	if len(staleReleases) == 0 {
		return
	}

	staleIDs := make([]string, 0, len(staleReleases))
	candidates := make(map[string]struct{})
	for _, release := range staleReleases {
		staleIDs = append(staleIDs, release.ID)
		for _, artifact := range release.Artifacts {
			if artifact.DownloadURL == "" && strings.TrimSpace(artifact.ObjectKey) != "" {
				candidates[artifact.ObjectKey] = struct{}{}
			}
		}
	}
	if len(candidates) == 0 {
		return
	}

	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var protectedKeys []string
	if err := s.db.Model(&database.ReleaseArtifact{}).
		Where("object_key IN ? AND release_id NOT IN ?", keys, staleIDs).
		Distinct().
		Pluck("object_key", &protectedKeys).Error; err != nil {
		slog.Warn("check shared artifacts for retention", "product_id", appID, "error", err)
		return
	}
	protected := make(map[string]struct{}, len(protectedKeys))
	for _, key := range protectedKeys {
		protected[key] = struct{}{}
	}
	for _, key := range keys {
		if _, ok := protected[key]; ok {
			continue
		}
		if err := retentionStore.Delete(ctx, key); err != nil {
			slog.Warn("delete expired release artifact", "product_id", appID, "object_key", key, "error", err)
			continue
		}
		if err := s.db.Model(&database.ReleaseArtifact{}).
			Where("release_id IN ? AND object_key = ?", staleIDs, key).
			Update("expired_at", time.Now().UTC()).Error; err != nil {
			slog.Warn("mark expired release artifact", "product_id", appID, "object_key", key, "error", err)
		}
	}
}

func (s *ReleaseService) Yank(ctx context.Context, appID, releaseID string) (*database.Release, error) {
	release, err := s.findRelease(appID, releaseID)
	if err != nil {
		return nil, err
	}
	if release.Status == database.ReleaseStatusYanked {
		return release, nil
	}
	if release.Status != database.ReleaseStatusPublished {
		return nil, fmt.Errorf("%w: only published releases can be yanked", ErrConflict)
	}
	if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if err := s.db.Model(&database.Release{}).Where("id = ? AND app_id = ? AND status = ?", releaseID, appID, database.ReleaseStatusPublished).Updates(map[string]any{"status": database.ReleaseStatusYanked, "updated_at": now}).Error; err != nil {
		return nil, fmt.Errorf("yank release: %w", err)
	}
	yanked, err := s.loadRelease(releaseID)
	if err != nil {
		return nil, err
	}
	s.publishEvent(ctx, false, yanked)
	return yanked, nil
}

func (s *ReleaseService) GetPublicApp(ctx context.Context, appID string) (*gen.DyCustomApp, *gen.DyGetAppDeveloperResponse, *database.Release, error) {
	app, err := s.requireApp(ctx, appID, true)
	if err != nil {
		return nil, nil, nil, err
	}
	developer, err := s.apps.GetAppDeveloper(ctx, appID)
	if err != nil {
		return nil, nil, nil, dependencyError(err)
	}
	latest, err := s.latest(appID, string(database.ReleaseChannelStable))
	if err != nil {
		return nil, nil, nil, err
	}
	return app, developer, latest, nil
}

func (s *ReleaseService) ListReleases(ctx context.Context, appID string, query ReleaseListQuery) (*ReleaseListResult, error) {
	if _, err := s.requireApp(ctx, appID, true); err != nil {
		return nil, err
	}
	channel := normalize(query.Channel)
	if !validChannelName(channel) || query.Limit < 0 || query.Offset < 0 {
		return nil, fmt.Errorf("%w: channel, limit, and offset are invalid", ErrValidation)
	}
	if _, err := s.channelForApp(appID, channel); err != nil {
		return nil, err
	}
	platform, architecture := normalize(query.Platform), normalize(query.Architecture)
	if (platform == "") != (architecture == "") {
		return nil, fmt.Errorf("%w: platform and architecture must be provided together", ErrValidation)
	}
	limit := query.Limit
	if limit == 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var releases []*database.Release
	if err := s.db.Preload("Artifacts").Preload("Channels").Where("app_id = ? AND status = ?", appID, database.ReleaseStatusPublished).Find(&releases).Error; err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	if err := hydrateReleaseLocalizations(s.db, releases); err != nil {
		return nil, err
	}
	for _, release := range releases {
		hydrateLegacyChannel(release)
	}
	filtered := releases[:0]
	for _, release := range releases {
		if hasChannel(release, channel) && (platform == "" || hasArtifact(release, platform, architecture)) {
			filtered = append(filtered, release)
		}
	}
	releases = filtered
	sortReleases(releases)
	total := len(releases)
	if query.Offset >= total {
		releases = []*database.Release{}
	} else {
		end := query.Offset + limit
		if end > total {
			end = total
		}
		releases = releases[query.Offset:end]
	}
	return &ReleaseListResult{Data: releases, Total: total, Limit: limit, Offset: query.Offset}, nil
}

func (s *ReleaseService) ResolveUpdate(ctx context.Context, appID string, query UpdateQuery) (*UpdateResult, error) {
	if _, err := s.requireApp(ctx, appID, true); err != nil {
		return nil, err
	}
	if !validVersion(query.CurrentVersion) {
		return nil, fmt.Errorf("%w: invalid current_version", ErrValidation)
	}
	channel := normalize(query.Channel)
	platform, architecture := normalize(query.Platform), normalize(query.Architecture)
	if !validChannelName(channel) || platform == "" || architecture == "" {
		return nil, fmt.Errorf("%w: invalid update query", ErrValidation)
	}
	if _, err := normalizeLocale(query.Locale); err != nil {
		return nil, err
	}
	if _, err := s.channelForApp(appID, channel); err != nil {
		return nil, err
	}
	var releases []*database.Release
	if err := s.db.Preload("Artifacts").Preload("Channels").Where("app_id = ? AND status = ?", appID, database.ReleaseStatusPublished).Find(&releases).Error; err != nil {
		return nil, fmt.Errorf("resolve update: %w", err)
	}
	if err := hydrateReleaseLocalizations(s.db, releases); err != nil {
		return nil, err
	}
	for _, release := range releases {
		hydrateLegacyChannel(release)
	}
	sortReleases(releases)
	for _, release := range releases {
		if hasChannel(release, channel) && semver.Compare("v"+release.Version, "v"+query.CurrentVersion) > 0 && hasArtifact(release, platform, architecture) {
			result := &UpdateResult{UpdateAvailable: true, CurrentVersion: query.CurrentVersion, Release: release}
			s.recordCheck(ctx, appID, query)
			return result, nil
		}
	}
	s.recordCheck(ctx, appID, query)
	return &UpdateResult{CurrentVersion: query.CurrentVersion}, nil
}

func (s *ReleaseService) UsageMetrics(ctx context.Context, appID string, from, to time.Time) (*UsageMetrics, error) {
	if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	to = to.UTC()
	from = from.UTC()
	if from.IsZero() {
		from = to.Add(-30 * 24 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if !from.Before(to) {
		return nil, fmt.Errorf("%w: invalid metrics range", ErrValidation)
	}
	base := s.db.Model(&database.ClientCheck{}).Where("app_id = ? AND checked_at >= ? AND checked_at < ?", appID, from, to)
	var checks int64
	if err := base.Count(&checks).Error; err != nil {
		return nil, fmt.Errorf("count checks: %w", err)
	}
	var dau, mau int64
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if err := s.db.Model(&database.ClientCheck{}).Where("app_id = ? AND checked_at >= ? AND checked_at < ?", appID, today, today.Add(24*time.Hour)).Select("COUNT(DISTINCT visitor_hash)").Scan(&dau).Error; err != nil {
		return nil, fmt.Errorf("count dau: %w", err)
	}
	if err := s.db.Model(&database.ClientCheck{}).Where("app_id = ? AND checked_at >= ?", appID, time.Now().UTC().Add(-30*24*time.Hour)).Select("COUNT(DISTINCT visitor_hash)").Scan(&mau).Error; err != nil {
		return nil, fmt.Errorf("count mau: %w", err)
	}
	metrics := &UsageMetrics{
		From: from, To: to, Checks: checks, DAU: dau, MAU: mau,
		ByVersion: map[string]int64{}, ByChannel: map[string]int64{},
		ByPlatform: map[string]int64{}, ByArchitecture: map[string]int64{},
		ByOSVersion: map[string]int64{}, ByClientVersion: map[string]int64{},
		ByLocale: map[string]int64{},
	}
	var rows []struct {
		Value string
		Count int64
	}
	for column, target := range map[string]*map[string]int64{
		"version": &metrics.ByVersion, "channel": &metrics.ByChannel,
		"platform": &metrics.ByPlatform, "architecture": &metrics.ByArchitecture,
		"os_version": &metrics.ByOSVersion, "client_version": &metrics.ByClientVersion,
		"locale": &metrics.ByLocale,
	} {
		rows = nil
		if err := base.Select(column + " AS value, COUNT(*) AS count").Group(column).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("group metrics: %w", err)
		}
		for _, row := range rows {
			if row.Value == "" {
				continue
			}
			(*target)[row.Value] = row.Count
		}
	}
	return metrics, nil
}

func (s *ReleaseService) GetRelease(appID, releaseID string) (*database.Release, error) {
	return s.findRelease(appID, releaseID)
}

func (s *ReleaseService) requireApp(ctx context.Context, appID string, production bool) (*gen.DyCustomApp, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	if keyProductID := UploadAPIKeyProductID(ctx); keyProductID != "" {
		if keyProductID != appID {
			return nil, ErrForbidden
		}
		if _, err := s.loadProduct(appID); err != nil {
			return nil, err
		}
		return &gen.DyCustomApp{Id: appID, Status: gen.DyCustomAppStatus_DY_PRODUCTION}, nil
	}
	if s.publishers != nil {
		var product database.Product
		if err := s.db.Where("id = ?", appID).First(&product).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("%w: product", ErrNotFound)
			}
			return nil, fmt.Errorf("load product: %w", err)
		}
		publisher, err := s.publishers.GetPublisher(ctx, product.PublisherID)
		if err != nil {
			return nil, dependencyError(err)
		}
		if publisher == nil {
			return nil, fmt.Errorf("%w: publisher", ErrNotFound)
		}
		if !production {
			accountID := AccountID(ctx)
			if accountID == "" {
				return nil, ErrUnauthorized
			}
			valid, err := s.publishers.IsPublisherMember(ctx, product.PublisherID, accountID, gen.DyPublisherMemberRole_DY_EDITOR)
			if err != nil {
				return nil, dependencyError(err)
			}
			if !valid {
				return nil, ErrForbidden
			}
		}
		return &gen.DyCustomApp{Id: product.ID, Status: gen.DyCustomAppStatus_DY_PRODUCTION}, nil
	}
	if s.apps == nil {
		return nil, fmt.Errorf("%w: publisher service unavailable", ErrDependency)
	}
	app, err := s.apps.GetCustomApp(ctx, appID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, fmt.Errorf("%w: app", ErrNotFound)
		}
		return nil, dependencyError(err)
	}
	if app == nil {
		return nil, fmt.Errorf("%w: app", ErrNotFound)
	}
	if app.GetStatus() == gen.DyCustomAppStatus_DY_SUSPENDED {
		if production {
			return nil, fmt.Errorf("%w: app", ErrNotFound)
		}
		return nil, fmt.Errorf("%w: app is suspended", ErrForbidden)
	}
	if production && app.GetStatus() != gen.DyCustomAppStatus_DY_PRODUCTION {
		return nil, fmt.Errorf("%w: app is not in production", ErrForbidden)
	}
	return app, nil
}

func (s *ReleaseService) findReleaseByVersion(appID, version string) (*database.Release, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	version = strings.TrimSpace(version)
	if !validVersion(version) {
		return nil, fmt.Errorf("%w: invalid version", ErrValidation)
	}
	var release database.Release
	query := s.db.Preload("Artifacts").Preload("Channels").Where("app_id = ? AND version = ?", appID, version)
	if err := query.First(&release).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: release", ErrNotFound)
		}
		return nil, fmt.Errorf("load release by version: %w", err)
	}
	releases := []*database.Release{&release}
	if err := hydrateReleaseLocalizations(s.db, releases); err != nil {
		return nil, err
	}
	hydrateLegacyChannel(&release)
	return &release, nil
}

func (s *ReleaseService) findRelease(appID, releaseID string) (*database.Release, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	return s.loadReleaseWhere(releaseID, appID)
}

func (s *ReleaseService) loadRelease(id string) (*database.Release, error) {
	return s.loadReleaseWhere(id, "")
}

func (s *ReleaseService) loadReleaseWhere(id, appID string) (*database.Release, error) {
	var release database.Release
	query := s.db.Preload("Artifacts").Preload("Channels").Where("id = ?", id)
	if appID != "" {
		query = query.Where("app_id = ?", appID)
	}
	if err := query.First(&release).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: release", ErrNotFound)
		}
		return nil, fmt.Errorf("load release: %w", err)
	}
	releases := []*database.Release{&release}
	if err := hydrateReleaseLocalizations(s.db, releases); err != nil {
		return nil, err
	}
	hydrateLegacyChannel(&release)
	return &release, nil
}

func (s *ReleaseService) latest(appID, channel string) (*database.Release, error) {
	var releases []*database.Release
	if err := s.db.Preload("Artifacts").Preload("Channels").Where("app_id = ? AND status = ?", appID, database.ReleaseStatusPublished).Find(&releases).Error; err != nil {
		return nil, fmt.Errorf("latest release: %w", err)
	}
	if err := hydrateReleaseLocalizations(s.db, releases); err != nil {
		return nil, err
	}
	filtered := releases[:0]
	for _, release := range releases {
		if hasChannel(release, channel) {
			filtered = append(filtered, release)
		}
	}
	sortReleases(filtered)
	for _, release := range filtered {
		hydrateLegacyChannel(release)
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	return filtered[0], nil
}

func (s *ReleaseService) publishEvent(ctx context.Context, published bool, release *database.Release) {
	if s.events == nil || release == nil {
		return
	}
	names := channelNames(release)
	event := ReleaseEvent{EventID: uuid.NewString(), Timestamp: time.Now().UTC(), ProductID: release.AppID, ReleaseID: release.ID, Version: release.Version, Channels: names}
	if len(names) > 0 {
		event.Channel = names[0]
	}
	var err error
	if published {
		event.EventType, event.StreamName = "distribution.release.published.v1", "distribution_events"
		err = s.events.PublishPublished(ctx, event)
	} else {
		event.EventType, event.StreamName = "distribution.release.yanked.v1", "distribution_events"
		err = s.events.PublishYanked(ctx, event)
	}
	if err != nil {
		slog.Warn("release event publication failed", "release_id", release.ID, "published", published, "error", err)
	}
}

func (s *ReleaseService) recordCheck(ctx context.Context, appID string, query UpdateQuery) {
	if !s.analyticsEnabled || strings.TrimSpace(query.InstallationID) == "" {
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(query.InstallationID)); err != nil {
		return
	}
	digest := sha256.Sum256([]byte(s.analyticsSalt + ":" + strings.TrimSpace(query.InstallationID)))
	locale, _ := normalizeLocale(query.Locale)
	if locale == "" {
		locale = "und"
	}
	check := &database.ClientCheck{ID: uuid.NewString(), AppID: appID, VisitorHash: hex.EncodeToString(digest[:]), Version: strings.TrimSpace(query.CurrentVersion), Channel: normalize(query.Channel), Platform: normalize(query.Platform), Architecture: normalize(query.Architecture), Locale: locale, OSVersion: strings.TrimSpace(query.OSVersion), ClientVersion: strings.TrimSpace(query.ClientVersion), CheckedAt: time.Now().UTC()}
	if err := s.db.Create(check).Error; err != nil {
		slog.Warn("record update check failed", "product_id", appID, "error", err)
	}
	_ = ctx
}

func validateAppID(appID string) error {
	if _, err := uuid.Parse(strings.TrimSpace(appID)); err != nil {
		return fmt.Errorf("%w: app_id must be a UUID", ErrValidation)
	}
	return nil
}

func validVersion(version string) bool {
	return version != "" && !strings.HasPrefix(version, "v") && semver.IsValid("v"+version)
}

func normalize(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func normalizeLocale(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", "-"))
	if value == "" {
		return "", nil
	}
	tag, err := language.Parse(value)
	if err != nil || tag == language.Und {
		return "", fmt.Errorf("%w: invalid locale %q", ErrValidation, value)
	}
	return tag.String(), nil
}

func normalizeDescriptions(values map[string]string) (database.LocalizedText, error) {
	return normalizeLocalizedText(values, "description")
}

func normalizeTitles(values map[string]string) (database.LocalizedText, error) {
	return normalizeLocalizedText(values, "title")
}

func normalizeLocalizedText(values map[string]string, field string) (database.LocalizedText, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(database.LocalizedText, len(values))
	for locale, value := range values {
		normalizedLocale, err := normalizeLocale(locale)
		if err != nil {
			return nil, err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%w: %s for locale %q is empty", ErrValidation, field, locale)
		}
		if previous, exists := result[normalizedLocale]; exists && previous != value {
			return nil, fmt.Errorf("%w: duplicate locale %q", ErrValidation, normalizedLocale)
		}
		result[normalizedLocale] = value
	}
	return result, nil
}
func normalizeCloudFiles(values database.CloudFileReferenceList) (database.CloudFileReferenceList, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(database.CloudFileReferenceList, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Id = strings.TrimSpace(value.Id)
		value.Url = strings.TrimSpace(value.Url)
		value.Name = strings.TrimSpace(value.Name)
		if value.Id == "" && value.Url == "" {
			return nil, fmt.Errorf("%w: cloud file reference requires id or url", ErrValidation)
		}
		key := value.Id
		if key == "" {
			key = value.Url
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func hasArtifact(release *database.Release, platform, architecture string) bool {
	for _, artifact := range release.Artifacts {
		if artifact.ExpiredAt == nil && artifact.Platform == platform && artifact.Architecture == architecture {
			return true
		}
	}
	return false
}

func hasChannel(release *database.Release, name string) bool {
	for _, channel := range release.Channels {
		if channel.Name == name {
			return true
		}
	}
	return false
}

func channelNames(release *database.Release) []string {
	names := make([]string, 0, len(release.Channels))
	for _, channel := range release.Channels {
		names = append(names, channel.Name)
	}
	sort.Strings(names)
	return names
}

func hydrateLegacyChannel(release *database.Release) {
	names := channelNames(release)
	if len(names) > 0 {
		release.Channel = database.ReleaseChannel(names[0])
	}
}

func sortReleases(releases []*database.Release) {
	sort.SliceStable(releases, func(i, j int) bool {
		return semver.Compare("v"+releases[i].Version, "v"+releases[j].Version) > 0
	})
}

func dependencyError(err error) error {
	if err == nil {
		return ErrDependency
	}
	return fmt.Errorf("%w: %v", ErrDependency, err)
}

func isUniqueConstraint(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
