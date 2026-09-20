package tracing

// The gate exists because otel allocates a span object and a context to hold
// it even when nothing will ever record them, and Hermod starts spans per
// message and per node. It works by identity: the global provider is the same
// object until someone replaces it.
//
// That is an assumption about a library, so it is checked rather than trusted.

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestGateDetectsAnInstalledProvider(t *testing.T) {
	if Installed() {
		t.Fatal("a TracerProvider is already installed at test start; the identity capture cannot work " +
			"if something installs one from an init()")
	}

	tracer := otel.Tracer("gate-test")
	ctx, span := StartSpan(context.Background(), tracer, "before")
	if span.IsRecording() {
		t.Error("span is recording with no provider installed")
	}
	if ctx != context.Background() {
		t.Error("the context was replaced for a span that goes nowhere")
	}
	span.End() // must be safe

	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))

	if !Installed() {
		t.Fatal("installing an SDK provider was not detected: the gate would suppress every span for ever")
	}

	_, span = StartSpan(context.Background(), otel.Tracer("gate-test"), "after")
	span.End()

	var found bool
	for _, s := range rec.Ended() {
		if s.Name() == "after" {
			found = true
		}
	}
	if !found {
		t.Error("no span was recorded after a provider was installed")
	}
}

// A noop span must absorb everything a caller does to it, or the gate turns a
// missing provider into a crash.
func TestNoopSpanAbsorbsEveryCall(t *testing.T) {
	_, span := StartSpan(context.Background(), otel.Tracer("gate-test"), "noop")
	span.SetAttributes()
	span.RecordError(context.Canceled)
	span.AddEvent("e")
	span.SetName("n")
	span.End()
}
