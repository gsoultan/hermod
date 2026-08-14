// Package tracing carries trace context on a message rather than in a Go
// context.
//
// The pipeline reads on one goroutine and writes on others, joined by a buffer,
// so the context that saw the read is not the context that performs the write.
// A record therefore cannot be followed end to end through context.Context
// alone — which is why every messaging system propagates trace context as a
// field travelling with the payload, and why W3C traceparent is a header rather
// than a process-local value.
package tracing

import (
	"context"

	"github.com/user/hermod"
	"go.opentelemetry.io/otel/propagation"
)

// TraceParentKey is the metadata key the trace context is written to. It is the
// W3C name, so anything downstream that already understands traceparent — a
// queue consumer, a sink that forwards headers — needs no special case.
const TraceParentKey = "traceparent"

// propagator is used directly rather than through otel's global. The global is
// only configured when OTLP export is switched on
// (internal/observability.InitOTLP), and whether one stage of a pipeline can be
// linked to the next should not depend on whether anyone is currently
// exporting.
var propagator = propagation.TraceContext{}

// messageCarrier adapts a message's metadata to the TextMapCarrier the
// propagator writes through.
type messageCarrier struct{ msg hermod.Message }

func (c messageCarrier) Get(key string) string { return c.msg.Metadata()[key] }

func (c messageCarrier) Set(key, value string) { c.msg.SetMetadata(key, value) }

func (c messageCarrier) Keys() []string {
	md := c.msg.Metadata()
	keys := make([]string, 0, len(md))
	for k := range md {
		keys = append(keys, k)
	}
	return keys
}

// Inject stamps the span context active in ctx onto msg, so a later stage
// running on another goroutine can continue the same trace.
func Inject(ctx context.Context, msg hermod.Message) {
	if msg == nil {
		return
	}
	propagator.Inject(ctx, messageCarrier{msg: msg})
}

// Extract returns ctx carrying whatever span context msg was stamped with, or
// ctx unchanged if it carries none. A message from a source that predates this,
// or one rebuilt by a transformation that dropped metadata, simply starts a new
// trace rather than failing.
func Extract(ctx context.Context, msg hermod.Message) context.Context {
	if msg == nil {
		return ctx
	}
	return propagator.Extract(ctx, messageCarrier{msg: msg})
}
