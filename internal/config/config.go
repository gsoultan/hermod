package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Engine EngineConfig `json:"engine" yaml:"engine"`
	Buffer BufferConfig `json:"buffer" yaml:"buffer"`
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
