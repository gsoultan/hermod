package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/user/hermod/pkg/secrets"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Engine        EngineConfig        `json:"engine" yaml:"engine"`
	Buffer        BufferConfig        `json:"buffer" yaml:"buffer"`
	Secrets       secrets.Config      `json:"secrets" yaml:"secrets"`
	StateStore    StateStoreConfig    `json:"state_store" yaml:"state_store"`
	Observability ObservabilityConfig `json:"observability" yaml:"observability"`
	Auth          AuthConfig          `json:"auth" yaml:"auth"`
	FileStorage   FileStorageConfig   `json:"file_storage" yaml:"file_storage"`
}

type AuthConfig struct {
	OIDC OIDCConfig `json:"oidc" yaml:"oidc"`
}

type OIDCConfig struct {
	Enabled      bool     `json:"enabled" yaml:"enabled"`
	IssuerURL    string   `json:"issuer_url" yaml:"issuer_url"`
	ClientID     string   `json:"client_id" yaml:"client_id"`
	ClientSecret string   `json:"client_secret" yaml:"client_secret"`
	RedirectURL  string   `json:"redirect_url" yaml:"redirect_url"`
	Scopes       []string `json:"scopes" yaml:"scopes"`
}

type ObservabilityConfig struct {
	OTLP OTLPConfig `json:"otlp" yaml:"otlp"`
}

type OTLPConfig struct {
	Endpoint    string            `json:"endpoint" yaml:"endpoint"`
	Protocol    string            `json:"protocol" yaml:"protocol"` // grpc or http
	Insecure    bool              `json:"insecure" yaml:"insecure"`
	Headers     map[string]string `json:"headers" yaml:"headers"`
	ServiceName string            `json:"service_name" yaml:"service_name"`
}

type StateStoreConfig struct {
	Type     string `json:"type" yaml:"type"` // sqlite, redis, etcd
	Path     string `json:"path" yaml:"path"` // for sqlite
	Address  string `json:"address" yaml:"address"`
	Password string `json:"password" yaml:"password"`
	DB       int    `json:"db" yaml:"db"`
	Prefix   string `json:"prefix" yaml:"prefix"`
}

type EngineConfig struct {
	MaxRetries        int           `json:"max_retries" yaml:"max_retries"`
	RetryInterval     time.Duration `json:"retry_interval" yaml:"retry_interval"`
	ReconnectInterval time.Duration `json:"reconnect_interval" yaml:"reconnect_interval"`
	MaxInflight       int           `json:"max_inflight" yaml:"max_inflight"`
	DrainTimeout      time.Duration `json:"drain_timeout" yaml:"drain_timeout"`
}

type FileStorageConfig struct {
	Type     string   `json:"type" yaml:"type"` // local, s3
	LocalDir string   `json:"local_dir" yaml:"local_dir"`
	S3       S3Config `json:"s3" yaml:"s3"`
}

type S3Config struct {
	Endpoint        string `json:"endpoint" yaml:"endpoint"`
	Region          string `json:"region" yaml:"region"`
	Bucket          string `json:"bucket" yaml:"bucket"`
	AccessKeyID     string `json:"access_key_id" yaml:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key" yaml:"secret_access_key"`
	UseSSL          bool   `json:"use_ssl" yaml:"use_ssl"`
}

type BufferConfig struct {
	Type        string `json:"type" yaml:"type"`
	Size        int    `json:"size" yaml:"size"`
	Path        string `json:"path" yaml:"path"`
	Compression string `json:"compression" yaml:"compression"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	content := SubstituteEnvVars(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		// Try JSON if YAML fails
		if err := json.Unmarshal([]byte(content), &cfg); err != nil {
			return nil, fmt.Errorf("failed to decode config file (tried YAML and JSON): %w", err)
		}
	}

	return &cfg, nil
}

func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

var envRegex = regexp.MustCompile(`\${(\w+)(?::-([^}]*))?}`)

func SubstituteEnvVars(input string) string {
	return envRegex.ReplaceAllStringFunc(input, func(m string) string {
		matches := envRegex.FindStringSubmatch(m)
		if len(matches) < 2 {
			return m
		}
		envVar := matches[1]
		if val, ok := os.LookupEnv(envVar); ok {
			return val
		}
		if len(matches) > 2 && strings.Contains(m, ":-") {
			return matches[2]
		}
		return m
	})
}
