package builder

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod/internal/storage"
)

// answer is the structured output the model was asked for.
type answer struct {
	Name  string `json:"name"`
	Nodes []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		RefID      string `json:"ref_id"`
		ConfigJSON string `json:"config_json"`
	} `json:"nodes"`
	Edges []struct {
		SourceID     string `json:"source_id"`
		TargetID     string `json:"target_id"`
		SourceHandle string `json:"source_handle"`
	} `json:"edges"`
}

// errNoWorkflow is returned when the model's answer is not a workflow.
var errNoWorkflow = errors.New("the model did not answer with a workflow; try describing the automation differently")

// parseAnswer reads the model's text, tolerating a fenced code block around
// the object (providers without native structured output add one).
func parseAnswer(text string) (answer, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return answer{}, errNoWorkflow
	}
	var a answer
	if err := json.Unmarshal([]byte(text[start:end+1]), &a); err != nil {
		return answer{}, fmt.Errorf("%w: %w", errNoWorkflow, err)
	}
	return a, nil
}

// draftOf turns the answer into a workflow, keeping only what this caller
// may use and reporting everything it dropped or could not read.
func draftOf(a answer, kinds []Kind, req Request) (storage.Workflow, []Issue) {
	var issues []Issue
	types := nodeTypes(kinds)
	transTypes := map[string]bool{}
	for _, k := range kinds {
		if k.TransType != "" {
			transTypes[k.TransType] = true
		}
	}
	allowedRef := map[string]map[string]bool{TypeSource: refSet(req.Sources), TypeSink: refSet(req.Sinks)}

	wf := storage.Workflow{Name: strings.TrimSpace(a.Name), VHost: req.VHost, Active: false}
	if wf.Name == "" {
		wf.Name = "Draft workflow"
	}
	seen := map[string]bool{}
	for i, n := range a.Nodes {
		id := strings.TrimSpace(n.ID)
		if id == "" || seen[id] {
			id = fmt.Sprintf("node-%d", i+1)
		}
		seen[id] = true
		node := storage.WorkflowNode{ID: id, Type: n.Type, Config: map[string]any{}}
		issues = append(issues, readConfig(&node, n.ConfigJSON)...)
		issues = append(issues, kindIssues(node, types, transTypes)...)
		if refs, ok := allowedRef[n.Type]; ok && n.RefID != "" {
			if refs[n.RefID] {
				node.RefID = n.RefID
			} else {
				issues = append(issues, errorIssue(id, fmt.Sprintf("Node '%s' named a %s that is not available to you; it was cleared.", id, n.Type),
					"Choose the "+n.Type+" in the node's settings."))
			}
		}
		wf.Nodes = append(wf.Nodes, node)
	}
	wf.Edges, issues = edgesOf(a, seen, issues)
	layout(&wf)
	return wf, issues
}

// kindIssues reports a node whose type, or whose transformation, Hermod
// does not have.
func kindIssues(node storage.WorkflowNode, types []string, transTypes map[string]bool) []Issue {
	if !slices.Contains(types, node.Type) {
		return []Issue{errorIssue(node.ID, fmt.Sprintf("Node '%s' has type %q, which Hermod does not have.", node.ID, node.Type),
			"Replace it with one of the available node kinds.")}
	}
	if node.Type != TypeTransformation {
		return nil
	}
	if tt, _ := node.Config["transType"].(string); !transTypes[tt] {
		return []Issue{errorIssue(node.ID, fmt.Sprintf("Node '%s' names transformation %q, which Hermod does not have.", node.ID, tt),
			"Pick a transformation from the node palette.")}
	}
	return nil
}

// edgesOf keeps the edges between nodes that are in the draft.
func edgesOf(a answer, nodes map[string]bool, issues []Issue) ([]storage.WorkflowEdge, []Issue) {
	var edges []storage.WorkflowEdge
	for _, e := range a.Edges {
		if !nodes[e.SourceID] || !nodes[e.TargetID] {
			issues = append(issues, Issue{Severity: "warning",
				Message:        fmt.Sprintf("An edge from '%s' to '%s' joined a node that is not in the draft; it was dropped.", e.SourceID, e.TargetID),
				Recommendation: "Connect the nodes in the editor."})
			continue
		}
		edges = append(edges, storage.WorkflowEdge{
			ID: uuid.NewString(), SourceID: e.SourceID, TargetID: e.TargetID, SourceHandle: e.SourceHandle,
		})
	}
	return edges, issues
}

func readConfig(node *storage.WorkflowNode, raw string) []Issue {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &node.Config); err != nil || node.Config == nil {
		node.Config = map[string]any{}
		return []Issue{errorIssue(node.ID, fmt.Sprintf("Node '%s' came with settings that are not a JSON object.", node.ID),
			"Fill in the node's settings in the editor.")}
	}
	return nil
}

func errorIssue(nodeID, msg, rec string) Issue {
	return Issue{Severity: "error", Message: msg, Recommendation: rec, NodeID: nodeID}
}

func refSet(refs []Ref) map[string]bool {
	out := make(map[string]bool, len(refs))
	for _, r := range refs {
		out[r.ID] = true
	}
	return out
}

// Layout spacing on the editor canvas.
const (
	columnWidth = 280
	rowHeight   = 140
)

// layout places each node in a column by its distance from a node with no
// incoming edge, so the draft opens in the editor reading left to right.
func layout(wf *storage.Workflow) {
	incoming := map[string]int{}
	next := map[string][]string{}
	for _, e := range wf.Edges {
		incoming[e.TargetID]++
		next[e.SourceID] = append(next[e.SourceID], e.TargetID)
	}
	depth := map[string]int{}
	var queue []string
	for _, n := range wf.Nodes {
		if incoming[n.ID] == 0 {
			depth[n.ID] = 0
			queue = append(queue, n.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, t := range next[id] {
			if _, done := depth[t]; !done {
				depth[t] = depth[id] + 1
				queue = append(queue, t)
			}
		}
	}
	rows := map[int]int{}
	for i := range wf.Nodes {
		d := depth[wf.Nodes[i].ID] // a node only on a cycle stays in column 0
		wf.Nodes[i].X = float64(d * columnWidth)
		wf.Nodes[i].Y = float64(rows[d] * rowHeight)
		rows[d]++
	}
}
