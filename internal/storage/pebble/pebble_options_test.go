// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
