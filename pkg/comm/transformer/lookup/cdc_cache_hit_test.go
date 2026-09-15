package lookup

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// newCDCOrder builds the message shape pkg/comm/source/postgres emits for an
// INSERT: an operation plus an after-image payload, and an empty data map.
func newCDCOrder() hermod.Message {
	m := message.AcquireMessage()
	m.SetOperation(hermod.OpCreate)
	m.SetTable("orders")
	m.SetAfter([]byte(`{"id":"1","user_id":"u1"}`))
	return m
}

// TestCDCLookupSurvivesTheCacheHit runs the same query-mode lookup twice against
// a registry whose cache actually remembers, which is what production does: the
// Cache TTL field is empty by default and ttl <= 0 means "never expire".
//
// Query mode has no keyField, so nothing reads the message before the result is
// written. On the first message the query path calls msg.Data() and hydrates it;
// on the cache hit nothing does, and DefaultMessage.SetData then buries the
// after-image under a nested "after" key and writes the looked-up field at the
// root -- where Payload() no longer serialises it.
func TestCDCLookupSurvivesTheCacheHit(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	cfg := map[string]any{
		"sourceId":      "src1",
		"mode":          "query",
		"queryTemplate": "SELECT email FROM users WHERE id = 'u1'",
		"valueColumn":   "email",
		"targetField":   "user_email",
	}

	ctx := context.WithValue(context.Background(), hermod.RegistryKey, reg)

	for i, label := range []string{"first message (query path)", "second message (cache hit)"} {
		msg := newCDCOrder()
		out, err := tr.Transform(ctx, msg, cfg)
		if err != nil {
			t.Fatalf("%s: transform: %v", label, err)
		}
		payload := string(out.Payload())
		t.Logf("%s:\n  Payload() = %s", label, payload)

		if !strings.Contains(payload, "ada@example.com") {
			t.Errorf("%s: the looked-up value is not in the message payload the sink will read: %s",
				label, payload)
		}
		if strings.Contains(payload, `"after"`) {
			t.Errorf("%s: the after-image was nested under a second \"after\" key: %s", label, payload)
		}
		_ = i
	}
}

// TestNonCDCLookupSurvivesTheCacheHit is the same scenario on the message shape
// the RabbitMQ source produces: fields already merged into the data map and no
// operation set. It isolates whether the defect is about the cache or about the
// CDC envelope.
func TestNonCDCLookupSurvivesTheCacheHit(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	cfg := map[string]any{
		"sourceId":      "src1",
		"mode":          "query",
		"queryTemplate": "SELECT email FROM users WHERE id = 'u1'",
		"valueColumn":   "email",
		"targetField":   "user_email",
	}
	ctx := context.WithValue(context.Background(), hermod.RegistryKey, reg)

	for _, label := range []string{"first message (query path)", "second message (cache hit)"} {
		// As rabbitmq_queue.go does: SetPayload then SetData per field, no operation.
		msg := message.AcquireMessage()
		msg.SetPayload([]byte(`{"id":"1","user_id":"u1"}`))
		msg.SetData("id", "1")
		msg.SetData("user_id", "u1")

		out, err := tr.Transform(ctx, msg, cfg)
		if err != nil {
			t.Fatalf("%s: transform: %v", label, err)
		}
		payload := string(out.Payload())
		t.Logf("%s:\n  Payload() = %s", label, payload)
		if !strings.Contains(payload, "ada@example.com") {
			t.Errorf("%s: the looked-up value is not in the payload the sink reads: %s", label, payload)
		}
	}
}
