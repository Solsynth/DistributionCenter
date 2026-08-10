package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"src.solsynth.dev/sosys/distribution/internal/config"
	"src.solsynth.dev/sosys/distribution/internal/database"
	"src.solsynth.dev/sosys/distribution/internal/service"
	gen "src.solsynth.dev/sosys/go/proto"
)

type httpPublisherDirectory struct {
	accountID string
	publisher *gen.DyPublisher
	members   int
}

func (f *httpPublisherDirectory) Authenticate(_ context.Context, token string) (string, error) {
	if token != "sphere-token" {
		return "", service.ErrUnauthorized
	}
	return f.accountID, nil
}
func (f *httpPublisherDirectory) GetPublisher(context.Context, string) (*gen.DyPublisher, error) {
	return f.publisher, nil
}
func (f *httpPublisherDirectory) IsPublisherMember(context.Context, string, string, gen.DyPublisherMemberRole) (bool, error) {
	f.members++
	return true, nil
}

func TestPublisherRoutesUseSphereMembership(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:http-publisher-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Product{}, &database.Channel{}, &database.Release{}, &database.ReleaseArtifact{}, &database.ClientCheck{}); err != nil {
		t.Fatal(err)
	}
	publisherID, productID := uuid.NewString(), uuid.NewString()
	if err := db.Create(&database.Product{ID: productID, PublisherID: publisherID, Slug: "client", Name: "Client"}).Error; err != nil {
		t.Fatal(err)
	}
	directory := &httpPublisherDirectory{accountID: uuid.NewString(), publisher: &gen.DyPublisher{Id: publisherID, Name: "Example"}}
	releases := service.NewPublisherReleaseService(db, directory, nil, nil)
	server := New(config.Default())
	RegisterPublisherRoutes(server.Engine, releases, directory, config.Default())

	request := httptest.NewRequest(http.MethodPost, "/api/v1/products/"+productID+"/channels", strings.NewReader(`{"name":"stable"}`))
	request.Header.Set("Authorization", "Bearer sphere-token")
	response := httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create channel status = %d, body = %s", response.Code, response.Body.String())
	}
	if directory.members != 2 {
		t.Fatalf("membership calls = %d, want middleware and service checks", directory.members)
	}

	public := httptest.NewRecorder()
	server.Engine.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/products/"+productID, nil))
	if public.Code != http.StatusOK || !strings.Contains(public.Body.String(), `"publisher"`) {
		t.Fatalf("product status = %d, body = %s", public.Code, public.Body.String())
	}
}
