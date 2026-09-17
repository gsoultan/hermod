package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// Per-key ordering, end to end.
//
// TestSinkWriter_PerKeyShardingOrder proves the sink writer keeps per-key order
// when sharding is on — but it enqueues straight into sw.ch, so it never sees
// the part that reorders. processMessage runs on MaxInflight (128) workers
// pulling from one shared channel with no key affinity, so by the time a message
// reaches the writer the order is already gone.
//
// Measured before this existed: ten changes to one row arrived at the sink as
// 010 005 006 007 004 002 001 003 008 009. For CDC that is an older UPDATE
// landing after a newer one, and the row ends up holding the wrong value.
// ---------------------------------------------------------------------------

// keyedSource emits `max` messages that all name the same row.
type keyedSource struct {
	mu   sync.Mutex
	n    int
	max  int
	keys []string // ordering key per message index; "" means unkeyed
}

func (s *keyedSource) Read(ctx context.Context) (hermod.Message, error) {
	s.mu.Lock()
	if s.n >= s.max {
		s.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.n++
	i := s.n
	s.mu.Unlock()

	m := message.AcquireMessage()
	m.SetID(fmt.Sprintf("%03d", i))
	if len(s.keys) > 0 {
		if k := s.keys[(i-1)%len(s.keys)]; k != "" {
			m.SetMetadata(hermod.MetaOrderingKey, k)
		}
	}
	return m, nil
}

func (s *keyedSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *keyedSource) Ping(context.Context) error                { return nil }
func (s *keyedSource) Close() error                              { return nil }

// arrivalSink records the order messages reach the sink, per ordering key. The
// first write is slow, which is what lets a later message overtake an earlier
// one when there is no affinity.
type arrivalSink struct {
	mu     sync.Mutex
	perKey map[string][]string
	slowed bool
}

func newArrivalSink() *arrivalSink {
	return &arrivalSink{perKey: make(map[string][]string)}
}

func (a *arrivalSink) Write(_ context.Context, m hermod.Message) error {
	a.mu.Lock()
	slow := !a.slowed
	a.slowed = true
	a.mu.Unlock()
	if slow {
		time.Sleep(150 * time.Millisecond)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.perKey[m.Metadata()[hermod.MetaOrderingKey]] = append(
		a.perKey[m.Metadata()[hermod.MetaOrderingKey]], m.ID())
	return nil
}

func (a *arrivalSink) Ping(context.Context) error { return nil }
func (a *arrivalSink) Close() error               { return nil }

func (a *arrivalSink) order(key string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.perKey[key]...)
}

func (a *arrivalSink) total() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, v := range a.perKey {
		n += len(v)
	}
	return n
}

// runKeyed drives the engine until it has seen `want` writes or times out.
func runKeyed(t *testing.T, src *keyedSource, snk *arrivalSink, want int) {
	t.Helper()

	eng := NewEngine(src, []hermod.Sink{snk}, buffer.NewRingBuffer(128))
	eng.SetLogger(&testLogger{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); _ = eng.Start(ctx) }()

	deadline := time.After(5 * time.Second)
	for snk.total() < want {
		select {
		case <-deadline:
			t.Fatalf("only %d of %d messages reached the sink", snk.total(), want)
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the engine did not stop")
	}
}

func sequential(ids []string) bool {
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			return false
		}
	}
	return true
}

// Changes to one row must reach the sink in the order the source produced them.
func TestChangesToOneRowKeepTheirOrderEndToEnd(t *testing.T) {
	const n = 12
	src := &keyedSource{max: n, keys: []string{"public.orders:42"}}
	snk := newArrivalSink()

	runKeyed(t, src, snk, n)

	got := snk.order("public.orders:42")
	if len(got) != n {
		t.Fatalf("the sink saw %d changes, want %d", len(got), n)
	}
	if !sequential(got) {
		t.Errorf("changes to one row arrived out of order: %v\n"+
			"for CDC this lands an older UPDATE after a newer one and the row keeps the wrong value", got)
	}
}

// Different rows must still run in parallel: affinity that serialises everything
// is just a single worker with extra steps.
func TestDifferentRowsStillRunInParallel(t *testing.T) {
	const n = 24
	keys := []string{"public.orders:1", "public.orders:2", "public.orders:3", "public.orders:4"}
	src := &keyedSource{max: n, keys: keys}
	snk := newArrivalSink()

	start := time.Now()
	runKeyed(t, src, snk, n)
	elapsed := time.Since(start)

	for _, k := range keys {
		if got := snk.order(k); !sequential(got) {
			t.Errorf("row %s arrived out of order: %v", k, got)
		}
	}
	// One write sleeps 150ms. Serialising every key behind it would take far
	// longer than this; running in parallel it is barely more than one sleep.
	if elapsed > 3*time.Second {
		t.Errorf("four independent rows took %s, which means they were serialised", elapsed)
	}
}

// A message with no ordering key (a queue, a cron tick, a batch row) has no row
// order to preserve, so it must not be pinned to one worker.
func TestUnkeyedMessagesAreNotSerialised(t *testing.T) {
	const n = 16
	src := &keyedSource{max: n} // no keys at all
	snk := newArrivalSink()

	start := time.Now()
	runKeyed(t, src, snk, n)
	elapsed := time.Since(start)

	if snk.total() != n {
		t.Fatalf("the sink saw %d messages, want %d", snk.total(), n)
	}
	if elapsed > 3*time.Second {
		t.Errorf("unkeyed messages took %s, which means affinity serialised traffic "+
			"that has no order to keep", elapsed)
	}
}

// The sink writer's shards must key on the row too. With no shard_key_meta
// named, pickShard used to fall through to the message ID — the LSN for CDC —
// so turning sharding on scattered the changes it was meant to keep together.
func TestShardingKeysOnTheRowWithoutBeingTold(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	sw := &sinkWriter{
		engine:     e,
		sinkID:     "sink-a",
		useShards:  true,
		shardCount: 8,
		shards:     make([]chan *pendingMessage, 8),
		ch:         make(chan *pendingMessage, 1),
	}
	for i := range sw.shards {
		sw.shards[i] = make(chan *pendingMessage, 1)
	}

	shardOf := func(id, key string) chan *pendingMessage {
		m := message.AcquireMessage()
		defer m.Release()
		m.SetID(id) // distinct per change, like an LSN
		if key != "" {
			m.SetMetadata(hermod.MetaOrderingKey, key)
		}
		return sw.pickShard(m)
	}

	// Three changes to one row, each with its own LSN-like ID.
	a := shardOf("0/16B2E48", "public.orders:42")
	b := shardOf("0/16B2F10", "public.orders:42")
	c := shardOf("0/16B3002", "public.orders:42")

	if a != b || b != c {
		t.Error("three changes to one row went to different shards, so the shard " +
			"that exists to keep them in order cannot")
	}
}
