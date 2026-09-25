package control

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	msgpkg "github.com/gsoultan/hermod/pkg/comm/message"
)

// The branch a rule or case routes to has to be the label on the edge the
// editor drew from its handle. The contract lives in testdata/branch_names.json,
// which the editor's parity test reads too, so neither side can move alone.
func TestBranchNamesMatchTheEditorsHandles(t *testing.T) {
	raw, err := os.ReadFile("testdata/branch_names.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		Cases []struct {
			Node  string `json:"node"`
			Label string `json:"label"`
			Index int    `json:"index"`
			Want  string `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	for _, c := range fixture.Cases {
		if got := routeBranch(c.Node, c.Label, c.Index); got != c.Want {
			t.Errorf("routeBranch(%q, label %q, index %d) = %q, want %q", c.Node, c.Label, c.Index, got, c.Want)
		}
	}
}

// A message matching a rule with no label went down every route out of the
// router: the executor returned the empty label, and an empty branch takes
// every edge. It has to name the rule's own handle instead.
func TestRouterNamesAnUnnamedRuleByItsHandle(t *testing.T) {
	node := &storage.WorkflowNode{Config: map[string]any{"rules": []any{
		map[string]any{"label": "eu", "field": "region", "operator": "=", "value": "EU"},
		// Second in the list, so an index that is off by one shows.
		map[string]any{"label": "", "field": "region", "operator": "=", "value": "US"},
	}}}
	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetData("region", "US")

	_, branch, err := (&RouterNode{}).Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if branch != "rule_1" {
		t.Errorf("branch = %q, want %q: the id the editor gives the unnamed rule's handle", branch, "rule_1")
	}
}

func TestSwitchNamesAnUnnamedCaseByItsHandle(t *testing.T) {
	node := &storage.WorkflowNode{Config: map[string]any{
		"field": "region",
		"cases": []any{
			map[string]any{"label": "eu", "value": "EU"},
			map[string]any{"label": "", "value": "US"},
		},
	}}
	m := msgpkg.AcquireMessage()
	defer msgpkg.ReleaseMessage(m)
	m.SetData("region", "US")

	_, branch, err := (&SwitchNode{}).Execute(context.Background(), &switchStubCtx{}, "wf1", node, m)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if branch != "case_1" {
		t.Errorf("branch = %q, want %q: the id the editor gives the unnamed case's handle", branch, "case_1")
	}
}
