// Package config loads DistributionCenter configuration.
package config

import (
	"fmt"
	"os"
	"strings"

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

	Database struct {
		DSN string `toml:"dsn"`
	} `toml:"database"`

	Sphere struct {
		Target        string `toml:"target"`
		UseTLS        bool   `toml:"useTLS"`
		TLSSkipVerify bool   `toml:"tlsSkipVerify"`
	} `toml:"sphere"`

	// Develop is retained only for compatibility with revision-1 local
	// configuration files. Publisher ownership is resolved through Sphere.
	Develop struct {
		Target        string `toml:"target"`
		UseTLS        bool   `toml:"useTLS"`
		TLSSkipVerify bool   `toml:"tlsSkipVerify"`
	} `toml:"develop"`

	S3 struct {
		Endpoint  string `toml:"endpoint"`
		AccessKey string `toml:"accessKey"`
		SecretKey string `toml:"secretKey"`
		Bucket    string `toml:"bucket"`
		Region    string `toml:"region"`
		PublicURL string `toml:"publicURL"`
	} `toml:"s3"`

	Eventbus struct {
		URL string `toml:"url"`
	} `toml:"eventbus"`

	Analytics struct {
		Enabled bool   `toml:"enabled"`
		Salt    string `toml:"salt"`
	} `toml:"analytics"`
}

// Validate checks the required dependencies for the durable marketplace
// service. Optional discovery and event-bus settings are intentionally not
// included.
func (c *Config) Validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"sphere.target", c.Sphere.Target},
		{"s3.endpoint", c.S3.Endpoint},
		{"s3.accessKey", c.S3.AccessKey},
		{"s3.secretKey", c.S3.SecretKey},
		{"s3.bucket", c.S3.Bucket},
		{"s3.publicURL", c.S3.PublicURL},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return fmt.Errorf("%s is required", item.name)
		}
	}
	return nil
}

// Default returns local-development values for non-composition tests. The
// durable application still requires external dependency settings in Load.
func Default() *Config {
	cfg := &Config{
		ServiceName: "distribution",
		Version:     "dev",
	}
	cfg.HTTP.Port = "8080"
	cfg.GRPC.Port = "9090"
	cfg.Discovery.LeaseSeconds = 30
	cfg.Discovery.Weight = 1
	cfg.Analytics.Enabled = true
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
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
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
	setStr("DISTRIBUTION_DATABASE_DSN", &cfg.Database.DSN)
	setStr("DISTRIBUTION_SPHERE_TARGET", &cfg.Sphere.Target)
	setBool("DISTRIBUTION_SPHERE_USETLS", &cfg.Sphere.UseTLS)
	setBool("DISTRIBUTION_SPHERE_TLS_SKIP_VERIFY", &cfg.Sphere.TLSSkipVerify)
	setStr("DISTRIBUTION_DEVELOP_TARGET", &cfg.Develop.Target)
	setBool("DISTRIBUTION_DEVELOP_USETLS", &cfg.Develop.UseTLS)
	setBool("DISTRIBUTION_DEVELOP_TLS_SKIP_VERIFY", &cfg.Develop.TLSSkipVerify)
	setStr("DISTRIBUTION_S3_ENDPOINT", &cfg.S3.Endpoint)
	setStr("DISTRIBUTION_S3_ACCESS_KEY", &cfg.S3.AccessKey)
	setStr("DISTRIBUTION_S3_SECRET_KEY", &cfg.S3.SecretKey)
	setStr("DISTRIBUTION_S3_BUCKET", &cfg.S3.Bucket)
	setStr("DISTRIBUTION_S3_REGION", &cfg.S3.Region)
	setStr("DISTRIBUTION_S3_PUBLIC_URL", &cfg.S3.PublicURL)
	setStr("DISTRIBUTION_EVENTBUS_URL", &cfg.Eventbus.URL)
	setBool("DISTRIBUTION_ANALYTICS_ENABLED", &cfg.Analytics.Enabled)
	setStr("DISTRIBUTION_ANALYTICS_SALT", &cfg.Analytics.Salt)
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
