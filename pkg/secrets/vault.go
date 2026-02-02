package secrets

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/vault/api"
)

// VaultManager implements secrets.Manager for HashiCorp Vault.
type VaultManager struct {
	client *api.Client
	mount  string // The KV v2 mount path, e.g., "secret"
}

// NewVaultManager creates a new VaultManager.
func NewVaultManager(address, token, mount string) (*VaultManager, error) {
	config := api.DefaultConfig()
	config.Address = address

	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create vault client: %w", err)
	}

	client.SetToken(token)

	if mount == "" {
		mount = "secret"
	}

	return &VaultManager{
		client: client,
		mount:  mount,
	}, nil
}

// Get retrieves a secret from Vault.
// key format: "path/to/secret" or "path/to/secret:field"
// If field is not provided, it defaults to "value".
func (m *VaultManager) Get(ctx context.Context, key string) (string, error) {
	path := key
	field := "value"

	if strings.Contains(key, ":") {
		parts := strings.SplitN(key, ":", 2)
		path = parts[0]
		field = parts[1]
	}

	// KV v2 path requires /data/ between mount and path
	vaultPath := fmt.Sprintf("%s/data/%s", m.mount, path)

	secret, err := m.client.Logical().Read(vaultPath)
	if err != nil {
		return "", fmt.Errorf("failed to read secret from vault: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("secret not found: %s", key)
	}

	// KV v2 data is nested under "data"
	data, ok := secret.Data["data"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid secret data format for %s", key)
	}

	val, ok := data[field]
	if !ok {
		return "", fmt.Errorf("field %s not found in secret %s", field, path)
	}

	return fmt.Sprintf("%v", val), nil
}
