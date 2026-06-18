package engine

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/buffer"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/engine/config"
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
			BackpressureStrategy: config.BPDropOldest,
			BackpressureBuffer:   5,
		},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
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
			BackpressureStrategy: config.BPDropNewest,
			BackpressureBuffer:   5,
		},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
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
			BackpressureStrategy: config.BPSampling,
			SamplingRate:         0.2, // Keep 20%
		},
	})

	// Start the engine in a goroutine and then stop it after some time
	ctx, cancel := context.WithCancel(t.Context())
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
			BackpressureStrategy: config.BPSpillToDisk,
			BackpressureBuffer:   5,
			SpillPath:            spillDir,
			SpillMaxSize:         1024 * 1024,
		},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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

func TestSinkWriter_CircuitBreaker(t *testing.T) {
	sink := &mockSink{received: make(chan hermod.Message, 10)}
	eng := NewEngine(nil, nil, nil)
	eng.logger = NewDefaultLogger()
	sw := &sinkWriter{
		engine: eng,
		sink:   sink,
		sinkID: "sink1",
		config: SinkConfig{
			CircuitBreakerThreshold: 2,
			CircuitBreakerInterval:  1 * time.Minute,
			CircuitBreakerCoolDown:  100 * time.Millisecond,
			BatchSize:               1,
			BatchTimeout:            10 * time.Millisecond,
		},
		ch: make(chan *pendingMessage, 10),
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go sw.run(ctx)

	// Trigger failure 1
	pm1 := acquirePendingMessage(message.AcquireMessage())
	sink.fail = 1
	sw.ch <- pm1
	<-pm1.done
	if st := sw.circuitState(); st != "closed" {
		t.Errorf("expected closed, got %s", st)
	}

	// Trigger failure 2 -> Open circuit
	pm2 := acquirePendingMessage(message.AcquireMessage())
	sink.fail = 1
	sw.ch <- pm2
	<-pm2.done
	if st := sw.circuitState(); st != "open" {
		t.Errorf("expected open, got %s", st)
	}

	// Send message while open -> should fail immediately without sink write
	pm3 := acquirePendingMessage(message.AcquireMessage())
	sw.ch <- pm3
	err := <-pm3.done
	if err == nil {
		t.Errorf("expected circuit breaker error, got nil")
	}

	// Wait for cool down
	time.Sleep(150 * time.Millisecond)

	// Send message -> should be half-open and success -> close
	pm4 := acquirePendingMessage(message.AcquireMessage())
	sw.ch <- pm4
	<-pm4.done
	if st := sw.circuitState(); st != "closed" {
		t.Errorf("expected closed after success in half-open, got %s", st)
	}
}

type parallelMockSink struct {
	mu     sync.Mutex
	writes int
	delay  time.Duration
	err    error
}

func (s *parallelMockSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return s.err
}

func (s *parallelMockSink) Ping(ctx context.Context) error { return nil }
func (s *parallelMockSink) Close() error                   { return nil }

type parallelMockSource struct {
	msg hermod.Message
	fed bool
}

func (s *parallelMockSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.fed {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.fed = true
	return s.msg, nil
}

func (s *parallelMockSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *parallelMockSource) Ping(ctx context.Context) error                    { return nil }
func (s *parallelMockSource) Close() error                                      { return nil }

type parallelMockMessage struct {
	id string
}

func (m *parallelMockMessage) ID() string                    { return m.id }
func (m *parallelMockMessage) Operation() hermod.Operation   { return hermod.OpCreate }
func (m *parallelMockMessage) Table() string                 { return "test" }
func (m *parallelMockMessage) Schema() string                { return "public" }
func (m *parallelMockMessage) Before() []byte                { return nil }
func (m *parallelMockMessage) After() []byte                 { return nil }
func (m *parallelMockMessage) Payload() []byte               { return nil }
func (m *parallelMockMessage) Metadata() map[string]string   { return nil }
func (m *parallelMockMessage) Data() map[string]any          { return nil }
func (m *parallelMockMessage) SetData(key string, value any) {}
func (m *parallelMockMessage) SetMetadata(key, value string) {}
func (m *parallelMockMessage) Clone() hermod.Message         { return m }
func (m *parallelMockMessage) ClearPayloads()                {}
func (m *parallelMockMessage) Retain()                       {}
func (m *parallelMockMessage) Release()                      {}

type parallelMockBuffer struct {
	ch     chan hermod.Message
	closed bool
}

func (b *parallelMockBuffer) Produce(ctx context.Context, msg hermod.Message) error {
	b.ch <- msg
	return nil
}

func (b *parallelMockBuffer) Consume(ctx context.Context, handler hermod.Handler) error {
	for msg := range b.ch {
		if err := handler(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

func (b *parallelMockBuffer) Close() error {
	if !b.closed {
		close(b.ch)
		b.closed = true
	}
	return nil
}

func TestParallelSinks(t *testing.T) {
	s1 := &parallelMockSink{delay: 200 * time.Millisecond}
	s2 := &parallelMockSink{delay: 200 * time.Millisecond}

	source := &parallelMockSource{msg: &parallelMockMessage{id: "1"}}
	buffer := &parallelMockBuffer{ch: make(chan hermod.Message, 1)}

	e := NewEngine(source, []hermod.Sink{s1, s2}, buffer)
	e.SetIDs("conn1", "src1", []string{"sink1", "sink2"})
	e.SetConfig(Config{MaxRetries: 3, StatusInterval: 1 * time.Second})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go e.Start(ctx)

	// Wait for processing - should take ~200ms if parallel, ~400ms if sequential
	start := time.Now()
	for {
		s1.mu.Lock()
		w1 := s1.writes
		s1.mu.Unlock()
		s2.mu.Lock()
		w2 := s2.writes
		s2.mu.Unlock()
		if w1 == 1 && w2 == 1 {
			break
		}
		if time.Since(start) > 1*time.Second {
			t.Fatal("Timeout waiting for sinks")
		}
		time.Sleep(10 * time.Millisecond)
	}
	duration := time.Since(start)

	if duration > 350*time.Millisecond {
		t.Errorf("Parallel execution failed, took too long: %v (likely sequential)", duration)
	}

	cancel()
}

func TestDeadLetterQueue(t *testing.T) {
	s1 := &parallelMockSink{err: errors.New("permanent failure")}
	dlq := &parallelMockSink{}

	source := &parallelMockSource{msg: &parallelMockMessage{id: "dlq-test"}}
	buffer := &parallelMockBuffer{ch: make(chan hermod.Message, 1)}

	e := NewEngine(source, []hermod.Sink{s1}, buffer)
	e.SetDeadLetterSink(dlq)
	e.SetIDs("conn-dlq", "src1", []string{"sink1"})
	// Set retries to 1 so it fails fast
	e.SetConfig(Config{MaxRetries: 1, RetryInterval: 10 * time.Millisecond, StatusInterval: 1 * time.Second})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go e.Start(ctx)

	// Wait for processing
	start := time.Now()
	for {
		dlq.mu.Lock()
		w := dlq.writes
		dlq.mu.Unlock()
		if w == 1 {
			break
		}
		if time.Since(start) > 1*time.Second {
			t.Fatal("Timeout waiting for DLQ")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
}
