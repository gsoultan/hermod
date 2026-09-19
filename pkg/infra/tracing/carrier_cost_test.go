package tracing

// Extract runs on every sink write. The propagator asks the carrier for
// "traceparent" and then "tracestate", and each Get cloned the message's whole
// metadata map to read one key — so a message carrying a dozen metadata
// entries paid two full map copies per write to answer two lookups, one of
// which is almost always absent.
//
// Cloning is not gratuitous: Metadata() copies under the read lock because
// handing out the live map would race with a concurrent SetMetadata. Reading a
// single key under the same lock is both cheaper and equally safe.

import (
	"context"
	"fmt"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	"go.opentelemetry.io/otel/trace"
)

func msgWithMetadata(t *testing.T, entries int) *message.DefaultMessage {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(func() { message.ReleaseMessage(m) })
	for i := range entries {
		m.SetMetadata(fmt.Sprintf("key_%d", i), fmt.Sprintf("value_%d", i))
	}
	return m
}

// TestExtractCostDoesNotScaleWithMetadataSize is the point: reading two keys
// must not get more expensive because the message carries more metadata.
func TestExtractCostDoesNotScaleWithMetadataSize(t *testing.T) {
	few := msgWithMetadata(t, 2)
	many := msgWithMetadata(t, 64)
	ctx := context.Background()

	fewAllocs := testing.AllocsPerRun(200, func() { Extract(ctx, few) })
	manyAllocs := testing.AllocsPerRun(200, func() { Extract(ctx, many) })

	if manyAllocs > fewAllocs+1 {
		t.Errorf("Extract allocates %v times with 2 metadata entries but %v with 64: the map is still being copied per lookup",
			fewAllocs, manyAllocs)
	}
}

// Whatever it costs, it has to still work: a stamped message must round-trip
// its trace context, and an unstamped one must not invent one.
func TestInjectExtractRoundTrip(t *testing.T) {
	m := msgWithMetadata(t, 8)

	stamped := msgWithMetadata(t, 8)
	stamped.SetMetadata(TraceParentKey, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	got := Extract(context.Background(), stamped)
	sc := trace.SpanContextFromContext(got)
	if !sc.IsValid() {
		t.Fatal("a message carrying a valid traceparent extracted no span context")
	}
	if sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id = %s, want 4bf92f3577b34da6a3ce929d0e0e4736", sc.TraceID())
	}

	if trace.SpanContextFromContext(Extract(context.Background(), m)).IsValid() {
		t.Error("a message with no traceparent produced a valid span context")
	}
}
