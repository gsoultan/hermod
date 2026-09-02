package registry

import (
	"database/sql"
	"testing"

	"github.com/gsoultan/Hermod/internal/storage"
	sqlstorage "github.com/gsoultan/Hermod/internal/storage/sql"
	"github.com/gsoultan/Hermod/pkg/comm/message"
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
