package secrets

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/vault/api"
)

// OpenBaoManager implements secrets.Manager for OpenBao.
// Since OpenBao is wire-compatible with HashiCorp Vault, we use the same client.
type OpenBaoManager struct {
	client *api.Client
	mount  string // The KV v2 mount path, e.g., "secret"
}

// NewOpenBaoManager creates a new OpenBaoManager.
func NewOpenBaoManager(address, token, mount string) (*OpenBaoManager, error) {
	config := api.DefaultConfig()
	config.Address = address

	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create openbao client: %w", err)
	}

	client.SetToken(token)

	if mount == "" {
		mount = "secret"
	}

	return &OpenBaoManager{
		client: client,
		mount:  mount,
	}, nil
}

// Get retrieves a secret from OpenBao.
// key format: "path/to/secret" or "path/to/secret:field"
// If field is not provided, it defaults to "value".
func (m *OpenBaoManager) Get(ctx context.Context, key string) (string, error) {
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
		return "", fmt.Errorf("failed to read secret from openbao: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("secret not found: %s", key)
	}

	// KV v2 data is nested under "data"
	data, ok := secret.Data["data"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid secret data format for %s", key)
	}

	val, ok := data[field]
	if !ok {
		return "", fmt.Errorf("field %s not found in secret %s", field, path)
	}

	return fmt.Sprintf("%v", val), nil
}
