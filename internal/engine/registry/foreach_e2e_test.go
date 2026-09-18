package registry

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine/config"
	"github.com/gsoultan/hermod/pkg/infra/state"
)

// arraySource emits `count` order rows, each carrying a three-line array, then
// blocks like a caught-up source.
type arraySource struct {
	count   int
	emitted atomic.Int64
}

func (s *arraySource) Read(ctx context.Context) (hermod.Message, error) {
	if s.emitted.Load() >= int64(s.count) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	i := s.emitted.Add(1)
	msg := message.AcquireMessage()
	msg.SetData("order_id", fmt.Sprintf("o-%d", i))
	msg.SetData("lines", []any{
		map[string]any{"sku": "A-1", "qty": 1},
		map[string]any{"sku": "B-7", "qty": 2},
		map[string]any{"sku": "C-9", "qty": 3},
	})
	return msg, nil
}

func (s *arraySource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *arraySource) Ping(ctx context.Context) error                    { return nil }
func (s *arraySource) Close() error                                      { return nil }

// A foreach node exists to make the sink write one row per array element. That
// is the claim on the node's own config panel, and until this test it was never
// checked past the executor's return value: the registry's traversal carried one
// message per node, so items 2..N were dropped between the node and the sink and
// the workflow reported success having written a third of the data.
func TestForeachNodeWritesOneRowPerArrayItem(t *testing.T) {
	const orders = 5
	const linesPerOrder = 3

	store := newPipeStorage()
	reg := NewRegistry(store)

	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}

	wf := storage.Workflow{
		ID:   "wf-foreach",
		Name: "wf-foreach",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "fan", Type: "foreach", Config: map[string]any{"arrayPath": "lines"}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "fan"},
			{ID: "e2", SourceID: "fan", TargetID: "out"},
		},
		MaxRetries:    5,
		RetryInterval: "10ms",
	}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, wf, &arraySource{count: orders}, sinks)
	defer stop()

	want := orders * linesPerOrder
	ok := waitUntil(t, 30*time.Second, "one write per line item", func() bool {
		return sinks["snk-out"].count() >= want
	})
	if !ok {
		t.Fatalf("sink received %d rows for %d orders of %d lines; want %d",
			sinks["snk-out"].count(), orders, linesPerOrder, want)
	}

	// Volume alone would pass if the same item were written three times. Every
	// (order, sku) pair has to be there exactly once.
	seen := map[string]int{}
	for _, row := range sinks["snk-out"].received() {
		item, _ := row["_item"].(map[string]any)
		if item == nil {
			t.Fatalf("row written without _item: %v", row)
		}
		seen[fmt.Sprintf("%v/%v", row["order_id"], item["sku"])]++
	}
	if len(seen) != want {
		t.Fatalf("sink saw %d distinct (order, sku) pairs, want %d: %v", len(seen), want, seen)
	}

	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("fan-out pipeline over-released %d message reference(s)", n)
	}
}

// The transformation of the same name does something else: it keeps one message
// and materialises the expanded array on it. One row in, one row out — and the
// expanded list present. Asserting it here is what keeps the two from being
// quietly merged.
func TestForeachTransformationMaterialisesArrayOnOneMessage(t *testing.T) {
	const orders = 4

	store := newPipeStorage()
	reg := NewRegistry(store)

	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}

	wf := storage.Workflow{
		ID:   "wf-fanout-transform",
		Name: "wf-fanout-transform",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "exp", Type: "transformation", Config: map[string]any{
				"transType":   "fanout",
				"arrayPath":   "lines",
				"resultField": "expanded",
			}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "exp"},
			{ID: "e2", SourceID: "exp", TargetID: "out"},
		},
		MaxRetries:    5,
		RetryInterval: "10ms",
	}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, wf, &arraySource{count: orders}, sinks)
	defer stop()

	ok := waitUntil(t, 30*time.Second, "one write per order", func() bool {
		return sinks["snk-out"].count() >= orders
	})
	if !ok {
		t.Fatalf("sink received %d rows, want %d", sinks["snk-out"].count(), orders)
	}

	for _, row := range sinks["snk-out"].received() {
		expanded, _ := row["expanded"].([]any)
		if len(expanded) != 3 {
			t.Fatalf("expected the 3-line array under \"expanded\", got %#v in %v", row["expanded"], row)
		}
		if _, split := row["_item"]; split {
			t.Fatalf("the fanout transformation must not split the message: %v", row)
		}
	}
	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("fanout pipeline over-released %d message reference(s)", n)
	}
}

// startForeachPipeline is startMultiPipeline with a single, fixed source, so the
// tests above can hand it a source fixture that emits arrays.
func startForeachPipeline(t *testing.T, reg *Registry, wf storage.Workflow, src hermod.Source, sinks map[string]*pipeSink) func() {
	t.Helper()
	reg.SetFactories(
		func(cfg factory.SourceConfig) (hermod.Source, error) { return src, nil },
		func(cfg factory.SinkConfig) (hermod.Sink, error) {
			if s, ok := sinks[cfg.ID]; ok {
				return s, nil
			}
			return nil, fmt.Errorf("no sink fixture for %q", cfg.ID)
		},
	)
	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatalf("StartWorkflow(%s): %v", wf.ID, err)
	}
	if eng, ok := reg.GetEngine(wf.ID); ok {
		for id := range sinks {
			eng.UpdateSinkConfig(id, func(cfg *config.SinkConfig) {
				cfg.BatchSize = 1
				cfg.BatchTimeout = 5 * time.Millisecond
			})
		}
	}
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = reg.StopEngine(stopCtx, wf.ID)
	}
}

// foreach -> collect -> sink is the pattern the pair exists for: split a row's
// array, do something per item, then write one row carrying all of them.
//
// Collect emits only when it has seen `_fanout_total` items, so the dropped
// fan-out did not merely lose two thirds of this pipeline — the group never
// completed and the sink was written to zero times, for every order, with the
// workflow green.
func TestForeachThenCollectEmitsOneBatchPerOrder(t *testing.T) {
	const orders = 3

	store := newPipeStorage()
	reg := NewRegistry(store)
	reg.SetStateStore(state.NewMemoryStore())

	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}

	wf := storage.Workflow{
		ID:   "wf-foreach-collect",
		Name: "wf-foreach-collect",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "fan", Type: "foreach", Config: map[string]any{"arrayPath": "lines"}},
			{ID: "gather", Type: "collect", Config: map[string]any{"targetField": "picked"}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "fan"},
			{ID: "e2", SourceID: "fan", TargetID: "gather"},
			{ID: "e3", SourceID: "gather", TargetID: "out"},
		},
		MaxRetries:    5,
		RetryInterval: "10ms",
	}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, wf, &arraySource{count: orders}, sinks)
	defer stop()

	ok := waitUntil(t, 30*time.Second, "one collected batch per order", func() bool {
		return sinks["snk-out"].count() >= orders
	})
	if !ok {
		t.Fatalf("sink received %d batches for %d orders; want %d",
			sinks["snk-out"].count(), orders, orders)
	}

	for _, row := range sinks["snk-out"].received() {
		picked, _ := row["picked"].([]any)
		if len(picked) != 3 {
			t.Fatalf("expected all 3 items in the collected batch, got %#v in %v", row["picked"], row)
		}
	}
	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("collect pipeline over-released %d message reference(s)", n)
	}
}
