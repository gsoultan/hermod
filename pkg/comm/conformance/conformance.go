// Package conformance provides a shared contract suite that every Hermod
// source and sink must satisfy.
//
// Why this exists: the connector layer is the part of Hermod a user actually
// touches, and it was also the least tested — most connector packages had no
// tests at all. Per-connector test files would not have fixed that, because the
// interesting properties are the same for all of them and nobody writes the
// same six tests forty times.
//
// So the properties live here once, and a connector opts in with a single line
// in the registry (see connectors_test.go).
//
// SCOPE: these tests run with no live infrastructure. They cover the contract a
// connector must honour regardless of whether a server is reachable — lifecycle,
// nil-safety, context deadlines, and behaviour after Close. They deliberately do
// NOT cover data-path correctness, which needs real infrastructure and lives in
// the integration tests behind the HERMOD_INTEGRATION build tag.
//
// That boundary is the point: a connector passing this suite is not "verified",
// it is merely "not obviously broken before it ever reaches the network".
package conformance

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

const (
	// ctxDeadline is the deadline given to every operation under test. It
	// stands in for the bounded contexts the engine and the readiness probes
	// actually pass.
	ctxDeadline = 2 * time.Second

	// returnBudget is how long past its deadline an operation may take to
	// return. Generous on purpose: the point is to catch connectors that ignore
	// the context entirely and fall back to a driver default of 30s or more,
	// not to police scheduling jitter.
	returnBudget = 8 * time.Second

	// closeBudget bounds how long the suite waits on Close before moving on.
	closeBudget = 5 * time.Second
)

// Options configures a suite run.
type Options struct {
	// KnownGaps maps an operation name ("Read", "Ping", "Write") to the reason
	// that operation currently ignores its context deadline.
	//
	// This is a ratchet, modelled on this repository's golangci-lint config: a
	// gate that can never be green is a gate everyone learns to ignore. A listed
	// operation that still fails is reported and skipped rather than failing the
	// build — but an UNLISTED operation that fails is a hard failure, and a
	// LISTED operation that now passes is ALSO a hard failure, telling you to
	// delete the entry.
	//
	// So the list can only shrink. New context leaks cannot get in, and fixed
	// ones cannot sit on the list pretending to still be broken.
	KnownGaps map[string]string
}

func firstOpt(opts []Options) Options {
	if len(opts) == 0 {
		return Options{}
	}
	return opts[0]
}

// boundedCtx returns the context every operation under test receives.
func boundedCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), ctxDeadline)
	t.Cleanup(cancel)
	return ctx
}

// assertReturnsWithin runs op with a bounded context and fails if op has not
// returned returnBudget later, or if it panics.
//
// This is the single most valuable property in the suite. A connector that
// ignores its context does not fail visibly — it hangs, holding a goroutine and
// a connection, and turns a bounded shutdown into a hard kill.
func assertReturnsWithin(t *testing.T, o Options, op string, fn func(context.Context)) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), ctxDeadline)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer guardPanic(t, op)
		fn(ctx)
	}()

	reason, known := o.KnownGaps[op]

	select {
	case <-done:
		if known {
			t.Fatalf("%s is listed in KnownGaps (%q) but now respects its context deadline. "+
				"Remove the entry so the gap cannot silently come back.", op, reason)
		}

	case <-time.After(returnBudget):
		msg := fmt.Sprintf("%s ignored a %s context deadline and had not returned after %s; "+
			"it is using its driver's default timeout instead of the caller's",
			op, ctxDeadline, returnBudget)
		if known {
			// Reported, not hidden: the run stays green but the gap is visible
			// in the test log alongside its reason.
			t.Skipf("KNOWN GAP: %s (%s)", msg, reason)
		} else {
			t.Error(msg)
		}
	}
}

// RunSinkSuite runs the sink contract against newSink, which must return a
// fresh sink that has NOT connected to anything. Each property gets its own
// instance, so one property's Close cannot affect the next.
func RunSinkSuite(t *testing.T, name string, newSink func() hermod.Sink, opts ...Options) {
	t.Helper()
	o := firstOpt(opts)

	t.Run(name+"/CloseIsIdempotent", func(t *testing.T) {

		s := newSink()
		defer guardPanic(t, "Close")

		_ = s.Close()
		// A second Close must not panic. Double-close happens routinely:
		// shutdown races with a supervisor-driven restart, and both paths call
		// Close. Panicking here takes down the whole worker, not just the sink.
		_ = s.Close()
	})

	t.Run(name+"/WriteNilMessageDoesNotPanic", func(t *testing.T) {

		s := newSink()
		defer closeBounded(t, s)

		// A nil message reaching a sink is a bug upstream, but the sink must
		// surface it as an error rather than crash the worker process.
		assertReturnsWithin(t, o, "Write", func(ctx context.Context) { _ = s.Write(ctx, nil) })
	})

	t.Run(name+"/PingRespectsContextDeadline", func(t *testing.T) {

		s := newSink()
		defer closeBounded(t, s)

		// Ping is the readiness path: /readyz calls it behind a probe timeout
		// measured in seconds. A connector that ignores the deadline and uses
		// its driver's own (often 30s+) turns a readiness check into a stuck
		// goroutine and a probe that never reports honestly.
		assertReturnsWithin(t, o, "Ping", func(ctx context.Context) { _ = s.Ping(ctx) })
	})

	t.Run(name+"/WriteAfterCloseDoesNotPanic", func(t *testing.T) {

		s := newSink()

		_ = s.Close()
		assertReturnsWithin(t, o, "Write", func(ctx context.Context) { _ = s.Write(ctx, nil) })
	})

	if _, ok := newSink().(hermod.BatchSink); ok {
		runBatchSinkSuite(t, o, name, newSink)
	}
}

// runBatchSinkSuite covers the optional hermod.BatchSink contract. Split out of
// RunSinkSuite so each stays readable and within the repo's function-length rule.
func runBatchSinkSuite(t *testing.T, o Options, name string, newSink func() hermod.Sink) {
	t.Helper()

	// newBatch returns a fresh sink already narrowed to BatchSink. The caller has
	// established that this connector implements it, so a failed assertion here
	// is a programming error rather than a test outcome.
	newBatch := func(t *testing.T) hermod.BatchSink {
		t.Helper()
		s := newSink()
		b, ok := s.(hermod.BatchSink)
		if !ok {
			t.Fatalf("%s: sink stopped implementing hermod.BatchSink between constructions", name)
		}
		return b
	}

	t.Run(name+"/WriteBatchEmptyIsNoop", func(t *testing.T) {
		b := newBatch(t)
		defer closeBounded(t, b)
		defer guardPanic(t, "WriteBatch(empty)")

		// The engine flushes on a timer as well as on a size threshold, so an
		// empty batch is a normal occurrence, not an error.
		if err := b.WriteBatch(boundedCtx(t), nil); err != nil {
			t.Errorf("WriteBatch with no messages returned %v, want nil", err)
		}
	})

	t.Run(name+"/WriteBatchAllNilDoesNotPanic", func(t *testing.T) {
		b := newBatch(t)
		defer closeBounded(t, b)

		assertReturnsWithin(t, o, "Write", func(ctx context.Context) {
			_ = b.WriteBatch(ctx, []hermod.Message{nil, nil})
		})
	})
}

// RunSourceSuite runs the source contract against newSource, which must return
// a fresh source that has NOT connected to anything.
func RunSourceSuite(t *testing.T, name string, newSource func() hermod.Source, opts ...Options) {
	t.Helper()
	o := firstOpt(opts)

	t.Run(name+"/CloseIsIdempotent", func(t *testing.T) {

		s := newSource()
		defer guardPanic(t, "Close")

		_ = s.Close()
		_ = s.Close()
	})

	t.Run(name+"/PingRespectsContextDeadline", func(t *testing.T) {

		s := newSource()
		defer closeBounded(t, s)

		assertReturnsWithin(t, o, "Ping", func(ctx context.Context) { _ = s.Ping(ctx) })
	})

	t.Run(name+"/ReadRespectsContextDeadline", func(t *testing.T) {

		s := newSource()
		defer closeBounded(t, s)

		// Either an error or a message is acceptable. Blocking past the
		// deadline is not: Read is the engine's inner loop, so a source that
		// ignores cancellation wedges pipeline shutdown and defeats the
		// supervisor's restart path.
		assertReturnsWithin(t, o, "Read", func(ctx context.Context) { _, _ = s.Read(ctx) })
	})

	t.Run(name+"/AckNilMessageDoesNotPanic", func(t *testing.T) {
		s := newSource()
		defer closeBounded(t, s)

		// Bounded, not merely panic-guarded. The Kafka source's Ack dereferenced
		// a nil message and did not crash — it stopped returning, which an
		// unbounded call turns into a hung suite rather than a failed assertion.
		assertReturnsWithin(t, o, "Ack", func(ctx context.Context) { _ = s.Ack(ctx, nil) })
	})
}

// closeBounded closes c and reports if it does not return promptly.
//
// Close takes no context, so a connector that blocks in it cannot be
// interrupted by the caller at all. That matters at shutdown: the engine closes
// its sources and sinks inside the orchestrator's termination grace period, and
// one connector blocking there is enough to turn a graceful drain into SIGKILL.
//
// This logs rather than fails. A blocking Close is a real defect, but it is a
// different defect from the context-deadline contract this suite gates on, and
// mixing the two would make the gate mean two things at once.
func closeBounded(t *testing.T, c io.Closer) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		_ = c.Close()
	}()

	select {
	case <-done:
	case <-time.After(closeBudget):
		t.Logf("WARNING: Close() did not return within %s. "+
			"Close takes no context, so this cannot be interrupted by the caller "+
			"and will eat into the shutdown grace period.", closeBudget)
	}
}

// guardPanic converts a panic into a test failure naming the operation, so a
// crashing connector reports which call broke instead of taking down the whole
// test binary with an opaque stack.
func guardPanic(t *testing.T, op string) {
	t.Helper()
	if r := recover(); r != nil {
		t.Errorf("%s panicked: %v", op, r)
	}
}
