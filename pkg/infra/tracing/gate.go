package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// defaultTracerProvider is the global provider as it stands at package
// initialisation, before anything has installed one.
//
// otel offers no way to ask "is a real provider installed", but it hands back
// the same default object until someone replaces it, so identity answers the
// question. Captured at init because InitOTLP is called from main rather than
// from an init function, so package initialisation always runs first.
// Installing a provider from an init() would defeat this — nothing in Hermod
// does, and TestGateDetectsAnInstalledProvider would notice.
var defaultTracerProvider = otel.GetTracerProvider()

// Installed reports whether anything has installed a TracerProvider.
func Installed() bool {
	return otel.GetTracerProvider() != defaultTracerProvider
}

// StartSpan begins a span, or returns the context untouched when no
// TracerProvider is installed.
//
// With nothing installed — the default — otel still allocates a span object
// and a context to hold it, per call, for a span that goes nowhere. Hermod
// starts spans per message and per node, where that was about a fifth of
// everything the pipeline allocated.
//
// The span returned in that case is whatever the context already carries,
// which for an untraced pipeline is otel's noop span: End, SetAttributes,
// RecordError and SetStatus are all safe no-ops on it, and IsRecording is
// false, so callers need no extra branch.
func StartSpan(ctx context.Context, tracer trace.Tracer, name string) (context.Context, trace.Span) {
	if !Installed() {
		return ctx, trace.SpanFromContext(ctx)
	}
	return tracer.Start(ctx, name)
}
