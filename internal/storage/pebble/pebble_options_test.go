package pebble

import (
	"testing"

	"github.com/cockroachdb/pebble"
)

func TestCacheBytesFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want int64
	}{
		{"Unset", "", defaultCacheBytes},
		{"Valid", "64", 64 << 20},
		{"Invalid", "abc", defaultCacheBytes},
		{"Zero", "0", defaultCacheBytes},
		{"Negative", "-1", defaultCacheBytes},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HERMOD_PEBBLE_CACHE_MB", tc.env)
			if got := cacheBytesFromEnv(); got != tc.want {
				t.Fatalf("cacheBytesFromEnv() = %d; want %d", got, tc.want)
			}
		})
	}
}

func TestNewPebbleOptions_Bounds(t *testing.T) {
	cache := pebble.NewCache(defaultCacheBytes)
	defer cache.Unref()

	opts := newPebbleOptions(cache)

	if opts.Cache != cache {
		t.Fatalf("Cache not wired into options")
	}
	if opts.MemTableSize != defaultMemTableBytes {
		t.Fatalf("MemTableSize = %d; want %d", opts.MemTableSize, defaultMemTableBytes)
	}
	if opts.MaxOpenFiles != defaultMaxOpenFiles {
		t.Fatalf("MaxOpenFiles = %d; want %d", opts.MaxOpenFiles, defaultMaxOpenFiles)
	}
	if opts.MaxConcurrentCompactions == nil || opts.MaxConcurrentCompactions() != 1 {
		t.Fatalf("MaxConcurrentCompactions should be bounded to 1")
	}
}
