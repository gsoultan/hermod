package registry

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// A message trace is only worth opening if it is a record of *this* message.
//
// Three places in the trace machinery hand a step a payload it did not capture
// itself, and each one is somewhere message N's data can be filed under message
// N+1:
//
//   - the router snapshot taken before the traversal (pkg/engine/runner.go:868),
//   - the ctx-cached LastTraceSnapshotKey that recordTraceStep prefers over
//     msg.ToMap() (pkg/engine/telemetry_methods.go:59),
//   - the shared pipeline pointer doApplyTransformation reads at registry.go:1459
//     and writes at registry.go:1485.
//
// The failure mode is silent. The trace still renders, the steps are still in
// order, the payload is still valid JSON — it is just not about the message
// whose id is at the top of the page. That is exactly how the db_lookup cache
// key collapse hid: the enriched block was byte-identical across messages while
// the input field differed.
//
// So drive the real traversal with a source whose every record differs, and
// assert each recorded step carries its own message's data — and that two
// different inputs never produce the same output. Identical payloads for
// different records is the defect, not a coincidence.
// ---------------------------------------------------------------------------

// varyingSource emits `count` records that differ in the fields the workflow
// reads, then blocks like a caught-up source.
//
// `amount` is deliberately constant: anything that caches or keys on it would
// collapse every message into one, and holding it still is what makes that
// visible rather than masked by a field that happens to vary.
type varyingSource struct {
	count   int
	emitted atomic.Int64
}

func (s *varyingSource) Read(ctx context.Context) (hermod.Message, error) {
	i := s.emitted.Add(1)
	if i > int64(s.count) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("m-%d", i))
	msg.SetData("order_id", fmt.Sprintf("o-%d", i))
	msg.SetData("city", fmt.Sprintf("city-%d", i))
	msg.SetData("amount", 100)
	return msg, nil
}

func (s *varyingSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *varyingSource) Ping(ctx context.Context) error                    { return nil }
func (s *varyingSource) Close() error                                      { return nil }

// traceStorage is the pipeline storage plus the one method that makes a trace
// observable: the registry is its own TraceRecorder and writes every step to
// the *log* store, so SetLogStorage is what has to be pointed here.
type traceStorage struct {
	*pipeStorage
	mu    sync.Mutex
	steps map[string][]hermod.TraceStep
}

func newTraceStorage() *traceStorage {
	return &traceStorage{pipeStorage: newPipeStorage(), steps: make(map[string][]hermod.TraceStep)}
}

func (s *traceStorage) RecordTraceStep(_ context.Context, _, messageID string, step hermod.TraceStep) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps[messageID] = append(s.steps[messageID], step)
	return nil
}

func (s *traceStorage) stepsFor(messageID string) []hermod.TraceStep {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]hermod.TraceStep, len(s.steps[messageID]))
	copy(out, s.steps[messageID])
	return out
}

func (s *traceStorage) tracedMessages() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.steps)
}

// traceFidelityWorkflow is a two-step pipeline inside one transformation node.
//
// The second step reads what the first one wrote (`route` is built from
// `city_tag`), which is what puts the shared pipeline snapshot pointer on the
// path: step 2's recorded "before" comes from that pointer rather than from the
// message, so a pointer carrying someone else's payload shows up here and
// nowhere else.
func traceFidelityWorkflow(id string) storage.Workflow {
	return storage.Workflow{
		ID:   id,
		Name: id,
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "enrich", Type: "transformation", Config: map[string]any{
				"transType": "pipeline",
				"steps": `[{"transType":"set","column.city_tag":"upper(source.city)"},` +
					`{"transType":"set","column.route":"concat(source.city_tag, source.order_id)"}]`,
			}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "enrich"},
			{ID: "e2", SourceID: "enrich", TargetID: "out"},
		},
		TraceSampleRate: 1.0,
		MaxRetries:      5,
		RetryInterval:   "10ms",
	}
}

// wantRoute is the only correct output for record i. Deriving it here rather
// than reading it back from the sink is the point: a test that compares the
// pipeline against itself cannot tell "every message produced its own answer"
// from "every message produced the same answer".
func wantRoute(i int) string {
	return fmt.Sprintf("CITY-%do-%d", i, i)
}

// TestDifferentSourceRecordsProduceDifferentResults is the data-flow half:
// records that differ at the source must differ at the sink, each carrying the
// value derived from its own input and no one else's.
func TestDifferentSourceRecordsProduceDifferentResults(t *testing.T) {
	const records = 12

	store := newTraceStorage()
	reg := NewRegistry(store)
	reg.SetLogStorage(store)

	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, traceFidelityWorkflow("wf-trace-results"), &varyingSource{count: records}, sinks)
	defer stop()

	if !waitUntil(t, 30*time.Second, "one write per record", func() bool {
		return sinks["snk-out"].count() >= records
	}) {
		t.Fatalf("sink received %d rows, want %d", sinks["snk-out"].count(), records)
	}

	// Every delivered row must carry the route built from its own order_id and
	// city. Delivery is at-least-once, so index by order_id rather than by
	// arrival order and let a duplicate agree with itself.
	got := map[string]string{}
	for _, row := range sinks["snk-out"].received() {
		orderID, _ := row["order_id"].(string)
		if orderID == "" {
			t.Fatalf("row delivered without an order_id: %v", row)
		}
		route, _ := row["route"].(string)
		if prev, seen := got[orderID]; seen && prev != route {
			t.Fatalf("order %s was delivered twice with different routes %q and %q", orderID, prev, route)
		}
		got[orderID] = route
	}

	if len(got) != records {
		t.Fatalf("sink saw %d distinct orders, want %d: %v", len(got), records, got)
	}

	distinct := map[string]string{}
	for i := 1; i <= records; i++ {
		orderID := fmt.Sprintf("o-%d", i)
		want := wantRoute(i)
		if got[orderID] != want {
			t.Errorf("order %s was delivered with route %q, want %q — the node produced a value that is not derived from this record's own input",
				orderID, got[orderID], want)
		}
		if owner, clash := distinct[got[orderID]]; clash {
			t.Errorf("orders %s and %s were both delivered with route %q; two different source records must not produce the same result",
				owner, orderID, got[orderID])
		}
		distinct[got[orderID]] = orderID
	}

	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("pipeline over-released %d message reference(s)", n)
	}
}

// TestTraceStepsRecordTheirOwnMessagesData is the trace half: every step
// persisted under message m-i must describe record i, at every node, on both
// halves of the step.
func TestTraceStepsRecordTheirOwnMessagesData(t *testing.T) {
	const records = 12

	store := newTraceStorage()
	reg := NewRegistry(store)
	reg.SetLogStorage(store)

	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, traceFidelityWorkflow("wf-trace-steps"), &varyingSource{count: records}, sinks)
	defer stop()

	if !waitUntil(t, 30*time.Second, "one write per record", func() bool {
		return sinks["snk-out"].count() >= records
	}) {
		t.Fatalf("sink received %d rows, want %d", sinks["snk-out"].count(), records)
	}

	// Steps are recorded on detached goroutines, so the last of them lands after
	// the write that released the wait above.
	//
	// Waiting on the *node ids* rather than on a step count is what keeps the
	// assertions below honest: workflow_start and router are the engine's own
	// pseudo-nodes and each reaches the recorder by its own path — router
	// through RecordTraceStepSnapshot with a payload captured before the
	// traversal, the rest through recordTraceStep, which prefers a cached ctx
	// snapshot over the message. A count alone would pass while the one step
	// that exercises a given path had not arrived.
	want := []string{"workflow_start", "router", "enrich", "set"}
	traced := func() bool {
		if store.tracedMessages() < records {
			return false
		}
		for i := 1; i <= records; i++ {
			if !hasNodes(store.stepsFor(fmt.Sprintf("m-%d", i)), want) {
				return false
			}
		}
		return true
	}
	if !waitUntil(t, 30*time.Second, "every message's trace steps to land", traced) {
		for i := 1; i <= records; i++ {
			id := fmt.Sprintf("m-%d", i)
			t.Logf("%s: %s", id, describeSteps(store.stepsFor(id)))
		}
		t.Fatalf("only %d of %d messages were traced with %v", store.tracedMessages(), records, want)
	}

	// A pipeline of two steps has to record two, under the transformation type.
	// One would mean a step was dropped, and a dropped step is invisible in the
	// viewer -- the trace simply reads as though the node did less work.
	for i := 1; i <= records; i++ {
		msgID := fmt.Sprintf("m-%d", i)
		var sets int
		for _, step := range store.stepsFor(msgID) {
			if step.NodeID == "set" {
				sets++
			}
		}
		if sets != 2 {
			t.Errorf("%s recorded %d \"set\" steps for a two-step pipeline, want 2: %s",
				msgID, sets, describeSteps(store.stepsFor(msgID)))
		}
	}

	// Every payload recorded under m-i must belong to record i. A step that
	// names another record's order_id is a step filed under the wrong message.
	for i := 1; i <= records; i++ {
		msgID := fmt.Sprintf("m-%d", i)
		wantOrder := fmt.Sprintf("o-%d", i)
		wantCity := fmt.Sprintf("city-%d", i)

		for _, step := range store.stepsFor(msgID) {
			for half, payload := range map[string]map[string]any{"before": step.Before, "after": step.After} {
				if payload == nil {
					continue
				}
				if order, ok := payload["order_id"].(string); ok && order != wantOrder {
					t.Errorf("%s step %q recorded %s=%q, but this trace belongs to record %s",
						msgID, step.NodeID, half, order, wantOrder)
				}
				if city, ok := payload["city"].(string); ok && city != wantCity {
					t.Errorf("%s step %q recorded %s city=%q, want %q",
						msgID, step.NodeID, half, city, wantCity)
				}
			}
		}
	}

	// The step that wrote `route` must have been handed the `city_tag` the step
	// before it produced — for this message. This is the shared pipeline
	// snapshot pointer: step 2's "before" comes from the pointer, not from the
	// message, so a stale pointer is visible here and nowhere else.
	for i := 1; i <= records; i++ {
		msgID := fmt.Sprintf("m-%d", i)
		wantTag := fmt.Sprintf("CITY-%d", i)

		var routeStep *hermod.TraceStep
		for idx, step := range store.stepsFor(msgID) {
			if step.After != nil && step.After["route"] != nil && step.NodeID == "set" {
				steps := store.stepsFor(msgID)
				routeStep = &steps[idx]
				break
			}
		}
		if routeStep == nil {
			t.Errorf("%s: no pipeline step recorded the route it produced; steps: %s", msgID, describeSteps(store.stepsFor(msgID)))
			continue
		}
		if routeStep.Before == nil {
			t.Errorf("%s: the route step recorded no input payload at all", msgID)
			continue
		}
		if tag, _ := routeStep.Before["city_tag"].(string); tag != wantTag {
			t.Errorf("%s: the route step was recorded as having been handed city_tag=%q, want %q — the step before it, on this message, produced %q",
				msgID, tag, wantTag, wantTag)
		}
		if route, _ := routeStep.After["route"].(string); route != wantRoute(i) {
			t.Errorf("%s: the route step recorded route=%q, want %q", msgID, route, wantRoute(i))
		}
	}

	// Across messages, the traversal's node step must never record the same
	// payload twice. One repeated payload is the whole failure this file is
	// about, and counting distinct values is what catches it when every
	// individual assertion above happens to pass on a shared field.
	owner := map[string]string{}
	for i := 1; i <= records; i++ {
		msgID := fmt.Sprintf("m-%d", i)
		for _, step := range store.stepsFor(msgID) {
			if step.NodeID != "enrich" || step.After == nil {
				continue
			}
			route, _ := step.After["route"].(string)
			if route == "" {
				t.Errorf("%s: the enrich node step recorded no route: %#v", msgID, step.After)
				continue
			}
			if prev, clash := owner[route]; clash && prev != msgID {
				t.Errorf("messages %s and %s both recorded route %q at the enrich node; two records that differ at the source cannot trace identically",
					prev, msgID, route)
			}
			owner[route] = msgID
		}
	}

	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("pipeline over-released %d message reference(s)", n)
	}
}

// A step's `before` is not stored. GetMessageTrace rebuilds it from the
// previous step's `after`, in timestamp order -- so a step whose timestamp and
// whose payload come from different moments corrupts its *neighbour's* before
// image, not just its own.
//
// traversal.runNode stamped a node step with the node's start time and
// snapshotted the payload when it recorded, which is after the node ran. For a
// leaf node the two moments are close enough to be one. For a `pipeline` node
// they are not: each pipeline step records again under its own transType at its
// own, later timestamp, so the parent sorted *ahead of its own children*
// carrying their finished output. The first child was then shown as having been
// handed a field it had not computed yet -- and its `after`, which lacks that
// field, read as a deletion.
//
// This is the same defect the router fix of 2026-09-17 was written for. A single
// transformation cannot show it: it produces one sub-step with the same payload
// as its parent. It takes a pipeline.
func TestAPipelineNodeStepDoesNotSortAheadOfItsOwnSteps(t *testing.T) {
	const records = 6

	store := newTraceStorage()
	reg := NewRegistry(store)
	reg.SetLogStorage(store)

	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}

	message.ResetOverReleaseCount()
	stop := startForeachPipeline(t, reg, traceFidelityWorkflow("wf-trace-order"), &varyingSource{count: records}, sinks)
	defer stop()

	if !waitUntil(t, 30*time.Second, "one write per record", func() bool {
		return sinks["snk-out"].count() >= records
	}) {
		t.Fatalf("sink received %d rows, want %d", sinks["snk-out"].count(), records)
	}

	want := []string{"workflow_start", "router", "enrich", "set"}
	if !waitUntil(t, 30*time.Second, "every message's trace steps to land", func() bool {
		if store.tracedMessages() < records {
			return false
		}
		for i := 1; i <= records; i++ {
			steps := store.stepsFor(fmt.Sprintf("m-%d", i))
			if !hasNodes(steps, want) || countNode(steps, "set") < 2 {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("only %d of %d messages were fully traced", store.tracedMessages(), records)
	}

	for i := 1; i <= records; i++ {
		msgID := fmt.Sprintf("m-%d", i)
		steps := inTimestampOrder(store.stepsFor(msgID))

		// The root cause: the node's own step carries the work its sub-steps
		// did, so it cannot be timestamped before them.
		var node, lastSub time.Time
		for _, s := range steps {
			switch s.NodeID {
			case "enrich":
				node = s.Timestamp
			case "set":
				if s.Timestamp.After(lastSub) {
					lastSub = s.Timestamp
				}
			}
		}
		if node.Before(lastSub) {
			t.Errorf("%s: the \"enrich\" node step is timestamped %s, ahead of its own last pipeline step at %s — "+
				"it carries that step's output, so the trace shows the node's result before the work that produced it",
				msgID, node.Format("15:04:05.000000"), lastSub.Format("15:04:05.000000"))
		}

		// The symptom a reader sees. This workflow only ever adds fields, so no
		// step may be shown as having been handed a field it then dropped.
		for j := 1; j < len(steps); j++ {
			prev, cur := steps[j-1], steps[j]
			if cur.Timestamp.Equal(prev.Timestamp) {
				// A tie makes the order arbitrary rather than wrong.
				continue
			}
			before := prev.After // exactly what GetMessageTrace reconstructs
			for k := range before {
				if _, kept := cur.After[k]; !kept {
					t.Errorf("%s: step %q reads as having deleted %q — its reconstructed before comes from %q, "+
						"which ran later but sorted earlier",
						msgID, cur.NodeID, k, prev.NodeID)
				}
			}
		}
	}

	if n := message.OverReleaseCount(); n != 0 {
		t.Fatalf("pipeline over-released %d message reference(s)", n)
	}
}

// inTimestampOrder copies the steps into the order GetMessageTrace reads them
// in. Stable, so steps sharing a timestamp keep the order they were recorded in
// rather than swapping between runs.
func inTimestampOrder(steps []hermod.TraceStep) []hermod.TraceStep {
	out := make([]hermod.TraceStep, len(steps))
	copy(out, steps)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out
}

// countNode counts the steps recorded under one node id.
func countNode(steps []hermod.TraceStep, nodeID string) int {
	var n int
	for _, s := range steps {
		if s.NodeID == nodeID {
			n++
		}
	}
	return n
}

// hasNodes reports whether every wanted node id appears in the trace.
func hasNodes(steps []hermod.TraceStep, want []string) bool {
	seen := make(map[string]struct{}, len(steps))
	for _, s := range steps {
		seen[s.NodeID] = struct{}{}
	}
	for _, w := range want {
		if _, ok := seen[w]; !ok {
			return false
		}
	}
	return true
}

// describeSteps renders a trace compactly enough to read in a failure message.
func describeSteps(steps []hermod.TraceStep) string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, fmt.Sprintf("%s(after=%v)", s.NodeID, s.After))
	}
	return fmt.Sprint(out)
}
