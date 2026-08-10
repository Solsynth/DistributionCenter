package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
	gen "src.solsynth.dev/sosys/go/proto"

	"src.solsynth.dev/sosys/distribution/internal/config"
	"src.solsynth.dev/sosys/distribution/internal/database"
	"src.solsynth.dev/sosys/distribution/internal/service"
)

type marketplaceDirectory struct {
	app      *gen.DyCustomApp
	secretOK bool
}

func (d *marketplaceDirectory) GetCustomApp(context.Context, string) (*gen.DyCustomApp, error) {
	return d.app, nil
}
func (d *marketplaceDirectory) GetAppDeveloper(context.Context, string) (*gen.DyGetAppDeveloperResponse, error) {
	return &gen.DyGetAppDeveloperResponse{Developer: &gen.DyDeveloper{PublisherName: "Example"}}, nil
}
func (d *marketplaceDirectory) CheckCustomAppSecret(context.Context, string, string, bool) (bool, error) {
	return d.secretOK, nil
}

type marketplaceStore struct {
	objects map[string]*service.ArtifactMetadata
	public  map[string]bool
}

func (s *marketplaceStore) Head(_ context.Context, key string) (*service.ArtifactMetadata, error) {
	metadata, ok := s.objects[key]
	if !ok {
		return nil, errors.New("missing")
	}
	return metadata, nil
}
func (s *marketplaceStore) PresignedUpload(context.Context, string, string) (*url.URL, error) {
	return url.Parse("https://s3.test/upload")
}
func (s *marketplaceStore) SetPublic(_ context.Context, key string) error {
	s.public[key] = true
	return nil
}
func (s *marketplaceStore) UnsetPublic(_ context.Context, key string) error {
	delete(s.public, key)
	return nil
}
func (s *marketplaceStore) PublicURL(key string) string { return "https://cdn.test/" + key }

func newMarketplaceServer(t *testing.T) (*Server, *marketplaceDirectory, *marketplaceStore, string) {
	t.Helper()
	db, err := database.OpenDSN("file:http-test-" + uuid.NewString() + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	appID := uuid.NewString()
	directory := &marketplaceDirectory{app: &gen.DyCustomApp{Id: appID, Status: gen.DyCustomAppStatus_DY_PRODUCTION}, secretOK: true}
	store := &marketplaceStore{objects: map[string]*service.ArtifactMetadata{}, public: map[string]bool{}}
	releases := service.NewReleaseService(db.DB, directory, store, nil)
	server := New(config.Default())
	RegisterRoutes(server.Engine, releases, directory, config.Default())
	return server, directory, store, appID
}

func request(server *Server, method, target, body, authorization string) *httptest.ResponseRecorder {
	var payload *bytes.Reader
	if body == "" {
		payload = bytes.NewReader(nil)
	} else {
		payload = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, target, payload)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	server.Engine.ServeHTTP(response, req)
	return response
}

func TestMarketplaceDraftPublishUpdateFlow(t *testing.T) {
	server, _, store, appID := newMarketplaceServer(t)
	initial := request(server, http.MethodGet, "/api/v1/apps/"+appID, "", "")
	if initial.Code != http.StatusOK || !bytes.Contains(initial.Body.Bytes(), []byte(`"latest":null`)) {
		t.Fatalf("initial app status = %d, body = %s", initial.Code, initial.Body.String())
	}
	objectKey := "artifacts/" + appID + "/one/app.tar"
	store.objects[objectKey] = &service.ArtifactMetadata{ObjectKey: objectKey, FileName: "app.tar", MimeType: "application/octet-stream", Size: 5, Hash: "sha256-a"}
	body := `{"version":"1.2.0","channel":"stable","artifacts":[{"object_key":"` + objectKey + `","platform":"macos","architecture":"arm64"}]}`
	created := request(server, http.MethodPost, "/api/v1/apps/"+appID+"/releases", body, "Bearer secret")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var draft ReleaseView
	if err := json.Unmarshal(created.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Status != string(database.ReleaseStatusDraft) || draft.Artifacts[0].DownloadURL != "" {
		t.Fatalf("draft = %#v", draft)
	}
	published := request(server, http.MethodPost, "/api/v1/apps/"+appID+"/releases/"+draft.ID+"/publish", "", "Bearer secret")
	if published.Code != http.StatusOK {
		t.Fatalf("publish status = %d, body = %s", published.Code, published.Body.String())
	}
	var release ReleaseView
	if err := json.Unmarshal(published.Body.Bytes(), &release); err != nil {
		t.Fatal(err)
	}
	missingChannel := request(server, http.MethodGet, "/api/v1/apps/"+appID+"/releases", "", "")
	if missingChannel.Code != http.StatusBadRequest {
		t.Fatalf("missing list channel status = %d, body = %s", missingChannel.Code, missingChannel.Body.String())
	}
	stableList := request(server, http.MethodGet, "/api/v1/apps/"+appID+"/releases?channel=stable&platform=macos&architecture=arm64", "", "")
	if stableList.Code != http.StatusOK || !bytes.Contains(stableList.Body.Bytes(), []byte(`"total":1`)) {
		t.Fatalf("stable list status = %d, body = %s", stableList.Code, stableList.Body.String())
	}
	if release.Status != string(database.ReleaseStatusPublished) || release.Artifacts[0].DownloadURL != "https://cdn.test/"+objectKey {
		t.Fatalf("published = %#v", release)
	}
	update := request(server, http.MethodGet, "/api/v1/apps/"+appID+"/update?current_version=1.1.0&channel=stable&platform=macos&architecture=arm64", "", "")
	if update.Code != http.StatusOK || !bytes.Contains(update.Body.Bytes(), []byte(`"update_available":true`)) || !bytes.Contains(update.Body.Bytes(), []byte(`"version":"1.2.0"`)) {
		t.Fatalf("update status = %d, body = %s", update.Code, update.Body.String())
	}
	noUpdate := request(server, http.MethodGet, "/api/v1/apps/"+appID+"/update?current_version=1.2.0&channel=stable&platform=macos&architecture=arm64", "", "")
	if noUpdate.Code != http.StatusOK || !bytes.Contains(noUpdate.Body.Bytes(), []byte(`"update_available":false`)) || !bytes.Contains(noUpdate.Body.Bytes(), []byte(`"release":null`)) {
		t.Fatalf("no update status = %d, body = %s", noUpdate.Code, noUpdate.Body.String())
	}
	if got := request(server, http.MethodPost, "/api/v1/apps/"+appID+"/releases/"+draft.ID+"/yank", "", "Bearer secret"); got.Code != http.StatusOK {
		t.Fatalf("yank status = %d, body = %s", got.Code, got.Body.String())
	}
	yankedUpdate := request(server, http.MethodGet, "/api/v1/apps/"+appID+"/update?current_version=1.0.0&channel=stable&platform=macos&architecture=arm64", "", "")
	if yankedUpdate.Code != http.StatusOK || !bytes.Contains(yankedUpdate.Body.Bytes(), []byte(`"update_available":false`)) {
		t.Fatalf("yanked update status = %d, body = %s", yankedUpdate.Code, yankedUpdate.Body.String())
	}
}

func TestMarketplaceBearerAndVisibilityErrors(t *testing.T) {
	server, directory, _, appID := newMarketplaceServer(t)
	body := `{"version":"1.0.0","channel":"stable","artifacts":[]}`
	if response := request(server, http.MethodPost, "/api/v1/apps/"+appID+"/releases", body, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer status = %d", response.Code)
	}
	directory.secretOK = false
	if response := request(server, http.MethodPost, "/api/v1/apps/"+appID+"/releases", body, "Bearer bad"); response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer status = %d", response.Code)
	}
	directory.app.Status = gen.DyCustomAppStatus_DY_SUSPENDED
	if response := request(server, http.MethodGet, "/api/v1/apps/"+appID, "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("suspended app status = %d", response.Code)
	}
}
