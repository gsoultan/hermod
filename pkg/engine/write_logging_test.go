package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// Per-message logging volume.
//
// The engine logged one INFO line for every message it successfully wrote to
// a sink. That is the event which, in a data integration platform, happens
// more than any other by construction — and logging it at INFO means the
// steady state of a healthy pipeline is an unbounded log stream.
//
// It was found by the soak: with -v, thirty-four seconds of the CI run
// produced 299,399 of these lines and 75 MB of log, at roughly 8,800 lines a
// second on a two-core runner, and killed the job. On the workstation, where
// the same soak sustains ~54,000 messages a second, the same code path would
// emit ~54,000 lines a second — of the order of a hundred gigabytes an hour,
// to say repeatedly that nothing unusual happened.
//
// A successful write is the definition of unremarkable, so it belongs at
// DEBUG, where an operator debugging one workflow can still turn it on.
// Failures, retries, reconnections and drops stay at their existing levels:
// those are the lines that should survive being read.
func TestASuccessfulWriteDoesNotLogPerMessageAtInfo(t *testing.T) {
	const messages = 50

	src := &countingSource{limit: messages}
	sink := &countingSink{done: make(chan struct{})}
	logger := &testLogger{}

	eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(64))
	eng.SetLogger(logger)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _ = eng.Start(ctx) }()

	select {
	case <-sink.done:
	case <-ctx.Done():
		t.Fatalf("only %d of %d messages were written before the deadline", sink.count(), messages)
	}

	logger.mu.Lock()
	defer logger.mu.Unlock()
	var perWrite int
	for _, m := range logger.infos {
		if strings.Contains(m, "Message written to sink") {
			perWrite++
		}
	}
	if perWrite > 0 {
		t.Errorf("%d INFO lines for %d successful writes: a healthy pipeline logs "+
			"one line per message, so its steady state is an unbounded log stream",
			perWrite, messages)
	}
}

// countingSource emits a fixed number of messages then blocks until the
// context ends, so the engine keeps running while the test asserts.
type countingSource struct {
	limit int
	sent  int
}

func (s *countingSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.sent >= s.limit {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.sent++
	m := message.AcquireMessage()
	m.SetID("m")
	m.SetPayload([]byte(`{"v":1}`))
	return m, nil
}

func (s *countingSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *countingSource) Ping(context.Context) error                { return nil }
func (s *countingSource) Close() error                              { return nil }

// countingSink closes done once it has accepted the expected number.
type countingSink struct {
	mu   sync.Mutex
	n    int
	done chan struct{}
}

func (s *countingSink) Write(context.Context, hermod.Message) error {
	s.mu.Lock()
	s.n++
	if s.n == 50 {
		close(s.done)
	}
	s.mu.Unlock()
	return nil
}

func (s *countingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func (s *countingSink) Ping(context.Context) error { return nil }
func (s *countingSink) Close() error               { return nil }
