package sql

import (
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// A step's `before` is not stored. GetMessageTrace rebuilds it from the
// *previous* step's `after`, walking the rows in timestamp order.
//
// That reconstruction is only correct if "previous" means the previous step of
// the same message. Two messages moving through one workflow interleave in
// time -- that is what a workflow running at all looks like -- so if the walk
// ever spanned messages, opening message A's trace would show A's node being
// handed B's payload. Nothing about that renders as an error: the trace is
// well-formed, the timestamps are in order, and the data is real. It is just
// another record's.
//
// TestGetMessageTrace_ReconstructsBeforeFromThePreviousStep pins the
// reconstruction, but with a single message in the table it cannot distinguish
// "the previous step of this message" from "the previous row". This does.
func TestGetMessageTrace_KeepsInterleavedMessagesApart(t *testing.T) {
	s, _ := newTraceStorage(t)

	const wf = "wf-iso"
	base := time.Now().UTC().Add(-time.Minute)
	nodes := []string{"workflow_start", "enrich", "sink"}

	// Interleave: a@0s, b@1s, a@2s, b@3s, a@4s, b@5s. Ordered by timestamp
	// alone, every one of A's steps is preceded by one of B's.
	for i, node := range nodes {
		for _, m := range []struct{ id, order string }{{"msg-a", "o-a"}, {"msg-b", "o-b"}} {
			offset := time.Duration(i*2)*time.Second + map[string]time.Duration{"msg-a": 0, "msg-b": time.Second}[m.id]
			err := s.RecordTraceStep(t.Context(), wf, m.id, hermod.TraceStep{
				NodeID:    node,
				Timestamp: base.Add(offset),
				Duration:  10 * time.Millisecond,
				After:     map[string]any{"order_id": m.order, "node": node, "stage": fmt.Sprintf("%s-%d", m.order, i)},
			})
			if err != nil {
				t.Fatalf("RecordTraceStep(%s/%s): %v", m.id, node, err)
			}
		}
	}

	for _, m := range []struct{ id, order string }{{"msg-a", "o-a"}, {"msg-b", "o-b"}} {
		tr, err := s.GetMessageTrace(t.Context(), wf, m.id)
		if err != nil {
			t.Fatalf("GetMessageTrace(%s): %v", m.id, err)
		}
		if len(tr.Steps) != len(nodes) {
			t.Fatalf("%s: got %d steps, want %d — the read pulled in another message's rows: %+v",
				m.id, len(tr.Steps), len(nodes), tr.Steps)
		}

		for i, step := range tr.Steps {
			if got := step.After["order_id"]; got != m.order {
				t.Errorf("%s step %d (%s) recorded after order_id=%v, want %q",
					m.id, i, step.NodeID, got, m.order)
			}
			if i == 0 {
				if step.Before != nil {
					t.Errorf("%s: the first step has no predecessor, so Before must be nil, got %v", m.id, step.Before)
				}
				continue
			}
			if step.Before == nil {
				t.Fatalf("%s step %d has no Before; it should carry the previous step's After", m.id, i)
			}
			if got := step.Before["order_id"]; got != m.order {
				t.Errorf("%s step %d (%s) was reconstructed as having been handed order_id=%v, want %q — "+
					"`before` was taken from the previous row rather than this message's previous step",
					m.id, i, step.NodeID, got, m.order)
			}
			if got, want := step.Before["stage"], tr.Steps[i-1].After["stage"]; got != want {
				t.Errorf("%s step %d Before stage=%v, want the previous step's After stage=%v", m.id, i, got, want)
			}
		}
	}
}

// The same isolation on the write side: a step recorded for one message must
// not surface in another message's trace just because the two share a workflow
// and a node id.
func TestGetMessageTrace_DoesNotMergeStepsSharingANodeID(t *testing.T) {
	s, _ := newTraceStorage(t)

	const wf = "wf-shared-node"
	base := time.Now().UTC().Add(-time.Minute)

	for i := range 4 {
		msgID := fmt.Sprintf("msg-%d", i)
		err := s.RecordTraceStep(t.Context(), wf, msgID, hermod.TraceStep{
			NodeID:    "enrich", // every message goes through the same node
			Timestamp: base.Add(time.Duration(i) * time.Second),
			After:     map[string]any{"order_id": fmt.Sprintf("o-%d", i)},
		})
		if err != nil {
			t.Fatalf("RecordTraceStep(%s): %v", msgID, err)
		}
	}

	seen := map[string]string{}
	for i := range 4 {
		msgID := fmt.Sprintf("msg-%d", i)
		tr, err := s.GetMessageTrace(t.Context(), wf, msgID)
		if err != nil {
			t.Fatalf("GetMessageTrace(%s): %v", msgID, err)
		}
		if len(tr.Steps) != 1 {
			t.Fatalf("%s: got %d steps, want 1 — %+v", msgID, len(tr.Steps), tr.Steps)
		}
		order, _ := tr.Steps[0].After["order_id"].(string)
		if want := fmt.Sprintf("o-%d", i); order != want {
			t.Errorf("%s: trace shows order_id=%q, want %q", msgID, order, want)
		}
		if owner, clash := seen[order]; clash {
			t.Errorf("%s and %s both trace to order_id=%q; four different records cannot share one trace",
				owner, msgID, order)
		}
		seen[order] = msgID
	}
}
