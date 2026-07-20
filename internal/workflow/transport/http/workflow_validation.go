package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/user/hermod/internal/storage"
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

	issues := h.ValidateWorkflow(wf)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(issues)
}

// ValidateWorkflow performs deep server-side validation for workflow configuration.
// It returns a list of issues with human-friendly recommendations.
func (h *WorkflowHandler) ValidateWorkflow(wf storage.Workflow) []ValidationIssue {
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

	nodeMap := make(map[string]storage.WorkflowNode)
	for _, n := range wf.Nodes {
		nodeMap[n.ID] = n
	}

	hasSource := false
	hasSink := false

	// Check nodes
	for _, n := range wf.Nodes {
		// Support both explicit node type and legacy transType config
		nodeType := n.Type
		if nodeType == "" {
			if tt, ok := n.Config["transType"].(string); ok {
				nodeType = tt
			}
		}

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

	// Check edges
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

	// Check for orphaned nodes
	incomingCount := make(map[string]int)
	outgoingCount := make(map[string]int)
	for _, e := range wf.Edges {
		outgoingCount[e.SourceID]++
		incomingCount[e.TargetID]++
	}

	for _, n := range wf.Nodes {
		nodeType := n.Type
		if nodeType == "" {
			if tt, ok := n.Config["transType"].(string); ok {
				nodeType = tt
			}
		}

		if nodeType != "source" && incomingCount[n.ID] == 0 {
			issues = append(issues, ValidationIssue{
				Severity:       "warning",
				Message:        fmt.Sprintf("Node '%s' is not connected to any input.", n.ID),
				Recommendation: "This node is isolated and won't receive any data. Connect it to a source or another node's output.",
				NodeID:         n.ID,
			})
		}
		if nodeType != "sink" && outgoingCount[n.ID] == 0 {
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
