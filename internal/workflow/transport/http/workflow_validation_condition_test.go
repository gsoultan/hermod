package http

// A condition node whose regex does not compile rejects every message. The
// engine now fails loudly at run time rather than dropping traffic silently,
// but by then the workflow has already been deployed and the failure costs a
// dead-letter per message. The editor can see it before any of that: the
// pattern is right there in the node's config.

import (
	"context"
	"encoding/json"
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

// A condition node that cannot tell one message from another is not a decision,
// and each of these was one nobody made:
//
//   - no conditions at all: EvaluateConditions reads an empty list as true, so
//     every message took the TRUE branch;
//   - a row with no field: it compares "" with the value, so every message got
//     the same answer;
//   - an operator EvaluateConditions does not know (==, equals, or none): no
//     case matches, so every message took the FALSE branch.
//
// None of them logged anything. The editor saves conditions as an array, and
// older or imported workflows hold a JSON string or the single-row fields, so
// each case is checked in all three shapes.
func TestValidateWorkflowFlagsAConditionThatCannotDecide(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}

	row := func(field, op, value string) map[string]any {
		return map[string]any{"field": field, "operator": op, "value": value}
	}
	shapes := map[string]func(rows ...map[string]any) map[string]any{
		"array": func(rows ...map[string]any) map[string]any {
			list := make([]any, len(rows))
			for i, r := range rows {
				list[i] = r
			}
			return map[string]any{"label": "Condition (If)", "type": "condition", "conditions": list}
		},
		"JSON string": func(rows ...map[string]any) map[string]any {
			b, err := json.Marshal(rows)
			if err != nil {
				t.Fatalf("marshal rows: %v", err)
			}
			return map[string]any{"label": "Condition (If)", "type": "condition", "conditions": string(b)}
		},
	}

	cases := []struct {
		name string
		rows []map[string]any
		// want is a fragment the error must contain; "" means no error.
		want string
	}{
		{"one complete row", []map[string]any{row("status", "=", "active")}, ""},
		{"every operator the editor offers", []map[string]any{
			row("a", "=", "1"), row("a", "!=", "1"), row("a", ">", "1"), row("a", ">=", "1"),
			row("a", "<", "1"), row("a", "<=", "1"), row("a", "contains", "1"),
			row("a", "not_contains", "1"), row("a", "regex", "1"), row("a", "not_regex", "1"),
		}, ""},
		{"the aliases the engine accepts", []map[string]any{
			row("a", "eq", "1"), row("a", "neq", "1"), row("a", "gt", "1"),
			row("a", "gte", "1"), row("a", "lt", "1"), row("a", "lte", "1"),
		}, ""},
		{"an empty value is a value", []map[string]any{row("status", "=", "")}, ""},
		{"no conditions", nil, "TRUE"},
		{"a row with no field", []map[string]any{row("", "=", "active")}, "no field"},
		{"a row whose field is blank", []map[string]any{row("   ", "=", "active")}, "no field"},
		{"the second row has no field", []map[string]any{row("status", "=", "active"), row("", "=", "x")}, "condition 2"},
		{"an operator from another language", []map[string]any{row("status", "==", "active")}, `"=="`},
		{"an operator spelled out", []map[string]any{row("status", "equals", "active")}, `"equals"`},
		{"no operator", []map[string]any{row("status", "", "active")}, "no operator"},
	}

	for shapeName, shape := range shapes {
		for _, tc := range cases {
			t.Run(shapeName+"/"+tc.name, func(t *testing.T) {
				assertConditionIssue(t, h, shape(tc.rows...), tc.want)
			})
		}
	}

	// The single-row fields an older workflow holds.
	legacy := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{"complete", map[string]any{"field": "status", "operator": "=", "value": "active"}, ""},
		{"no field, so no condition", map[string]any{"field": "", "operator": "=", "value": "active"}, "TRUE"},
		{"no operator", map[string]any{"field": "status", "value": "active"}, "no operator"},
		{"unknown operator", map[string]any{"field": "status", "operator": "==", "value": "active"}, `"=="`},
	}
	for _, tc := range legacy {
		t.Run("single row/"+tc.name, func(t *testing.T) {
			assertConditionIssue(t, h, tc.config, tc.want)
		})
	}
}

// A switch reads its own field and cases, not a condition list, so none of the
// condition-only checks may reach it: parsed as conditions, a switch's config is
// a row with a field and no operator.
func TestValidateWorkflowLeavesASwitchToItsOwnChecks(t *testing.T) {
	h := &WorkflowHandler{Handler: &handlers.Handler{}}
	wf := conditionWorkflow("=", "x")
	wf.Nodes[1] = storage.WorkflowNode{ID: "cond1", Type: "switch", Config: map[string]any{
		"field": "region",
		"cases": []any{map[string]any{"label": "eu", "value": "EU"}},
	}}
	for _, issue := range h.ValidateWorkflow(context.Background(), wf) {
		if issue.NodeID == "cond1" && issue.Severity == "error" {
			t.Errorf("a valid switch was flagged: %q", issue.Message)
		}
	}
}

func assertConditionIssue(t *testing.T, h *WorkflowHandler, config map[string]any, want string) {
	t.Helper()
	wf := conditionWorkflow("=", "x")
	wf.Nodes[1].Config = config

	var errs []string
	for _, issue := range h.ValidateWorkflow(context.Background(), wf) {
		if issue.NodeID == "cond1" && issue.Severity == "error" {
			errs = append(errs, issue.Message)
		}
	}
	switch {
	case want == "" && len(errs) > 0:
		t.Errorf("config %v was flagged: %q", config, errs)
	case want != "" && len(errs) == 0:
		t.Errorf("config %v validated clean; it gives every message the same answer", config)
	case want != "" && !strings.Contains(strings.Join(errs, "\n"), want):
		t.Errorf("config %v was flagged with %q, want a message containing %q", config, errs, want)
	}
}
