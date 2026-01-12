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
	"github.com/user/hermod/pkg/message"
)

type mockSource struct {
	msg hermod.Message
}

func (s *mockSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return s.msg, nil
	}
}

func (s *mockSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }

func (s *mockSource) Ping(ctx context.Context) error { return nil }

func (s *mockSource) Close() error { return nil }

type mockSink struct {
	received chan hermod.Message
	fail     int
}

func (s *mockSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.fail > 0 {
		s.fail--
		return fmt.Errorf("mock sink error")
	}
	s.received <- msg
	return nil
}

func (s *mockSink) Ping(ctx context.Context) error { return nil }

func (s *mockSink) Close() error { return nil }

type mockSourceWithLimit struct {
	limit int
	count int
	mu    sync.Mutex
	onAck func()
}

func (s *mockSourceWithLimit) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count >= s.limit {
		// Block until context is done to simulate no more messages
		s.mu.Unlock()
		<-ctx.Done()
		s.mu.Lock()
		return nil, ctx.Err()
	}
	s.count++
	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("msg-%d", s.count))
	return msg, nil
}

func (s *mockSourceWithLimit) Ack(ctx context.Context, msg hermod.Message) error {
	if s.onAck != nil {
		s.onAck()
	}
	return nil
}

func (s *mockSourceWithLimit) Ping(ctx context.Context) error { return nil }

func (s *mockSourceWithLimit) Close() error { return nil }

func TestEngine(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("test-1")
	msg.SetPayload([]byte("hello"))

	source := &mockSource{msg: msg}
	sink := &mockSink{received: make(chan hermod.Message, 1)}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go func() {
		err := eng.Start(ctx)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("engine error: %v", err)
		}
	}()

	select {
	case received := <-sink.received:
		if received.ID() != "test-1" {
			t.Errorf("expected ID test-1, got %s", received.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for message")
	}
}

func TestEngineRetry(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("test-retry")
	msg.SetPayload([]byte("hello retry"))

	source := &mockSource{msg: msg}
	sink := &mockSink{received: make(chan hermod.Message, 1), fail: 2}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetConfig(Config{
		MaxRetries:    3,
		RetryInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go func() {
		_ = eng.Start(ctx)
	}()

	select {
	case received := <-sink.received:
		if received.ID() != "test-retry" {
			t.Errorf("expected ID test-retry, got %s", received.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for message with retries")
	}
}

type failPingSource struct {
	mockSource
}

func (s *failPingSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }

func (s *failPingSource) Ping(ctx context.Context) error {
	return fmt.Errorf("ping failed")
}

func TestEnginePreflightFail(t *testing.T) {
	source := &failPingSource{}
	sink := &mockSink{}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	err := eng.Start(context.Background())
	if err == nil || err.Error() != "source ping failed: ping failed" {
		t.Errorf("expected ping failure error, got %v", err)
	}
}

type slowMockSource struct {
	messages []hermod.Message
	mu       sync.Mutex
}

func (s *slowMockSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	msg := s.messages[0]
	s.messages = s.messages[1:]
	return msg, nil
}

func (s *slowMockSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }

func (s *slowMockSource) Ping(ctx context.Context) error { return nil }
func (s *slowMockSource) Close() error                   { return nil }

type counterSink struct {
	receivedCount int
	mu            sync.Mutex
}

func (s *counterSink) Write(ctx context.Context, msg hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Simulate some work
	time.Sleep(10 * time.Millisecond)
	s.receivedCount++
	return nil
}

func (s *counterSink) Ping(ctx context.Context) error { return nil }
func (s *counterSink) Close() error                   { return nil }

func TestEngineAck(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("test-ack")
	msg.SetPayload([]byte("hello ack"))

	ackCalled := make(chan string, 1)
	source := &ackMockSource{
		mockSource: mockSource{msg: msg},
		ackCalled:  ackCalled,
	}
	sink := &mockSink{received: make(chan hermod.Message, 1)}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go func() {
		_ = eng.Start(ctx)
	}()

	select {
	case id := <-ackCalled:
		if id != "test-ack" {
			t.Errorf("expected ack for test-ack, got %s", id)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for ack")
	}
}

type ackMockSource struct {
	mockSource
	ackCalled chan string
}

func (s *ackMockSource) Ack(ctx context.Context, msg hermod.Message) error {
	s.ackCalled <- msg.ID()
	return nil
}

func TestEngineGracefulShutdown(t *testing.T) {
	numMessages := 10
	messages := make([]hermod.Message, numMessages)
	for i := 0; i < numMessages; i++ {
		m := message.AcquireMessage()
		m.SetID(fmt.Sprintf("%d", i))
		messages[i] = m
	}

	source := &slowMockSource{messages: messages}
	sink := &counterSink{}
	rb := buffer.NewRingBuffer(20)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- eng.Start(ctx)
	}()

	// Wait for some messages to be read into the buffer
	time.Sleep(100 * time.Millisecond)

	// Suddenly shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Engine stopped with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Engine took too long to stop")
	}

	sink.mu.Lock()
	count := sink.receivedCount
	sink.mu.Unlock()

	if count < numMessages {
		t.Errorf("Data loss detected: sent %d messages, but sink received only %d", numMessages, count)
	}
}

func TestEngineMultiSink(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("test-multi-1")
	msg.SetPayload([]byte("hello multi-sink"))

	source := &mockSource{msg: msg}
	sink1 := &mockSink{received: make(chan hermod.Message, 1)}
	sink2 := &mockSink{received: make(chan hermod.Message, 1)}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink1, sink2}, rb)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go func() {
		_ = eng.Start(ctx)
	}()

	var received1, received2 bool
	for i := 0; i < 2; i++ {
		select {
		case m := <-sink1.received:
			if m.ID() != "test-multi-1" {
				t.Errorf("sink1: expected test-multi-1, got %s", m.ID())
			}
			received1 = true
		case m := <-sink2.received:
			if m.ID() != "test-multi-1" {
				t.Errorf("sink2: expected test-multi-1, got %s", m.ID())
			}
			received2 = true
		case <-ctx.Done():
		}
	}

	if !received1 {
		t.Error("sink1 did not receive message")
	}
	if !received2 {
		t.Error("sink2 did not receive message")
	}
}
