package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/buffer"
)

type statusMockSource struct {
	hermod.Source
	pingErr error
}

func (m *statusMockSource) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *statusMockSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type statusMockSink struct {
	hermod.Sink
	pingErr error
}

func (m *statusMockSink) Ping(ctx context.Context) error {
	return m.pingErr
}

func TestEngineGranularStatus(t *testing.T) {
	src := &statusMockSource{}
	snk1 := &statusMockSink{}
	snk2 := &statusMockSink{}
	buf := buffer.NewRingBuffer(10)

	eng := NewEngine(src, []hermod.Sink{snk1, snk2}, buf)
	eng.SetIDs("conn-1", "src-1", []string{"snk-1", "snk-2"})
	eng.SetConfig(Config{
		ReconnectInterval: 10 * time.Millisecond,
		StatusInterval:    50 * time.Millisecond,
	})

	var mu sync.Mutex
	var lastUpdate StatusUpdate
	updateCount := 0

	eng.SetOnStatusChange(func(u StatusUpdate) {
		mu.Lock()
		lastUpdate = u
		updateCount++
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go eng.Start(ctx)

	// Initial status should be running (eventually)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if lastUpdate.EngineStatus != "running" {
		t.Errorf("Expected engine status running, got %s", lastUpdate.EngineStatus)
	}
	if lastUpdate.SourceStatus != "running" {
		t.Errorf("Expected source status running, got %s", lastUpdate.SourceStatus)
	}
	if lastUpdate.SinkStatuses["snk-1"] != "running" {
		t.Errorf("Expected sink-1 status running, got %s", lastUpdate.SinkStatuses["snk-1"])
	}
	mu.Unlock()

	// Simulate source failure
	src.pingErr = errors.New("source down")
	time.Sleep(150 * time.Millisecond) // Wait for status checker

	mu.Lock()
	if lastUpdate.SourceStatus != "reconnecting" {
		t.Errorf("Expected source status reconnecting, got %s", lastUpdate.SourceStatus)
	}
	if lastUpdate.EngineStatus != "reconnecting:source" {
		t.Errorf("Expected engine status reconnecting:source, got %s", lastUpdate.EngineStatus)
	}
	mu.Unlock()

	// Simulate sink failure
	src.pingErr = nil
	snk1.pingErr = errors.New("sink down")
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	if lastUpdate.SourceStatus != "running" {
		t.Errorf("Expected source status running, got %s", lastUpdate.SourceStatus)
	}
	if lastUpdate.SinkStatuses["snk-1"] != "reconnecting" {
		t.Errorf("Expected sink-1 status reconnecting, got %s", lastUpdate.SinkStatuses["snk-1"])
	}
	if lastUpdate.SinkStatuses["snk-2"] != "running" {
		t.Errorf("Expected sink-2 status running, got %s", lastUpdate.SinkStatuses["snk-2"])
	}
	if lastUpdate.EngineStatus != "reconnecting:sink:snk-1" {
		t.Errorf("Expected engine status reconnecting:sink:snk-1, got %s", lastUpdate.EngineStatus)
	}
	mu.Unlock()
}
