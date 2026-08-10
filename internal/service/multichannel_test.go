package service

import (
	"context"
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
	release, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "2.0.0", Channels: []string{"stable", "experimental"}, Descriptions: map[string]string{"en-US": "A new release", "zh-CN": "新版本"}, Attachments: database.CloudFileReferenceList{{Id: "release-banner", Name: "Release banner", MimeType: "image/png", Size: 12}, {Url: "https://cdn.example/notes.pdf", Name: "Release notes", MimeType: "application/pdf"}}, Artifacts: []ArtifactInput{{ObjectKey: key, Platform: "macos", Architecture: "arm64"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Channels) != 2 || release.Descriptions["zh-CN"] != "新版本" || len(release.Attachments) != 2 {
		t.Fatalf("release metadata = %#v", release)
	}
	release, err = svc.UpdateRelease(ctx, appID, release.ID, UpdateReleaseInput{
		Version: "2.1.0", Channels: []string{"stable", "experimental"},
		ReleaseNotes: "Updated release", Descriptions: map[string]string{"en-US": "Updated release", "zh-CN": "更新版本"},
	})
	if err != nil || release.Version != "2.1.0" || release.Descriptions["zh-CN"] != "更新版本" {
		t.Fatalf("updated release metadata = %#v, error = %v", release, err)
	}
	var localizationCount int64
	countErr := svc.db.Model(&database.Localization{}).Where("resource_id IN ?", []string{channel.ID, release.ID}).Count(&localizationCount).Error
	if countErr != nil || localizationCount != 6 {
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
	if metrics.Checks != 1 || metrics.DAU != 1 || metrics.MAU != 1 || metrics.ByChannel["experimental"] != 1 || metrics.ByLocale["zh-CN"] != 1 {
		t.Fatalf("metrics = %#v", metrics)
	}
}
