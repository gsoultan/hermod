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
		if s.msg == nil {
			return nil, nil
		}
		return s.msg.Clone(), nil
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

type failPingSink struct {
	mockSink
}

func (s *failPingSink) Ping(ctx context.Context) error {
	return fmt.Errorf("sink ping failed")
}

func TestEngineSinkPreflightFail(t *testing.T) {
	source := &mockSource{}
	sink := &failPingSink{}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	err := eng.Start(context.Background())
	expectedErr := "sink pre-flight checks failed after 3 attempts"
	if err == nil || err.Error() != expectedErr {
		t.Errorf("expected %q, got %v", expectedErr, err)
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

func TestEnginePerSinkRetry(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("test-retry")
	msg.SetPayload([]byte("hello-retry"))

	source := &mockSource{msg: msg}
	sink1 := &mockSink{received: make(chan hermod.Message, 1), fail: 2} // Should succeed on 3rd attempt (j=2)
	sink2 := &mockSink{received: make(chan hermod.Message, 1), fail: 4} // Should fail with default 3 retries, but we'll set it to 5

	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink1, sink2}, rb)
	eng.SetSinkConfigs([]SinkConfig{
		{MaxRetries: 3, RetryInterval: 1 * time.Millisecond},
		{MaxRetries: 5, RetryInterval: 1 * time.Millisecond},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go func() {
		err := eng.Start(ctx)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			// Expected error might happen if context expires
		}
	}()

	// Wait for sink1
	select {
	case <-sink1.received:
		// Success
	case <-time.After(200 * time.Millisecond):
		t.Error("sink1 did not receive message in time")
	}

	// Wait for sink2
	select {
	case <-sink2.received:
		// Success
	case <-time.After(200 * time.Millisecond):
		t.Error("sink2 did not receive message in time (should have retried 5 times)")
	}
}

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

type mockSourceWithPing struct {
	msg       hermod.Message
	failPings int
	pingCount int
	mu        sync.Mutex
}

func (s *mockSourceWithPing) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return s.msg, nil
	}
}

func (s *mockSourceWithPing) Ack(ctx context.Context, msg hermod.Message) error { return nil }

func (s *mockSourceWithPing) Ping(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pingCount++
	if s.pingCount <= s.failPings {
		return fmt.Errorf("ping failed")
	}
	return nil
}

func (s *mockSourceWithPing) Close() error { return nil }

func TestEngineSourceReconnect(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("test-reconnect")
	msg.SetPayload([]byte("hello-reconnect"))

	// Fail first 2 pings
	source := &mockSourceWithPing{msg: msg, failPings: 2}
	sink := &mockSink{received: make(chan hermod.Message, 1)}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	// Set very short reconnect interval for test
	eng.SetSourceConfig(SourceConfig{ReconnectIntervals: []time.Duration{10 * time.Millisecond}})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go func() {
		_ = eng.Start(ctx)
	}()

	select {
	case <-sink.received:
		// Success
		source.mu.Lock()
		count := source.pingCount
		source.mu.Unlock()
		if count < 3 {
			t.Errorf("expected at least 3 pings, got %d", count)
		}
	case <-time.After(400 * time.Millisecond):
		t.Error("message not received after reconnection")
	}
}

func TestEngineSourceMultiReconnect(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("test-multi-reconnect")
	msg.SetPayload([]byte("hello-multi-reconnect"))

	// Fail first 3 pings
	source := &mockSourceWithPing{msg: msg, failPings: 3}
	sink := &mockSink{received: make(chan hermod.Message, 1)}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	// Set multiple reconnect intervals: 10ms, 20ms, 50ms
	// 1st fail -> wait 10ms
	// 2nd fail -> wait 20ms
	// 3rd fail -> wait 50ms
	// 4th success
	eng.SetSourceConfig(SourceConfig{
		ReconnectIntervals: []time.Duration{
			10 * time.Millisecond,
			20 * time.Millisecond,
			50 * time.Millisecond,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	go func() {
		_ = eng.Start(ctx)
	}()

	select {
	case <-sink.received:
		elapsed := time.Since(start)
		// Expected total wait: 10 + 20 + 50 = 80ms
		if elapsed < 80*time.Millisecond {
			t.Errorf("expected at least 80ms elapsed, got %v", elapsed)
		}
		source.mu.Lock()
		count := source.pingCount
		source.mu.Unlock()
		if count < 4 {
			t.Errorf("expected at least 4 pings, got %d", count)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("message not received after multiple reconnections")
	}
}

func TestEngineSinkMultiRetry(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("test-multi-retry")

	source := &mockSource{msg: msg}
	sink := &mockSink{received: make(chan hermod.Message, 1), fail: 3} // Fail 3 times, succeed on 4th
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetSinkConfigs([]SinkConfig{
		{
			MaxRetries: 5,
			RetryIntervals: []time.Duration{
				10 * time.Millisecond,
				20 * time.Millisecond,
				30 * time.Millisecond,
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	go func() {
		_ = eng.Start(ctx)
	}()

	select {
	case <-sink.received:
		elapsed := time.Since(start)
		// Expected wait: 10 + 20 + 30 = 60ms
		if elapsed < 60*time.Millisecond {
			t.Errorf("expected at least 60ms elapsed, got %v", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("message not received after multiple sink retries")
	}
}
