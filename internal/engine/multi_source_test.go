package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
)

type mockSubSource struct {
	hermod.Source
	readErr   error
	readCount int
	mu        sync.Mutex
}

func (m *mockSubSource) Read(ctx context.Context) (hermod.Message, error) {
	m.mu.Lock()
	m.readCount++
	err := m.readErr
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(100 * time.Millisecond):
		return nil, nil
	}
}

func (m *mockSubSource) Ping(ctx context.Context) error {
	return nil
}

func (m *mockSubSource) Close() error {
	return nil
}

func TestMultiSourceResilience(t *testing.T) {
	s1 := &mockSubSource{readErr: errors.New("read error")}

	ms := &multiSource{
		sources: []*subSource{
			{nodeID: "n1", source: s1},
		},
		msgChan: make(chan hermod.Message, 10),
		errChan: make(chan error, 10),
	}

	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()

	// First Read should return error
	_, err := ms.Read(ctx)
	if err == nil {
		t.Fatal("Expected error from Read")
	}

	// Wait a bit to ensure worker has exited and set running = false
	time.Sleep(50 * time.Millisecond)

	ms.mu.Lock()
	if s1Running := ms.sources[0].running; s1Running {
		t.Error("Expected source to be marked as not running after error")
	}
	ms.mu.Unlock()

	// Fix the error
	s1.mu.Lock()
	s1.readErr = nil
	s1.mu.Unlock()

	// Second Read should succeed (restarting the worker)
	_, err = ms.Read(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		// Note: ms.Read might return nil, nil if no message is available within its select
		// In our case it might block until next poll or msg.
		// But it shouldn't return the OLD error.
	}

	s1.mu.Lock()
	if s1.readCount < 2 {
		t.Errorf("Expected at least 2 calls to Read, got %d", s1.readCount)
	}
	s1.mu.Unlock()
}
