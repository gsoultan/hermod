// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package filter

import "sync"

// RotatingBloomFilter uses two Bloom Filters to provide a sliding window of deduplication.
type RotatingBloomFilter struct {
	mu       sync.RWMutex
	current  *BloomFilter
	previous *BloomFilter
	count    uint
	limit    uint
	m        uint
	k        uint
}

// NewRotatingBloomFilter creates a new RotatingBloomFilter that rotates after limit additions.
func NewRotatingBloomFilter(m, k, limit uint) *RotatingBloomFilter {
	return &RotatingBloomFilter{
		current: NewBloomFilter(m, k),
		limit:   limit,
		m:       m,
		k:       k,
	}
}

// Add adds data to the current filter and rotates if limit is reached.
func (f *RotatingBloomFilter) Add(data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.count >= f.limit {
		f.rotate()
	}

	f.current.Add(data)
	f.count++
}

// Test checks both current and previous filters.
func (f *RotatingBloomFilter) Test(data []byte) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.current.Test(data) {
		return true
	}
	if f.previous != nil && f.previous.Test(data) {
		return true
	}
	return false
}

// Reset clears both filters.
func (f *RotatingBloomFilter) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current.Reset()
	f.previous = nil
	f.count = 0
}

func (f *RotatingBloomFilter) rotate() {
	f.previous = f.current
	f.current = NewBloomFilter(f.m, f.k)
	f.count = 0
}
