package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
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
}

type BufferConfig struct {
	Type        string `json:"type" yaml:"type"`
	Size        int    `json:"size" yaml:"size"`
	Path        string `json:"path" yaml:"path"`
	Compression string `json:"compression" yaml:"compression"`
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var cfg Config
	if err := yaml.NewDecoder(file).Decode(&cfg); err != nil {
		// Try JSON if YAML fails
		file.Seek(0, 0)
		if err := json.NewDecoder(file).Decode(&cfg); err != nil {
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

var envRegex = regexp.MustCompile(`\${(\w+)}`)

func SubstituteEnvVars(input string) string {
	return envRegex.ReplaceAllStringFunc(input, func(m string) string {
		envVar := envRegex.FindStringSubmatch(m)[1]
		if val, ok := os.LookupEnv(envVar); ok {
			return val
		}
		return m
	})
}
