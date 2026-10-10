package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// RunStatus is how a manual run ended.
type RunStatus string

const (
	// RunCompleted: every branch ran to its end without a node failing.
	RunCompleted RunStatus = "completed"
	// RunFailed: at least one node or sink write failed.
	RunFailed RunStatus = "failed"
	// RunWaiting: nothing failed and the message is held at an approval (or
	// another node that holds it) until someone acts.
	RunWaiting RunStatus = "waiting"
)

// RunResult is what RunWorkflowOnce reports. RunID is the id of the message
// the run sent, which is also the id its trace is stored under.
type RunResult struct {
	RunID  string             `json:"run_id"`
	Status RunStatus          `json:"status"`
	Steps  []hermod.TraceStep `json:"steps"`
}

// manualRunKey carries a run's recorder on the walk's context, so the shared
// replay walk records steps only for a manual run.
type manualRunKey struct{}

// manualRun records the steps of one manual run into the log store.
type manualRun struct {
	r          *Registry
	workflowID string
	runID      string

	mu      sync.Mutex
	steps   []hermod.TraceStep
	failed  bool
	waiting bool
}

func manualRunFrom(ctx context.Context) *manualRun {
	rec, _ := ctx.Value(manualRunKey{}).(*manualRun)
	return rec
}

// record stores one node's step: its first output, or its input when it
// emitted nothing. A nil recorder (any walk that is not a manual run) records
// nothing.
func (m *manualRun) record(ctx context.Context, node *storage.WorkflowNode, start time.Time, in hermod.Message, out []hermod.Message, branch string, err error) {
	if m == nil {
		return
	}
	payload := in
	if len(out) > 0 && out[0] != nil {
		payload = out[0]
	}
	step := hermod.TraceStep{
		NodeID:    node.ID,
		Timestamp: time.Now(),
		Duration:  time.Since(start),
		After:     payload.ToMap(),
		Lineage:   "manual_run",
	}
	if err != nil {
		step.Error = err.Error()
	}
	m.mu.Lock()
	m.steps = append(m.steps, step)
	if err != nil {
		m.failed = true
	} else if len(out) == 0 && branch == "pending" {
		m.waiting = true
	}
	m.mu.Unlock()
	m.store(ctx, step)
}

// store writes a step to the log store. A trace write is bounded and does not
// end with the run's context: the step of a node the caller cancelled is the
// step most worth keeping.
func (m *manualRun) store(ctx context.Context, step hermod.TraceStep) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	m.r.RecordStep(ctx, m.workflowID, m.runID, step)
}

func (m *manualRun) result() RunResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := RunCompleted
	switch {
	case m.failed:
		status = RunFailed
	case m.waiting:
		status = RunWaiting
	}
	return RunResult{RunID: m.runID, Status: status, Steps: append([]hermod.TraceStep(nil), m.steps...)}
}

// RunWorkflowOnce sends msg through wf once, starting at sourceNodeID (the
// workflow's first source node when empty), and returns the run's outcome.
//
// It is "run now with this input": the nodes really run and the sinks really
// write, through the same walk a resumed approval takes, whether or not the
// workflow is running. Every step is written to the workflow's traces under
// the run id regardless of the workflow's trace sample rate, so the run is in
// the workflow's run history. msg is not modified; the run works on a copy
// with a fresh id.
func (r *Registry) RunWorkflowOnce(ctx context.Context, wf storage.Workflow, msg hermod.Message, sourceNodeID string) (RunResult, error) {
	if msg == nil {
		return RunResult{}, errors.New("a run needs an input message")
	}
	source, err := runSourceNode(wf, sourceNodeID)
	if err != nil {
		return RunResult{}, err
	}
	r.prepareWorkflowNodes(ctx, wf.Nodes)
	nodeMap, adj := workflowGraph(wf)
	sinks, sinkNodeToIndex, err := r.buildWalkSinks(ctx, wf)
	if err != nil {
		return RunResult{}, err
	}
	defer func() {
		for _, s := range sinks {
			_ = s.Close()
		}
	}()

	in := msg.Clone()
	defer in.Release()
	runID := uuid.NewString()
	if setter, ok := in.(interface{ SetID(string) }); ok {
		setter.SetID(runID)
	}
	in.SetMetadata("_hermod_workflow_id", wf.ID)
	in.SetMetadata("_hermod_source", "manual_run")
	if scoped, ok := in.(hermod.VHostScoped); ok && wf.VHost != "" {
		scoped.SetVHost(wf.VHost)
	}

	rec := &manualRun{r: r, workflowID: wf.ID, runID: runID}
	ctx = context.WithValue(ctx, manualRunKey{}, rec)
	rec.record(ctx, source, time.Now(), in, []hermod.Message{in}, "", nil)
	r.broadcastLog(wf.ID, "INFO", fmt.Sprintf("Manual run %s started at node %s", runID, r.getNodeName(*source)))

	r.resumeFromNode(ctx, wf.ID, source.ID, in, r.liveEngine(wf.ID), wf, nodeMap, adj, sinks, sinkNodeToIndex, "")
	return rec.result(), ctx.Err()
}

// runSourceNode is the source node a run starts at.
func runSourceNode(wf storage.Workflow, id string) (*storage.WorkflowNode, error) {
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Type != "source" {
			continue
		}
		if id == "" || n.ID == id {
			return n, nil
		}
	}
	if id != "" {
		return nil, fmt.Errorf("node %q is not a source node of this workflow", id)
	}
	return nil, errors.New("the workflow has no source node to start a run at")
}
