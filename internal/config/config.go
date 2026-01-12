package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Engine EngineConfig `json:"engine" yaml:"engine"`
	Buffer BufferConfig `json:"buffer" yaml:"buffer"`
}

type EngineConfig struct {
	MaxRetries    int           `json:"max_retries" yaml:"max_retries"`
	RetryInterval time.Duration `json:"retry_interval" yaml:"retry_interval"`
}

type BufferConfig struct {
	Type string `json:"type" yaml:"type"`
	Size int    `json:"size" yaml:"size"`
	Path string `json:"path" yaml:"path"`
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
