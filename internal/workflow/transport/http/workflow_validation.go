package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
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
