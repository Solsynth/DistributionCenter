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
