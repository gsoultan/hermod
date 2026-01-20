package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/user/hermod"
)

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
func (m *parallelMockMessage) Data() map[string]interface{}  { return nil }
func (m *parallelMockMessage) SetMetadata(key, value string) {}
func (m *parallelMockMessage) Clone() hermod.Message         { return m }
func (m *parallelMockMessage) ClearPayloads()                {}

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

	ctx, cancel := context.WithCancel(context.Background())
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

	ctx, cancel := context.WithCancel(context.Background())
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
