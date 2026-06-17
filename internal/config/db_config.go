package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type DBConfig struct {
	Type            string `yaml:"type" json:"type"`
	Conn            string `yaml:"conn" json:"conn"`
	LogType         string `yaml:"log_type" json:"log_type"`
	LogConn         string `yaml:"log_conn" json:"log_conn"`
	JWTSecret       string `yaml:"jwt_secret" json:"jwt_secret"`
	CryptoMasterKey string `yaml:"crypto_master_key" json:"crypto_master_key"`
}

func getDBConfigPath() string {
	return GetConfigPath("db_config.yaml")
}

func LoadDBConfig() (*DBConfig, error) {
	var cfg DBConfig
	path := getDBConfigPath()
	data, err := os.ReadFile(path)
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
	if err := EnsureConfigDir(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	// db_config.yaml holds sensitive secrets (JWTSecret, CryptoMasterKey),
	// so it must not be world/group readable.
	return os.WriteFile(getDBConfigPath(), data, 0600)
}

func IsDBConfigured() bool {
	if _, err := os.Stat(getDBConfigPath()); err == nil {
		return true
	}
	// Also considered configured if minimal environment variables are set
	return os.Getenv("HERMOD_DB_TYPE") != "" && os.Getenv("HERMOD_DB_CONN") != ""
}
