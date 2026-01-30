package secrets

import (
	"context"
	"os"
	"strings"
)

// Manager defines the interface for external secret managers.
type Manager interface {
	Get(ctx context.Context, key string) (string, error)
}

// EnvManager resolves secrets from environment variables.
type EnvManager struct {
	Prefix string
}

func (m *EnvManager) Get(ctx context.Context, key string) (string, error) {
	val := os.Getenv(m.Prefix + key)
	if val == "" {
		// Fallback without prefix
		val = os.Getenv(key)
	}
	return val, nil
}

// CombinedManager tries multiple secret managers in order.
type CombinedManager struct {
	Managers []Manager
}

func (m *CombinedManager) Get(ctx context.Context, key string) (string, error) {
	for _, mgr := range m.Managers {
		val, err := mgr.Get(ctx, key)
		if err == nil && val != "" {
			return val, nil
		}
	}
	return "", nil
}

// ResolveSecret takes a value and if it starts with "secret:",
// it attempts to resolve it using the provided manager.
func ResolveSecret(ctx context.Context, mgr Manager, value string) string {
	if strings.HasPrefix(value, "secret:") {
		key := strings.TrimPrefix(value, "secret:")
		if mgr != nil {
			val, err := mgr.Get(ctx, key)
			if err == nil && val != "" {
				return val
			}
		}
	}
	return value
}
