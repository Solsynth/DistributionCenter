package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMultiChannelReleaseAndUpdateTelemetry(t *testing.T) {
	svc, files, appID := newReleaseFixture(t)
	ctx := context.Background()
	channel, err := svc.CreateChannel(ctx, appID, CreateChannelInput{Name: "experimental", DisplayName: "Experimental", Descriptions: map[string]string{"en-US": "Experimental builds", "zh-CN": "实验版本"}})
	if err != nil {
		t.Fatal(err)
	}
	if channel.Descriptions["zh-CN"] != "实验版本" {
		t.Fatalf("channel metadata = %#v", channel)
	}
	key := "artifacts/" + appID + "/multi/app.tar"
	files.objects[key] = &ArtifactMetadata{ObjectKey: key, FileName: "app.tar", MimeType: "application/octet-stream", Size: 8, Hash: "sha256-multi"}
	release, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "2.0.0", Channels: []string{"stable", "experimental"}, Descriptions: map[string]string{"en-US": "A new release", "zh-CN": "新版本"}, Attachments: []string{"file:release-banner.png", "https://cdn.example/notes.pdf"}, Artifacts: []ArtifactInput{{ObjectKey: key, Platform: "macos", Architecture: "arm64"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Channels) != 2 || release.Descriptions["zh-CN"] != "新版本" || len(release.Attachments) != 2 {
		t.Fatalf("release metadata = %#v", release)
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
