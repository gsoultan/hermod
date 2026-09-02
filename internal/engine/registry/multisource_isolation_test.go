package registry

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
)

// isoSource is a sub-source whose behaviour is switchable at runtime so a test
// can break one member of a multi-source workflow and watch the others.
type isoSource struct {
	name    string
	failing atomic.Bool
	reads   atomic.Int64
	fails   atomic.Int64
	emitted atomic.Int64
	limit   int64 // 0 = unlimited
}

func (s *isoSource) Read(ctx context.Context) (hermod.Message, error) {
	s.reads.Add(1)
	if s.failing.Load() {
		s.fails.Add(1)
		return nil, errors.New(s.name + ": down")
	}
	if s.limit > 0 && s.emitted.Load() >= s.limit {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	n := s.emitted.Add(1)
	msg := message.AcquireMessage()
	msg.SetData("origin", s.name)
	msg.SetData("seq", n)
	return msg, nil
}

func (s *isoSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *isoSource) Ping(ctx context.Context) error                    { return nil }
func (s *isoSource) Close() error                                      { return nil }

func newIsoMultiSource(sources ...*isoSource) *multiSource {
	ms := &multiSource{
		msgChan: make(chan hermod.Message, 64),
		errChan: make(chan error, 8),
	}
	for _, s := range sources {
		ms.sources = append(ms.sources, &subSource{
			nodeID:   "node-" + s.name,
			sourceID: "src-" + s.name,
			source:   s,
		})
	}
	return ms
}

// TestMultiSourceHealthySiblingsKeepDeliveringWhileOnePeerIsDown is the core
// isolation contract. The engine drives one Read loop over the multiplexer, and
// a read error there makes it back off the *entire* workflow (see runner.go's
// "Source read error" path). So if a broken sub-source can win the race to be
// reported, one dead Kafka topic stalls a healthy Postgres CDC stream sharing
// the same workflow. Data that is already available must be preferred over a
// sibling's error.
func TestMultiSourceHealthySiblingsKeepDeliveringWhileOnePeerIsDown(t *testing.T) {
	broken := &isoSource{name: "broken"}
	broken.failing.Store(true)
	healthyA := &isoSource{name: "healthy-a"}
	healthyB := &isoSource{name: "healthy-b"}

	ms := newIsoMultiSource(broken, healthyA, healthyB)
	defer ms.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Drain as the engine would. Every message the healthy sources produce must
	// be readable without the broken peer's error interrupting the loop.
	const want = 50
	got := 0
	errs := 0
	deadline := time.Now().Add(10 * time.Second)
	for got < want && time.Now().Before(deadline) {
		msg, err := ms.Read(ctx)
		if err != nil {
			errs++
			if errs > want {
				t.Fatalf("read errors (%d) outnumbered delivered messages (%d): "+
					"a broken sub-source is starving its healthy peers", errs, got)
			}
			continue
		}
		if msg != nil {
			got++
			msg.Release()
		}
	}

	if got < want {
		t.Fatalf("delivered %d/%d messages from healthy sources in 10s (%d errors from the broken peer)",
			got, want, errs)
	}

	// Both healthy sources must get to run. Whichever goroutine is scheduled
	// first can fill the buffered channel on its own, so keep draining until
	// both have contributed rather than sampling at an arbitrary moment.
	bothRan := func() bool { return healthyA.emitted.Load() > 0 && healthyB.emitted.Load() > 0 }
	for deadline := time.Now().Add(10 * time.Second); !bothRan() && time.Now().Before(deadline); {
		msg, err := ms.Read(ctx)
		if err == nil && msg != nil {
			msg.Release()
		}
	}
	if !bothRan() {
		t.Errorf("one healthy source never ran: a=%d b=%d", healthyA.emitted.Load(), healthyB.emitted.Load())
	}
}

// TestMultiSourceBrokenPeerDoesNotHotSpin guards the CPU cost of isolation.
// Isolating a failing sub-source must not turn into an unthrottled retry loop:
// the sub-source reader exits on error and is respawned lazily, so without a
// per-source backoff a permanently dead source burns a core.
func TestMultiSourceBrokenPeerDoesNotHotSpin(t *testing.T) {
	broken := &isoSource{name: "broken"}
	broken.failing.Store(true)
	healthy := &isoSource{name: "healthy"}

	ms := newIsoMultiSource(broken, healthy)
	defer ms.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stop := time.Now().Add(2 * time.Second)
	for time.Now().Before(stop) {
		msg, err := ms.Read(ctx)
		if err == nil && msg != nil {
			msg.Release()
		}
	}

	// Two seconds of driving the multiplexer as hard as the engine can. With a
	// backoff the broken source is retried a handful of times; without one it is
	// retried on every single Read.
	const maxRetries = 200
	if n := broken.reads.Load(); n > maxRetries {
		t.Errorf("broken sub-source was retried %d times in 2s (limit %d); "+
			"it is hot-spinning instead of backing off", n, maxRetries)
	}
	if broken.reads.Load() == 0 {
		t.Error("broken sub-source was never retried; a recovered source would stay dead")
	}
}

// TestMultiSourceRecoveredPeerResumes proves the backoff is a delay, not a
// tombstone: a source that starts working again must rejoin the workflow.
func TestMultiSourceRecoveredPeerResumes(t *testing.T) {
	flaky := &isoSource{name: "flaky"}
	flaky.failing.Store(true)
	healthy := &isoSource{name: "healthy"}

	ms := newIsoMultiSource(flaky, healthy)
	defer ms.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Let it fail for a moment, then heal it.
	drain := func(d time.Duration) {
		stop := time.Now().Add(d)
		for time.Now().Before(stop) {
			msg, err := ms.Read(ctx)
			if err == nil && msg != nil {
				msg.Release()
			}
		}
	}
	drain(300 * time.Millisecond)
	flaky.failing.Store(false)

	deadline := time.Now().Add(10 * time.Second)
	for flaky.emitted.Load() == 0 && time.Now().Before(deadline) {
		msg, err := ms.Read(ctx)
		if err == nil && msg != nil {
			msg.Release()
		}
	}

	if flaky.emitted.Load() == 0 {
		t.Fatalf("recovered sub-source never resumed after %v (reads=%d fails=%d)",
			10*time.Second, flaky.reads.Load(), flaky.fails.Load())
	}
}

// TestMultiSourceReportsErrorWhenEverySourceIsDown keeps the isolation honest.
// Preferring data over errors must not swallow the error when there is no data:
// a workflow whose sources are all down has to surface that so the engine sets
// "reconnecting:source" and the status is not a silent lie.
func TestMultiSourceReportsErrorWhenEverySourceIsDown(t *testing.T) {
	a := &isoSource{name: "a"}
	a.failing.Store(true)
	b := &isoSource{name: "b"}
	b.failing.Store(true)

	ms := newIsoMultiSource(a, b)
	defer ms.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := ms.Read(ctx)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return // surfaced, as required
		}
		if msg != nil {
			msg.Release()
			t.Fatal("a multi-source with no working sources produced a message")
		}
	}
	t.Fatal("no source error was ever surfaced; the engine would never enter reconnect")
}

// idleSource returns (nil, nil): no message, no error. runner.go treats that as
// a legitimate "nothing right now" (it does `if m == nil { continue }`), so
// sources are entitled to do it.
type idleSource struct {
	reads  atomic.Int64
	closed atomic.Bool
}

func (s *idleSource) Read(ctx context.Context) (hermod.Message, error) {
	s.reads.Add(1)
	return nil, nil
}
func (s *idleSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *idleSource) Ping(ctx context.Context) error                    { return nil }
func (s *idleSource) Close() error                                      { s.closed.Store(true); return nil }

// TestMultiSourceReaderExitsOnCancel is the goroutine-leak contract for the
// per-source reader goroutines.
//
// The reader loop only returns on a read error or when a *send* of a real
// message loses the race to ctx.Done(). A source that yields (nil, nil) hits
// neither branch, so the goroutine keeps looping after the workflow is
// cancelled and is never reclaimed: one leaked goroutine per source per
// stop/start cycle, spinning on the CPU for the life of the process.
func TestMultiSourceReaderExitsOnCancel(t *testing.T) {
	before := runtime.NumGoroutine()

	const cycles = 20
	for range cycles {
		idle := &idleSource{}
		ms := newIsoMultiSource(&isoSource{name: "unused"})
		ms.sources = []*subSource{{nodeID: "n-idle", sourceID: "s-idle", source: idle}}

		ctx, cancel := context.WithCancel(context.Background())
		// Read blocks while the source stays silent, so drive it from its own
		// goroutine; the point of the test is the reader it spawns.
		done := make(chan struct{})
		go func() { defer close(done); _, _ = ms.Read(ctx) }()
		time.Sleep(5 * time.Millisecond)
		cancel()
		<-done
		_ = ms.Close()
	}

	// Give the runtime room to reap.
	deadline := time.Now().Add(10 * time.Second)
	after := runtime.NumGoroutine()
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		after = runtime.NumGoroutine()
		if after <= before+2 {
			break
		}
	}

	if after > before+2 {
		t.Errorf("goroutines grew from %d to %d after %d cancelled multi-source cycles; "+
			"sub-source readers are not exiting on cancellation", before, after, cycles)
	}
}
