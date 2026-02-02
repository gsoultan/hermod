package secrets

import (
	"context"
	"fmt"
)

// Config defines the configuration for secret managers.
type Config struct {
	Type    string      `yaml:"type" json:"type"` // env, vault, aws, azure, openbao
	Vault   VaultConfig `yaml:"vault" json:"vault"`
	OpenBao VaultConfig `yaml:"openbao" json:"openbao"` // OpenBao uses same config as Vault
	AWS     AWSConfig   `yaml:"aws" json:"aws"`
	Azure   AzureConfig `yaml:"azure" json:"azure"`
	Env     EnvConfig   `yaml:"env" json:"env"`
}

type VaultConfig struct {
	Address string `yaml:"address" json:"address"`
	Token   string `yaml:"token" json:"token"`
	Mount   string `yaml:"mount" json:"mount"`
}

type AWSConfig struct {
	Region string `yaml:"region" json:"region"`
}

type AzureConfig struct {
	VaultURL string `yaml:"vault_url" json:"vault_url"`
}

type EnvConfig struct {
	Prefix string `yaml:"prefix" json:"prefix"`
}

// NewManager creates a secret manager based on the provided configuration.
func NewManager(ctx context.Context, cfg Config) (Manager, error) {
	switch cfg.Type {
	case "env":
		return &EnvManager{Prefix: cfg.Env.Prefix}, nil
	case "vault":
		return NewVaultManager(cfg.Vault.Address, cfg.Vault.Token, cfg.Vault.Mount)
	case "openbao":
		return NewOpenBaoManager(cfg.OpenBao.Address, cfg.OpenBao.Token, cfg.OpenBao.Mount)
	case "aws":
		return NewAWSSecretsManager(ctx, cfg.AWS.Region)
	case "azure":
		return NewAzureKeyVaultManager(cfg.Azure.VaultURL)
	default:
		return nil, fmt.Errorf("unsupported secret manager type: %s", cfg.Type)
	}
}
