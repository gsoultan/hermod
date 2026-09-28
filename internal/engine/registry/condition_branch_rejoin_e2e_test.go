package registry

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// A condition whose branches meet again is the ordinary way to draw "tag the
// rows that match, pass the rest through":
//
//	src ─> if ─true──> mark ─> out
//	       └──false──────────┘
//
// Every message reaches `out` whichever way the condition goes. It did not: a
// false message waited in `out` for its other edge, the pruned true branch
// arrived last, and the traversal pruned `out` instead of running it. Which
// edge arrives last follows the order the edges were drawn, so this workflow —
// false edge drawn first — wrote every true row and not one false row, while
// the editor's simulation, which walks the graph its own way, showed both.
//
// It is driven from a stored workflow through StartWorkflow, with the
// condition saved the way the editor saves it, because the loss was in the
// assembly: the condition node and the sink were each correct on their own.
func TestConditionBranchesThatMeetAgainDeliverEveryMessage(t *testing.T) {
	const rows = 6 // seq 1..6: three take each branch

	store := newPipeStorage()
	reg := NewRegistry(store)
	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}

	wf := storage.Workflow{
		ID:   "wf-if-rejoin",
		Name: "wf-if-rejoin",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "if", Type: "condition", Config: map[string]any{
				"label": "Condition (If)", "type": "condition",
				"conditions": []any{map[string]any{"field": "seq", "operator": ">", "value": "3"}},
			}},
			{ID: "mark", Type: "transformation", Config: map[string]any{"transType": "set", "column.lane": "'true'"}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e-in", SourceID: "src", TargetID: "if"},
			{ID: "e-false", SourceID: "if", TargetID: "out", SourceHandle: "false"},
			{ID: "e-true", SourceID: "if", TargetID: "mark", SourceHandle: "true"},
			{ID: "e-mark", SourceID: "mark", TargetID: "out"},
		},
		MaxRetries:    5,
		RetryInterval: "10ms",
	}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, wf, &pipeSource{name: "orders", count: rows}, sinks)
	defer stop()

	waitUntil(t, 20*time.Second, "every row at the sink", func() bool {
		return sinks["snk-out"].distinct() >= rows
	})

	lanes := map[string]any{}
	for _, row := range sinks["snk-out"].received() {
		lanes[fmt.Sprint(row["seq"])] = row["lane"]
	}
	for seq := 1; seq <= rows; seq++ {
		lane, arrived := lanes[strconv.Itoa(seq)]
		switch {
		case !arrived:
			t.Errorf("row seq=%d never reached the sink (it took the %s branch)", seq, branchOf(seq))
		case seq > 3 && lane != "true":
			t.Errorf("row seq=%d took the true branch but arrived without its lane: %v", seq, lane)
		case seq <= 3 && lane != nil:
			t.Errorf("row seq=%d took the false branch but arrived marked %v", seq, lane)
		}
	}
	if n := message.OverReleaseCount(); n != 0 {
		t.Errorf("the pipeline over-released %d message reference(s)", n)
	}
}

func branchOf(seq int) string {
	if seq > 3 {
		return "true"
	}
	return "false"
}
