package engine

import (
	"context"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/buffer"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/engine/config"
	"github.com/gsoultan/Hermod/pkg/engine/idempotency"
)

// ---------------------------------------------------------------------------
// Delivery is at-least-once, so duplicates are permitted. What turns that into
// "exactly-once as observed at the destination" is the sink's upsert: every SQL
// sink writes with ON CONFLICT / ON DUPLICATE KEY / MERGE keyed on the message
// id, so a redelivered message overwrites its own row instead of adding one.
//
// That only holds if a redelivered message carries the *same* identity. If the
// engine minted a fresh key each time it saw a message, every duplicate would
// land as a new row and the guarantee would be silently false — the pipeline
// would look healthy while the destination accumulated duplicates.
// ---------------------------------------------------------------------------

// TestRedeliveredMessageKeepsItsIdempotencyKey is the property the upsert
// depends on.
func TestRedeliveredMessageKeepsItsIdempotencyKey(t *testing.T) {
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("order-42")
	msg.SetData("amount", 100)

	first, _ := idempotency.EnsureIdempotencyID(msg)

	// The same message handed to the engine again, as happens after a restart
	// with unacknowledged work, or a retry after a sink error.
	for i := range 5 {
		again, _ := idempotency.EnsureIdempotencyID(msg)
		if again != first {
			t.Fatalf("redelivery %d produced key %q, first delivery produced %q; "+
				"the sink's upsert would insert a second row instead of overwriting",
				i+1, again, first)
		}
	}
	if first != "order-42" {
		t.Errorf("key is %q, want the message id: sinks key their upsert on it", first)
	}
}

// TestIdempotencyKeyMetadataWinsOverID lets a source that knows its own natural
// key override the transport-level id — a CDC row's primary key, say, which is
// stable across a resnapshot where the message id is not.
func TestIdempotencyKeyMetadataWinsOverID(t *testing.T) {
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("transport-id-changes-every-time")
	msg.SetMetadata("idempotency_key", "customer:7")

	for range 3 {
		if got, _ := idempotency.EnsureIdempotencyID(msg); got != "customer:7" {
			t.Fatalf("key is %q, want the metadata key", got)
		}
	}
}

// TestMessageWithNoIdentityGetsAFreshKeyEachTime pins the documented limit of
// the guarantee, so it cannot quietly change.
//
// A message with neither an id nor an idempotency_key is given a generated
// UUID. Two deliveries of such a message are genuinely indistinguishable, so
// they get different keys and an upsert cannot collapse them. That is not a bug
// — there is nothing to deduplicate on — but it does mean exactly-once at the
// destination requires the source to supply a stable identity.
func TestMessageWithNoIdentityGetsAFreshKeyEachTime(t *testing.T) {
	seen := map[string]bool{}
	for range 3 {
		msg := message.AcquireMessage()
		msg.SetData("k", "v") // no ID, no idempotency_key
		key, _ := idempotency.EnsureIdempotencyID(msg)
		if key == "" {
			t.Fatal("no key was generated; the message would be untraceable")
		}
		seen[key] = true
		msg.Release()
	}
	if len(seen) != 3 {
		t.Errorf("got %d distinct keys across 3 identity-less messages, want 3; "+
			"if this ever collapses, unrelated messages would overwrite each other", len(seen))
	}
}

// TestSinkSeesAStableKeyForARedeliveredMessage checks the property through the
// engine rather than the helper: whatever reaches the sink must carry the same
// identity on a redelivery, because that identity is what the upsert keys on.
func TestSinkSeesAStableKeyForARedeliveredMessage(t *testing.T) {
	const id = "stable-1"

	deliver := func() string {
		sink := &recordingSink{}
		src := &drainSource{}
		m := message.AcquireMessage()
		m.SetID(id)
		m.SetPayload([]byte(`{"k":"v"}`))
		src.messages = []hermod.Message{m}

		eng := NewEngine(src, []hermod.Sink{sink}, buffer.NewRingBuffer(8))
		cfg := config.DefaultConfig()
		cfg.DrainTimeout = 5 * time.Second
		eng.SetConfig(cfg)

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- eng.Start(ctx) }()

		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && len(sink.received()) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
		<-errCh

		got := sink.received()
		if len(got) == 0 {
			t.Fatal("nothing reached the sink")
		}
		return got[0]
	}

	first := deliver()
	second := deliver()

	if first != second {
		t.Errorf("the same message delivered twice reached the sink as %q then %q; "+
			"a sink upsert keys on this, so the duplicate would land as a new row",
			first, second)
	}
	if first != id {
		t.Errorf("sink saw id %q, want %q", first, id)
	}
}
