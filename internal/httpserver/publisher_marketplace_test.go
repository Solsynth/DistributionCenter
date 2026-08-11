package httpserver

import (
	"context"
	"encoding/json"
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
	superuser bool
}

func (f *httpPublisherDirectory) Authenticate(_ context.Context, token string) (string, error) {
	if token != "sphere-token" {
		return "", service.ErrUnauthorized
	}
	return f.accountID, nil
}
func (f *httpPublisherDirectory) AuthenticateAccount(ctx context.Context, token string) (service.AuthenticatedAccount, error) {
	accountID, err := f.Authenticate(ctx, token)
	if err != nil {
		return service.AuthenticatedAccount{}, err
	}
	return service.AuthenticatedAccount{ID: accountID, IsSuperuser: f.superuser}, nil
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
	if err := db.AutoMigrate(&database.Product{}, &database.Channel{}, &database.Release{}, &database.ReleaseArtifact{}, &database.ClientCheck{}, &database.Localization{}); err != nil {
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

	request := httptest.NewRequest(http.MethodPost, "/api/products/"+productID+"/channels", strings.NewReader(`{"name":"stable","display_name":"Stable","display_names":{"en-US":"Stable","zh-CN":"稳定版"},"descriptions":{"en-US":"Stable builds","zh-CN":"稳定版本"}}`))
	request.Header.Set("Authorization", "Bearer sphere-token")
	response := httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"display_names":{"en-US":"Stable","zh-CN":"稳定版"}`) {
		t.Fatalf("create channel status = %d, body = %s", response.Code, response.Body.String())
	}
	var createdChannel database.Channel
	if err := json.Unmarshal(response.Body.Bytes(), &createdChannel); err != nil {
		t.Fatal(err)
	}
	updateChannelRequest := httptest.NewRequest(http.MethodPut, "/api/products/"+productID+"/channels/"+createdChannel.ID, strings.NewReader(`{"display_name":"Stable","display_names":{"en-US":"Stable","zh-CN":"稳定渠道"},"description":"Stable builds","descriptions":{"en-US":"Stable builds","zh-CN":"稳定版本"}}`))
	updateChannelRequest.AddCookie(&http.Cookie{Name: "AuthToken", Value: "sphere-token"})
	updatedChannel := httptest.NewRecorder()
	server.Engine.ServeHTTP(updatedChannel, updateChannelRequest)
	if updatedChannel.Code != http.StatusOK || !strings.Contains(updatedChannel.Body.String(), `"display_names":{"en-US":"Stable","zh-CN":"稳定渠道"}`) {
		t.Fatalf("update channel status = %d, body = %s", updatedChannel.Code, updatedChannel.Body.String())
	}
	if directory.members != 4 {
		t.Fatalf("membership calls = %d, want create and update checks", directory.members)
	}
	publisherProducts := httptest.NewRecorder()
	server.Engine.ServeHTTP(publisherProducts, httptest.NewRequest(http.MethodGet, "/api/publishers/Example/products", nil))
	if publisherProducts.Code != http.StatusOK || !strings.Contains(publisherProducts.Body.String(), `"data"`) {
		t.Fatalf("publisher products status = %d, body = %s", publisherProducts.Code, publisherProducts.Body.String())
	}

	createProductRequest := httptest.NewRequest(http.MethodPost, "/api/publishers/Example/products", strings.NewReader(`{"slug":"desktop","name":"Desktop"}`))
	createProductRequest.Header.Set("Authorization", "Bearer sphere-token")
	createdProduct := httptest.NewRecorder()
	server.Engine.ServeHTTP(createdProduct, createProductRequest)
	if createdProduct.Code != http.StatusCreated || directory.members != 6 {
		t.Fatalf("create publisher product status = %d, body = %s, membership calls = %d", createdProduct.Code, createdProduct.Body.String(), directory.members)
	}
	var created database.Product
	if err := json.Unmarshal(createdProduct.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	updateProductRequest := httptest.NewRequest(http.MethodPut, "/api/products/"+created.ID, strings.NewReader(`{"slug":"desktop-updated","name":"Desktop Updated","names":{"en-US":"Desktop Updated","zh-CN":"桌面客户端"},"description":"Updated","descriptions":{"en-US":"Updated","zh-CN":"已更新"},"icon":{"id":"icon-file","name":"Icon","file_meta":{"source":"upload"},"user_meta":{"alt":"App icon"},"mime_type":"image/png","hash":"icon-hash","size":12},"background":{"id":"hero-file","name":"Hero","mime_type":"image/png","size":42},"previews":[{"id":"preview-file","name":"Preview","mime_type":"image/png","width":640,"height":480,"size":24}]}`))
	updateProductRequest.Header.Set("Authorization", "Bearer sphere-token")
	updatedProduct := httptest.NewRecorder()
	server.Engine.ServeHTTP(updatedProduct, updateProductRequest)
	if updatedProduct.Code != http.StatusOK || !strings.Contains(updatedProduct.Body.String(), `"slug":"desktop-updated"`) || !strings.Contains(updatedProduct.Body.String(), `"names":{"en-US":"Desktop Updated","zh-CN":"桌面客户端"}`) || !strings.Contains(updatedProduct.Body.String(), `"descriptions":{"en-US":"Updated","zh-CN":"已更新"}`) || !strings.Contains(updatedProduct.Body.String(), `"previews":[{"id":"preview-file"`) || !strings.Contains(updatedProduct.Body.String(), `"file_meta":{"source":"upload"}`) {
		t.Fatalf("update product status = %d, body = %s", updatedProduct.Code, updatedProduct.Body.String())
	}
	localizedProducts := httptest.NewRecorder()
	server.Engine.ServeHTTP(localizedProducts, httptest.NewRequest(http.MethodGet, "/api/publishers/Example/products", nil))
	if localizedProducts.Code != http.StatusOK || !strings.Contains(localizedProducts.Body.String(), `"names":{"en-US":"Desktop Updated","zh-CN":"桌面客户端"}`) {
		t.Fatalf("localized product list status = %d, body = %s", localizedProducts.Code, localizedProducts.Body.String())
	}
	deleteProductRequest := httptest.NewRequest(http.MethodDelete, "/api/products/"+created.ID, nil)
	deleteProductRequest.Header.Set("Authorization", "Bearer sphere-token")
	deletedProduct := httptest.NewRecorder()
	server.Engine.ServeHTTP(deletedProduct, deleteProductRequest)
	if deletedProduct.Code != http.StatusNoContent {
		t.Fatalf("delete product status = %d, body = %s", deletedProduct.Code, deletedProduct.Body.String())
	}

	public := httptest.NewRecorder()
	server.Engine.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/products/"+productID, nil))
	if public.Code != http.StatusOK || !strings.Contains(public.Body.String(), `"publisher"`) {
		t.Fatalf("product status = %d, body = %s", public.Code, public.Body.String())
	}
}

type routePermissionChecker struct {
	allowed map[string]bool
	calls   []string
}

func (c *routePermissionChecker) HasPermission(_ context.Context, _ string, key string) (bool, error) {
	c.calls = append(c.calls, key)
	return c.allowed[key], nil
}

func TestPublisherRoutesRequireFineGrainedPermissions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:http-publisher-permissions-test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Product{}, &database.UploadAPIKey{}, &database.Channel{}, &database.Release{}, &database.ReleaseArtifact{}, &database.ClientCheck{}, &database.Localization{}); err != nil {
		t.Fatal(err)
	}
	publisherID, productID := uuid.NewString(), uuid.NewString()
	if err := db.Create(&database.Product{ID: productID, PublisherID: publisherID, Slug: "client", Name: "Client"}).Error; err != nil {
		t.Fatal(err)
	}
	directory := &httpPublisherDirectory{accountID: uuid.NewString(), publisher: &gen.DyPublisher{Id: publisherID, Name: "Example"}}
	releases := service.NewPublisherReleaseService(db, directory, nil, nil)
	checker := &routePermissionChecker{allowed: map[string]bool{}}
	releases.SetPermissionChecker(checker)
	server := New(config.Default())
	RegisterPublisherRoutes(server.Engine, releases, directory, config.Default())

	request := httptest.NewRequest(http.MethodPut, "/api/products/"+productID, strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer sphere-token")
	response := httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("update product status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(checker.calls) != 1 || checker.calls[0] != service.PermissionProductsUpdate {
		t.Fatalf("permission calls = %v, want [%q]", checker.calls, service.PermissionProductsUpdate)
	}

	checker.allowed[service.PermissionMetricsRead] = true
	metrics := httptest.NewRecorder()
	metricsRequest := httptest.NewRequest(http.MethodGet, "/api/products/"+productID+"/metrics", nil)
	metricsRequest.Header.Set("Authorization", "Bearer sphere-token")
	server.Engine.ServeHTTP(metrics, metricsRequest)
	if metrics.Code == http.StatusForbidden {
		t.Fatalf("metrics permission unexpectedly denied: %s", metrics.Body.String())
	}
	if len(checker.calls) != 2 || checker.calls[1] != service.PermissionMetricsRead {
		t.Fatalf("permission calls = %v, want update then metrics", checker.calls)
	}
	directory.superuser = true
	superuserRequest := httptest.NewRequest(http.MethodPost, "/api/products/"+productID+"/upload-api-keys", strings.NewReader(`{"name":"Superuser CI"}`))
	superuserRequest.AddCookie(&http.Cookie{Name: "AuthToken", Value: "sphere-token"})
	superuserResponse := httptest.NewRecorder()
	server.Engine.ServeHTTP(superuserResponse, superuserRequest)
	if superuserResponse.Code != http.StatusCreated {
		t.Fatalf("superuser upload key status = %d, body = %s", superuserResponse.Code, superuserResponse.Body.String())
	}
	if len(checker.calls) != 2 {
		t.Fatalf("superuser permission calls = %v, want no additional permission check", checker.calls)
	}
}
