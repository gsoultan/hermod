package http

// A condition node whose regex does not compile rejects every message. The
// engine now fails loudly at run time rather than dropping traffic silently,
// but by then the workflow has already been deployed and the failure costs a
// dead-letter per message. The editor can see it before any of that: the
// pattern is right there in the node's config.

import (
	"context"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

func conditionWorkflow(operator, value string) storage.Workflow {
	return storage.Workflow{
		Name: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "src1", Type: "source", RefID: "s1"},
			{ID: "cond1", Type: "condition", Config: map[string]any{
				"field":    "status",
				"operator": operator,
				"value":    value,
			}},
			{ID: "snk1", Type: "sink", RefID: "k1"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src1", TargetID: "cond1"},
			{ID: "e2", SourceID: "cond1", TargetID: "snk1", SourceHandle: "true"},
		},
	}
}

func TestValidateWorkflowFlagsAnUncompilableConditionPattern(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}

	cases := []struct {
		name      string
		operator  string
		value     string
		wantIssue bool
	}{
		{"valid regex", "regex", `^(active|pending)$`, false},
		{"valid not_regex", "not_regex", `^archived$`, false},
		{"invalid regex", "regex", `[unclosed`, true},
		{"invalid not_regex", "not_regex", `a(b`, true},
		{"unbalanced paren", "regex", `*nope`, true},
		// Resolved per message against that message's data — not judgeable here.
		{"templated pattern", "regex", `{{.pattern}}`, false},
		// A non-regex operator never compiles its value.
		{"equality with regex-looking value", "eq", `[unclosed`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := h.ValidateWorkflow(context.Background(), conditionWorkflow(tc.operator, tc.value))

			var found *string
			for i := range issues {
				if issues[i].NodeID == "cond1" && issues[i].Severity == "error" {
					found = &issues[i].Message
					break
				}
			}

			if tc.wantIssue && found == nil {
				t.Fatalf("pattern %q produced no error issue; a workflow that drops 100%% of its traffic validated clean. Issues: %+v", tc.value, issues)
			}
			if !tc.wantIssue && found != nil {
				t.Fatalf("pattern %q was flagged as an error: %q", tc.value, *found)
			}
			if tc.wantIssue && !strings.Contains(*found, tc.value) {
				t.Errorf("issue %q does not quote the offending pattern %q", *found, tc.value)
			}
		})
	}
}
