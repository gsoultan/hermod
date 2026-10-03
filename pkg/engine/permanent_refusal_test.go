package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

// scriptedRefuser fails every write with err and counts the attempts.
type scriptedRefuser struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (s *scriptedRefuser) Write(context.Context, hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}
func (s *scriptedRefuser) Ping(context.Context) error { return nil }
func (s *scriptedRefuser) Close() error               { return nil }

func (s *scriptedRefuser) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// parkingSink is a dead-letter sink that accepts and counts.
type parkingSink struct {
	mu     sync.Mutex
	parked int
}

func (s *parkingSink) Write(context.Context, hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parked++
	return nil
}
func (s *parkingSink) Ping(context.Context) error { return nil }
func (s *parkingSink) Close() error               { return nil }

var errTooBig = fmt.Errorf("payload is 4633 bytes, the limit is 4096: %w", hermod.ErrPermanent)

func engineWithRetries(t *testing.T, retries int) *Engine {
	t.Helper()
	eng := NewEngine(nil, nil, nil)
	eng.SetLogger(&testLogger{})
	cfg := config.DefaultConfig()
	cfg.MaxRetries = retries
	cfg.RetryInterval = time.Millisecond
	eng.SetConfig(cfg)
	return eng
}

// The engine retries a failed write max_retries times, waiting longer each
// time, before it parks the message. A refusal the sink has said is permanent
// is the same answer on every attempt: the wait only delays the dead letter,
// and for a dead FCM token every attempt is another real call to FCM.
func TestAPermanentRefusalGoesStraightToTheDeadLetterSink(t *testing.T) {
	sink := &scriptedRefuser{err: errTooBig}
	dlq := &parkingSink{}
	eng := engineWithRetries(t, 5)
	eng.SetDeadLetterSink(dlq)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("m-1")

	if err := eng.writeToSink(context.Background(), sink, msg, "sink-a", -1); err != nil {
		t.Fatalf("writeToSink = %v; the message was parked, which is the outcome", err)
	}
	if n := sink.attempts(); n != 1 {
		t.Errorf("the sink was asked %d times, want 1", n)
	}
	if dlq.parked != 1 {
		t.Errorf("the dead-letter sink took %d messages, want 1", dlq.parked)
	}
}

// Without a dead-letter sink the refusal comes back to the caller, still
// recognisably permanent, and the message is left unacknowledged — so the
// source redelivers it. The retry backoff was what paced that redelivery;
// returning at once would turn one unsendable row into a tight loop against
// the sink. The sink is asked once, and the wait is kept.
func TestAPermanentRefusalWithNoDeadLetterSinkKeepsThePace(t *testing.T) {
	sink := &scriptedRefuser{err: errTooBig}
	eng := NewEngine(nil, nil, nil)
	eng.SetLogger(&testLogger{})
	cfg := config.DefaultConfig()
	cfg.MaxRetries = 3
	cfg.RetryInterval = 20 * time.Millisecond
	eng.SetConfig(cfg)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)

	start := time.Now()
	err := eng.writeToSink(context.Background(), sink, msg, "sink-a", -1)
	elapsed := time.Since(start)

	if !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("writeToSink = %v, want an error that is still permanent", err)
	}
	if n := sink.attempts(); n != 1 {
		t.Errorf("the sink was asked %d times, want 1", n)
	}
	// 20ms + 40ms + 60ms of backoff, less the 20% jitter it may take off.
	if floor := 96 * time.Millisecond; elapsed < floor {
		t.Errorf("returned after %v, under the %v the retries would have taken; a redelivering source would spin", elapsed, floor)
	}
}

// With a dead-letter sink there is nothing to pace: the message is parked and
// the source moves on.
func TestAPermanentRefusalIsParkedWithoutWaiting(t *testing.T) {
	sink := &scriptedRefuser{err: errTooBig}
	eng := NewEngine(nil, nil, nil)
	eng.SetLogger(&testLogger{})
	cfg := config.DefaultConfig()
	cfg.MaxRetries = 3
	cfg.RetryInterval = time.Second
	eng.SetConfig(cfg)
	eng.SetDeadLetterSink(&parkingSink{})

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)

	start := time.Now()
	if err := eng.writeToSink(context.Background(), sink, msg, "sink-a", -1); err != nil {
		t.Fatalf("writeToSink = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("parking took %v; it waited out a backoff meant for a retry that never happens", elapsed)
	}
}

// The control for both: an ordinary failure still spends the whole budget.
func TestATransientFailureIsStillRetried(t *testing.T) {
	sink := &scriptedRefuser{err: errors.New("connection reset")}
	eng := engineWithRetries(t, 4)
	eng.SetDeadLetterSink(&parkingSink{})

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)

	_ = eng.writeToSink(context.Background(), sink, msg, "sink-a", -1)
	if n := sink.attempts(); n != 4 {
		t.Errorf("the sink was asked %d times, want 4", n)
	}
}

// The circuit breaker exists to stop hammering a sink that is down. A push to
// an uninstalled app's token is refused by a perfectly healthy FCM, and a table
// of device registrations has plenty of those: counted as failures, a run of
// stale tokens opened the breaker and stopped every message to every live
// device behind them.
func TestPermanentRefusalsDoNotOpenTheCircuitBreaker(t *testing.T) {
	run := func(t *testing.T, refusal error) string {
		t.Helper()
		eng := engineWithRetries(t, 1)
		sw := &sinkWriter{
			engine: eng,
			sink:   &scriptedRefuser{err: refusal},
			sinkID: "sink1",
			config: SinkConfig{
				CircuitBreakerThreshold: 2,
				CircuitBreakerInterval:  time.Minute,
				CircuitBreakerCoolDown:  time.Minute,
				BatchSize:               1,
				BatchTimeout:            10 * time.Millisecond,
			},
			ch: make(chan *pendingMessage, 10),
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go sw.run(ctx)

		for range 3 {
			pm := acquirePendingMessage(message.AcquireMessage())
			sw.ch <- pm
			<-pm.done
		}
		return sw.circuitState()
	}

	if st := run(t, errTooBig); st != "closed" {
		t.Errorf("three permanent refusals left the breaker %s; they say nothing about the sink's health", st)
	}
	// The control: the same three as ordinary failures do open it.
	if st := run(t, errors.New("connection reset")); st != "open" {
		t.Errorf("three ordinary failures left the breaker %s, want open", st)
	}
}
