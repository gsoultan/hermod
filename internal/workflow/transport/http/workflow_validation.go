package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/noderetry"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

type ValidationIssue struct {
	Severity       string `json:"severity"` // "error", "warning"
	Message        string `json:"message"`
	Recommendation string `json:"recommendation"`
	NodeID         string `json:"node_id,omitempty"`
}

func (h *WorkflowHandler) HandleValidateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := h.Storage.GetWorkflow(r.Context(), id)
	if err != nil {
		h.JsonError(w, "Workflow not found", http.StatusNotFound)
		return
	}

	issues := h.ValidateWorkflow(r.Context(), wf)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(issues)
}

// effectiveNodeType is the node's own type, falling back to the legacy
// transType config for nodes saved before Type was written.
func effectiveNodeType(n storage.WorkflowNode) string {
	if n.Type != "" {
		return n.Type
	}
	if tt, ok := n.Config["transType"].(string); ok {
		return tt
	}
	return ""
}

// nodeConfigIssues checks each node's own configuration and reports whether the
// workflow has an entry point and somewhere to put the result.
func nodeConfigIssues(wf storage.Workflow) (issues []ValidationIssue, hasSource, hasSink bool) {
	for _, n := range wf.Nodes {
		nodeType := effectiveNodeType(n)

		switch nodeType {
		case "source":
			hasSource = true
			if n.RefID == "" {
				issues = append(issues, ValidationIssue{
					Severity:       "error",
					Message:        fmt.Sprintf("Source node '%s' is not configured.", n.ID),
					Recommendation: "Select a data source (e.g., Postgres CDC, MQTT) from the node configuration panel by clicking on the node.",
					NodeID:         n.ID,
				})
			}
		case "sink":
			hasSink = true
			if n.RefID == "" {
				issues = append(issues, ValidationIssue{
					Severity:       "error",
					Message:        fmt.Sprintf("Sink node '%s' is not configured.", n.ID),
					Recommendation: "Select a data destination (e.g., Elasticsearch, Webhook) from the node configuration panel by clicking on the node.",
					NodeID:         n.ID,
				})
			}
		case "foreach", "fanout":
			ap, _ := n.Config["arrayPath"].(string)
			if strings.TrimSpace(ap) == "" {
				issues = append(issues, ValidationIssue{
					Severity:       "error",
					Message:        fmt.Sprintf("Node '%s' (%s) is missing the 'arrayPath' configuration.", n.ID, nodeType),
					Recommendation: "Specify the JSON path to the array you want to iterate over (e.g., '$.items'). This tells Hermod which part of the message to split.",
					NodeID:         n.ID,
				})
			}
		case "condition", "switch":
			// A regex that does not compile does not match nothing — it
			// rejects everything. The engine now fails loudly on it rather
			// than dropping traffic in silence, but by then the workflow is
			// deployed and every message costs a dead-letter. The pattern is
			// in the node's config, so it can be caught here instead.
			if err := evaluator.ValidateConditions(evaluator.ParseConditions(n.Config)); err != nil {
				issues = append(issues, ValidationIssue{
					Severity:       "error",
					Message:        fmt.Sprintf("Node '%s' has a condition that cannot compile and would reject every message: %v", n.ID, err),
					Recommendation: "Fix the regular expression in this node's condition. Until it compiles, the node takes its 'false' branch for every message, so the workflow silently delivers nothing.",
					NodeID:         n.ID,
				})
			}
			if nodeType == "condition" {
				issues = append(issues, undecidableConditionIssues(n)...)
			}
		case "transformation", "mapping", "data_conversion", "fuzzy_lookup", "term_extraction", "aggregate":
			issues = append(issues, unwritableExpressionIssues(n, nodeType)...)
		case "filter":
			condition, _ := n.Config["condition"].(string)
			if strings.TrimSpace(condition) == "" {
				issues = append(issues, ValidationIssue{
					Severity:       "warning",
					Message:        fmt.Sprintf("Filter node '%s' has an empty condition.", n.ID),
					Recommendation: "An empty condition might pass all messages or none. Define a rule like 'data.price > 100' to filter your data.",
					NodeID:         n.ID,
				})
			}
		}
	}
	return issues, hasSource, hasSink
}

// unwritableExpressionIssues reports a node that reads an expression and has
// no field to write the result to.
//
// Five transformations take "a field or an expression" and default their
// output to a name built from that field. For a call there is no such name
// (see evaluator.OutputField), and the engine refuses the first message. The
// config is all it takes to know, so the workflow is refused here instead of
// after it has been started.
func unwritableExpressionIssues(n storage.WorkflowNode, nodeType string) []ValidationIssue {
	transType := nodeType
	if nodeType == "transformation" {
		transType, _ = n.Config["transType"].(string)
	}

	// Each row is one field the node reads and the target it was given.
	type row struct{ field, target string }
	var rows []row
	read := func(m map[string]any) row {
		field, _ := m["field"].(string)
		target, _ := m["targetField"].(string)
		return row{field, target}
	}
	switch transType {
	case "mapping", "fuzzy_lookup", "term_extraction", "aggregate":
		rows = append(rows, read(n.Config))
	case "data_conversion":
		// The row list is authoritative when present, as it is in the node.
		if list, ok := n.Config["conversions"].([]any); ok {
			for _, entry := range list {
				if m, ok := entry.(map[string]any); ok {
					rows = append(rows, read(m))
				}
			}
		} else {
			rows = append(rows, read(n.Config))
		}
	}

	var issues []ValidationIssue
	for _, r := range rows {
		if _, err := evaluator.OutputField(r.field, r.target, ""); err != nil {
			issues = append(issues, ValidationIssue{
				Severity:       "error",
				Message:        fmt.Sprintf("Node '%s' reads %s but has no target field to write the result to.", n.ID, r.field),
				Recommendation: "Set the node's Target Field. A plain field is written back to itself, but an expression has no field of its own.",
				NodeID:         n.ID,
			})
		}
	}
	return issues
}

// undecidableConditionIssues reports a condition node that gives every message
// the same answer without anyone having chosen it.
//
// EvaluateConditions reads an empty list as true, compares a row with no field
// as "", and matches nothing for an operator it does not know. So an If node
// with no conditions sent every message down TRUE, and one with an unknown
// operator sent every message down FALSE, with nothing logged either way.
//
// They are errors here and not in the engine on purpose. The registry's own
// validation, which a workflow passes when Hermod restarts it, does not run
// these, so a workflow that already runs keeps running; it cannot be saved or
// started again as it is. A switch reads its own field and cases, not this
// list, so it is not checked here.
func undecidableConditionIssues(n storage.WorkflowNode) (issues []ValidationIssue) {
	conditions := evaluator.ParseConditions(n.Config)
	if len(conditions) == 0 {
		return []ValidationIssue{{
			Severity:       "error",
			Message:        fmt.Sprintf("Condition node '%s' has no conditions, so every message takes its TRUE branch.", n.ID),
			Recommendation: "Add a condition to this node. If every message should go the same way, remove the node and connect its input to that branch instead.",
			NodeID:         n.ID,
		}}
	}
	for i, cond := range conditions {
		field, _ := cond["field"].(string)
		op, _ := cond["operator"].(string)
		var problem string
		switch {
		case strings.TrimSpace(field) == "":
			problem = "has no field, so it compares an empty value and gives every message the same answer"
		case op == "":
			problem = "has no operator, so it matches no message and every message takes the FALSE branch"
		case !evaluator.IsConditionOperator(op):
			problem = fmt.Sprintf("uses the operator %q, which Hermod does not recognise, so it matches no message and every message takes the FALSE branch", op)
		default:
			continue
		}
		issues = append(issues, ValidationIssue{
			Severity:       "error",
			Message:        fmt.Sprintf("Condition node '%s': condition %d %s.", n.ID, i+1, problem),
			Recommendation: "Choose the field this condition tests and one of =, !=, >, >=, <, <=, contains, not contains, regex or not regex — or remove the condition.",
			NodeID:         n.ID,
		})
	}
	return issues
}

// edgeIssues reports connections whose endpoints are not in the workflow.
func edgeIssues(wf storage.Workflow) (issues []ValidationIssue) {
	nodeMap := make(map[string]struct{}, len(wf.Nodes))
	for _, n := range wf.Nodes {
		nodeMap[n.ID] = struct{}{}
	}

	for _, e := range wf.Edges {
		if _, ok := nodeMap[e.SourceID]; !ok {
			issues = append(issues, ValidationIssue{
				Severity:       "error",
				Message:        fmt.Sprintf("Connection '%s' refers to a missing source node.", e.ID),
				Recommendation: "This connection appears to be broken. Try deleting and reconnecting the nodes in the editor.",
			})
		}
		if _, ok := nodeMap[e.TargetID]; !ok {
			issues = append(issues, ValidationIssue{
				Severity:       "error",
				Message:        fmt.Sprintf("Connection '%s' refers to a missing target node.", e.ID),
				Recommendation: "This connection appears to be broken. Try deleting and reconnecting the nodes in the editor.",
			})
		}
	}
	return issues
}

// orphanIssues reports nodes that nothing feeds or that feed nothing.
func orphanIssues(wf storage.Workflow) (issues []ValidationIssue) {
	incoming := make(map[string]int)
	outgoing := make(map[string]int)
	for _, e := range wf.Edges {
		outgoing[e.SourceID]++
		incoming[e.TargetID]++
	}

	for _, n := range wf.Nodes {
		nodeType := effectiveNodeType(n)

		if nodeType != "source" && incoming[n.ID] == 0 {
			issues = append(issues, ValidationIssue{
				Severity:       "warning",
				Message:        fmt.Sprintf("Node '%s' is not connected to any input.", n.ID),
				Recommendation: "This node is isolated and won't receive any data. Connect it to a source or another node's output.",
				NodeID:         n.ID,
			})
		}
		if nodeType != "sink" && outgoing[n.ID] == 0 {
			issues = append(issues, ValidationIssue{
				Severity:       "warning",
				Message:        fmt.Sprintf("Node '%s' has no outgoing connections.", n.ID),
				Recommendation: "Data reaching this node will not go further. If you want to save or process this data, connect it to a sink or the next node.",
				NodeID:         n.ID,
			})
		}
	}
	return issues
}

// ValidateWorkflow performs deep server-side validation for workflow configuration.
// It returns a list of issues with human-friendly recommendations.
func (h *WorkflowHandler) ValidateWorkflow(ctx context.Context, wf storage.Workflow) []ValidationIssue {
	var issues []ValidationIssue

	if wf.Name == "" {
		issues = append(issues, ValidationIssue{
			Severity:       "error",
			Message:        "Workflow name is missing.",
			Recommendation: "Please provide a unique and descriptive name for your workflow in the settings.",
		})
	}

	if len(wf.Nodes) == 0 {
		issues = append(issues, ValidationIssue{
			Severity:       "error",
			Message:        "The workflow has no nodes.",
			Recommendation: "A workflow must contain at least one source and one sink to be functional. Open the editor and add nodes from the sidebar.",
		})
		return issues // Can't validate much else without nodes
	}

	nodeIssues, hasSource, hasSink := nodeConfigIssues(wf)
	issues = append(issues, nodeIssues...)

	if !hasSource {
		issues = append(issues, ValidationIssue{
			Severity:       "error",
			Message:        "Workflow is missing a source node.",
			Recommendation: "Every workflow needs an entry point to receive data. Add a 'source' node and connect it to the next step.",
		})
	}
	if !hasSink {
		issues = append(issues, ValidationIssue{
			Severity:       "warning",
			Message:        "Workflow has no sink nodes.",
			Recommendation: "Without a sink, data processed by this workflow will not be persisted. Add a 'sink' node to save your results to a database or external system.",
		})
	}

	issues = append(issues, h.cdcQueryTargetIssues(ctx, wf)...)
	issues = append(issues, edgeIssues(wf)...)
	issues = append(issues, orphanIssues(wf)...)
	issues = append(issues, aiNodeIssues(wf)...)
	issues = append(issues, retryIssues(wf)...)

	return issues
}

// retryIssues checks each node's retry policy (see noderetry). One the engine
// cannot read is an error: the node would run once and the operator would
// believe it retries. One that can never fire is a warning.
func retryIssues(wf storage.Workflow) (issues []ValidationIssue) {
	for _, n := range wf.Nodes {
		if !noderetry.Configured(n.Config) {
			continue
		}
		if _, err := noderetry.Parse(n.Config); err != nil {
			issues = append(issues, ValidationIssue{
				Severity:       "error",
				Message:        fmt.Sprintf("Node '%s' has a retry policy that cannot be read: %v", n.ID, err),
				Recommendation: `Set retry to {"maxAttempts": 3, "backoff": "1s", "maxBackoff": "30s"}; "on" is an optional regular expression the error must match.`,
				NodeID:         n.ID,
			})
			continue
		}
		switch onError, _ := n.Config["onError"].(string); onError {
		case "continue", "drop":
			issues = append(issues, ValidationIssue{
				Severity:       "warning",
				Message:        fmt.Sprintf("Node '%s' retries on failure but its On Error setting is %q, which handles every failure first, so it never retries.", n.ID, onError),
				Recommendation: "Set On Error to fail for the retry policy to apply, or remove the retry policy.",
				NodeID:         n.ID,
			})
		}
	}
	return issues
}

// nodeTransformationType reads the transformation a node runs. The editor
// leaves Type as "transformation" and writes the real type to
// Config["transType"]; older nodes put it in Type directly.
func nodeTransformationType(n storage.WorkflowNode) string {
	if tt, ok := n.Config["transType"].(string); ok && tt != "" {
		return tt
	}
	return n.Type
}

// sourceReader reads a workflow's sources once each. A workflow can name the
// same source from several nodes, and validation runs on every save.
type sourceReader struct {
	ctx   context.Context
	store interface {
		GetSource(ctx context.Context, id string) (storage.Source, error)
	}
	seen map[string]*storage.Source
}

// get reports the source, or false when there is no id or it cannot be read. A
// failed read is not evidence of anything, and a missing source already has its
// own issue.
func (r *sourceReader) get(id string) (storage.Source, bool) {
	if id == "" {
		return storage.Source{}, false
	}
	if src, ok := r.seen[id]; ok {
		if src == nil {
			return storage.Source{}, false
		}
		return *src, true
	}
	src, err := r.store.GetSource(r.ctx, id)
	if err != nil {
		r.seen[id] = nil
		return storage.Source{}, false
	}
	r.seen[id] = &src
	return src, true
}

func cdcQueryTargetIssue(nodeID, what, name string) ValidationIssue {
	return ValidationIssue{
		Severity:       "warning",
		Message:        fmt.Sprintf("%s on node '%s' runs queries against '%s', which has CDC enabled.", what, nodeID, name),
		Recommendation: fmt.Sprintf("A query target has to be a non-CDC source, so this will fail when the workflow runs. Turn CDC off on '%s', or register a second, non-CDC source for the same database and point this node at that one.", name),
		NodeID:         nodeID,
	}
}

// lookupTargetIssues reports a db_lookup node whose source is a CDC one.
// db_lookup names its source in the node config; execute_sql uses the same keys
// but writes rather than reads, which is a different question with a different
// answer (see SQLConfig.tsx).
func lookupTargetIssues(n storage.WorkflowNode, sources *sourceReader) (issues []ValidationIssue) {
	if nodeTransformationType(n) != "db_lookup" {
		return nil
	}
	for _, key := range storage.NodeConfigSourceKeys {
		id, _ := n.Config[key].(string)
		src, ok := sources.get(id)
		if !ok || hermod.SourceAllowsDirectQueries(src.Type, src.Config) {
			continue
		}
		issues = append(issues, cdcQueryTargetIssue(n.ID, "The lookup", src.Name))
	}
	return issues
}

// batchDelegateIssues reports a source node holding a batch_sql source whose
// delegate is a CDC one. batch_sql has no connection of its own, so the database
// it queries is one hop further on, in its source_id.
func batchDelegateIssues(n storage.WorkflowNode, sources *sourceReader) []ValidationIssue {
	if n.Type != "source" {
		return nil
	}
	src, ok := sources.get(n.RefID)
	if !ok || src.Type != "batch_sql" {
		return nil
	}
	delegate, ok := sources.get(src.Config["source_id"])
	if !ok || hermod.SourceAllowsDirectQueries(delegate.Type, delegate.Config) {
		return nil
	}
	return []ValidationIssue{cdcQueryTargetIssue(n.ID, "The batch SQL source", delegate.Name)}
}

// cdcQueryTargetIssues reports nodes that aim SQL at a source configured for
// change data capture. The engine already refuses both -- a db_lookup on a CDC
// source (requireNonCDCSource) and a batch_sql source on a CDC delegate
// (Registry.requireNonCDCDelegate) -- but only when it builds them, which is
// after the workflow has been saved and started. A workflow created through the
// API or restored from a bundle never passes through the editor's pickers, so
// this is the only place it can be told before it runs.
//
// These are warnings. validateWorkflow turns an error into a 400 on save, and a
// workflow whose lookup source somebody has since switched to CDC has to stay
// editable so it can be fixed.
func (h *WorkflowHandler) cdcQueryTargetIssues(ctx context.Context, wf storage.Workflow) []ValidationIssue {
	if h.Storage == nil {
		return nil
	}

	sources := &sourceReader{ctx: ctx, store: h.Storage, seen: map[string]*storage.Source{}}

	var issues []ValidationIssue
	for _, n := range wf.Nodes {
		issues = append(issues, lookupTargetIssues(n, sources)...)
		issues = append(issues, batchDelegateIssues(n, sources)...)
	}
	return issues
}
