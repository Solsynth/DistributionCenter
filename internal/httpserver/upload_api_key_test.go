package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/distribution/internal/config"
	"src.solsynth.dev/sosys/distribution/internal/database"
	"src.solsynth.dev/sosys/distribution/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"
)

type uploadTestDirectory struct {
	accountID  string
	publisher  *gen.DyPublisher
	validToken string
}

func (d *uploadTestDirectory) Authenticate(_ context.Context, token string) (string, error) {
	if token != d.validToken {
		return "", service.ErrUnauthorized
	}
	return d.accountID, nil
}
func (d *uploadTestDirectory) GetPublisher(context.Context, string) (*gen.DyPublisher, error) {
	return d.publisher, nil
}
func (d *uploadTestDirectory) IsPublisherMember(context.Context, string, string, gen.DyPublisherMemberRole) (bool, error) {
	return true, nil
}

type uploadTestStore struct{}

func (uploadTestStore) Head(context.Context, string) (*service.ArtifactMetadata, error) {
	return &service.ArtifactMetadata{Size: 1, Hash: "hash"}, nil
}
func (uploadTestStore) PresignedUpload(context.Context, string, string, string) (*url.URL, error) {
	return url.Parse("https://s3.example.test/upload")
}
func (uploadTestStore) SetPublic(context.Context, string) error   { return nil }
func (uploadTestStore) UnsetPublic(context.Context, string) error { return nil }
func (uploadTestStore) PublicURL(string) string                   { return "https://cdn.example.test/artifact" }
func (uploadTestStore) PresignedDownload(context.Context, string) (*url.URL, error) {
	return url.Parse("https://s3.example.test/signed-get")
}

type signedOnlyStore struct{ uploadTestStore }

func (signedOnlyStore) PublicURL(string) string { return "" }

func TestUploadAPIKeyRoutesScopeArtifactUpload(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:http-upload-key-test-"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Product{}, &database.UploadAPIKey{}, &database.Channel{}, &database.Release{}, &database.ReleaseArtifact{}, &database.ClientCheck{}, &database.Localization{}); err != nil {
		t.Fatal(err)
	}
	publisherID, productID := uuid.NewString(), uuid.NewString()
	if err := db.Create(&database.Product{ID: productID, PublisherID: publisherID, Slug: "desktop", Name: "Desktop"}).Error; err != nil {
		t.Fatal(err)
	}
	directory := &uploadTestDirectory{accountID: uuid.NewString(), publisher: &gen.DyPublisher{Id: publisherID, Name: "Example"}, validToken: "sphere-token"}
	releases := service.NewPublisherReleaseService(db, directory, uploadTestStore{}, nil)
	server := New(config.Default())
	RegisterPublisherRoutes(server.Engine, releases, directory, config.Default())

	request := httptest.NewRequest(http.MethodPost, "/api/products/"+productID+"/upload-api-keys", strings.NewReader(`{"name":"CI"}`))
	request.Header.Set("Authorization", "Bearer sphere-token")
	response := httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create key status = %d, body = %s", response.Code, response.Body.String())
	}
	var created service.CreatedUploadAPIKey
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Key == "" || !strings.HasPrefix(created.Key, "dcu_") {
		t.Fatalf("created key = %#v", created)
	}

	releaseRequest := httptest.NewRequest(http.MethodPost, "/api/products/"+productID+"/releases", strings.NewReader(`{"version":"1.0.0","channels":["stable"]}`))
	releaseRequest.Header.Set("Authorization", "Bearer sphere-token")
	releaseResponse := httptest.NewRecorder()
	server.Engine.ServeHTTP(releaseResponse, releaseRequest)
	if releaseResponse.Code != http.StatusCreated {
		t.Fatalf("create release status = %d, body = %s", releaseResponse.Code, releaseResponse.Body.String())
	}
	var release struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(releaseResponse.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	if release.ID == "" {
		t.Fatal("created release ID is empty")
	}
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/products/"+productID+"/artifacts/upload-url", strings.NewReader(`{"file_name":"desktop.tar.gz","mime_type":"application/gzip","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","channel":"beta","version":"2.0.0"}`))
	uploadRequest.Header.Set("Authorization", "Bearer "+created.Key)
	uploadResponse := httptest.NewRecorder()
	server.Engine.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusOK || !strings.Contains(uploadResponse.Body.String(), `"upload_url":"https://s3.example.test/upload"`) || !strings.Contains(uploadResponse.Body.String(), `"release_id"`) {
		t.Fatalf("key upload status = %d, body = %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var prepared service.ArtifactUpload
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.ReleaseID == "" || prepared.Version != "2.0.0" {
		t.Fatalf("prepared release = %#v", prepared)
	}
	var preparedRelease database.Release
	if err := db.Preload("Channels").Where("id = ?", prepared.ReleaseID).First(&preparedRelease).Error; err != nil {
		t.Fatal(err)
	}
	if len(preparedRelease.Channels) != 1 || preparedRelease.Channels[0].Name != "beta" {
		t.Fatalf("prepared release channels = %#v", preparedRelease.Channels)
	}
	cookieUploadRequest := httptest.NewRequest(http.MethodPost, "/api/products/"+productID+"/artifacts/upload-url", strings.NewReader(`{"file_name":"desktop.tar.gz","mime_type":"application/gzip","version":"3.0.0"}`))
	cookieUploadRequest.AddCookie(&http.Cookie{Name: "AuthToken", Value: "sphere-token"})
	cookieUploadResponse := httptest.NewRecorder()
	server.Engine.ServeHTTP(cookieUploadResponse, cookieUploadRequest)
	if cookieUploadResponse.Code != http.StatusOK || !strings.Contains(cookieUploadResponse.Body.String(), `"upload_url":"https://s3.example.test/upload"`) {
		t.Fatalf("cookie upload status = %d, body = %s", cookieUploadResponse.Code, cookieUploadResponse.Body.String())
	}

	attachRequest := httptest.NewRequest(http.MethodPost, "/api/products/"+productID+"/releases/2.0.0/artifacts", strings.NewReader(`{"object_key":"`+prepared.ObjectKey+`","platform":"macos","architecture":"arm64"}`))
	attachRequest.Header.Set("Authorization", "Bearer "+created.Key)
	attachResponse := httptest.NewRecorder()
	server.Engine.ServeHTTP(attachResponse, attachRequest)
	if attachResponse.Code != http.StatusCreated {
		t.Fatalf("version attach status = %d, body = %s", attachResponse.Code, attachResponse.Body.String())
	}

	revokeRequest := httptest.NewRequest(http.MethodDelete, "/api/products/"+productID+"/upload-api-keys/"+created.ID, nil)
	revokeRequest.Header.Set("Authorization", "Bearer sphere-token")
	revokeResponse := httptest.NewRecorder()
	server.Engine.ServeHTTP(revokeResponse, revokeRequest)
	if revokeResponse.Code != http.StatusNoContent {
		t.Fatalf("revoke key status = %d, body = %s", revokeResponse.Code, revokeResponse.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/products/"+productID+"/artifacts/upload-url", strings.NewReader(`{"file_name":"desktop.tar.gz"}`))
	request.Header.Set("Authorization", "Bearer "+created.Key)
	response = httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key status = %d, body = %s", response.Code, response.Body.String())
	}
	_ = release
}

func TestReleaseViewUsesDirectAndSignedArtifactURLs(t *testing.T) {
	now := time.Now().UTC()
	release := &database.Release{
		ID:          uuid.NewString(),
		AppID:       uuid.NewString(),
		Version:     "1.0.0",
		Status:      database.ReleaseStatusPublished,
		PublishedAt: &now,
		Artifacts: []database.ReleaseArtifact{
			{ID: uuid.NewString(), ObjectKey: "artifacts/app/object.tar.gz", Platform: "linux", Architecture: "x86_64"},
			{ID: uuid.NewString(), DownloadURL: "https://downloads.example.test/object.tar.gz", Platform: "macos", Architecture: "arm64"},
		},
	}
	view := releaseView(release, signedOnlyStore{})
	if view.Artifacts[0].DownloadURL != "https://s3.example.test/signed-get" {
		t.Fatalf("signed artifact URL = %q", view.Artifacts[0].DownloadURL)
	}
	if view.Artifacts[1].DownloadURL != "https://downloads.example.test/object.tar.gz" {
		t.Fatalf("direct artifact URL = %q", view.Artifacts[1].DownloadURL)
	}
}

func TestReleaseViewReportsExpiredArtifacts(t *testing.T) {
	expiredAt := time.Now().UTC()
	release := &database.Release{
		ID:     uuid.NewString(),
		AppID:  uuid.NewString(),
		Status: database.ReleaseStatusPublished,
		Artifacts: []database.ReleaseArtifact{{
			ID:        uuid.NewString(),
			ObjectKey: "artifacts/app/expired.tar.gz",
			ExpiredAt: &expiredAt,
		}},
	}
	view := releaseView(release, signedOnlyStore{})
	if len(view.Artifacts) != 1 || !view.Artifacts[0].Expired || view.Artifacts[0].DownloadURL != "" {
		t.Fatalf("expired artifact view = %#v", view.Artifacts)
	}
}
