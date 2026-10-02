package secrets

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Manager defines the interface for external secret managers.
type Manager interface {
	Get(ctx context.Context, key string) (string, error)
}

// DefaultEnvPrefix is where an env secret is read from when no prefix is set.
const DefaultEnvPrefix = "HERMOD_SECRET_"

// AllowUnprefixedEnv, set to true in the operator's own environment, lets a
// secret with no prefixed variable fall back to its bare name, as every lookup
// did before. It exists so a deployment can rename its variables without an
// outage, and it will be removed.
const AllowUnprefixedEnv = "HERMOD_SECRETS_ALLOW_UNPREFIXED"

// EnvManager resolves secrets from environment variables.
type EnvManager struct {
	Prefix string
}

var (
	warnUnprefixedRead    sync.Once
	warnUnprefixedRefused sync.Once
)

// Get reads the secret named key from the variable Prefix+key -- or from key
// itself when it already carries the prefix -- and from nothing else. An empty
// Prefix means DefaultEnvPrefix.
//
// It used to fall back to os.Getenv(key). Keys reach here from connector
// configs and expressions, so that fallback let anyone who could edit a
// workflow read any variable of the process: `secret:HERMOD_JWT_SECRET` in a
// connector config, env('HERMOD_JWT_SECRET') in a transformation.
func (m *EnvManager) Get(ctx context.Context, key string) (string, error) {
	prefix := m.Prefix
	if prefix == "" {
		prefix = DefaultEnvPrefix
	}
	name := key
	if !strings.HasPrefix(key, prefix) {
		name = prefix + key
	}
	if val := os.Getenv(name); val != "" {
		return val, nil
	}

	bare := os.Getenv(key)
	if bare == "" {
		return "", nil
	}
	// Once each, so a workflow evaluating this per message cannot flood the
	// log; the key is named, never its value.
	if allowed, _ := strconv.ParseBool(os.Getenv(AllowUnprefixedEnv)); allowed {
		warnUnprefixedRead.Do(func() {
			log.Printf("secrets: %q was read from the unprefixed variable %s because %s is set; rename it to %s -- the fallback will be removed",
				key, key, AllowUnprefixedEnv, name)
		})
		return bare, nil
	}
	warnUnprefixedRefused.Do(func() {
		log.Printf("secrets: %q has no variable %s; the unprefixed %s is not read. Rename it, or set %s=true until you can",
			key, name, key, AllowUnprefixedEnv)
	})
	return "", nil
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

// ResolveSecret takes a value and if it is marked as a secret (e.g. "secret:KEY" or "{{secret:KEY}}"),
// it attempts to resolve it using the provided manager.
func ResolveSecret(ctx context.Context, mgr Manager, value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") {
		trimmed = strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	}

	if after, ok := strings.CutPrefix(trimmed, "secret:"); ok {
		key := after
		if mgr != nil {
			val, err := mgr.Get(ctx, key)
			if err == nil && val != "" {
				return val
			}
		}
	}
	return value
}

// VHostStore reads one secret a vhost holds; found is false when it has none
// by that name.
type VHostStore interface {
	VHostSecret(ctx context.Context, vhost, name string) (value string, found bool, err error)
}

// ScopedManager answers a secret for the vhost asking for it.
type ScopedManager interface {
	GetScoped(ctx context.Context, vhost, key string) (string, error)
}

// VHostManager answers a secret from the asking vhost's own store, and from the
// global manager when that vhost holds none by the name. A vhost can therefore
// keep its own API_KEY, override a shared one, and still read the
// HERMOD_SECRET_ variables or Vault entries it read before. It is never
// answered from another vhost's store.
type VHostManager struct {
	Store  VHostStore
	Global Manager
}

// Get answers without a vhost: the global manager only.
func (m *VHostManager) Get(ctx context.Context, key string) (string, error) {
	return m.GetScoped(ctx, "", key)
}

// GetScoped answers key for vhost. A store that fails is an error rather than a
// fall-through: the global manager may hold a different value under the same
// name, and handing that one over would be worse than handing over nothing.
func (m *VHostManager) GetScoped(ctx context.Context, vhost, key string) (string, error) {
	if vhost != "" && m.Store != nil {
		value, found, err := m.Store.VHostSecret(ctx, vhost, key)
		if err != nil {
			return "", fmt.Errorf("reading secret %q of vhost %q: %w", key, vhost, err)
		}
		if found {
			return value, nil
		}
	}
	if m.Global == nil {
		return "", nil
	}
	return m.Global.Get(ctx, key)
}
