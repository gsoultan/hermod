package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Following one record from the source that produced it to the sink that wrote
// it.
//
// Spans existed on the write side — sink.write, sink.write_batch,
// RunWorkflowNode — and nowhere else, so every sink write was the root of its
// own trace. That is the wrong shape for the question anyone actually asks at
// three in the morning, which is "where did *this* row go, and how long did
// each step take". Answering it needs the read and the write in one trace.
//
// A Go context cannot carry that here. The read loop and the sink writers are
// different goroutines with different contexts, joined by a buffer, so the
// trace context has to travel *on the message* — which is how every messaging
// system propagates it, and why W3C traceparent is a header rather than a
// process-local value.

// spanRelay forwards spans to whichever recorder the running test installed.
//
// otel's global tracer takes its delegate exactly once per process. The
// engine's tracer is package-level (`var tracer = otel.Tracer(...)` in
// engine.go), so it is created at init, before any test runs: the first
// SetTracerProvider binds it and every later one is ignored *for that tracer*.
//
// This helper used to build a TracerProvider per test, which therefore worked
// for the first test to call it and silently recorded nothing for every test
// after — reported as "no source.receive span was recorded; spans seen: []",
// which reads like a product defect in trace propagation rather than a broken
// fixture. Reproduce the old behaviour with `-count=2` on a single test.
//
// So one provider is installed once per process and the recorder behind it is
// swapped instead.
type spanRelay struct {
	mu  sync.Mutex
	cur *tracetest.SpanRecorder
}

func (r *spanRelay) set(rec *tracetest.SpanRecorder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cur = rec
}

func (r *spanRelay) current() *tracetest.SpanRecorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cur
}

func (r *spanRelay) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	if c := r.current(); c != nil {
		c.OnStart(parent, s)
	}
}

func (r *spanRelay) OnEnd(s sdktrace.ReadOnlySpan) {
	if c := r.current(); c != nil {
		c.OnEnd(s)
	}
}

func (r *spanRelay) Shutdown(context.Context) error   { return nil }
func (r *spanRelay) ForceFlush(context.Context) error { return nil }

var (
	testSpanRelay = &spanRelay{}
	testSpanOnce  sync.Once
)

// recordSpans records the spans produced while the calling test runs.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	testSpanOnce.Do(func() {
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(testSpanRelay)))
	})
	rec := tracetest.NewSpanRecorder()
	testSpanRelay.set(rec)
	t.Cleanup(func() { testSpanRelay.set(nil) })
	return rec
}

// traceSource emits exactly one message and then blocks, so the trace under
// test is not buried among repeats.
type traceSource struct {
	msg  hermod.Message
	sent bool
}

func (s *traceSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.sent {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.sent = true
	return s.msg, nil
}
func (s *traceSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *traceSource) Ping(context.Context) error                { return nil }
func (s *traceSource) Close() error                              { return nil }

type traceSink struct{ got chan hermod.Message }

func (s *traceSink) Write(ctx context.Context, msg hermod.Message) error {
	select {
	case s.got <- msg:
	default:
	}
	return nil
}
func (s *traceSink) Ping(context.Context) error { return nil }
func (s *traceSink) Close() error               { return nil }

func TestSinkWriteJoinsTheTraceTheSourceReadStarted(t *testing.T) {
	rec := recordSpans(t)

	msg := message.AcquireMessage()
	msg.SetID("trace-1")
	msg.SetPayload([]byte("hello"))

	src := &traceSource{msg: msg}
	snk := &traceSink{got: make(chan hermod.Message, 1)}
	eng := NewEngine(src, []hermod.Sink{snk}, buffer.NewRingBuffer(10))

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() {
		if err := eng.Start(ctx); err != nil &&
			!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("engine: %v", err)
		}
	}()

	select {
	case <-snk.got:
	case <-ctx.Done():
		t.Fatal("the sink never received the message")
	}

	// Give the write span a moment to end before reading the recorder.
	deadline := time.Now().Add(2 * time.Second)
	var read, write sdktrace.ReadOnlySpan
	for time.Now().Before(deadline) {
		read, write = nil, nil
		for _, s := range rec.Ended() {
			switch s.Name() {
			case "source.receive":
				read = s
			case "sink.write":
				write = s
			}
		}
		if read != nil && write != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if read == nil {
		var names []string
		for _, s := range rec.Ended() {
			names = append(names, s.Name())
		}
		t.Fatalf("no source.receive span was recorded; spans seen: %v\n"+
			"nothing marks where a record entered the pipeline, so a trace can only ever "+
			"begin at the sink and the read is invisible", names)
	}
	if write == nil {
		t.Fatal("no sink.write span was recorded")
	}

	if read.SpanContext().TraceID() != write.SpanContext().TraceID() {
		t.Errorf("the sink write is in trace %s and the source read is in trace %s\n"+
			"they are two unrelated traces, so a record cannot be followed from the source "+
			"that produced it to the sink that wrote it — which is the one question a trace "+
			"is for",
			write.SpanContext().TraceID(), read.SpanContext().TraceID())
	}

	if write.Parent().SpanID() != read.SpanContext().SpanID() {
		t.Errorf("the sink write's parent is %s, want the source read's span %s\n"+
			"same trace but not linked, so the waterfall does not show the handover",
			write.Parent().SpanID(), read.SpanContext().SpanID())
	}
}
