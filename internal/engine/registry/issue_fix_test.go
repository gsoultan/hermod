package registry

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
)

type mockSecretManager struct {
	resolved bool
}

func (m *mockSecretManager) Get(ctx context.Context, key string) (string, error) {
	m.resolved = true
	return "resolved-value", nil
}

func TestSecretResolutionInGetOrOpenDB(t *testing.T) {
	sm := &mockSecretManager{}
	reg := NewRegistry(&mockStorage{})
	reg.SetSecretManager(sm)

	src := storage.Source{
		ID:   "test-source",
		Type: "sqlite",
		Config: map[string]string{
			"path":     ":memory:",
			"password": "{{secret:DB_PASS}}",
		},
	}

	// We don't want to actually open a DB connection if possible,
	// but GetOrOpenDB will try to.
	// Since we use sqlite :memory:, it should be fine.

	_, err := reg.GetOrOpenDB(src)
	if err != nil {
		t.Logf("GetOrOpenDB returned error (expected if driver not registered): %v", err)
	}

	if !sm.resolved {
		t.Errorf("SecretManager.GetSecret was not called")
	}
}
