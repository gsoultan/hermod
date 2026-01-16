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

type reproMockSource struct {
	hermod.Source
	pingErr error
}

func (m *reproMockSource) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *reproMockSource) Read(ctx context.Context) (hermod.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *reproMockSource) Close() error { return nil }

type reproMockSink struct {
	hermod.Sink
}

func (m *reproMockSink) Ping(ctx context.Context) error                      { return nil }
func (m *reproMockSink) Write(ctx context.Context, msg hermod.Message) error { return nil }
func (m *reproMockSink) Close() error                                        { return nil }

func TestSinkStatusWhenSourceIsDown(t *testing.T) {
	src := &reproMockSource{pingErr: errors.New("source down")}
	snk := &reproMockSink{}
	buf := buffer.NewRingBuffer(10)

	eng := NewEngine(src, []hermod.Sink{snk}, buf)
	eng.SetIDs("conn-1", "src-1", []string{"snk-1"})
	eng.SetConfig(Config{
		ReconnectInterval: 10 * time.Millisecond,
		StatusInterval:    50 * time.Millisecond,
	})

	var mu sync.Mutex
	var updates []StatusUpdate
	receivedReconnecting := false

	eng.SetOnStatusChange(func(u StatusUpdate) {
		mu.Lock()
		updates = append(updates, u)
		if u.EngineStatus == "reconnecting:source" {
			receivedReconnecting = true
		}
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go eng.Start(ctx)

	// Wait for a few ticks of the status checker
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if !receivedReconnecting {
		t.Fatal("Never received reconnecting:source status")
	}

	// Find the latest update with reconnecting:source
	var lastReconnectingUpdate *StatusUpdate
	for i := len(updates) - 1; i >= 0; i-- {
		if updates[i].EngineStatus == "reconnecting:source" {
			lastReconnectingUpdate = &updates[i]
			break
		}
	}

	if lastReconnectingUpdate == nil {
		t.Fatal("Could not find reconnecting:source update in the list")
	}

	if lastReconnectingUpdate.SourceStatus != "reconnecting" {
		t.Errorf("Expected SourceStatus reconnecting, got %s", lastReconnectingUpdate.SourceStatus)
	}
	if lastReconnectingUpdate.SinkStatuses["snk-1"] != "running" {
		t.Errorf("Expected SinkStatus running for snk-1, got %s", lastReconnectingUpdate.SinkStatuses["snk-1"])
	}
}
