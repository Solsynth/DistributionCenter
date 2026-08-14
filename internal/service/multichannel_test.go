package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"src.solsynth.dev/sosys/distribution/internal/database"
)

func TestMultiChannelReleaseAndUpdateTelemetry(t *testing.T) {
	svc, files, appID := newReleaseFixture(t)
	ctx := context.Background()
	channel, err := svc.CreateChannel(ctx, appID, CreateChannelInput{
		Name: "experimental", DisplayName: "Experimental",
		DisplayNames: map[string]string{"en-US": "Experimental", "zh-CN": "实验版本"},
		Descriptions: map[string]string{"en-US": "Experimental builds", "zh-CN": "实验版本"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if channel.DisplayNames["zh-CN"] != "实验版本" || channel.Descriptions["zh-CN"] != "实验版本" {
		t.Fatalf("channel metadata = %#v", channel)
	}
	channels, err := svc.ListChannels(ctx, appID)
	var listedChannel *database.Channel
	for _, item := range channels {
		if item.Channel.ID == channel.ID {
			listedChannel = item.Channel
			break
		}
	}
	if err != nil || listedChannel == nil || listedChannel.DisplayNames["zh-CN"] != "实验版本" {
		t.Fatalf("listed channel metadata = %#v, error = %v", channels, err)
	}
	updatedChannel, err := svc.UpdateChannel(ctx, appID, channel.ID, UpdateChannelInput{
		DisplayName: "Experimental", DisplayNames: map[string]string{"en-US": "Experimental", "zh-CN": "实验渠道"},
		Description: "Experimental builds", Descriptions: map[string]string{"en-US": "Experimental builds", "zh-CN": "实验渠道"},
	})
	if err != nil || updatedChannel.DisplayNames["zh-CN"] != "实验渠道" {
		t.Fatalf("updated channel metadata = %#v, error = %v", updatedChannel, err)
	}
	key := "artifacts/" + appID + "/multi/app.tar"
	files.objects[key] = &ArtifactMetadata{ObjectKey: key, FileName: "app.tar", MimeType: "application/octet-stream", Size: 8, Hash: "sha256-multi"}
	release, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "2.0.0", Channels: []string{"stable", "experimental"}, Title: "New release", Titles: map[string]string{"en-US": "New release", "zh-CN": "新版本"}, Metadata: database.JSONMap{"minimum_os": "13.0"}, ForceUpdate: true, Descriptions: map[string]string{"en-US": "A new release", "zh-CN": "新版本"}, Attachments: database.CloudFileReferenceList{{Id: "release-banner", Name: "Release banner", MimeType: "image/png", Size: 12}, {Url: "https://cdn.example/notes.pdf", Name: "Release notes", MimeType: "application/pdf"}}})
	if err != nil {
		t.Fatal(err)
	}
	release, err = svc.AddArtifact(ctx, appID, release.ID, ArtifactInput{ObjectKey: key, Platform: "macos", Architecture: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Channels) != 2 || release.Title != "New release" || release.Titles["zh-CN"] != "新版本" || release.Descriptions["zh-CN"] != "新版本" || len(release.Attachments) != 2 || release.Metadata["minimum_os"] != "13.0" || !release.ForceUpdate {
		t.Fatalf("release metadata = %#v", release)
	}
	release, err = svc.UpdateRelease(ctx, appID, release.ID, UpdateReleaseInput{
		Version: "2.1.0", Channels: []string{"stable", "experimental"}, Title: "Updated release",
		Titles:       map[string]string{"en-US": "Updated release", "zh-CN": "更新版本"},
		ReleaseNotes: "Updated release", Descriptions: map[string]string{"en-US": "Updated release", "zh-CN": "更新版本"},
	})
	if err != nil || release.Version != "2.1.0" || release.Title != "Updated release" || release.Titles["zh-CN"] != "更新版本" || release.Descriptions["zh-CN"] != "更新版本" {
		t.Fatalf("updated release metadata = %#v, error = %v", release, err)
	}
	var localizationCount int64
	countErr := svc.db.Model(&database.Localization{}).Where("resource_id IN ?", []string{channel.ID, release.ID}).Count(&localizationCount).Error
	if countErr != nil || localizationCount != 8 {
		t.Fatalf("localization rows = %d, error = %+v", localizationCount, countErr)
	}
	if _, err := svc.Publish(ctx, appID, release.ID); err != nil {
		t.Fatal(err)
	}
	installationID := uuid.NewString()
	update, err := svc.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.0.0", Channel: "experimental", Platform: "macos", Architecture: "arm64", InstallationID: installationID, Locale: "zh_CN", OSVersion: "14.0", ClientVersion: "1.5.0"})
	if err != nil || !update.UpdateAvailable || update.Release.ID != release.ID {
		t.Fatalf("update = %#v, error = %v", update, err)
	}
	metrics, err := svc.UsageMetrics(ctx, appID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Checks != 1 || metrics.DAU != 1 || metrics.MAU != 1 || metrics.ByVersion["1.0.0"] != 1 || metrics.ByChannel["experimental"] != 1 || metrics.ByLocale["zh-CN"] != 1 || metrics.ByOSVersion["14.0"] != 1 || metrics.ByClientVersion["1.5.0"] != 1 {
		t.Fatalf("metrics = %#v", metrics)
	}
}
func TestDeleteCustomChannelDetachesReleases(t *testing.T) {
	svc, _, appID := newReleaseFixture(t)
	ctx := context.Background()
	custom, err := svc.CreateChannel(ctx, appID, CreateChannelInput{Name: "experimental", DisplayName: "Experimental"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "1.0.0", Channels: []string{"stable", "experimental"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteChannel(ctx, appID, custom.ID); err != nil {
		t.Fatal(err)
	}
	var attached int64
	if err := svc.db.Table("release_channels").Where("channel_id = ?", custom.ID).Count(&attached).Error; err != nil {
		t.Fatal(err)
	}
	if attached != 0 {
		t.Fatalf("release associations = %d, want 0", attached)
	}
	if err := svc.DeleteChannel(ctx, appID, custom.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted channel error = %v, want not found", err)
	}
	stable, err := svc.channelForApp(appID, "stable")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteChannel(ctx, appID, stable.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("built-in channel deletion error = %v, want conflict", err)
	}
}

func TestChannelArtifactRetentionValidation(t *testing.T) {
	svc, _, appID := newReleaseFixture(t)
	svc.ConfigureArtifactRetention(3)
	ctx := context.Background()

	tooHigh := 4
	if _, err := svc.CreateChannel(ctx, appID, CreateChannelInput{Name: "preview", ArtifactRetention: &tooHigh}); !errors.Is(err, ErrValidation) {
		t.Fatalf("retention above platform limit error = %v, want validation", err)
	}
	negative := -1
	if _, err := svc.CreateChannel(ctx, appID, CreateChannelInput{Name: "preview", ArtifactRetention: &negative}); !errors.Is(err, ErrValidation) {
		t.Fatalf("negative retention error = %v, want validation", err)
	}
	disabled := 0
	channel, err := svc.CreateChannel(ctx, appID, CreateChannelInput{Name: "preview", ArtifactRetention: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if channel.ArtifactRetention == nil || *channel.ArtifactRetention != 0 {
		t.Fatalf("channel retention = %#v, want explicit zero", channel.ArtifactRetention)
	}
	if _, err := svc.UpdateChannel(ctx, appID, channel.ID, UpdateChannelInput{ArtifactRetention: &tooHigh}); !errors.Is(err, ErrValidation) {
		t.Fatalf("updated retention above platform limit error = %v, want validation", err)
	}
}

func TestChannelArtifactRetentionControlsCleanup(t *testing.T) {
	svc, files, appID := newReleaseFixture(t)
	svc.ConfigureArtifactRetention(3)
	ctx := context.Background()
	one := 1
	channel, err := svc.CreateChannel(ctx, appID, CreateChannelInput{Name: "preview", ArtifactRetention: &one})
	if err != nil {
		t.Fatal(err)
	}

	for index, version := range []string{"1.0.0", "2.0.0"} {
		key := "artifacts/" + appID + "/" + version + "/preview.tar"
		files.objects[key] = &ArtifactMetadata{ObjectKey: key, FileName: "preview.tar", Size: int64(index + 1), Hash: version}
		release, createErr := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: version, Channels: []string{channel.Name}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		release, createErr = svc.AddArtifact(ctx, appID, release.ID, ArtifactInput{ObjectKey: key, Platform: "macos", Architecture: "arm64"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = svc.Publish(ctx, appID, release.ID); createErr != nil {
			t.Fatal(createErr)
		}
	}
	if len(files.deleteCalls) != 1 || files.deleteCalls[0] != "artifacts/"+appID+"/1.0.0/preview.tar" {
		t.Fatalf("deleted artifacts = %#v, want oldest preview artifact", files.deleteCalls)
	}

	disabled := 0
	if _, err := svc.UpdateChannel(ctx, appID, channel.ID, UpdateChannelInput{ArtifactRetention: &disabled}); err != nil {
		t.Fatal(err)
	}
	for index, version := range []string{"3.0.0", "4.0.0"} {
		key := "artifacts/" + appID + "/" + version + "/preview.tar"
		files.objects[key] = &ArtifactMetadata{ObjectKey: key, FileName: "preview.tar", Size: int64(index + 3), Hash: version}
		release, createErr := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: version, Channels: []string{channel.Name}})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = svc.AddArtifact(ctx, appID, release.ID, ArtifactInput{ObjectKey: key, Platform: "macos", Architecture: "arm64"}); createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = svc.Publish(ctx, appID, release.ID); createErr != nil {
			t.Fatal(createErr)
		}
	}
	if len(files.deleteCalls) != 1 {
		t.Fatalf("cleanup with channel retention disabled deleted = %#v", files.deleteCalls)
	}
}
