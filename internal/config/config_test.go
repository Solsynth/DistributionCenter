package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndEnvironmentOverrides(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("DISTRIBUTION_HTTP_PORT", "18080")
	t.Setenv("DISTRIBUTION_DISCOVERY_ENABLED", "1")
	t.Setenv("DISTRIBUTION_DISCOVERY_SERVICE", "distribution-test")
	t.Setenv("DISTRIBUTION_DATABASE_DSN", "file::memory:?cache=shared")
	t.Setenv("DISTRIBUTION_AUTH_TARGET", "stargate:9090")
	t.Setenv("DISTRIBUTION_SPHERE_TARGET", "sphere:9090")
	t.Setenv("DISTRIBUTION_S3_ENDPOINT", "http://s3.example.test")
	t.Setenv("DISTRIBUTION_S3_ACCESS_KEY", "access")
	t.Setenv("DISTRIBUTION_S3_SECRET_KEY", "secret")
	t.Setenv("DISTRIBUTION_S3_BUCKET", "artifacts")
	t.Setenv("DISTRIBUTION_RELEASES_ARTIFACT_RETENTION", "2")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ServiceName != "distribution" {
		t.Fatalf("ServiceName = %q, want distribution", cfg.ServiceName)
	}
	if cfg.HTTP.Port != "18080" {
		t.Fatalf("HTTP.Port = %q, want 18080", cfg.HTTP.Port)
	}
	if !cfg.Discovery.Enabled {
		t.Fatal("Discovery.Enabled = false, want true")
	}
	if cfg.Discovery.Service != "distribution-test" {
		t.Fatalf("Discovery.Service = %q, want distribution-test", cfg.Discovery.Service)
	}
	if cfg.Auth.Target != "stargate:9090" {
		t.Fatalf("Auth.Target = %q, want stargate:9090", cfg.Auth.Target)
	}
	if cfg.Releases.ArtifactRetention != 2 {
		t.Fatalf("Releases.ArtifactRetention = %d, want 2", cfg.Releases.ArtifactRetention)
	}
}
func TestValidateBaseURL(t *testing.T) {
	cfg := Default()
	cfg.Database.DSN = "file::memory:?cache=shared"
	cfg.Auth.Target = "stargate:9090"
	cfg.Sphere.Target = "sphere:9090"
	cfg.S3.Endpoint = "http://s3.example.test"
	cfg.S3.AccessKey = "access"
	cfg.S3.SecretKey = "secret"
	cfg.S3.Bucket = "artifacts"
	cfg.HTTP.BaseURL = "https://distribution.example.test"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with base URL = %v", err)
	}
	cfg.HTTP.BaseURL = "https://distribution.example.test/api"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted base URL with path")
	}
}

func TestValidateS3Configuration(t *testing.T) {
	cfg := Default()
	cfg.Database.DSN = "file::memory:?cache=shared"
	cfg.Auth.Target = "stargate:9090"
	cfg.Sphere.Target = "sphere:9090"
	cfg.S3.Endpoint = "http://s3.example.test"
	cfg.S3.AccessKey = "access"
	cfg.S3.SecretKey = "secret"
	cfg.S3.Bucket = "artifacts"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with S3 configuration = %v", err)
	}
}

func TestLoadRejectsMissingRequiredDependencies(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "missing.toml"))
	if _, err := Load(""); err == nil {
		t.Fatal("Load() error = nil, want missing database.dsn error")
	}
}

func TestLoadRejectsMalformedToml(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[http\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want parse error")
	}
}
