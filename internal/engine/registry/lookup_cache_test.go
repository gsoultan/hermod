// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package registry

import (
	"strconv"
	"testing"
	"time"
)

func TestLookupCache_SetGet(t *testing.T) {
	r := NewRegistry(nil)

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
	r := NewRegistry(nil)

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
	r := NewRegistry(nil)

	for i := range MaxLookupCacheSize + 100 {
		r.SetLookupCache("k"+strconv.Itoa(i), i, 0)
	}

	size, _ := r.GetLookupCacheSize()

	if size > MaxLookupCacheSize {
		t.Fatalf("lookup cache size = %d; want <= %d", size, MaxLookupCacheSize)
	}
}
