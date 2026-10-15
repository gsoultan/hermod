// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
