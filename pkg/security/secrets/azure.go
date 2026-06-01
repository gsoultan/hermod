package secrets

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// AzureKeyVaultManager implements secrets.Manager for Azure Key Vault.
type AzureKeyVaultManager struct {
	client *azsecrets.Client
}

// NewAzureKeyVaultManager creates a new AzureKeyVaultManager.
func NewAzureKeyVaultManager(vaultURL string) (*AzureKeyVaultManager, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create default azure credential: %w", err)
	}

	client, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure secrets client: %w", err)
	}

	return &AzureKeyVaultManager{
		client: client,
	}, nil
}

// Get retrieves a secret from Azure Key Vault.
func (m *AzureKeyVaultManager) Get(ctx context.Context, key string) (string, error) {
	resp, err := m.client.GetSecret(ctx, key, "", nil)
	if err != nil {
		return "", fmt.Errorf("failed to get secret from azure: %w", err)
	}

	if resp.Value != nil {
		return *resp.Value, nil
	}

	return "", fmt.Errorf("secret %s has no value", key)
}
