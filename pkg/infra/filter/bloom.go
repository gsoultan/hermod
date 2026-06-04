package filter

import (
	"hash/fnv"
	"sync"
)

// BloomFilter implements a thread-safe Bloom Filter for high-speed deduplication.
type BloomFilter struct {
	mu     sync.RWMutex
	bitset []uint64
	m      uint
	k      uint
}

// NewBloomFilter creates a new Bloom Filter with m bits and k hash functions.
func NewBloomFilter(m, k uint) *BloomFilter {
	return &BloomFilter{
		bitset: make([]uint64, (m+63)/64),
		m:      m,
		k:      k,
	}
}

// Add adds data to the filter.
func (f *BloomFilter) Add(data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	h1, h2 := f.baseHashes(data)
	for i := uint(0); i < f.k; i++ {
		idx := (uint(h1) + i*uint(h2)) % f.m
		f.bitset[idx/64] |= (1 << (idx % 64))
	}
}

// Test returns true if data might be in the filter.
func (f *BloomFilter) Test(data []byte) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	h1, h2 := f.baseHashes(data)
	for i := uint(0); i < f.k; i++ {
		idx := (uint(h1) + i*uint(h2)) % f.m
		if (f.bitset[idx/64] & (1 << (idx % 64))) == 0 {
			return false
		}
	}
	return true
}

// Reset clears the filter.
func (f *BloomFilter) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.bitset {
		f.bitset[i] = 0
	}
}

func (f *BloomFilter) baseHashes(data []byte) (uint64, uint64) {
	h := fnv.New64a()
	h.Write(data)
	s1 := h.Sum64()

	h2 := fnv.New64()
	h2.Write(data)
	s2 := h2.Sum64()

	return s1, s2
}
