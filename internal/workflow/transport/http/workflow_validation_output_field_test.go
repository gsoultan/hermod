package http

// A node whose field is a call and which has no target field has nowhere to
// write its result. The engine refuses it on the first message; the config is
// already here when the workflow is saved or started, so it is refused then.

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

func transformationWorkflow(config map[string]any) storage.Workflow {
	return storage.Workflow{
		Name: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "src1", Type: "source", RefID: "s1"},
			{ID: "t1", Type: "transformation", Config: config},
			{ID: "snk1", Type: "sink", RefID: "k1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src1", TargetID: "t1"},
			{ID: "e2", SourceID: "t1", TargetID: "snk1"},
		},
	}
}

func TestValidateWorkflowFlagsACallWithNoTargetField(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}

	cases := []struct {
		name      string
		config    map[string]any
		wantIssue bool
	}{
		{"mapping: a call, no target", map[string]any{"transType": "mapping", "field": "lower(source.status)"}, true},
		{"mapping: a call with a target", map[string]any{"transType": "mapping", "field": "lower(source.status)", "targetField": "label"}, false},
		{"mapping: a plain field", map[string]any{"transType": "mapping", "field": "status"}, false},
		{"fuzzy lookup: a call, no target", map[string]any{"transType": "fuzzy_lookup", "field": "lower(source.city)"}, true},
		{"term extraction: a call, no target", map[string]any{"transType": "term_extraction", "field": "trim(source.note)"}, true},
		{"aggregate: a call, no target", map[string]any{"transType": "aggregate", "field": "toint(source.amount)", "type": "sum"}, true},
		{"aggregate: a call only as the grouping key", map[string]any{"transType": "aggregate", "field": "amount", "groupBy": "lower(source.region)"}, false},
		{"data conversion: one row of two is a call with no target", map[string]any{"transType": "data_conversion", "conversions": []any{
			map[string]any{"field": "amount", "targetType": "int"},
			map[string]any{"field": "trim(source.qty)", "targetType": "int"},
		}}, true},
		{"data conversion: every call has a target", map[string]any{"transType": "data_conversion", "conversions": []any{
			map[string]any{"field": "trim(source.qty)", "targetType": "int", "targetField": "qty"},
		}}, false},
		{"data conversion: the single-field form", map[string]any{"transType": "data_conversion", "field": "trim(source.qty)", "targetType": "int"}, true},
		// A Set Fields value is a call by design, and has its own target: the row.
		{"set fields is not one of these", map[string]any{"transType": "set", "column.a": "lower(source.a)"}, false},
		// Mask reads its field as a path; whatever it holds is not this fault.
		{"mask is not one of these", map[string]any{"transType": "mask", "field": "lower(source.email)"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := h.ValidateWorkflow(context.Background(), transformationWorkflow(tc.config))

			var found *ValidationIssue
			for i := range issues {
				if issues[i].NodeID == "t1" && issues[i].Severity == "error" {
					found = &issues[i]
					break
				}
			}
			if tc.wantIssue && found == nil {
				t.Fatalf("no error for node t1; issues: %+v", issues)
			}
			if !tc.wantIssue && found != nil {
				t.Fatalf("unexpected error for node t1: %s", found.Message)
			}
			if found != nil && !strings.Contains(strings.ToLower(found.Message+found.Recommendation), "target field") {
				t.Errorf("the issue should say to set a target field: %s / %s", found.Message, found.Recommendation)
			}
		})
	}
}
