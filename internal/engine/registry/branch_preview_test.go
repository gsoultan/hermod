package registry

import (
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"

	// Node executors register themselves in init(), the same way transformers
	// do. control holds the routing nodes this file is about.
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/control"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/core"

	// Transformers register in init() too; without these "mask" and "validator"
	// look unregistered and the table below would pass for the wrong reason.
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/core"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/security"
)

// TestBranchNodesArePreviewable covers the editor's "Test" button on a switch
// node.
//
// The button posts the node's type to /api/transformations/test, which ran the
// transformer registry and nothing else. switch, condition and router are not
// transformers -- they are node executors, registered separately and dispatched
// at runtime by registry_routing.go -- so the preview answered
//
//	unknown transformation type "switch": no transformer is registered under that name
//
// for a node that worked perfectly well in a running workflow. The message
// blamed the user's configuration for a gap in the preview path.
func TestBranchNodesArePreviewable(t *testing.T) {
	cases := `[{"label":"active-users","operator":"=","value":"active"}]`

	for _, tc := range []struct {
		nodeType string
		config   map[string]any
		want     string
	}{
		{"switch", map[string]any{"field": "status", "cases": cases}, "active-users"},
		{"condition", map[string]any{"field": "status", "operator": "=", "value": "active"}, "true"},
		{"condition", map[string]any{"field": "status", "operator": "=", "value": "archived"}, "false"},
		{"router", map[string]any{
			"rules": `[{"label":"actives","field":"status","operator":"=","value":"active"}]`,
		}, "actives"},
		{"router", map[string]any{
			"rules": `[{"label":"archived","field":"status","operator":"=","value":"archived"}]`,
		}, "default"},
	} {
		t.Run(tc.nodeType+"/"+tc.want, func(t *testing.T) {
			reg := newSimRegistry(t)

			msg := message.AcquireMessage()
			t.Cleanup(msg.Release)
			msg.SetAfter([]byte(`{"status":"active"}`))

			out, branch, err := reg.PreviewBranch(t.Context(), tc.nodeType, tc.config, msg)
			if err != nil {
				t.Fatalf("previewing a %s node failed: %v", tc.nodeType, err)
			}
			for _, m := range out {
				if m != nil && m != msg {
					t.Cleanup(m.Release)
				}
			}
			if branch != tc.want {
				t.Errorf("branch = %q, want %q", branch, tc.want)
			}
		})
	}
}

// TestPreviewRefusesNodesWithSideEffects holds the line the marker interface
// draws. A preview runs against a sample message in the editor; a node that
// writes to a sink, records an approval or mutates workflow state must not run
// there just because someone pressed a button.
func TestPreviewRefusesNodesWithSideEffects(t *testing.T) {
	reg := newSimRegistry(t)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetAfter([]byte(`{"k":"v"}`))

	// approval is a registered node executor that is deliberately not
	// preview-safe: running it would record a real approval request.
	if _, _, err := reg.PreviewBranch(t.Context(), "approval", map[string]any{}, msg); err == nil {
		t.Error("previewing an approval node was allowed; a node with side effects " +
			"must not run just because the editor offered a Test button")
	}
}

// TestTransformerAndNodeNamesAreDistinguished covers the three answers a
// preview can give, because two of them used to be the same sentence.
//
// "validator" is registered both as a transformer and as a node executor, and
// the transformer is the one a preview wants -- asking "is it a node?" first
// would turn away a node that transforms perfectly well.
func TestTransformerAndNodeNamesAreDistinguished(t *testing.T) {
	reg := newSimRegistry(t)

	for _, tc := range []struct {
		name              string
		typ               string
		canTransform      bool
		isNode            bool
		branchPreviewable bool
	}{
		{"a transformer", "mask", true, false, false},
		{"both, transformer wins", "validator", true, true, false},
		{"a routing node", "switch", false, true, true},
		{"a node with side effects", "stateful", false, true, false},
		{"a typo", "no_such_thing", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reg.CanTransform(tc.typ); got != tc.canTransform {
				t.Errorf("CanTransform(%q) = %v, want %v", tc.typ, got, tc.canTransform)
			}
			if got := reg.IsWorkflowNode(tc.typ); got != tc.isNode {
				t.Errorf("IsWorkflowNode(%q) = %v, want %v", tc.typ, got, tc.isNode)
			}
			if got := reg.IsBranchPreviewable(tc.typ); got != tc.branchPreviewable {
				t.Errorf("IsBranchPreviewable(%q) = %v, want %v", tc.typ, got, tc.branchPreviewable)
			}
		})
	}
}
