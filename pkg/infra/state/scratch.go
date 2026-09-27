package state

import (
	"context"
	"slices"
	"sync"

	"github.com/gsoultan/hermod"
)

// NewScratchStore returns a state store that is empty when made and gone when
// dropped. A preview runs against one: a Join / Enrich that stores a record can
// have it looked up by a later step of the same preview, and nothing the
// preview writes reaches the state a running workflow keeps.
//
// It is a map rather than NewMemoryStore's SQLite ":memory:" database because
// a preview makes one per request, and StateStore has no Close to release a
// database handle with.
func NewScratchStore() hermod.StateStore {
	return &scratchStore{m: map[string][]byte{}}
}

type scratchStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *scratchStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return nil, nil
	}
	return slices.Clone(v), nil
}

func (s *scratchStore) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == nil {
		value = []byte{}
	}
	s.m[key] = slices.Clone(value)
	return nil
}

func (s *scratchStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}
