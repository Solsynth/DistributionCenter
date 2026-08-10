package service

import (
	"context"
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

type ReleaseEvent struct {
	EventID    string    `json:"event_id,omitempty"`
	Timestamp  time.Time `json:"timestamp,omitempty"`
	EventType  string    `json:"event_type,omitempty"`
	StreamName string    `json:"stream_name,omitempty"`
	AppID      string    `json:"app_id"`
	ReleaseID  string    `json:"release_id"`
	Version    string    `json:"version"`
	Channel    string    `json:"channel"`
}

type ReleaseEventPublisher interface {
	PublishPublished(context.Context, ReleaseEvent) error
	PublishYanked(context.Context, ReleaseEvent) error
}

type CreateReleaseInput struct {
	Version string
	Channel string

	ReleaseNotes string
	Artifacts    []ArtifactInput
}

type ArtifactInput struct {
	ObjectKey    string
	Platform     string
	Architecture string
}

type ArtifactUploadInput struct {
	FileName string
	MimeType string
}

type ArtifactUpload struct {
	ObjectKey string
	UploadURL string
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
}

func (s *ReleaseService) PublicURL(objectKey string) string {
	if s == nil || s.artifacts == nil {
		return ""
	}
	return s.artifacts.PublicURL(objectKey)
}

type UpdateResult struct {
	UpdateAvailable bool
	CurrentVersion  string
	Release         *database.Release
}

type ReleaseService struct {
	db        *gorm.DB
	apps      AppDirectory
	artifacts ArtifactStore
	events    ReleaseEventPublisher
}

func NewReleaseService(db *gorm.DB, apps AppDirectory, artifacts ArtifactStore, events ReleaseEventPublisher) *ReleaseService {
	return &ReleaseService{db: db, apps: apps, artifacts: artifacts, events: events}
}

func (s *ReleaseService) PrepareArtifactUpload(ctx context.Context, appID string, input ArtifactUploadInput) (*ArtifactUpload, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(filepath.Base(input.FileName))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return nil, fmt.Errorf("%w: file_name is required", ErrValidation)
	}
	key := "artifacts/" + appID + "/" + uuid.NewString() + "/" + name
	uploadURL, err := s.artifacts.PresignedUpload(ctx, key, strings.TrimSpace(input.MimeType))
	if err != nil {
		return nil, dependencyError(err)
	}
	return &ArtifactUpload{ObjectKey: key, UploadURL: uploadURL.String()}, nil
}

func (s *ReleaseService) CreateRelease(ctx context.Context, appID string, input CreateReleaseInput) (*database.Release, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	if _, err := s.requireApp(ctx, appID, false); err != nil {
		return nil, err
	}
	version, channel, err := validateReleaseFields(input.Version, input.Channel)
	if err != nil {
		return nil, err
	}
	if len(input.Artifacts) == 0 {
		return nil, fmt.Errorf("%w: at least one artifact is required", ErrValidation)
	}
	artifacts := make([]database.ReleaseArtifact, 0, len(input.Artifacts))
	seen := make(map[string]struct{}, len(input.Artifacts))
	for _, inputArtifact := range input.Artifacts {
		objectKey := strings.TrimSpace(inputArtifact.ObjectKey)
		platform := normalize(inputArtifact.Platform)
		architecture := normalize(inputArtifact.Architecture)
		if objectKey == "" || platform == "" || architecture == "" {
			return nil, fmt.Errorf("%w: artifact object_key, platform, and architecture are required", ErrValidation)
		}
		if !strings.HasPrefix(objectKey, "artifacts/"+appID+"/") {
			return nil, fmt.Errorf("%w: artifact does not belong to app", ErrConflict)
		}
		key := objectKey + "\x00" + platform + "\x00" + architecture
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: duplicate artifact", ErrConflict)
		}
		seen[key] = struct{}{}
		metadata, err := s.artifacts.Head(ctx, objectKey)
		if err != nil || metadata == nil || metadata.Size < 0 || strings.TrimSpace(metadata.Hash) == "" {
			return nil, fmt.Errorf("%w: artifact %s is missing or incomplete", ErrConflict, objectKey)
		}
		artifacts = append(artifacts, database.ReleaseArtifact{
			ID:           uuid.NewString(),
			ObjectKey:    objectKey,
			Platform:     platform,
			Architecture: architecture,
			FileName:     metadata.FileName,
			MimeType:     metadata.MimeType,
			Size:         metadata.Size,
			Hash:         metadata.Hash,
		})
	}

	release := &database.Release{ID: uuid.NewString(), AppID: appID, Version: version, Channel: channel, ReleaseNotes: input.ReleaseNotes, Status: database.ReleaseStatusDraft, Artifacts: artifacts}
	if err := s.db.Create(release).Error; err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("%w: release version and channel already exist", ErrConflict)
		}
		return nil, fmt.Errorf("create release: %w", err)
	}
	return s.loadRelease(release.ID)
}

func (s *ReleaseService) Publish(ctx context.Context, appID, releaseID string) (*database.Release, error) {
	release, err := s.findRelease(appID, releaseID)
	if err != nil {
		return nil, err
	}
	if release.Status == database.ReleaseStatusPublished {
		return release, nil
	}
	if release.Status != database.ReleaseStatusDraft {
		return nil, fmt.Errorf("%w: only draft releases can be published", ErrConflict)
	}
	app, err := s.requireApp(ctx, appID, true)
	if err != nil {
		return nil, err
	}
	if app.GetStatus() != gen.DyCustomAppStatus_DY_PRODUCTION {
		return nil, fmt.Errorf("%w: app is not in production", ErrForbidden)
	}
	if len(release.Artifacts) == 0 {
		return nil, fmt.Errorf("%w: release has no artifacts", ErrConflict)
	}
	for _, artifact := range release.Artifacts {
		metadata, err := s.artifacts.Head(ctx, artifact.ObjectKey)
		if err != nil || metadata == nil || metadata.Size < 0 || strings.TrimSpace(metadata.Hash) == "" {
			return nil, fmt.Errorf("%w: artifact %s is no longer complete", ErrConflict, artifact.ObjectKey)
		}
	}
	public := make([]string, 0, len(release.Artifacts))
	for _, artifact := range release.Artifacts {
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
	return published, nil
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
	latest, err := s.latest(appID, database.ReleaseChannelStable)
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
	if channel == "" {
		channel = string(database.ReleaseChannelStable)
	}
	if !validChannel(channel) || query.Limit < 0 || query.Offset < 0 {
		return nil, fmt.Errorf("%w: invalid release list query", ErrValidation)
	}
	limit := query.Limit
	if limit == 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var releases []*database.Release
	if err := s.db.Preload("Artifacts").Where("app_id = ? AND channel = ? AND status = ?", appID, channel, database.ReleaseStatusPublished).Find(&releases).Error; err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	platform, architecture := normalize(query.Platform), normalize(query.Architecture)
	if platform != "" && architecture != "" {
		filtered := releases[:0]
		for _, release := range releases {
			if hasArtifact(release, platform, architecture) {
				filtered = append(filtered, release)
			}
		}
		releases = filtered
	}
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
	if !validChannel(channel) || platform == "" || architecture == "" {
		return nil, fmt.Errorf("%w: invalid update query", ErrValidation)
	}
	var releases []*database.Release
	if err := s.db.Preload("Artifacts").Where("app_id = ? AND channel = ? AND status = ?", appID, channel, database.ReleaseStatusPublished).Find(&releases).Error; err != nil {
		return nil, fmt.Errorf("resolve update: %w", err)
	}
	sortReleases(releases)
	for _, release := range releases {
		if semver.Compare("v"+release.Version, "v"+query.CurrentVersion) > 0 && hasArtifact(release, platform, architecture) {
			return &UpdateResult{UpdateAvailable: true, CurrentVersion: query.CurrentVersion, Release: release}, nil
		}
	}
	return &UpdateResult{CurrentVersion: query.CurrentVersion}, nil
}

func (s *ReleaseService) GetRelease(appID, releaseID string) (*database.Release, error) {
	return s.findRelease(appID, releaseID)
}

func (s *ReleaseService) requireApp(ctx context.Context, appID string, production bool) (*gen.DyCustomApp, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
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
		return nil, fmt.Errorf("%w: app", ErrNotFound)
	}
	return app, nil
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
	query := s.db.Preload("Artifacts").Where("id = ?", id)
	if appID != "" {
		query = query.Where("app_id = ?", appID)
	}
	if err := query.First(&release).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: release", ErrNotFound)
		}
		return nil, fmt.Errorf("load release: %w", err)
	}
	return &release, nil
}

func (s *ReleaseService) latest(appID string, channel database.ReleaseChannel) (*database.Release, error) {
	var releases []*database.Release
	if err := s.db.Preload("Artifacts").Where("app_id = ? AND channel = ? AND status = ?", appID, channel, database.ReleaseStatusPublished).Find(&releases).Error; err != nil {
		return nil, fmt.Errorf("latest release: %w", err)
	}
	sortReleases(releases)
	if len(releases) == 0 {
		return nil, nil
	}
	return releases[0], nil
}

func (s *ReleaseService) publishEvent(ctx context.Context, published bool, release *database.Release) {
	if s.events == nil || release == nil {
		return
	}
	event := ReleaseEvent{EventID: uuid.NewString(), Timestamp: time.Now().UTC(), AppID: release.AppID, ReleaseID: release.ID, Version: release.Version, Channel: string(release.Channel)}
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

func validateAppID(appID string) error {
	if _, err := uuid.Parse(strings.TrimSpace(appID)); err != nil {
		return fmt.Errorf("%w: app_id must be a UUID", ErrValidation)
	}
	return nil
}

func validateReleaseFields(version, channel string) (string, database.ReleaseChannel, error) {
	version = strings.TrimSpace(version)
	if !validVersion(version) {
		return "", "", fmt.Errorf("%w: invalid version", ErrValidation)
	}
	channel = normalize(channel)
	if !validChannel(channel) {
		return "", "", fmt.Errorf("%w: unknown channel", ErrValidation)
	}
	return version, database.ReleaseChannel(channel), nil
}

func validVersion(version string) bool {
	return version != "" && !strings.HasPrefix(version, "v") && semver.IsValid("v"+version)
}

func validChannel(channel string) bool {
	switch database.ReleaseChannel(channel) {
	case database.ReleaseChannelStable, database.ReleaseChannelBeta, database.ReleaseChannelNightly:
		return true
	default:
		return false
	}
}

func normalize(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func hasArtifact(release *database.Release, platform, architecture string) bool {
	for _, artifact := range release.Artifacts {
		if artifact.Platform == platform && artifact.Architecture == architecture {
			return true
		}
	}
	return false
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
