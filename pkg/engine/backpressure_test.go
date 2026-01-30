package engine

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/buffer"
)

type slowSink struct {
	mu     sync.Mutex
	msgs   []hermod.Message
	delay  time.Duration
	writes int
}

func (s *slowSink) Write(ctx context.Context, msg hermod.Message) error {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()

	time.Sleep(s.delay)

	s.mu.Lock()
	s.msgs = append(s.msgs, msg)
	s.mu.Unlock()
	return nil
}

func (s *slowSink) Ping(ctx context.Context) error { return nil }
func (s *slowSink) Close() error                   { return nil }

func TestBackpressureDropOldest(t *testing.T) {
	source := &mockSourceWithLimit{limit: 20}
	sink := &slowSink{delay: 50 * time.Millisecond}
	rb := buffer.NewRingBuffer(100)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetSinkConfigs([]SinkConfig{
		{
			BackpressureStrategy: BPDropOldest,
			BackpressureBuffer:   5,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go eng.Start(ctx)

	// Wait for engine to process
	time.Sleep(1 * time.Second)

	sink.mu.Lock()
	defer sink.mu.Unlock()

	// 20 messages sent. Buffer size is 5. Sink is slow (50ms).
	// After ~1s, sink should have processed some, but many should have been dropped.
	// We just want to ensure it didn't block and finished.
	if len(sink.msgs) == 0 {
		t.Errorf("Expected some messages to reach the sink, got 0")
	}
	if len(sink.msgs) >= 20 {
		t.Logf("Warning: all messages reached sink, maybe it wasn't slow enough for 2s timeout. count: %d", len(sink.msgs))
	} else {
		t.Logf("Backpressure DropOldest: processed %d/20 messages", len(sink.msgs))
	}
}

func TestBackpressureDropNewest(t *testing.T) {
	source := &mockSourceWithLimit{limit: 20}
	sink := &slowSink{delay: 50 * time.Millisecond}
	rb := buffer.NewRingBuffer(100)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetSinkConfigs([]SinkConfig{
		{
			BackpressureStrategy: BPDropNewest,
			BackpressureBuffer:   5,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go eng.Start(ctx)

	time.Sleep(1 * time.Second)

	sink.mu.Lock()
	defer sink.mu.Unlock()

	if len(sink.msgs) == 0 {
		t.Errorf("Expected some messages to reach the sink, got 0")
	}
	t.Logf("Backpressure DropNewest: processed %d/20 messages", len(sink.msgs))
}

func TestBackpressureSampling(t *testing.T) {
	source := &mockSourceWithLimit{limit: 100}
	msink := &mockSink{received: make(chan hermod.Message, 100)} // Fast sink to isolate sampling
	rb := buffer.NewRingBuffer(200)

	eng := NewEngine(source, []hermod.Sink{msink}, rb)
	eng.SetSinkConfigs([]SinkConfig{
		{
			BackpressureStrategy: BPSampling,
			SamplingRate:         0.2, // Keep 20%
		},
	})

	// Start the engine in a goroutine and then stop it after some time
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go eng.Start(ctx)

	// Wait enough for 100 messages to be read and sampled
	time.Sleep(500 * time.Millisecond)
	cancel()

	count := len(msink.received)

	// Out of 100, we expect around 20 messages.
	// Allow some variance due to randomness.
	if count < 5 || count > 40 {
		t.Errorf("Sampling failed: expected ~20 messages, got %d", count)
	} else {
		t.Logf("Backpressure Sampling: processed %d/100 messages", count)
	}
}

func TestBackpressureSpillToDisk(t *testing.T) {
	source := &mockSourceWithLimit{limit: 50}
	sink := &slowSink{delay: 100 * time.Millisecond}
	rb := buffer.NewRingBuffer(100)

	spillDir := "test-spill"
	_ = os.RemoveAll(spillDir)
	defer os.RemoveAll(spillDir)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetSinkConfigs([]SinkConfig{
		{
			BackpressureStrategy: BPSpillToDisk,
			BackpressureBuffer:   5,
			SpillPath:            spillDir,
			SpillMaxSize:         1024 * 1024,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go eng.Start(ctx)

	// Wait for all messages to be read from source.
	// Since we spill to disk, source shouldn't block for long.
	time.Sleep(2 * time.Second)

	sink.mu.Lock()
	count := len(sink.msgs)
	sink.mu.Unlock()

	// Should have processed some and spilled the rest, eventually all should reach the sink if we wait.
	t.Logf("Backpressure SpillToDisk: processed %d messages so far", count)

	// Wait more for disk to drain
	time.Sleep(3 * time.Second)

	sink.mu.Lock()
	finalCount := len(sink.msgs)
	sink.mu.Unlock()

	t.Logf("Backpressure SpillToDisk: final processed %d/50 messages", finalCount)

	if finalCount < 20 {
		t.Errorf("Expected more messages to reach the sink via spill buffer, got %d", finalCount)
	}
}
