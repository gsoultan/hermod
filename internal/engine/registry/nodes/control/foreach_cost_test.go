package control

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	msgpkg "github.com/gsoultan/hermod/pkg/comm/message"
)

// lineItems builds an order body with n line items under "lines".
func lineItems(n int) []any {
	arr := make([]any, n)
	for i := range arr {
		arr[i] = map[string]any{"sku": fmt.Sprintf("s-%d", i), "qty": i}
	}
	return arr
}

func orderWith(n int) hermod.Message {
	m := msgpkg.AcquireMessage()
	m.SetID("order-1")
	m.SetData("order_id", "o-1")
	m.SetData("lines", lineItems(n))
	return m
}

func fanOut(t *testing.T, msg hermod.Message, config map[string]any) ([]hermod.Message, error) {
	t.Helper()
	if config == nil {
		config = map[string]any{}
	}
	config["arrayPath"] = "lines"
	node := &storage.WorkflowNode{ID: "n1", Type: "foreach", Config: config}
	out, _, err := (&ForeachNode{}).Execute(context.Background(), &stubCtx{}, "wf", node, msg)
	return out, err
}

// Message.Clone deep-copies every data value, so cloning once per item copied
// the whole array onto every one of the N clones — the cost of a fan-out grew
// with the square of the array. Measured before this was fixed: 100 items
// 3.6 MB, 1000 items 353 MB, 4000 items 5.64 GB for a single message. A 4000
// line order was enough to exhaust a 7 GB CI runner, and the node had no bound.
//
// An item's own copy is all a fanned-out message needs; the other N-1 items are
// carried and never read.
func TestForeach_DoesNotCopyTheWholeArrayOntoEveryClone(t *testing.T) {
	msg := orderWith(4)
	defer msg.Release()

	out, err := fanOut(t, msg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(out))
	}
	defer func() {
		for _, m := range out {
			m.Release()
		}
	}()

	for i, m := range out {
		if _, carried := m.Data()["lines"]; carried {
			t.Fatalf("message %d still carries the whole source array", i)
		}
		item, ok := m.Data()["_item"].(map[string]any)
		if !ok {
			t.Fatalf("message %d has no _item: %v", i, m.Data())
		}
		if item["sku"] != fmt.Sprintf("s-%d", i) {
			t.Fatalf("message %d got item %v, want s-%d", i, item["sku"], i)
		}
		// The rest of the row still travels with each item.
		if m.Data()["order_id"] != "o-1" {
			t.Fatalf("message %d lost the row it came from: %v", i, m.Data())
		}
	}

	// No two messages may share an item: a transformation writing into _item on
	// one branch must not be visible on another.
	first := out[0].Data()["_item"].(map[string]any)
	first["sku"] = "mutated"
	if out[1].Data()["_item"].(map[string]any)["sku"] == "mutated" {
		t.Fatal("fanned-out messages share their item")
	}
}

// The shape of the cost, not a fixed budget. Quadratic growth shows up as ~4x
// for a doubled array; linear is ~2x. The gate is deliberately loose (3x) so
// this fails on the class of regression rather than on allocator noise.
func TestForeach_FanoutCostGrowsLinearlyWithTheArray(t *testing.T) {
	alloc := func(n int) uint64 {
		msg := orderWith(n)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		out, err := fanOut(t, msg, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		runtime.ReadMemStats(&after)
		for _, m := range out {
			m.Release()
		}
		msg.Release()
		return after.TotalAlloc - before.TotalAlloc
	}

	small := alloc(500)
	large := alloc(1000)

	if small == 0 {
		t.Fatal("measured no allocation at all; the benchmark is not measuring the fan-out")
	}
	if ratio := float64(large) / float64(small); ratio > 3 {
		t.Fatalf("doubling the array multiplied allocation by %.1fx (%d -> %d bytes); "+
			"linear growth is ~2x, quadratic is ~4x", ratio, small, large)
	}
}

// A bound, because the array is upstream-controlled: a source row decides how
// many messages one message becomes, and how much memory that costs.
func TestForeach_RejectsAnArrayOverTheCap(t *testing.T) {
	msg := orderWith(defaultMaxFanoutItems + 1)
	defer msg.Release()

	out, err := fanOut(t, msg, nil)
	if err == nil {
		t.Fatalf("expected an error for a %d-item array, got %d messages", defaultMaxFanoutItems+1, len(out))
	}
	// The error has to be actionable: how many arrived, what the cap is, and the
	// key that raises it. A bound nobody can find is a bound nobody can clear.
	for _, want := range []string{strconv.Itoa(defaultMaxFanoutItems + 1), strconv.Itoa(defaultMaxFanoutItems), "maxItems"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	if len(out) != 0 {
		t.Fatalf("an over-cap fan-out must deliver nothing, got %d messages", len(out))
	}
}

func TestForeach_MaxItemsIsConfigurable(t *testing.T) {
	msg := orderWith(3)
	defer msg.Release()

	if _, err := fanOut(t, msg, map[string]any{"maxItems": "2"}); err == nil {
		t.Fatal("expected 3 items to exceed a maxItems of 2")
	}

	msg2 := orderWith(3)
	defer msg2.Release()
	out, err := fanOut(t, msg2, map[string]any{"maxItems": 5})
	if err != nil {
		t.Fatalf("3 items under a maxItems of 5 should fan out: %v", err)
	}
	for _, m := range out {
		m.Release()
	}
	if len(out) != 3 {
		t.Fatalf("got %d messages, want 3", len(out))
	}
}

// Dropping the array is what makes the fan-out linear, so opting back in is
// opting back into the old cost. It exists for a workflow that reads the whole
// list downstream of the split.
func TestForeach_KeepSourceArrayOptsBackIn(t *testing.T) {
	msg := orderWith(3)
	defer msg.Release()

	out, err := fanOut(t, msg, map[string]any{"keepSourceArray": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		for _, m := range out {
			m.Release()
		}
	}()

	for i, m := range out {
		lines, ok := m.Data()["lines"].([]any)
		if !ok || len(lines) != 3 {
			t.Fatalf("message %d should still carry the source array, got %v", i, m.Data()["lines"])
		}
	}
}

// A nested array path has to be removed at its own level, not by its first
// segment — deleting "order" to drop "order.lines" would take the whole object
// and everything else under it with it.
func TestForeach_NestedArrayPathDropsOnlyTheArray(t *testing.T) {
	msg := msgpkg.AcquireMessage()
	defer msg.Release()
	msg.SetID("order-1")
	msg.SetData("order", map[string]any{
		"id":    "o-1",
		"lines": lineItems(3),
	})

	node := &storage.WorkflowNode{ID: "n1", Type: "foreach", Config: map[string]any{"arrayPath": "order.lines"}}
	out, _, err := (&ForeachNode{}).Execute(context.Background(), &stubCtx{}, "wf", node, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		for _, m := range out {
			m.Release()
		}
	}()
	if len(out) != 3 {
		t.Fatalf("got %d messages, want 3", len(out))
	}

	for i, m := range out {
		order, ok := m.Data()["order"].(map[string]any)
		if !ok {
			t.Fatalf("message %d lost the object holding the array: %v", i, m.Data())
		}
		if order["id"] != "o-1" {
			t.Fatalf("message %d lost a sibling of the array: %v", i, order)
		}
		if _, carried := order["lines"]; carried {
			t.Fatalf("message %d still carries the nested source array", i)
		}
	}
}
