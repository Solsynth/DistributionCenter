package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/url"
	"src.solsynth.dev/sosys/distribution/internal/database"
	gen "src.solsynth.dev/sosys/go/proto"
	"strings"
	"testing"
	"time"
)

type fakeDirectory struct {
	app      *gen.DyCustomApp
	secretOK bool
}

func (f *fakeDirectory) GetCustomApp(context.Context, string) (*gen.DyCustomApp, error) {
	return f.app, nil
}
func (f *fakeDirectory) GetAppDeveloper(context.Context, string) (*gen.DyGetAppDeveloperResponse, error) {
	return &gen.DyGetAppDeveloperResponse{Developer: &gen.DyDeveloper{PublisherName: "Example"}}, nil
}
func (f *fakeDirectory) CheckCustomAppSecret(context.Context, string, string, bool) (bool, error) {
	return f.secretOK, nil
}

type fakeArtifactStore struct {
	objects     map[string]*ArtifactMetadata
	public      map[string]bool
	setErr      error
	deleteErr   error
	failAfter   int
	setCalls    []string
	unsetCalls  []string
	deleteCalls []string
}

func (f *fakeArtifactStore) Head(_ context.Context, key string) (*ArtifactMetadata, error) {
	metadata, ok := f.objects[key]
	if !ok {
		return nil, errors.New("missing object")
	}
	return metadata, nil
}
func (f *fakeArtifactStore) PresignedUpload(context.Context, string, string, string) (*url.URL, error) {
	return url.Parse("https://s3.example.test/upload")
}
func (f *fakeArtifactStore) PresignedDownload(context.Context, string) (*url.URL, error) {
	return url.Parse("https://s3.example.test/signed-get")
}

func (f *fakeArtifactStore) SetPublic(_ context.Context, key string) error {
	f.setCalls = append(f.setCalls, key)
	if f.setErr != nil || (f.failAfter > 0 && len(f.setCalls) == f.failAfter) {
		if f.setErr != nil {
			return f.setErr
		}
		return errors.New("tagging failed")
	}
	f.public[key] = true
	return nil
}
func (f *fakeArtifactStore) UnsetPublic(_ context.Context, key string) error {
	f.unsetCalls = append(f.unsetCalls, key)
	delete(f.public, key)
	return nil
}

func (f *fakeArtifactStore) Delete(_ context.Context, key string) error {
	f.deleteCalls = append(f.deleteCalls, key)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.objects, key)
	return nil
}

func newReleaseFixture(t *testing.T) (*ReleaseService, *fakeArtifactStore, string) {
	t.Helper()
	db, err := database.OpenDSN("file:release-test-" + uuid.NewString() + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	appID := uuid.NewString()
	files := &fakeArtifactStore{objects: map[string]*ArtifactMetadata{}, public: map[string]bool{}}
	apps := &fakeDirectory{app: &gen.DyCustomApp{Id: appID, Status: gen.DyCustomAppStatus_DY_PRODUCTION}, secretOK: true}
	return NewReleaseService(db.DB, apps, files, nil), files, appID
}

func TestCompareReleaseVersionsIncludesBuildMetadata(t *testing.T) {
	if compareReleaseVersions("1.3.0+20", "1.3.0+15") <= 0 {
		t.Fatal("build 20 should sort after build 15")
	}
	if compareReleaseVersions("1.3.1+1", "1.3.0+99") <= 0 {
		t.Fatal("patch version should sort after lower patch version")
	}
}

func TestValidVersionAcceptsOpaqueIdentifiers(t *testing.T) {
	for _, version := range []string{"5299ca", "main-2026.08", "v2.0.1"} {
		if !validVersion(version) {
			t.Fatalf("validVersion(%q) = false", version)
		}
	}
	for _, version := range []string{"", " 5299ca", "-5299ca", "5299ca/", strings.Repeat("a", 129)} {
		if validVersion(version) {
			t.Fatalf("validVersion(%q) = true", version)
		}
	}
}

func TestPrepareArtifactUploadAcceptsOpaqueVersion(t *testing.T) {
	service, _, appID := newReleaseFixture(t)
	ctx := context.Background()
	upload, err := service.PrepareArtifactUpload(ctx, appID, ArtifactUploadInput{
		FileName: "daemon.tar.gz",
		MimeType: "application/gzip",
		Channel:  "rolling",
		Version:  "5299ca",
	})
	if err != nil {
		t.Fatalf("prepare opaque version upload: %v", err)
	}
	if upload.Version != "5299ca" {
		t.Fatalf("prepared upload version = %q, want 5299ca", upload.Version)
	}
}

func TestSortReleasesUsesCreationDateBeforeVersion(t *testing.T) {
	olderHigherVersion := &database.Release{
		Version:   "1.3.0+20",
		CreatedAt: time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC),
	}
	newerLowerVersion := &database.Release{
		Version:   "1.3.0+15",
		CreatedAt: time.Date(2026, time.August, 12, 11, 0, 0, 0, time.UTC),
	}
	releases := []*database.Release{olderHigherVersion, newerLowerVersion}
	sortReleases(releases)
	if releases[0] != newerLowerVersion {
		t.Fatalf("sorted releases = %#v, want newest creation date first", releases)
	}
}

func TestReleaseLifecycleAndUpdateSelection(t *testing.T) {
	service, files, appID := newReleaseFixture(t)
	key := "artifacts/" + appID + "/one/app.tar"
	files.objects[key] = &ArtifactMetadata{ObjectKey: key, FileName: "app.tar", MimeType: "application/octet-stream", Size: 12, Hash: "sha256-a"}
	ctx := context.Background()
	release, err := service.CreateRelease(ctx, appID, CreateReleaseInput{Version: "1.2.0", Channel: " stable "})
	if err != nil {
		t.Fatal(err)
	}
	if release.Status != database.ReleaseStatusDraft || len(release.Artifacts) != 0 {
		t.Fatalf("draft = %#v", release)
	}
	release, err = service.AddArtifact(ctx, appID, release.ID, ArtifactInput{ObjectKey: key, Platform: "MacOS", Architecture: "ARM64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Artifacts) != 1 || release.Artifacts[0].Hash != "sha256-a" {
		t.Fatalf("attached artifact = %#v", release.Artifacts)
	}
	if _, err := service.CreateRelease(ctx, appID, CreateReleaseInput{Version: "1.2.0", Channel: "stable"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate error = %v, want conflict", err)
	}
	published, err := service.Publish(ctx, appID, release.Version)
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.DownloadArtifact(ctx, published.Artifacts[0].ID)
	if err != nil || target.String() != "https://s3.example.test/signed-get" {
		t.Fatalf("download target = %v, error = %v", target, err)
	}
	if _, err := service.DownloadArtifact(ctx, published.Artifacts[0].ID); err != nil {
		t.Fatal(err)
	}
	counted, err := service.GetRelease(appID, published.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counted.DownloadCount != 2 || counted.Artifacts[0].DownloadCount != 2 {
		t.Fatalf("download counts = release %d, artifact %d", counted.DownloadCount, counted.Artifacts[0].DownloadCount)
	}

	if published.Status != database.ReleaseStatusPublished || len(files.setCalls) != 0 || len(files.unsetCalls) != 0 {
		t.Fatalf("published = %#v, unexpected public-tag calls = %#v/%#v", published, files.setCalls, files.unsetCalls)
	}
	edited, err := service.UpdateRelease(ctx, appID, release.ID, UpdateReleaseInput{Version: "1.2.0", Channel: "stable", ReleaseNotes: "published edits remain allowed"})
	if err != nil || edited.Status != database.ReleaseStatusPublished || edited.ReleaseNotes != "published edits remain allowed" {
		t.Fatalf("published edit = %#v, error = %v", edited, err)
	}
	update, err := service.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.1.0", Channel: "stable", Platform: "macos", Architecture: "arm64"})
	if err != nil || !update.UpdateAvailable || update.Release.Version != "1.2.0" {
		t.Fatalf("update = %#v, error = %v", update, err)
	}
	noUpdate, err := service.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.2.0", Channel: "stable", Platform: "macos", Architecture: "arm64"})
	if err != nil || noUpdate.UpdateAvailable || noUpdate.Release != nil {
		t.Fatalf("no update = %#v, error = %v", noUpdate, err)
	}
	unknownCurrent, err := service.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "3.0.0", Channel: "stable", Platform: "macos", Architecture: "arm64"})
	if err != nil || !unknownCurrent.UpdateAvailable || unknownCurrent.Release == nil || unknownCurrent.Release.Version != "1.2.0" {
		t.Fatalf("unknown current version = %#v, error = %v", unknownCurrent, err)
	}
	if _, err := service.Yank(ctx, appID, release.ID); err != nil {
		t.Fatal(err)
	}
	noUpdate, err = service.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.0.0", Channel: "stable", Platform: "macos", Architecture: "arm64"})
	if err != nil || noUpdate.UpdateAvailable {
		t.Fatalf("yanked update = %#v, error = %v", noUpdate, err)
	}
}

func TestManagedReleaseListingAndDraftDeletion(t *testing.T) {
	svc, files, appID := newReleaseFixture(t)
	ctx := context.Background()
	draft, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "1.0.0", Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	public, err := svc.ListReleases(ctx, appID, ReleaseListQuery{Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if public.Total != 0 {
		t.Fatalf("public release list = %#v, want no drafts", public)
	}
	managed, err := svc.ListManagedReleases(ctx, appID, ReleaseListQuery{Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if managed.Total != 1 || managed.Data[0].Status != database.ReleaseStatusDraft {
		t.Fatalf("managed release list = %#v, want one draft", managed)
	}
	if err := svc.DeleteRelease(ctx, appID, draft.ID); err != nil {
		t.Fatal(err)
	}
	managed, err = svc.ListManagedReleases(ctx, appID, ReleaseListQuery{Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if managed.Total != 0 {
		t.Fatalf("managed release list after deletion = %#v, want empty", managed)
	}

	key := "artifacts/" + appID + "/published/app.tar"
	files.objects[key] = &ArtifactMetadata{ObjectKey: key, FileName: "app.tar", Size: 1, Hash: "hash"}
	published, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "2.0.0", Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddArtifact(ctx, appID, published.ID, ArtifactInput{ObjectKey: key, Platform: "macos", Architecture: "arm64"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(ctx, appID, published.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteRelease(ctx, appID, published.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete published release error = %v, want conflict", err)
	}
}

func TestChannelsAndDeveloperSelectedTargets(t *testing.T) {
	svc, files, appID := newReleaseFixture(t)
	mac := "artifacts/" + appID + "/mac/app.tar"
	windows := "artifacts/" + appID + "/windows/app.exe"
	files.objects[mac] = &ArtifactMetadata{ObjectKey: mac, FileName: "app.tar", Size: 10, Hash: "mac-hash"}
	files.objects[windows] = &ArtifactMetadata{ObjectKey: windows, FileName: "app.exe", Size: 20, Hash: "windows-hash"}
	ctx := context.Background()
	stable, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "1.0.0", Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	stable, err = svc.AddArtifact(ctx, appID, stable.ID, ArtifactInput{ObjectKey: mac, Platform: "macos", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "2.0.0", Channel: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	beta, err = svc.AddArtifact(ctx, appID, beta.ID, ArtifactInput{ObjectKey: windows, Platform: "windows", Architecture: "x86_64"})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "3.0.0", Channel: "nightly"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddArtifact(ctx, appID, duplicate.ID, ArtifactInput{ObjectKey: mac, Platform: "macos", Architecture: "arm64"}); err != nil {
		t.Fatal(err)
	}
	overridden, err := svc.AddArtifact(ctx, appID, duplicate.ID, ArtifactInput{ObjectKey: windows, Platform: "macos", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(overridden.Artifacts) != 1 || overridden.Artifacts[0].ObjectKey != windows {
		t.Fatalf("overridden artifact = %#v, want single artifact with object_key %q", overridden.Artifacts, windows)
	}
	if overridden.Artifacts[0].ID == "" {
		t.Fatalf("overridden artifact missing ID")
	}
	if _, err := svc.Publish(ctx, appID, stable.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(ctx, appID, beta.ID); err != nil {
		t.Fatal(err)
	}
	betaUpdate, err := svc.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.0.0", Channel: "beta", Platform: "windows", Architecture: "x86_64"})
	if err != nil || !betaUpdate.UpdateAvailable || betaUpdate.Release.Version != "2.0.0" {
		t.Fatalf("beta update = %#v, error = %v", betaUpdate, err)
	}
	stableOnWindows, err := svc.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.0.0", Channel: "stable", Platform: "windows", Architecture: "x86_64"})
	if err != nil || stableOnWindows.UpdateAvailable {
		t.Fatalf("wrong target update = %#v, error = %v", stableOnWindows, err)
	}
	if _, err := svc.ListReleases(ctx, appID, ReleaseListQuery{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing list channel error = %v, want validation", err)
	}
	betaList, err := svc.ListReleases(ctx, appID, ReleaseListQuery{Channel: "beta", Platform: "windows", Architecture: "x86_64"})
	if err != nil || betaList.Total != 1 || betaList.Data[0].Channel != database.ReleaseChannelBeta {
		t.Fatalf("beta list = %#v, error = %v", betaList, err)
	}
}

func TestPublishDoesNotTagPublicObjects(t *testing.T) {
	service, files, appID := newReleaseFixture(t)
	first := "artifacts/" + appID + "/one/app.tar"
	second := "artifacts/" + appID + "/two/app.tar"
	files.objects[first] = &ArtifactMetadata{ObjectKey: first, FileName: "app.tar", Size: 1, Hash: "a"}
	files.objects[second] = &ArtifactMetadata{ObjectKey: second, FileName: "app.tar", Size: 2, Hash: "b"}
	release, err := service.CreateRelease(context.Background(), appID, CreateReleaseInput{Version: "1.0.0", Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	release, err = service.AddArtifact(context.Background(), appID, release.ID, ArtifactInput{ObjectKey: first, Platform: "macos", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	release, err = service.AddArtifact(context.Background(), appID, release.ID, ArtifactInput{ObjectKey: second, Platform: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	files.failAfter = 1
	if _, err := service.Publish(context.Background(), appID, release.ID); err != nil {
		t.Fatalf("publish error = %v", err)
	}
	if len(files.setCalls) != 0 || len(files.unsetCalls) != 0 {
		t.Fatalf("unexpected public-tag calls = %#v/%#v", files.setCalls, files.unsetCalls)
	}
}

func TestPublishCleansArtifactsOutsideRetentionWindow(t *testing.T) {
	svc, files, appID := newReleaseFixture(t)
	svc.ConfigureArtifactRetention(2)
	ctx := context.Background()
	versions := []string{"1.0.0", "2.0.0", "3.0.0"}
	keys := make([]string, 0, len(versions))
	var firstReleaseID string
	for index, version := range versions {
		key := "artifacts/" + appID + "/" + version + "/app.tar"
		keys = append(keys, key)
		files.objects[key] = &ArtifactMetadata{ObjectKey: key, FileName: "app.tar", Size: int64(index + 1), Hash: version}
		release, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: version, Channel: "stable"})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			firstReleaseID = release.ID
		}
		release, err = svc.AddArtifact(ctx, appID, release.ID, ArtifactInput{ObjectKey: key, Platform: "macos", Architecture: "arm64"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Publish(ctx, appID, release.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(files.deleteCalls) != 1 || files.deleteCalls[0] != keys[0] {
		t.Fatalf("deleted artifacts = %#v, want %#v", files.deleteCalls, []string{keys[0]})
	}
	if _, ok := files.objects[keys[0]]; ok {
		t.Fatalf("expired artifact %q remains in store", keys[0])
	}
	expired, err := svc.GetRelease(appID, firstReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired.Artifacts) != 1 || expired.Artifacts[0].ExpiredAt == nil {
		t.Fatalf("expired release artifact = %#v", expired.Artifacts)
	}
	update, err := svc.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "0.0.0", Channel: "stable", Platform: "macos", Architecture: "arm64"})
	if err != nil || !update.UpdateAvailable || update.Release.Version != versions[2] {
		t.Fatalf("update after cleanup = %#v, error = %v", update, err)
	}
}

func TestUpdateTelemetryFallsBackToClientIP(t *testing.T) {
	svc, files, appID := newReleaseFixture(t)
	ctx := context.Background()
	key := "artifacts/" + appID + "/ip/app.tar"
	files.objects[key] = &ArtifactMetadata{ObjectKey: key, FileName: "app.tar", MimeType: "application/octet-stream", Size: 8, Hash: "sha256-ip"}
	release, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "3.0.0", Channels: []string{"stable"}, Title: "IP telemetry"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddArtifact(ctx, appID, release.ID, ArtifactInput{ObjectKey: key, Platform: "macos", Architecture: "arm64"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(ctx, appID, release.ID); err != nil {
		t.Fatal(err)
	}
	// No installation id: the client IP must drive the visitor identity.
	for i := 0; i < 2; i++ {
		update, err := svc.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.0.0", Channel: "stable", Platform: "macos", Architecture: "arm64", ClientIP: "203.0.113.42"})
		if err != nil || !update.UpdateAvailable {
			t.Fatalf("update = %#v, error = %v", update, err)
		}
	}
	// A non-UUID installation id falls back to the same IP identity.
	if _, err := svc.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.0.0", Channel: "stable", Platform: "macos", Architecture: "arm64", InstallationID: "not-a-uuid", ClientIP: "203.0.113.42"}); err != nil {
		t.Fatal(err)
	}
	// A different client IP is a distinct visitor.
	if _, err := svc.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.0.0", Channel: "stable", Platform: "macos", Architecture: "arm64", ClientIP: "198.51.100.7"}); err != nil {
		t.Fatal(err)
	}
	metrics, err := svc.UsageMetrics(ctx, appID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Checks != 4 || metrics.DAU != 2 || metrics.MAU != 2 || metrics.ByVersion["1.0.0"] != 4 {
		t.Fatalf("metrics = %#v", metrics)
	}
}
