package secrets

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// mapVHostStore holds secrets per vhost, the way the storage backends do.
type mapVHostStore struct {
	secrets map[string]map[string]string
	err     error
}

func (s mapVHostStore) VHostSecret(_ context.Context, vhost, name string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.secrets[vhost][name]
	return v, ok, nil
}

// mapManager is a global secret manager answering from a map.
type mapManager map[string]string

func (m mapManager) Get(_ context.Context, key string) (string, error) { return m[key], nil }

// A workflow reads its own vhost's secrets first and the global manager after:
// a vhost can hold its own API_KEY, override a shared one, and still use the
// HERMOD_SECRET_ variables and Vault entries it used before. It never reads
// another vhost's.
func TestVHostManagerReadsTheVHostThenTheGlobalManager(t *testing.T) {
	mgr := &VHostManager{
		Store: mapVHostStore{secrets: map[string]map[string]string{
			"tenant-a": {"API_KEY": "a-key", "SHARED": "a-override"},
			"tenant-b": {"API_KEY": "b-key", "ONLY_B": "b-only"},
		}},
		Global: mapManager{"SHARED": "global-shared", "GLOBAL_ONLY": "global-only"},
	}

	tests := []struct {
		name  string
		vhost string
		key   string
		want  string
	}{
		{"its own secret", "tenant-a", "API_KEY", "a-key"},
		{"the same name in another vhost", "tenant-b", "API_KEY", "b-key"},
		{"a vhost overrides a global secret", "tenant-a", "SHARED", "a-override"},
		{"a vhost without it falls back to global", "tenant-b", "SHARED", "global-shared"},
		{"a global-only secret", "tenant-a", "GLOBAL_ONLY", "global-only"},
		{"another vhost's secret is not visible", "tenant-a", "ONLY_B", ""},
		{"no vhost reads global only", "", "API_KEY", ""},
		{"no vhost still reads global", "", "GLOBAL_ONLY", "global-only"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mgr.GetScoped(t.Context(), tc.vhost, tc.key)
			if err != nil {
				t.Fatalf("GetScoped(%q, %q): %v", tc.vhost, tc.key, err)
			}
			if got != tc.want {
				t.Errorf("GetScoped(%q, %q) = %q, want %q", tc.vhost, tc.key, got, tc.want)
			}
		})
	}
}

// A store that cannot answer is an error, not a fall-through: the global
// manager may hold a different value under the same name, and sending that one
// would be worse than sending nothing.
func TestVHostManagerDoesNotFallBackWhenTheStoreFails(t *testing.T) {
	mgr := &VHostManager{
		Store:  mapVHostStore{err: errors.New("database is down")},
		Global: mapManager{"API_KEY": "global-key"},
	}
	got, err := mgr.GetScoped(t.Context(), "tenant-a", "API_KEY")
	if err == nil || got != "" {
		t.Errorf("GetScoped = %q, %v; want an error and no value", got, err)
	}
}

// scopedCounting answers per vhost and counts lookups.
type scopedCounting struct {
	values map[string]string // "vhost/key" -> value
	calls  atomic.Int64
}

func (m *scopedCounting) Get(ctx context.Context, key string) (string, error) {
	return m.GetScoped(ctx, "", key)
}

func (m *scopedCounting) GetScoped(_ context.Context, vhost, key string) (string, error) {
	m.calls.Add(1)
	return m.values[vhost+"/"+key], nil
}

// The cache is keyed by who asked. Keyed by name alone, the first vhost to read
// API_KEY would decide what every other vhost got for a minute.
func TestCachedManagerKeepsEachVHostsAnswerApart(t *testing.T) {
	inner := &scopedCounting{values: map[string]string{"tenant-a/API_KEY": "a-key", "tenant-b/API_KEY": "b-key"}}
	c := NewCachedManager(inner, time.Minute, 8, time.Second)

	for range 2 {
		for vhost, want := range map[string]string{"tenant-a": "a-key", "tenant-b": "b-key", "": ""} {
			got, err := c.GetScoped(t.Context(), vhost, "API_KEY")
			if err != nil || got != want {
				t.Fatalf("GetScoped(%q) = %q, %v; want %q", vhost, got, err, want)
			}
		}
	}
	if n := inner.calls.Load(); n != 3 {
		t.Errorf("3 vhosts read twice made %d lookups, want 3", n)
	}
}

// Rotating a secret takes effect at once, not when the entry expires.
func TestCachedManagerForgetsAnInvalidatedSecret(t *testing.T) {
	inner := &scopedCounting{values: map[string]string{"tenant-a/API_KEY": "old", "tenant-b/API_KEY": "b-key"}}
	c := NewCachedManager(inner, time.Minute, 8, time.Second)

	_, _ = c.GetScoped(t.Context(), "tenant-a", "API_KEY")
	_, _ = c.GetScoped(t.Context(), "tenant-b", "API_KEY")
	inner.values["tenant-a/API_KEY"] = "new"
	c.Invalidate("tenant-a", "API_KEY")

	if got, _ := c.GetScoped(t.Context(), "tenant-a", "API_KEY"); got != "new" {
		t.Errorf("after Invalidate tenant-a reads %q, want new", got)
	}
	_, _ = c.GetScoped(t.Context(), "tenant-b", "API_KEY")
	if n := inner.calls.Load(); n != 3 {
		t.Errorf("%d lookups, want 3: invalidating tenant-a must not drop tenant-b", n)
	}
}
