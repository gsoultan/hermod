package registry

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Workflow simulation.
//
// TestWorkflow is what the editor's "test" button calls: it runs a sample
// message through the node chain without starting an engine and returns what
// each step produced. It is how a user decides their pipeline is correct before
// pointing it at production data.
//
// It had no Go coverage at all. The only thing exercising it was
// rabbitmq_e2e.spec.ts — which, despite the name, mocks its RabbitMQ connection
// and never contacts a broker; the assertions were entirely about this endpoint.
// So the spec sat in a nightly job spinning up RabbitMQ it never used, while the
// behaviour it actually tested went unverified everywhere else.
//
// An inaccurate simulation is worse than a missing one: it tells the user their
// transformation chain works, and they ship it.
// ---------------------------------------------------------------------------

func newSimRegistry(t *testing.T) *Registry {
	t.Helper()
	db, err := sql.Open("sqlite", "file:sim_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := sqlstorage.NewSQLStorage(db, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	// The workflow validator resolves the source and sink a node refers to, so
	// they have to exist even though a simulation never connects to either.
	// Without them every test here would fail on "missing source" -- including
	// the invalid-workflow one, which would then pass for the wrong reason.
	if err := store.CreateSource(t.Context(), storage.Source{
		ID: "src-1", Name: "sim source", Type: "webhook",
		Config: map[string]string{"path": "/sim"},
	}); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := store.CreateSink(t.Context(), storage.Sink{
		ID: "snk-1", Name: "sim sink", Type: "sqlite",
		Config: map[string]string{"path": ":memory:", "table": "sim"},
	}); err != nil {
		t.Fatalf("create sink: %v", err)
	}

	return NewRegistry(store)
}

// simWorkflow is a source -> two transformations -> sink chain, the shape the
// spec built through the editor.
func simWorkflow() storage.Workflow {
	return storage.Workflow{
		ID: "sim-wf", Name: "simulated", Active: false,
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "t1", Type: "transformation", Config: map[string]any{
				"transType":      "set",
				"column.country": "'USA'",
			}},
			{ID: "t2", Type: "transformation", Config: map[string]any{
				"transType":     "set",
				"column.status": "'URGENT'",
			}},
			{ID: "snk", Type: "sink", RefID: "snk-1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "t1"},
			{ID: "e2", SourceID: "t1", TargetID: "t2"},
			{ID: "e3", SourceID: "t2", TargetID: "snk"},
		},
	}
}

// TestSimulationAppliesTheWholeChain is the property the button exists for.
func TestSimulationAppliesTheWholeChain(t *testing.T) {
	reg := newSimRegistry(t)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("sim-1")
	msg.SetAfter([]byte(`{"name":"John Doe","city":"New York"}`))

	steps, err := reg.TestWorkflow(t.Context(), simWorkflow(), msg)
	if err != nil {
		t.Fatalf("TestWorkflow: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("simulation returned no steps; the editor would show an empty result " +
			"for a workflow that does something")
	}

	last := steps[len(steps)-1]
	if last.Error != "" {
		t.Fatalf("last step reported an error: %s", last.Error)
	}

	// Both the original fields and everything the chain added must be present.
	// Losing an input field is the quieter failure: the simulation looks right
	// because the added fields are there.
	for field, want := range map[string]string{
		"name":    "John Doe",
		"city":    "New York",
		"country": "USA",
		"status":  "URGENT",
	} {
		got, ok := last.Payload[field]
		if !ok {
			t.Errorf("field %q missing from the simulated output; the user is shown a "+
				"result their real pipeline would not produce. Payload: %v", field, last.Payload)
			continue
		}
		if s, _ := got.(string); s != want {
			t.Errorf("field %q simulated as %v, want %q", field, got, want)
		}
	}
}

// TestSimulationReportsEveryNode keeps the step list useful: the editor draws
// one result per node, so a chain that silently collapses to a single step
// leaves the user unable to see where a transformation went wrong.
func TestSimulationReportsEveryNode(t *testing.T) {
	reg := newSimRegistry(t)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(`{"name":"John Doe"}`))

	steps, err := reg.TestWorkflow(t.Context(), simWorkflow(), msg)
	if err != nil {
		t.Fatalf("TestWorkflow: %v", err)
	}

	seen := map[string]bool{}
	for _, s := range steps {
		seen[s.NodeID] = true
	}
	for _, id := range []string{"t1", "t2"} {
		if !seen[id] {
			t.Errorf("node %q produced no step; the editor cannot show what it did. Saw %v",
				id, seen)
		}
	}
}

// TestSimulationRejectsAnInvalidWorkflow pins the guard. Simulating a workflow
// that could never run would report success for something the engine refuses.
func TestSimulationRejectsAnInvalidWorkflow(t *testing.T) {
	reg := newSimRegistry(t)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(`{"k":"v"}`))

	// An edge pointing at a node that does not exist.
	wf := simWorkflow()
	wf.Edges = append(wf.Edges, storage.WorkflowEdge{
		ID: "bad", SourceID: "t2", TargetID: "does-not-exist",
	})

	if _, err := reg.TestWorkflow(t.Context(), wf, msg); err == nil {
		t.Error("simulating a workflow with a dangling edge succeeded; the editor would " +
			"report a pipeline as tested that the engine will not start")
	}
}

// ---------------------------------------------------------------------------
// Previewing a workflow while it is being built.
//
// Refreshing AVAILABLE FIELDS in the editor re-runs the simulation so each node
// can read what the node before it emits. Two things kept that from reaching
// the next node:
//
//   - validation refused any graph with no reachable sink. Saving that same
//     workflow only warns, so this is the state of every workflow still being
//     put together — the moment the preview is needed most;
//   - every source node was seeded with the same message, so on a workflow with
//     two sources, refreshing one branch filled the other branch's nodes with
//     columns they will never see.
// ---------------------------------------------------------------------------

// sinklessWorkflow is simWorkflow before its sink has been added.
func sinklessWorkflow() storage.Workflow {
	wf := simWorkflow()
	wf.Nodes = wf.Nodes[:3]
	wf.Edges = wf.Edges[:2]
	return wf
}

// twoSourceWorkflow is two independent branches, each tagged by its own node,
// with no sink yet.
func twoSourceWorkflow() storage.Workflow {
	return storage.Workflow{
		ID: "sim-two", Name: "two sources",
		Nodes: []storage.WorkflowNode{
			{ID: "src-a", Type: "source", RefID: "src-1"},
			{ID: "src-b", Type: "source", RefID: "src-1"},
			{ID: "ta", Type: "transformation", Config: map[string]any{"transType": "set", "column.branch": "'a'"}},
			{ID: "tb", Type: "transformation", Config: map[string]any{"transType": "set", "column.branch": "'b'"}},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "ea", SourceID: "src-a", TargetID: "ta"},
			{ID: "eb", SourceID: "src-b", TargetID: "tb"},
		},
	}
}

func sampleMessage(t *testing.T, after string) *message.DefaultMessage {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(after))
	return msg
}

// payloadOf is the output the simulation reports for a node, the thing the
// editor reads for the node after it.
func payloadOf(steps []WorkflowStepResult, nodeID string) map[string]any {
	for _, s := range steps {
		if s.NodeID == nodeID && s.Payload != nil {
			return s.Payload
		}
	}
	return nil
}

func TestSimulationPreviewsAWorkflowThatHasNoSinkYet(t *testing.T) {
	reg := newSimRegistry(t)
	msg := sampleMessage(t, `{"name":"John Doe"}`)

	// The Test button keeps refusing it: the engine would not start this
	// workflow, and a passing test must not suggest otherwise.
	if _, err := reg.TestWorkflow(t.Context(), sinklessWorkflow(), msg); err == nil {
		t.Fatal("TestWorkflow accepted a workflow with no sink; the Test button would " +
			"report as tested a workflow the engine refuses to start")
	}

	steps, err := reg.SimulateWorkflow(t.Context(), sinklessWorkflow(), SimulationInput{Message: msg, Partial: true})
	if err != nil {
		t.Fatalf("a partial preview of a workflow with no sink yet was refused: %v", err)
	}
	got := payloadOf(steps, "t2")
	if got == nil {
		t.Fatalf("the last node has no output; the node after it would have nothing to "+
			"show. Steps: %+v", steps)
	}
	for field, want := range map[string]string{"name": "John Doe", "country": "USA", "status": "URGENT"} {
		if s, _ := got[field].(string); s != want {
			t.Errorf("field %q previewed as %v, want %q. Payload: %v", field, got[field], want, got)
		}
	}
}

// A partial preview relaxes what a workflow needs in order to *run*, not what
// it needs in order to be a graph.
func TestPartialSimulationStillRejectsABrokenGraph(t *testing.T) {
	cases := map[string]func(wf *storage.Workflow){
		"an edge to a node that does not exist": func(wf *storage.Workflow) {
			wf.Edges = append(wf.Edges, storage.WorkflowEdge{ID: "bad", SourceID: "t2", TargetID: "does-not-exist"})
		},
		"a cycle": func(wf *storage.Workflow) {
			wf.Edges = append(wf.Edges, storage.WorkflowEdge{ID: "back", SourceID: "t2", TargetID: "t1"})
		},
		"no source at all": func(wf *storage.Workflow) {
			wf.Nodes = wf.Nodes[1:]
			wf.Edges = wf.Edges[1:]
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			reg := newSimRegistry(t)
			wf := sinklessWorkflow()
			breakIt(&wf)
			in := SimulationInput{Message: sampleMessage(t, `{"k":"v"}`), Partial: true}
			if _, err := reg.SimulateWorkflow(t.Context(), wf, in); err == nil {
				t.Errorf("a partial preview accepted a workflow with %s", name)
			}
		})
	}
}

func TestSimulationSeedsEachSourceWithItsOwnSample(t *testing.T) {
	reg := newSimRegistry(t)
	in := SimulationInput{
		PerSource: map[string]hermod.Message{
			"src-a": sampleMessage(t, `{"only_in_a":"A"}`),
			"src-b": sampleMessage(t, `{"only_in_b":"B"}`),
		},
		Partial: true,
	}

	steps, err := reg.SimulateWorkflow(t.Context(), twoSourceWorkflow(), in)
	if err != nil {
		t.Fatalf("SimulateWorkflow: %v", err)
	}

	for node, want := range map[string]struct{ has, lacks string }{
		"src-a": {"only_in_a", "only_in_b"},
		"ta":    {"only_in_a", "only_in_b"},
		"src-b": {"only_in_b", "only_in_a"},
		"tb":    {"only_in_b", "only_in_a"},
	} {
		got := payloadOf(steps, node)
		if _, ok := got[want.has]; !ok {
			t.Errorf("node %s lacks %q, a column its own source sent. Payload: %v", node, want.has, got)
		}
		if _, ok := got[want.lacks]; ok {
			t.Errorf("node %s shows %q, a column from the other branch's source. Payload: %v", node, want.lacks, got)
		}
	}
}

// With only one branch's sample to hand, the other branch is left unreached
// rather than filled with a sample that is not its own.
func TestSimulationLeavesASourceWithoutASampleUnseeded(t *testing.T) {
	reg := newSimRegistry(t)
	in := SimulationInput{
		PerSource: map[string]hermod.Message{"src-a": sampleMessage(t, `{"only_in_a":"A"}`)},
		Partial:   true,
	}

	steps, err := reg.SimulateWorkflow(t.Context(), twoSourceWorkflow(), in)
	if err != nil {
		t.Fatalf("SimulateWorkflow: %v", err)
	}
	if _, ok := payloadOf(steps, "ta")["only_in_a"]; !ok {
		t.Errorf("the seeded branch lost its own sample. Steps: %+v", steps)
	}
	for _, node := range []string{"src-b", "tb"} {
		if got := payloadOf(steps, node); got != nil {
			t.Errorf("node %s on the unseeded branch was given %v", node, got)
		}
	}

	// A default message still reaches every source the map does not name.
	in.Message = sampleMessage(t, `{"shared":"S"}`)
	steps, err = reg.SimulateWorkflow(t.Context(), twoSourceWorkflow(), in)
	if err != nil {
		t.Fatalf("SimulateWorkflow with a default: %v", err)
	}
	if _, ok := payloadOf(steps, "tb")["shared"]; !ok {
		t.Errorf("the default message did not reach the unnamed source. Steps: %+v", steps)
	}
	if _, ok := payloadOf(steps, "ta")["shared"]; ok {
		t.Errorf("the default message overrode the sample named for src-a. Steps: %+v", steps)
	}
}

// ---------------------------------------------------------------------------
// The path the message took.
//
// The editor draws a simulation on the canvas: the edges the message travelled
// along light up, and every node says what happened to the message there. The
// step list carried neither. There was nothing to light, and a node the message
// never reached was reported exactly like one that dropped it -- `filtered`,
// nothing else -- so "this filter dropped it" and "nothing got here" looked the
// same.
// ---------------------------------------------------------------------------

// stepOf is the step reported for a node: the first one, which is the one that
// carries its output when it had any.
func stepOf(t *testing.T, steps []WorkflowStepResult, nodeID string) WorkflowStepResult {
	t.Helper()
	for _, s := range steps {
		if s.NodeID == nodeID {
			return s
		}
	}
	t.Fatalf("no step for node %q. Steps: %+v", nodeID, steps)
	return WorkflowStepResult{}
}

func TestSimulationReportsTheEdgesItsOutputTravelled(t *testing.T) {
	reg := newSimRegistry(t)

	steps, err := reg.TestWorkflow(t.Context(), simWorkflow(), sampleMessage(t, `{"name":"John Doe"}`))
	if err != nil {
		t.Fatalf("TestWorkflow: %v", err)
	}

	for node, want := range map[string][]string{"src": {"e1"}, "t1": {"e2"}, "t2": {"e3"}, "snk": nil} {
		if got := stepOf(t, steps, node).TakenEdges; !slices.Equal(got, want) {
			t.Errorf("node %s reports taken edges %v, want %v", node, got, want)
		}
	}
}

// conditionWorkflow sends gold customers one way and everyone else the other.
// Each branch is named on its edge the way the editor names it: the handle the
// edge leaves from, copied into the edge's label.
func conditionWorkflow() storage.Workflow {
	return storage.Workflow{
		ID: "sim-condition", Name: "condition",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "is-gold", Type: "condition", Config: map[string]any{"field": "tier", "operator": "=", "value": "gold"}},
			{ID: "gold", Type: "transformation", Config: map[string]any{"transType": "set", "column.lane": "'gold'"}},
			{ID: "other", Type: "transformation", Config: map[string]any{"transType": "set", "column.lane": "'other'"}},
			{ID: "after-other", Type: "transformation", Config: map[string]any{"transType": "set", "column.seen": "'yes'"}},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e-in", SourceID: "src", TargetID: "is-gold"},
			{ID: "e-true", SourceID: "is-gold", TargetID: "gold", SourceHandle: "true", Config: map[string]any{"label": "true"}},
			{ID: "e-false", SourceID: "is-gold", TargetID: "other", SourceHandle: "false", Config: map[string]any{"label": "false"}},
			{ID: "e-after", SourceID: "other", TargetID: "after-other"},
		},
	}
}

func TestSimulationReportsOnlyTheBranchAConditionTook(t *testing.T) {
	reg := newSimRegistry(t)
	in := SimulationInput{Message: sampleMessage(t, `{"tier":"gold"}`), Partial: true}

	steps, err := reg.SimulateWorkflow(t.Context(), conditionWorkflow(), in)
	if err != nil {
		t.Fatalf("SimulateWorkflow: %v", err)
	}

	if got := stepOf(t, steps, "is-gold").TakenEdges; !slices.Equal(got, []string{"e-true"}) {
		t.Errorf("the condition reports taken edges %v, want only the branch it took [e-true]", got)
	}
	if s := stepOf(t, steps, "gold"); s.Skipped || s.Payload == nil {
		t.Errorf("the node on the branch taken is reported as not reached: %+v", s)
	}
	// Everything past the branch not taken, not just the node right after it.
	for _, node := range []string{"other", "after-other"} {
		s := stepOf(t, steps, node)
		if !s.Skipped {
			t.Errorf("node %s is on the branch not taken but is not reported skipped: %+v", node, s)
		}
		if len(s.TakenEdges) > 0 {
			t.Errorf("node %s was never reached but reports taken edges %v", node, s.TakenEdges)
		}
	}
}

func TestSimulationTellsADroppedMessageFromOneThatNeverArrived(t *testing.T) {
	reg := newSimRegistry(t)
	wf := storage.Workflow{
		ID: "sim-filter", Name: "filter",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1"},
			{ID: "keep-silver", Type: "transformation", Config: map[string]any{
				"transType": "filter_data", "field": "tier", "operator": "=", "value": "silver",
			}},
			{ID: "after", Type: "transformation", Config: map[string]any{"transType": "set", "column.seen": "'yes'"}},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "keep-silver"},
			{ID: "e2", SourceID: "keep-silver", TargetID: "after"},
		},
	}
	in := SimulationInput{Message: sampleMessage(t, `{"tier":"gold"}`), Partial: true}

	steps, err := reg.SimulateWorkflow(t.Context(), wf, in)
	if err != nil {
		t.Fatalf("SimulateWorkflow: %v", err)
	}

	filter := stepOf(t, steps, "keep-silver")
	if !filter.Filtered || filter.Skipped {
		t.Errorf("the filter was reached and dropped the message; want filtered and not skipped, got %+v", filter)
	}
	if len(filter.TakenEdges) > 0 {
		t.Errorf("the filter emitted nothing but reports taken edges %v", filter.TakenEdges)
	}
	after := stepOf(t, steps, "after")
	if !after.Skipped {
		t.Errorf("nothing reached the node after the filter, but it is not reported skipped: %+v", after)
	}
	// Readers that only know `filtered` keep reading an unreached node the way
	// they always have.
	if !after.Filtered {
		t.Errorf("an unreached node stopped reporting filtered: %+v", after)
	}
}

// ---------------------------------------------------------------------------
// Routing.
//
// A routing node names the branch a message takes, and the simulation has to
// follow that the way the engine does. It honoured the branch only for
// condition and switch nodes, so a simulated router sent the sample down every
// route, and every node after it showed output the engine would never produce.
// ---------------------------------------------------------------------------

// routedWorkflow is a source feeding one routing node, with a lane per branch.
// Each lane's edge names its branch the way the editor does: the handle the
// edge leaves from, copied into its label.
func routedWorkflow(router storage.WorkflowNode, branches ...string) storage.Workflow {
	wf := storage.Workflow{
		ID: "sim-routed-" + router.Type, Name: "routed",
		Nodes: []storage.WorkflowNode{{ID: "src", Type: "source", RefID: "src-1"}, router},
		Edges: []storage.WorkflowEdge{{ID: "e-in", SourceID: "src", TargetID: router.ID}},
	}
	for _, b := range branches {
		lane := "lane-" + b
		wf.Nodes = append(wf.Nodes, storage.WorkflowNode{ID: lane, Type: "transformation", Config: map[string]any{
			"transType": "set", "column.lane": "'" + b + "'",
		}})
		wf.Edges = append(wf.Edges, storage.WorkflowEdge{
			ID: "e-" + b, SourceID: router.ID, TargetID: lane, SourceHandle: b, Config: map[string]any{"label": b},
		})
	}
	return wf
}

func TestSimulationTakesOnlyTheBranchARoutingNodeChose(t *testing.T) {
	regionRouter := storage.WorkflowNode{ID: "route", Type: "router", Config: map[string]any{"rules": []any{
		map[string]any{"label": "eu", "field": "region", "operator": "=", "value": "EU"},
		map[string]any{"label": "us", "field": "region", "operator": "=", "value": "US"},
	}}}
	regionSwitch := storage.WorkflowNode{ID: "route", Type: "switch", Config: map[string]any{
		"field": "region",
		"cases": []any{
			map[string]any{"label": "eu", "value": "EU"},
			map[string]any{"label": "us", "value": "US"},
		},
	}}
	isUS := storage.WorkflowNode{ID: "route", Type: "condition", Config: map[string]any{
		"field": "region", "operator": "=", "value": "US",
	}}
	// A rule or case whose label was cleared. The editor draws its handle as
	// rule_1 / case_1, so that is the label on the edge leaving it.
	unnamedRouter := storage.WorkflowNode{ID: "route", Type: "router", Config: map[string]any{"rules": []any{
		map[string]any{"label": "eu", "field": "region", "operator": "=", "value": "EU"},
		map[string]any{"label": "", "field": "region", "operator": "=", "value": "US"},
	}}}
	unnamedSwitch := storage.WorkflowNode{ID: "route", Type: "switch", Config: map[string]any{
		"field": "region",
		"cases": []any{
			map[string]any{"label": "eu", "value": "EU"},
			map[string]any{"label": "", "value": "US"},
		},
	}}

	cases := []struct {
		name     string
		node     storage.WorkflowNode
		branches []string
		sample   string
		want     string
	}{
		{"router", regionRouter, []string{"eu", "us", "default"}, `{"region":"US"}`, "us"},
		{"router with no rule matching", regionRouter, []string{"eu", "us", "default"}, `{"region":"APAC"}`, "default"},
		{"switch", regionSwitch, []string{"eu", "us", "default"}, `{"region":"US"}`, "us"},
		{"condition", isUS, []string{"true", "false"}, `{"region":"US"}`, "true"},
		{"router matching an unnamed rule", unnamedRouter, []string{"eu", "rule_1", "default"}, `{"region":"US"}`, "rule_1"},
		{"switch matching an unnamed case", unnamedSwitch, []string{"eu", "case_1", "default"}, `{"region":"US"}`, "case_1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := newSimRegistry(t)
			in := SimulationInput{Message: sampleMessage(t, tc.sample), Partial: true}

			steps, err := reg.SimulateWorkflow(t.Context(), routedWorkflow(tc.node, tc.branches...), in)
			if err != nil {
				t.Fatalf("SimulateWorkflow: %v", err)
			}

			// What the editor's canvas draws: the routing node names only the
			// edge to the branch it chose.
			if got, want := stepOf(t, steps, "route").TakenEdges, []string{"e-" + tc.want}; !slices.Equal(got, want) {
				t.Errorf("the routing node reports taken edges %v, want %v", got, want)
			}
			for _, b := range tc.branches {
				got := payloadOf(steps, "lane-"+b)
				switch {
				case b == tc.want && got == nil:
					t.Errorf("the %q branch was chosen, but its lane received nothing. Steps: %+v", b, steps)
				case b != tc.want && got != nil:
					t.Errorf("the %q branch was not chosen, but its lane received %v; the engine would never "+
						"send the message there", b, got)
				}
			}
		})
	}
}
