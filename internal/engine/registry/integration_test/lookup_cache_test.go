package registry

import (
	"github.com/user/hermod/internal/engine/registry"
	"strconv"
	"testing"
	"time"
)

func TestLookupCache_SetGet(t *testing.T) {
	r := registry.NewRegistry(nil)

	r.SetLookupCache("a", 123, 0)
	got, ok := r.GetLookupCache("a")
	if !ok {
		t.Fatalf("expected key 'a' to be present")
	}
	if got != 123 {
		t.Fatalf("GetLookupCache(a) = %v; want 123", got)
	}

	if _, ok := r.GetLookupCache("missing"); ok {
		t.Fatalf("expected miss for absent key")
	}
}

func TestLookupCache_TTLExpiry(t *testing.T) {
	r := registry.NewRegistry(nil)

	r.SetLookupCache("temp", "v", 20*time.Millisecond)
	if _, ok := r.GetLookupCache("temp"); !ok {
		t.Fatalf("expected key to be present before expiry")
	}

	time.Sleep(40 * time.Millisecond)

	if _, ok := r.GetLookupCache("temp"); ok {
		t.Fatalf("expected key to be evicted after TTL")
	}

	// Confirm the expired entry was actually removed from the map (no leak).
	r.lookupCacheMu.RLock()
	_, present := r.lookupCache["temp"]
	r.lookupCacheMu.RUnlock()
	if present {
		t.Fatalf("expired entry should have been deleted lazily on read")
	}
}

func TestLookupCache_SizeBound(t *testing.T) {
	r := registry.NewRegistry(nil)

	for i := range registry.MaxLookupCacheSize + 100 {
		r.SetLookupCache("k"+strconv.Itoa(i), i, 0)
	}

	size, _ := r.GetLookupCacheSize()

	if size > registry.MaxLookupCacheSize {
		t.Fatalf("lookup cache size = %d; want <= %d", size, registry.MaxLookupCacheSize)
	}
}
