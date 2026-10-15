// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package storage

import (
	"fmt"
	"testing"
)

// The dedup bookkeeping is keyed by message id, which a source supplies. It
// needs a bound and an eviction or a busy workflow grows it without limit.
func TestTraceDedupCacheIsBounded(t *testing.T) {
	c := NewTraceDedupCache(8)
	h := []byte("0123456789abcdef")
	for i := range 100 {
		c.MarkStored("wf", fmt.Sprintf("msg%d", i), h)
	}
	if n := c.Len(); n > 8 {
		t.Errorf("cache holds %d entries, cap is 8", n)
	}
	// The most recent entry is still there; the oldest is not.
	if !c.AlreadyStored("wf", "msg99", h) {
		t.Error("most recent entry evicted")
	}
	if c.AlreadyStored("wf", "msg0", h) {
		t.Error("oldest entry survived a 100-insert run through an 8-slot cache")
	}
}
