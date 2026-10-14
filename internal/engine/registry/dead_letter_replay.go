package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	pkgengine "github.com/gsoultan/hermod/pkg/engine"
)

var (
	// ErrDeadLetterTruncated: the kept payload was cut at the cap, so it is
	// not the message that failed. It can still be replayed with an edit.
	ErrDeadLetterTruncated = errors.New("the payload was too large to keep whole; edit it to replay it")
	// ErrDeadLetterNodeGone: the node or sink the message failed at is no
	// longer in the workflow. It can still be replayed from the start.
	ErrDeadLetterNodeGone = errors.New("the node this message failed at is no longer in the workflow; replay it from the start")
)

// DeadLetterReplay is how a dead letter is sent again.
type DeadLetterReplay struct {
	// Payload replaces the kept payload when set: an edit and replay.
	Payload []byte
	// FromStart runs the whole workflow instead of starting at the node or
	// sink that failed.
	FromStart bool
}

// deadLetterReplayKey marks a replay's walk. A node that fails it again
// leaves the dead letter where it is, in the queue, rather than parking a
// second copy of the message beside it.
type deadLetterReplayKey struct{}

func replayingDeadLetter(ctx context.Context) bool {
	return ctx.Value(deadLetterReplayKey{}) != nil
}

// failureMarks are the metadata keys the engine adds when it parks a message.
// A replayed message is a fresh attempt and carries none of them.
var failureMarks = []string{
	"_hermod_failed_node", "_hermod_failed_sink", "_hermod_last_error", "_hermod_failed_at", pkgengine.MetaDeadLettered,
}

// deadLetterMessage rebuilds the message a dead letter kept, with payload in
// place of the kept one when it is set.
func deadLetterMessage(dl storage.DeadLetter, payload []byte) (*message.DefaultMessage, error) {
	if payload == nil {
		if dl.Truncated {
			return nil, ErrDeadLetterTruncated
		}
		payload = dl.Payload
	} else if !json.Valid(payload) {
		return nil, errors.New("the edited payload is not valid JSON")
	}
	m := message.AcquireMessage()
	m.SetID(dl.MessageID)
	m.SetOperation(hermod.Operation(dl.Origin.Operation))
	m.SetTable(dl.Origin.Table)
	m.SetSchema(dl.Origin.Schema)
	if len(dl.Origin.Before) > 0 {
		m.SetBefore(dl.Origin.Before)
	}
	m.SetPayload(payload)
	for k, v := range dl.Metadata {
		if !slices.Contains(failureMarks, k) {
			m.SetMetadata(k, v)
		}
	}
	return m, nil
}

// replayStart is the node a replay starts at: the node that failed the
// message, or the sink node that refused it.
func replayStart(wf storage.Workflow, dl storage.DeadLetter) (*storage.WorkflowNode, error) {
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		switch {
		case dl.NodeID != "" && n.ID == dl.NodeID:
			return n, nil
		case dl.NodeID == "" && dl.SinkID != "" && n.Type == "sink" && (n.RefID == dl.SinkID || n.ID == dl.SinkID):
			return n, nil
		}
	}
	return nil, ErrDeadLetterNodeGone
}

// ReplayDeadLetter sends a dead letter through wf again, through the walk a
// manual run takes, and reports how it went. The nodes really run and the
// sinks really write. The message keeps its id, so a sink that deduplicates
// on it recognises a message it already has.
func (r *Registry) ReplayDeadLetter(ctx context.Context, wf storage.Workflow, dl storage.DeadLetter, opt DeadLetterReplay) (RunResult, error) {
	in, err := deadLetterMessage(dl, opt.Payload)
	if err != nil {
		return RunResult{}, err
	}
	defer in.Release()
	var start *storage.WorkflowNode
	if opt.FromStart {
		start, err = runSourceNode(wf, "")
	} else {
		start, err = replayStart(wf, dl)
	}
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

	in.SetMetadata("_hermod_workflow_id", wf.ID)
	in.SetMetadata("_hermod_source", "dead_letter_replay")
	if wf.VHost != "" {
		in.SetVHost(wf.VHost)
	}
	rec := &manualRun{r: r, workflowID: wf.ID, runID: uuid.NewString()}
	ctx = context.WithValue(context.WithValue(ctx, manualRunKey{}, rec), deadLetterReplayKey{}, dl.ID)
	r.broadcastLog(wf.ID, "INFO", fmt.Sprintf("Replaying dead letter %s (message %s) at node %s",
		dl.ID, dl.MessageID, r.getNodeName(*start)))

	eng := r.liveEngine(wf.ID)
	if start.Type == "source" {
		rec.record(ctx, start, time.Now(), in, []hermod.Message{in}, "", nil)
		r.resumeFromNode(ctx, wf.ID, start.ID, in, eng, wf, nodeMap, adj, sinks, sinkNodeToIndex, "")
	} else {
		r.runWorkflowNodeFromReplay(ctx, wf.ID, start, in, "", eng, wf, nodeMap, adj, sinks, sinkNodeToIndex)
	}
	return rec.result(), ctx.Err()
}

// PreviewDeadLetterReplay shows what each node would make of the replay,
// without writing anything: a simulation of the part of wf the replay runs.
func (r *Registry) PreviewDeadLetterReplay(ctx context.Context, wf storage.Workflow, dl storage.DeadLetter, opt DeadLetterReplay) ([]WorkflowStepResult, error) {
	in, err := deadLetterMessage(dl, opt.Payload)
	if err != nil {
		return nil, err
	}
	defer in.Release()
	if opt.FromStart {
		return r.SimulateWorkflow(ctx, wf, SimulationInput{Message: in, Partial: true})
	}
	start, err := replayStart(wf, dl)
	if err != nil {
		return nil, err
	}
	sub := replaySubgraph(wf, start.ID)
	steps, err := r.SimulateWorkflow(ctx, sub, SimulationInput{Message: in, Partial: true})
	if err != nil {
		return nil, err
	}
	out := steps[:0]
	for _, s := range steps {
		if s.NodeID != replayPreviewSource {
			out = append(out, s)
		}
	}
	return out, nil
}

// replayPreviewSource feeds the start node in a preview's subgraph.
const replayPreviewSource = "_dead_letter_replay"

// replaySubgraph is the part of wf a replay from startID reaches, fed by a
// source of its own.
func replaySubgraph(wf storage.Workflow, startID string) storage.Workflow {
	_, adj := workflowGraph(wf)
	reach := map[string]bool{startID: true}
	queue := []string{startID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, next := range adj[id] {
			if !reach[next] {
				reach[next] = true
				queue = append(queue, next)
			}
		}
	}
	sub := wf
	sub.Nodes = []storage.WorkflowNode{{ID: replayPreviewSource, Type: "source"}}
	for _, n := range wf.Nodes {
		if reach[n.ID] {
			sub.Nodes = append(sub.Nodes, n)
		}
	}
	sub.Edges = []storage.WorkflowEdge{{ID: replayPreviewSource, SourceID: replayPreviewSource, TargetID: startID}}
	for _, e := range wf.Edges {
		if reach[e.SourceID] && reach[e.TargetID] && !strings.EqualFold(e.TargetID, replayPreviewSource) {
			sub.Edges = append(sub.Edges, e)
		}
	}
	return sub
}
