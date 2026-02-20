package config

import (
	"gopkg.in/yaml.v3"
	"os"
)

type DBConfig struct {
	Type            string `yaml:"type" json:"type"`
	Conn            string `yaml:"conn" json:"conn"`
	LogType         string `yaml:"log_type" json:"log_type"`
	LogConn         string `yaml:"log_conn" json:"log_conn"`
	JWTSecret       string `yaml:"jwt_secret" json:"jwt_secret"`
	CryptoMasterKey string `yaml:"crypto_master_key" json:"crypto_master_key"`
}

const DBConfigPath = "db_config.yaml"

func LoadDBConfig() (*DBConfig, error) {
	var cfg DBConfig
	data, err := os.ReadFile(DBConfigPath)
	if err == nil {
		content := SubstituteEnvVars(string(data))
		if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Environment variable overrides (highest precedence)
	if v := os.Getenv("HERMOD_DB_TYPE"); v != "" {
		cfg.Type = v
	}
	if v := os.Getenv("HERMOD_DB_CONN"); v != "" {
		cfg.Conn = v
	}
	if v := os.Getenv("HERMOD_LOG_DB_TYPE"); v != "" {
		cfg.LogType = v
	}
	if v := os.Getenv("HERMOD_LOG_DB_CONN"); v != "" {
		cfg.LogConn = v
	}
	if v := os.Getenv("HERMOD_JWT_SECRET"); v != "" {
		cfg.JWTSecret = v
	}
	if v := os.Getenv("HERMOD_MASTER_KEY"); v != "" {
		cfg.CryptoMasterKey = v
	}

	// If everything is empty and file was missing, return original error
	if cfg.Type == "" && cfg.Conn == "" && err != nil {
		return nil, err
	}

	return &cfg, nil
}

func SaveDBConfig(cfg *DBConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(DBConfigPath, data, 0644)
}

func IsDBConfigured() bool {
	if _, err := os.Stat(DBConfigPath); err == nil {
		return true
	}
	// Also considered configured if minimal environment variables are set
	return os.Getenv("HERMOD_DB_TYPE") != "" && os.Getenv("HERMOD_DB_CONN") != ""
}
