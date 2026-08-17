package tracing

import (
	"context"
	"testing"

	"github.com/user/hermod/pkg/comm/message"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// The contract the pipeline's end-to-end traces rest on: what Inject writes,
// Extract reads back, and a message carrying nothing is not an error.

func startSpan(t *testing.T) (context.Context, trace.Span) {
	t.Helper()
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tp.Tracer("test").Start(context.Background(), "parent")
}

func TestInjectedContextIsExtractedBack(t *testing.T) {
	ctx, span := startSpan(t)
	defer span.End()
	want := span.SpanContext()

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	Inject(ctx, msg)

	got := trace.SpanContextFromContext(Extract(context.Background(), msg))
	if !got.IsValid() {
		t.Fatalf("nothing was extracted back; metadata holds %v\n"+
			"a stage that cannot read the trace context off a message starts a new trace, "+
			"and the record can no longer be followed across the handover", msg.Metadata())
	}
	if got.TraceID() != want.TraceID() {
		t.Errorf("trace id round-tripped as %s, want %s", got.TraceID(), want.TraceID())
	}
	if got.SpanID() != want.SpanID() {
		t.Errorf("span id round-tripped as %s, want %s", got.SpanID(), want.SpanID())
	}
}

// The key is the W3C name, so a sink that forwards metadata as headers or
// attributes propagates the trace to whatever consumes it next without needing
// to know anything about Hermod.
func TestInjectUsesTheW3CHeaderName(t *testing.T) {
	ctx, span := startSpan(t)
	defer span.End()

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	Inject(ctx, msg)

	if msg.Metadata()[TraceParentKey] == "" {
		t.Errorf("no %q in metadata after Inject; keys present: %v",
			TraceParentKey, msg.Metadata())
	}
}

// A message from a source that never stamped one — or one rebuilt by a
// transformation that dropped metadata — has to start a new trace rather than
// fail. Tracing is diagnostics; it must never be the reason a record does not
// move.
func TestExtractFromAnUnstampedMessageIsNotAnError(t *testing.T) {
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)

	ctx := Extract(context.Background(), msg)
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		t.Errorf("extracted a valid span context %v from a message carrying none", sc)
	}
}

func TestInjectAndExtractToleranceOfNilMessages(t *testing.T) {
	ctx, span := startSpan(t)
	defer span.End()

	Inject(ctx, nil) // must not panic
	if got := Extract(ctx, nil); got != ctx {
		t.Error("Extract with no message should return the context unchanged")
	}
}

// Messages are pooled. If Release did not clear metadata a recycled message
// would carry the previous record's traceparent and silently join a trace it
// has nothing to do with, which is worse than no trace at all.
func TestAReleasedMessageDoesNotCarryItsTraceIntoTheNextUser(t *testing.T) {
	ctx, span := startSpan(t)
	defer span.End()

	first := message.AcquireMessage()
	Inject(ctx, first)
	if first.Metadata()[TraceParentKey] == "" {
		t.Fatal("Inject wrote nothing, so this proves nothing")
	}
	first.Release()

	next := message.AcquireMessage()
	t.Cleanup(next.Release)
	if tp := next.Metadata()[TraceParentKey]; tp != "" {
		t.Errorf("a message taken from the pool still carries traceparent %q\n"+
			"it would be reported as part of the previous record's trace", tp)
	}
}
