package httpserver

import (
	"net/http/httptest"
	"testing"

	"src.solsynth.dev/sosys/distribution/internal/config"
)

func TestHealthAndReadiness(t *testing.T) {
	server := New(config.Default())

	request := httptest.NewRequest("GET", "/health", nil)
	response := httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("/health status = %d, want 200", response.Code)
	}

	request = httptest.NewRequest("GET", "/ready", nil)
	response = httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatalf("/ready before startup = %d, want 503", response.Code)
	}

	server.SetReady(true)
	request = httptest.NewRequest("GET", "/ready", nil)
	response = httptest.NewRecorder()
	server.Engine.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("/ready after startup = %d, want 200", response.Code)
	}
}
