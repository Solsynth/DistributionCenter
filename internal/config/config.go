// Package config loads DistributionCenter configuration.
package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// Config is the process configuration shared by the HTTP, gRPC, and service
// discovery surfaces.
type Config struct {
	ServiceName string `toml:"serviceName"`
	Version     string `toml:"version"`

	HTTP struct {
		Port string `toml:"port"`
	} `toml:"http"`

	GRPC struct {
		Port     string `toml:"port"`
		UseTLS   bool   `toml:"useTLS"`
		CertFile string `toml:"certFile"`
		KeyFile  string `toml:"keyFile"`
	} `toml:"grpc"`

	Discovery struct {
		Enabled           bool   `toml:"enabled"`
		Target            string `toml:"target"`
		RegistrationToken string `toml:"registrationToken"`
		Service           string `toml:"service"`
		InstanceID        string `toml:"instanceId"`
		HttpEndpoint      string `toml:"httpEndpoint"`
		GrpcEndpoint      string `toml:"grpcEndpoint"`
		LeaseSeconds      int    `toml:"leaseSeconds"`
		Weight            int    `toml:"weight"`
	} `toml:"discovery"`
}

// Default returns local-development defaults. External dependencies remain
// opt-in so a fresh checkout can start and expose health endpoints.
func Default() *Config {
	cfg := &Config{
		ServiceName: "distribution",
		Version:     "dev",
	}
	cfg.HTTP.Port = "8080"
	cfg.GRPC.Port = "9090"
	cfg.Discovery.LeaseSeconds = 30
	cfg.Discovery.Weight = 1
	return cfg
}

// Load reads the TOML file at path. A missing file is allowed so the binary
// can be configured entirely through environment variables.
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("CONFIG_PATH")
	}
	if path == "" {
		path = "config.example.toml"
	}

	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	} else if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	applyEnvOverrides(cfg)
	if cfg.Discovery.Service == "" {
		cfg.Discovery.Service = cfg.ServiceName
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	setStr("DISTRIBUTION_SERVICE_NAME", &cfg.ServiceName)
	setStr("DISTRIBUTION_VERSION", &cfg.Version)
	setStr("DISTRIBUTION_HTTP_PORT", &cfg.HTTP.Port)
	setStr("DISTRIBUTION_GRPC_PORT", &cfg.GRPC.Port)
	setBool("DISTRIBUTION_GRPC_USETLS", &cfg.GRPC.UseTLS)
	setStr("DISTRIBUTION_GRPC_CERT_FILE", &cfg.GRPC.CertFile)
	setStr("DISTRIBUTION_GRPC_KEY_FILE", &cfg.GRPC.KeyFile)
	setBool("DISTRIBUTION_DISCOVERY_ENABLED", &cfg.Discovery.Enabled)
	setStr("DISTRIBUTION_DISCOVERY_TARGET", &cfg.Discovery.Target)
	setStr("DISTRIBUTION_DISCOVERY_REGISTRATION_TOKEN", &cfg.Discovery.RegistrationToken)
	setStr("DISTRIBUTION_DISCOVERY_SERVICE", &cfg.Discovery.Service)
	setStr("DISTRIBUTION_DISCOVERY_INSTANCE_ID", &cfg.Discovery.InstanceID)
	setStr("DISTRIBUTION_DISCOVERY_HTTP_ENDPOINT", &cfg.Discovery.HttpEndpoint)
	setStr("DISTRIBUTION_DISCOVERY_GRPC_ENDPOINT", &cfg.Discovery.GrpcEndpoint)
}

func setStr(key string, dst *string) {
	if value := os.Getenv(key); value != "" {
		*dst = value
	}
}

func setBool(key string, dst *bool) {
	if value := os.Getenv(key); value != "" {
		*dst = value == "true" || value == "1"
	}
}
