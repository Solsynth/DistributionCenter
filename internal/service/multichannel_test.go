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
	if _, err := svc.CreateChannel(ctx, appID, CreateChannelInput{Name: "experimental", DisplayName: "Experimental"}); err != nil {
		t.Fatal(err)
	}
	key := "artifacts/" + appID + "/multi/app.tar"
	files.objects[key] = &ArtifactMetadata{ObjectKey: key, FileName: "app.tar", MimeType: "application/octet-stream", Size: 8, Hash: "sha256-multi"}
	release, err := svc.CreateRelease(ctx, appID, CreateReleaseInput{Version: "2.0.0", Channels: []string{"stable", "experimental"}, Artifacts: []ArtifactInput{{ObjectKey: key, Platform: "macos", Architecture: "arm64"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Channels) != 2 {
		t.Fatalf("channels = %#v", release.Channels)
	}
	if _, err := svc.Publish(ctx, appID, release.ID); err != nil {
		t.Fatal(err)
	}
	installationID := uuid.NewString()
	update, err := svc.ResolveUpdate(ctx, appID, UpdateQuery{CurrentVersion: "1.0.0", Channel: "experimental", Platform: "macos", Architecture: "arm64", InstallationID: installationID, OSVersion: "14.0", ClientVersion: "1.5.0"})
	if err != nil || !update.UpdateAvailable || update.Release.ID != release.ID {
		t.Fatalf("update = %#v, error = %v", update, err)
	}
	metrics, err := svc.UsageMetrics(ctx, appID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Checks != 1 || metrics.DAU != 1 || metrics.MAU != 1 || metrics.ByChannel["experimental"] != 1 {
		t.Fatalf("metrics = %#v", metrics)
	}
}
