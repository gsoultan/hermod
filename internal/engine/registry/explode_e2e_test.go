package registry

import (
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/structure"
)

// The explode node and a structural transformer from a stored workflow: the
// registry builds the nodes from their config, explode turns each order into
// one record per line with the line's fields merged in, and template_render
// runs on every one of them on the way to the sink.
func TestExplodeNodeFeedsEachElementDownstream(t *testing.T) {
	const orders = 3
	const linesPerOrder = 3

	reg := NewRegistry(newPipeStorage())
	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}

	wf := storage.Workflow{
		ID:   "wf-explode",
		Name: "wf-explode",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "exp", Type: "explode", Config: map[string]any{"arrayPath": "lines", "mode": "merge"}},
			{ID: "tpl", Type: "transformation", Config: map[string]any{
				"transType":   "template_render",
				"template":    "{{.order_id}}/{{.sku}}",
				"targetField": "label",
			}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "exp"},
			{ID: "e2", SourceID: "exp", TargetID: "tpl"},
			{ID: "e3", SourceID: "tpl", TargetID: "out"},
		},
		MaxRetries:    5,
		RetryInterval: "10ms",
	}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, wf, &arraySource{count: orders}, sinks)
	defer stop()

	want := orders * linesPerOrder
	if !waitUntil(t, 30*time.Second, "one write per line", func() bool {
		return sinks["snk-out"].count() >= want
	}) {
		t.Fatalf("sink received %d rows, want %d", sinks["snk-out"].count(), want)
	}

	seen := map[string]bool{}
	for _, row := range sinks["snk-out"].received() {
		if _, kept := row["lines"]; kept {
			t.Fatalf("the exploded array is still on the row: %v", row)
		}
		label := fmt.Sprint(row["label"])
		if label != fmt.Sprintf("%v/%v", row["order_id"], row["sku"]) {
			t.Fatalf("label = %q on %v: template_render did not run on the exploded row", label, row)
		}
		seen[label] = true
	}
	if len(seen) != want {
		t.Fatalf("sink saw %d distinct lines, want %d: %v", len(seen), want, seen)
	}
	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("explode pipeline over-released %d message reference(s)", n)
	}
}
