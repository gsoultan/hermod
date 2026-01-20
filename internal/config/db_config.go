package config

import (
	"gopkg.in/yaml.v3"
	"os"
)

type DBConfig struct {
	Type            string `yaml:"type" json:"type"`
	Conn            string `yaml:"conn" json:"conn"`
	JWTSecret       string `yaml:"jwt_secret" json:"jwt_secret"`
	CryptoMasterKey string `yaml:"crypto_master_key" json:"crypto_master_key"`
}

const DBConfigPath = "db_config.yaml"

func LoadDBConfig() (*DBConfig, error) {
	data, err := os.ReadFile(DBConfigPath)
	if err != nil {
		return nil, err
	}
	var cfg DBConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
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
	_, err := os.Stat(DBConfigPath)
	return err == nil
}
