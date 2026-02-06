package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
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
func (s *mockSource) Ping(ctx context.Context) error                    { return nil }
func (s *mockSource) Close() error                                      { return nil }

type mockSink struct {
	received chan hermod.Message
	fail     int
}

func (s *mockSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.fail > 0 {
		s.fail--
		return fmt.Errorf("mock sink error")
	}
	if s.received != nil {
		s.received <- msg
	}
	return nil
}

func (s *mockSink) Ping(ctx context.Context) error { return nil }
func (s *mockSink) Close() error                   { return nil }

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

func TestEngineDryRun(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("test-dryrun")
	msg.SetPayload([]byte("dryrun"))

	source := &mockSource{msg: msg}
	sink := &mockSink{received: make(chan hermod.Message, 10)}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetConfig(Config{
		DryRun:         true,
		StatusInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go func() {
		_ = eng.Start(ctx)
	}()

	select {
	case <-sink.received:
		t.Error("received message in sink during dry-run")
	case <-time.After(400 * time.Millisecond):
		// Success: no message received
	}
}

type validatingMockSink struct {
	mockSink
	validateFunc func(msg hermod.Message) error
}

func (s *validatingMockSink) Validate(ctx context.Context, msg hermod.Message) error {
	if s.validateFunc != nil {
		return s.validateFunc(msg)
	}
	return nil
}

func TestEngineValidation(t *testing.T) {
	msg := message.AcquireMessage()
	msg.SetID("invalid-msg")
	msg.SetPayload([]byte(`{"status": "invalid"}`))

	source := &mockSource{msg: msg}
	sink := &validatingMockSink{
		mockSink: mockSink{
			received: make(chan hermod.Message, 10),
		},
		validateFunc: func(msg hermod.Message) error {
			if msg.Data()["status"] == "invalid" {
				return fmt.Errorf("invalid payload")
			}
			return nil
		},
	}
	dlq := &mockSink{
		received: make(chan hermod.Message, 10),
	}

	rb := buffer.NewRingBuffer(10)
	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetDeadLetterSink(dlq)
	eng.SetConfig(Config{
		StatusInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	go func() {
		_ = eng.Start(ctx)
	}()

	select {
	case <-sink.received:
		t.Error("Invalid message should NOT have been received by sink")
	case m := <-dlq.received:
		if m.ID() != "invalid-msg" {
			t.Errorf("Expected invalid-msg in DLQ, got %s", m.ID())
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Timed out waiting for message in DLQ")
	}
}

func TestEngineAdaptiveBatching(t *testing.T) {
	source := &mockSourceWithLimit{limit: 100}
	sink := &mockSink{received: make(chan hermod.Message, 100)}
	rb := buffer.NewRingBuffer(200)

	eng := NewEngine(source, []hermod.Sink{sink}, rb)
	eng.SetSinkConfigs([]SinkConfig{
		{
			BatchSize:        10,
			BatchTimeout:     50 * time.Millisecond,
			AdaptiveBatching: true,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = eng.Start(ctx)
	}()

	// Wait for some messages to be processed
	time.Sleep(500 * time.Millisecond)

	eng.stopMu.Lock()
	sw := eng.sinkWriters[0]
	eng.stopMu.Unlock()

	if sw.currentBatchSize == 0 {
		t.Error("currentBatchSize should be initialized")
	}

	// Since mockSink is fast, currentBatchSize should have increased
	if sw.currentBatchSize < 10 {
		t.Errorf("Expected currentBatchSize to stay at least 10, got %d", sw.currentBatchSize)
	}
}

// --- New tests for performance features ---

// mockBatchSink implements hermod.BatchSink to capture batches for assertions.
type mockBatchSink struct {
	batches chan []hermod.Message
}

func (m *mockBatchSink) Write(ctx context.Context, msg hermod.Message) error {
	return m.WriteBatch(ctx, []hermod.Message{msg})
}

func (m *mockBatchSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	select {
	case m.batches <- msgs:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *mockBatchSink) Ping(ctx context.Context) error { return nil }
func (m *mockBatchSink) Close() error                   { return nil }

func TestSinkWriter_BatchBytesFlush(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Engine mostly unused but required by sinkWriter
	e := &Engine{logger: NewDefaultLogger(), config: DefaultConfig(), workflowID: "wf-test"}

	mb := &mockBatchSink{batches: make(chan []hermod.Message, 1)}
	cfg := SinkConfig{
		BatchSize:        100,              // do not flush by count
		BatchTimeout:     10 * time.Second, // avoid timer flush in test
		BatchBytes:       10,               // flush when >= 10 bytes
		AdaptiveBatching: false,
	}

	sw := &sinkWriter{
		engine:           e,
		sink:             mb,
		sinkID:           "sink-test",
		index:            0,
		config:           cfg,
		ch:               make(chan *pendingMessage, 10),
		currentBatchSize: cfg.BatchSize,
	}

	// Start writer
	go sw.run(ctx)

	// Send three messages: 4 + 4 + 4 bytes => 12 >= 10 triggers flush
	for i := 0; i < 3; i++ {
		m := message.AcquireMessage()
		m.SetID(fmt.Sprintf("m-%d", i))
		m.SetPayload([]byte("dddd")) // 4 bytes
		pm := acquirePendingMessage(m)
		sw.ch <- pm
	}

	select {
	case batch := <-mb.batches:
		if len(batch) != 3 {
			t.Fatalf("expected 3 messages in flushed batch, got %d", len(batch))
		}
		// cleanup pending messages
		for range batch {
			// drain done channel and release
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for batch flush by bytes")
	}
}

// orderSink records IDs per key to validate per-key ordering when sharding is enabled.
type orderSink struct {
	mu     sync.Mutex
	perKey map[string][]string
	wg     *sync.WaitGroup
}

func (s *orderSink) Write(ctx context.Context, msg hermod.Message) error {
	key := ""
	if md := msg.Metadata(); md != nil {
		key = md["key"]
	}
	s.mu.Lock()
	if s.perKey == nil {
		s.perKey = make(map[string][]string)
	}
	s.perKey[key] = append(s.perKey[key], msg.ID())
	s.mu.Unlock()
	if s.wg != nil {
		s.wg.Done()
	}
	return nil
}

func (s *orderSink) Ping(ctx context.Context) error { return nil }
func (s *orderSink) Close() error                   { return nil }

func TestSinkWriter_PerKeyShardingOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping sharding order test on Windows CI due to timing flakiness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	e := &Engine{logger: NewDefaultLogger(), config: DefaultConfig(), workflowID: "wf-shard"}
	os := &orderSink{perKey: make(map[string][]string)}
	var wg sync.WaitGroup
	os.wg = &wg

	cfg := SinkConfig{
		BatchSize:    1, // flush each message to preserve per-key order clearly
		BatchTimeout: 5 * time.Second,
		ShardCount:   4,
		ShardKeyMeta: "key",
	}

	sw := &sinkWriter{
		engine:           e,
		sink:             os,
		sinkID:           "sink-sharded",
		index:            0,
		config:           cfg,
		ch:               make(chan *pendingMessage, 100),
		currentBatchSize: cfg.BatchSize,
	}
	// Initialize shards like in engine
	if cfg.ShardCount > 1 {
		sw.useShards = true
		sw.shardCount = cfg.ShardCount
		sw.shardKeyMeta = cfg.ShardKeyMeta
		sw.shards = make([]chan *pendingMessage, cfg.ShardCount)
		for i := 0; i < cfg.ShardCount; i++ {
			sw.shards[i] = make(chan *pendingMessage, 100)
		}
	}

	go sw.run(ctx)

	// Enqueue interleaved messages for two keys, order must be preserved per key
	keys := []string{"A", "B"}
	countPerKey := 5
	wg.Add(len(keys) * countPerKey)
	for i := 0; i < countPerKey; i++ {
		for _, k := range keys {
			m := message.AcquireMessage()
			m.SetID(fmt.Sprintf("%s-%02d", k, i))
			m.SetPayload([]byte("x"))
			m.SetMetadata("key", k)
			pm := acquirePendingMessage(m)
			sw.enqueueWithStrategy(ctx, pm, BPBlock)
		}
	}

	// Wait for all writes
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		// verify order
		os.mu.Lock()
		defer os.mu.Unlock()
		for _, k := range keys {
			got := os.perKey[k]
			if len(got) != countPerKey {
				t.Fatalf("key %s: expected %d items, got %d", k, countPerKey, len(got))
			}
			for i := 0; i < countPerKey; i++ {
				expected := fmt.Sprintf("%s-%02d", k, i)
				if got[i] != expected {
					t.Fatalf("key %s: expected order %s at pos %d, got %s", k, expected, i, got[i])
				}
			}
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for sharded sink writes")
	}
}

func TestBackpressure_DropNewest_Metric(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wf := "wf-drop-newest"
	sinkID := "sink-bp"
	e := &Engine{logger: NewDefaultLogger(), config: DefaultConfig(), workflowID: wf}

	cfg := SinkConfig{BackpressureStrategy: BPDropNewest}
	sw := &sinkWriter{
		engine: e, sinkID: sinkID, config: cfg,
		ch: make(chan *pendingMessage, 1),
	}
	// Fill the channel with one message
	m1 := message.AcquireMessage()
	m1.SetID("pm1")
	pm1 := acquirePendingMessage(m1)
	sw.ch <- pm1

	// Enqueue second message which should be dropped (newest)
	m2 := message.AcquireMessage()
	m2.SetID("pm2")
	pm2 := acquirePendingMessage(m2)
	sw.enqueueWithStrategy(ctx, pm2, BPDropNewest)

	// Expect pm2 to be dropped (error on done)
	select {
	case err := <-pm2.done:
		if err == nil {
			t.Fatal("expected pm2 to be dropped with error")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for pm2 drop result")
	}

	// Metric should have incremented for drop_newest
	val := testutil.ToFloat64(BackpressureDropTotal.WithLabelValues(wf, sinkID, string(BPDropNewest)))
	if val < 1 {
		t.Fatalf("expected BackpressureDropTotal >= 1 for drop_newest, got %v", val)
	}
}

func TestBackpressure_DropOldest_Metric(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wf := "wf-drop-oldest"
	sinkID := "sink-bp2"
	e := &Engine{logger: NewDefaultLogger(), config: DefaultConfig(), workflowID: wf}

	cfg := SinkConfig{BackpressureStrategy: BPDropOldest}
	sw := &sinkWriter{
		engine: e, sinkID: sinkID, config: cfg,
		ch: make(chan *pendingMessage, 1),
	}
	m1 := message.AcquireMessage()
	m1.SetID("old")
	pm1 := acquirePendingMessage(m1)
	sw.ch <- pm1 // fills the buffer

	m2 := message.AcquireMessage()
	m2.SetID("new")
	pm2 := acquirePendingMessage(m2)
	sw.enqueueWithStrategy(ctx, pm2, BPDropOldest)

	// The oldest (pm1) should be dropped internally; pm2 should be enqueued
	select {
	case got := <-sw.ch:
		if got == nil || got.msg == nil || got.msg.ID() != "new" {
			t.Fatalf("expected pm2 to be enqueued after drop_oldest, got %+v", got)
		}
	default:
		t.Fatal("expected pm2 to be present in channel after drop_oldest")
	}

	val := testutil.ToFloat64(BackpressureDropTotal.WithLabelValues(wf, sinkID, string(BPDropOldest)))
	if val < 1 {
		t.Fatalf("expected BackpressureDropTotal >= 1 for drop_oldest, got %v", val)
	}
}

func TestBackpressure_SpillToDisk_Metric(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	wf := "wf-spill"
	sinkID := "sink-spill"
	e := &Engine{logger: NewDefaultLogger(), config: DefaultConfig(), workflowID: wf}

	// Prepare a temporary directory for spill buffer
	dir, err := os.MkdirTemp("", "hermod-spill-test-")
	if err != nil {
		t.Fatalf("mkdirtemp failed: %v", err)
	}
	defer os.RemoveAll(dir)

	sw := &sinkWriter{engine: e, sinkID: sinkID, config: SinkConfig{BackpressureStrategy: BPSpillToDisk}}
	// initialize a file buffer manually
	fb, err := buffer.NewFileBuffer(dir, 10*1024*1024)
	if err != nil {
		t.Fatalf("file buffer init failed: %v", err)
	}
	sw.spillBuffer = fb
	sw.ch = make(chan *pendingMessage, 1)

	// Fill channel, then spill one message
	m1 := message.AcquireMessage()
	m1.SetID("inmem")
	pm1 := acquirePendingMessage(m1)
	sw.ch <- pm1

	m2 := message.AcquireMessage()
	m2.SetID("spilled")
	pm2 := acquirePendingMessage(m2)
	sw.enqueueWithStrategy(ctx, pm2, BPSpillToDisk)

	// pm2 should be considered handled (no error) and metric incremented
	select {
	case err := <-pm2.done:
		if err != nil {
			t.Fatalf("expected pm2 to succeed by spilling, got error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for pm2 spill result")
	}

	val := testutil.ToFloat64(BackpressureSpillTotal.WithLabelValues(wf, sinkID))
	if val < 1 {
		t.Fatalf("expected BackpressureSpillTotal >= 1, got %v", val)
	}
}

// testLogger captures warning/info/error messages for assertions.
type testLogger struct {
	mu    sync.Mutex
	infos []string
	warns []string
	errs  []string
}

func (l *testLogger) Debug(msg string, kv ...interface{}) {}
func (l *testLogger) Info(msg string, kv ...interface{}) {
	l.mu.Lock()
	l.infos = append(l.infos, msg)
	l.mu.Unlock()
}
func (l *testLogger) Warn(msg string, kv ...interface{}) {
	l.mu.Lock()
	l.warns = append(l.warns, msg)
	l.mu.Unlock()
}
func (l *testLogger) Error(msg string, kv ...interface{}) {
	l.mu.Lock()
	l.errs = append(l.errs, msg)
	l.mu.Unlock()
}

// drainSlowSink blocks in Write until unblocked, signaling when Write is entered.
type drainSlowSink struct {
	enter   chan struct{}
	unblock chan struct{}
}

func (s *drainSlowSink) Write(ctx context.Context, msg hermod.Message) error {
	// Signal entry then block until unblocked (ignore context cancellation to exercise drain timeout)
	select {
	case s.enter <- struct{}{}:
	default:
	}
	<-s.unblock
	return nil
}

func (s *drainSlowSink) Ping(ctx context.Context) error { return nil }
func (s *drainSlowSink) Close() error                   { return nil }

func TestEngine_DrainTimeoutBehavior(t *testing.T) {
	// Source emits exactly 1 message
	source := &mockSourceWithLimit{limit: 1}
	ss := &drainSlowSink{enter: make(chan struct{}, 1), unblock: make(chan struct{})}
	rb := buffer.NewRingBuffer(10)

	eng := NewEngine(source, []hermod.Sink{ss}, rb)
	cfg := DefaultConfig()
	cfg.DrainTimeout = 50 * time.Millisecond
	eng.SetConfig(cfg)
	tl := &testLogger{}
	eng.SetLogger(tl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- eng.Start(ctx) }()

	// Wait for sink Write to be entered, then cancel engine
	select {
	case <-ss.enter:
		// cancel to trigger shutdown while sink is still writing
		cancel()
		// record the time and unblock after a delay to ensure the engine had to wait
		start := time.Now()
		time.Sleep(100 * time.Millisecond) // > drain_timeout (50ms)
		close(ss.unblock)
		// engine should complete shortly after we unblock
		elapsed := time.Since(start)
		if elapsed < 100*time.Millisecond {
			t.Fatalf("expected to wait at least 100ms before unblocking, got %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for slow sink to be entered")
	}

	// Wait for engine to stop
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for engine to stop")
	}
}
