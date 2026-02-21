package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/buffer"
	"github.com/user/hermod/pkg/engine"
)

type hangingSource struct {
	hermod.Source
	readCalled chan struct{}
}

func (s *hangingSource) Ping(ctx context.Context) error {
	return nil
}

func (s *hangingSource) Read(ctx context.Context) (hermod.Message, error) {
	s.readCalled <- struct{}{}
	// Simulate hang
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *hangingSource) Close() error {
	return nil
}

func TestEngineStatusWhenReadHangs(t *testing.T) {
	src := &hangingSource{readCalled: make(chan struct{}, 1)}
	buf := buffer.NewRingBuffer(10)
	eng := engine.NewEngine(src, []hermod.Sink{&mockSink{}}, buf)

	var status string
	var statusMu sync.Mutex
	eng.SetOnStatusChange(func(u engine.StatusUpdate) {
		statusMu.Lock()
		status = u.EngineStatus
		fmt.Printf("Status changed to: %s\n", u.EngineStatus)
		statusMu.Unlock()
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go eng.Start(ctx)

	// Wait for Read to be called
	select {
	case <-src.readCalled:
	case <-time.After(1 * time.Second):
		t.Fatal("Read was never called")
	}

	// Wait a bit to see if status remains "running"
	time.Sleep(100 * time.Millisecond)

	statusMu.Lock()
	if status != "running" {
		t.Errorf("Expected status to be running while Read is hanging, got %s", status)
	}
	statusMu.Unlock()
}

type flickeringSource struct {
	hermod.Source
	readErr error
	mu      sync.Mutex
}

func (s *flickeringSource) Ping(ctx context.Context) error {
	return nil // Ping always succeeds
}

func (s *flickeringSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil, s.readErr
}

func (s *flickeringSource) Close() error {
	return nil
}

func TestEngineStatusFlickering(t *testing.T) {
	src := &flickeringSource{readErr: errors.New("read error")}
	buf := buffer.NewRingBuffer(10)
	eng := engine.NewEngine(src, []hermod.Sink{&mockSink{}}, buf)
	eng.SetConfig(engine.Config{
		ReconnectInterval: 10 * time.Millisecond,
		StatusInterval:    100 * time.Millisecond,
	})

	statuses := []string{}
	var statusMu sync.Mutex
	eng.SetOnStatusChange(func(u engine.StatusUpdate) {
		statusMu.Lock()
		statuses = append(statuses, u.EngineStatus)
		fmt.Printf("Status changed to: %s\n", u.EngineStatus)
		statusMu.Unlock()
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go eng.Start(ctx)

	// Wait for some cycles
	time.Sleep(100 * time.Millisecond)

	statusMu.Lock()
	hasRunning := false
	hasReconnecting := false
	for _, s := range statuses {
		if s == "running" {
			hasRunning = true
		}
		if s == "reconnecting:source" {
			hasReconnecting = true
		}
	}
	statusMu.Unlock()

	if !hasRunning || !hasReconnecting {
		t.Errorf("Expected status to flicker between running and reconnecting:source, got %v", statuses)
	}
}
