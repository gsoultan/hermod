// Package executions reads a workflow's run history out of its message
// traces. A run is one triggering message: its trace is the run, its steps are
// the run's steps. Nothing is stored here; it is a view over the traces store,
// so retention, sampling and storage backends are the traces' own.
package executions

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
)

// Run statuses.
const (
	// StatusSucceeded: no step recorded an error.
	StatusSucceeded = "succeeded"
	// StatusFailed: at least one step recorded an error.
	StatusFailed = "failed"
	// StatusWaiting: nothing failed and the run's last step is an approval,
	// so the message is held until someone decides.
	StatusWaiting = "waiting"
)

// Step is one node's part in a run.
type Step struct {
	NodeID     string    `json:"node_id"`
	NodeType   string    `json:"node_type,omitempty"`
	Label      string    `json:"label,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	DurationMs int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
	// AI is the language model usage of this step's own calls.
	AI genai.Usage `json:"ai"`
	// Output is the message as the step left it; only on a single run.
	Output map[string]any `json:"output,omitempty"`
}

// Execution is one run of a workflow.
type Execution struct {
	RunID      string      `json:"run_id"`
	WorkflowID string      `json:"workflow_id"`
	StartedAt  time.Time   `json:"started_at"`
	DurationMs int64       `json:"duration_ms"`
	Status     string      `json:"status"`
	StepCount  int         `json:"step_count"`
	ErrorCount int         `json:"error_count"`
	AI         genai.Usage `json:"ai"`
	Steps      []Step      `json:"steps"`
}

// FromTrace builds a run from its trace. wf supplies node types, labels and
// edges; a step whose node is no longer in wf is still listed, untyped.
// withOutput includes each step's payload, which is large; a list leaves it out.
func FromTrace(wf storage.Workflow, t storage.MessageTrace, withOutput bool) Execution {
	ex := Execution{RunID: t.MessageID, WorkflowID: t.WorkflowID, Status: StatusSucceeded}
	if ex.WorkflowID == "" {
		ex.WorkflowID = wf.ID
	}
	nodes, preds := index(wf)

	steps := slices.Clone(t.Steps)
	slices.SortStableFunc(steps, func(a, b hermod.TraceStep) int { return a.Timestamp.Compare(b.Timestamp) })

	var start, end time.Time
	cums := make([]genai.Usage, len(steps))
	for i, s := range steps {
		if began := s.Timestamp.Add(-s.Duration); start.IsZero() || began.Before(start) {
			start = began
		}
		if s.Timestamp.After(end) {
			end = s.Timestamp
		}
		cums[i] = usageIn(s.After)

		step := Step{NodeID: s.NodeID, Timestamp: s.Timestamp, DurationMs: s.Duration.Milliseconds(), Error: s.Error}
		if n, ok := nodes[s.NodeID]; ok {
			step.NodeType = nodeType(n)
			step.Label, _ = n.Config["label"].(string)
			step.AI = delta(cums[i], baseline(steps, cums, i, preds[s.NodeID], nodes))
		}
		if withOutput {
			step.Output = s.After
		}
		if s.Error != "" {
			ex.ErrorCount++
		}
		ex.AI = add(ex.AI, step.AI)
		ex.Steps = append(ex.Steps, step)
	}
	ex.StepCount = len(ex.Steps)
	ex.StartedAt = start
	if !start.IsZero() {
		ex.DurationMs = end.Sub(start).Milliseconds()
	}
	ex.Status = status(ex)
	return ex
}

// index maps a workflow's nodes by id and each node to its predecessors.
func index(wf storage.Workflow) (map[string]storage.WorkflowNode, map[string][]string) {
	nodes := make(map[string]storage.WorkflowNode, len(wf.Nodes))
	for _, n := range wf.Nodes {
		nodes[n.ID] = n
	}
	preds := make(map[string][]string)
	for _, e := range wf.Edges {
		preds[e.TargetID] = append(preds[e.TargetID], e.SourceID)
	}
	return nodes, preds
}

func status(ex Execution) string {
	switch {
	case ex.ErrorCount > 0:
		return StatusFailed
	case len(ex.Steps) > 0 && ex.Steps[len(ex.Steps)-1].NodeType == "approval":
		return StatusWaiting
	default:
		return StatusSucceeded
	}
}

// baseline is the usage the message carried into step i: what the most recent
// earlier step of one of its predecessors carried out. Measuring against the
// node's own input, rather than whichever step happened to come before, keeps
// a fan-out's branches from counting each other's calls. When no predecessor
// step was traced (a step dropped under load), the most recent earlier step of
// a known node stands in.
func baseline(steps []hermod.TraceStep, cums []genai.Usage, i int, preds []string, nodes map[string]storage.WorkflowNode) genai.Usage {
	fallback, found := genai.Usage{}, false
	for j := i - 1; j >= 0; j-- {
		if slices.Contains(preds, steps[j].NodeID) {
			return cums[j]
		}
		if _, known := nodes[steps[j].NodeID]; known && !found {
			fallback, found = cums[j], true
		}
	}
	return fallback
}

func delta(cur, base genai.Usage) genai.Usage {
	return genai.Usage{
		Calls:        max(cur.Calls-base.Calls, 0),
		InputTokens:  max(cur.InputTokens-base.InputTokens, 0),
		OutputTokens: max(cur.OutputTokens-base.OutputTokens, 0),
	}
}

func add(a, b genai.Usage) genai.Usage {
	return genai.Usage{Calls: a.Calls + b.Calls, InputTokens: a.InputTokens + b.InputTokens, OutputTokens: a.OutputTokens + b.OutputTokens}
}

// usageIn reads the AI usage counters out of a step's stored payload. The
// metadata map is map[string]any once it has been through JSON and
// map[string]string when it has not.
func usageIn(after map[string]any) genai.Usage {
	get := func(key string) int64 {
		var v string
		switch md := after["metadata"].(type) {
		case map[string]any:
			v, _ = md[key].(string)
		case map[string]string:
			v = md[key]
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return genai.Usage{Calls: get(genai.MetaAICalls), InputTokens: get(genai.MetaAIInputTokens), OutputTokens: get(genai.MetaAIOutputTokens)}
}

// nodeType is the node's type, or the transformation it runs.
func nodeType(n storage.WorkflowNode) string {
	if n.Type == "transformation" {
		if tt, _ := n.Config["transType"].(string); tt != "" {
			return tt
		}
	}
	return n.Type
}

// engineMetaPrefix marks metadata the engine writes about a message's journey
// (its reply waiter, lineage, AI counters, verdicts). None of it belongs to the
// input, and replaying it would hand a new run the old run's bookkeeping.
const engineMetaPrefix = "_hermod_"

// Input is what a run started with, for a replay: the payload of its first
// step at a source node, and that node's id. Engine bookkeeping is removed
// from the payload's metadata; everything else is kept.
func Input(wf storage.Workflow, t storage.MessageTrace) (map[string]any, string, bool) {
	sources := make(map[string]bool)
	for _, n := range wf.Nodes {
		if n.Type == "source" {
			sources[n.ID] = true
		}
	}
	steps := slices.Clone(t.Steps)
	slices.SortStableFunc(steps, func(a, b hermod.TraceStep) int { return a.Timestamp.Compare(b.Timestamp) })
	for _, s := range steps {
		if !sources[s.NodeID] || s.After == nil {
			continue
		}
		in := maps.Clone(s.After)
		switch md := in["metadata"].(type) {
		case map[string]any:
			in["metadata"] = withoutEngineMeta(md)
		case map[string]string:
			out := make(map[string]any, len(md))
			for k, v := range md {
				out[k] = v
			}
			in["metadata"] = withoutEngineMeta(out)
		}
		return in, s.NodeID, true
	}
	return nil, "", false
}

func withoutEngineMeta(md map[string]any) map[string]any {
	out := make(map[string]any, len(md))
	for k, v := range md {
		if !strings.HasPrefix(k, engineMetaPrefix) {
			out[k] = v
		}
	}
	return out
}
