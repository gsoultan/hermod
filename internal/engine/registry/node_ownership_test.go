package registry

import (
	"testing"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/Hermod/internal/storage"
	"github.com/gsoultan/Hermod/pkg/comm/message"

	// Register every node executor and transformer under test.
	_ "github.com/gsoultan/Hermod/internal/engine/registry/nodes"
	_ "github.com/gsoultan/Hermod/pkg/comm/transformer/advanced"
	_ "github.com/gsoultan/Hermod/pkg/comm/transformer/core"
	_ "github.com/gsoultan/Hermod/pkg/comm/transformer/logic"
)

// ---------------------------------------------------------------------------
// The message ownership contract, asserted rather than assumed.
//
// Registry.RunWorkflowNode promises its caller one owned reference per returned
// message, and compensates for the common case by retaining when an executor
// passes the input straight through:
//
//	for _, m := range msgs { if m == msg { m.Retain() } }
//
// That compensation only covers returning *the input*. An executor that returns
// a message obtained anywhere else — a clone, a merge, something pulled out of
// node state — is responsible for handing over a reference of its own. Get it
// wrong in either direction and nothing fails loudly: too few references and the
// message goes back to the pool while still in use (duplicated and lost
// payloads); too many and it never returns at all (an unbounded leak).
//
// traversal.runNode's source branch got this wrong and silently corrupted data.
// These tests make the same mistake impossible to land unnoticed in any of the
// node executors.
// ---------------------------------------------------------------------------

// refCount reads a message's reference count. RefCount is deliberately not part
// of the hermod.Message interface — it is a diagnostic, not something every
// implementation must carry — so the pooled implementation is asserted here.
func refCount(t *testing.T, m hermod.Message) int32 {
	t.Helper()
	rc, ok := m.(interface{ RefCount() int32 })
	if !ok {
		t.Fatalf("message type %T does not expose RefCount; the ownership contract cannot be checked", m)
	}
	return rc.RefCount()
}

// ownershipCase describes one executor invocation to check.
type ownershipCase struct {
	name     string
	nodeType string
	config   map[string]any
	// data seeds the input message.
	data map[string]any
	// wantAtLeast is the minimum number of messages the executor should return.
	// Executors that legitimately return none (a filter that drops, a node that
	// suspends) use 0.
	wantAtLeast int
	// retainsInput marks executors that park the input in their own state — a
	// join waiting for its other branches, a collect accumulating a group. They
	// must take a reference of their own to do that, so the input's count is
	// expected to be one higher when Execute returns. Anything that does *not*
	// store the input must leave the count exactly as it found it.
	retainsInput bool
}

func ownershipCases() []ownershipCase {
	return []ownershipCase{
		{
			name:        "transformation/set",
			nodeType:    "transformation",
			config:      map[string]any{"transType": "set", "column.added": "'yes'"},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:     "transformation/parallel-pipeline",
			nodeType: "transformation",
			config: map[string]any{
				"transType": "pipeline",
				"steps":     `[{"transType":"set","column.a":"'1'"},{"transType":"set","column.b":"'2'"}]`,
			},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:        "condition",
			nodeType:    "condition",
			config:      map[string]any{"conditions": []map[string]any{{"field": "k", "operator": "equals", "value": "v"}}},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:        "switch",
			nodeType:    "switch",
			config:      map[string]any{"cases": []any{}},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:        "router",
			nodeType:    "router",
			config:      map[string]any{"routes": []any{}},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:        "foreach",
			nodeType:    "foreach",
			config:      map[string]any{"arrayPath": "items"},
			data:        map[string]any{"items": []any{"a", "b", "c"}},
			wantAtLeast: 3,
		},
		{
			name:        "stateful",
			nodeType:    "stateful",
			config:      map[string]any{"stateKey": "counter"},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:        "circuit_breaker",
			nodeType:    "circuit_breaker",
			config:      map[string]any{},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:        "validator",
			nodeType:    "validator",
			config:      map[string]any{"transType": "validator"},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 0,
		},
		{
			// join parks the message until its other branches arrive, so the
			// waiting branch legitimately holds a reference of its own.
			name:     "join/waiting",
			nodeType: "join",
			config: map[string]any{
				"key_path":         "order_id",
				"expected_sources": float64(2),
			},
			data:         map[string]any{"order_id": "o-1", "k": "v"},
			wantAtLeast:  0,
			retainsInput: true,
		},
		{
			name:        "collect/passthrough",
			nodeType:    "collect",
			config:      map[string]any{},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:        "deduplicate",
			nodeType:    "deduplicate",
			config:      map[string]any{"keyPath": "k"},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 0,
		},
		{
			name:        "log",
			nodeType:    "log",
			config:      map[string]any{"level": "info", "message": "e2e"},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:        "approval",
			nodeType:    "approval",
			config:      map[string]any{},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 0,
		},
		{
			name:        "sink",
			nodeType:    "sink",
			config:      map[string]any{},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 1,
		},
		{
			name:        "wait/zero-duration",
			nodeType:    "wait",
			config:      map[string]any{"duration": "1ms"},
			data:        map[string]any{"k": "v"},
			wantAtLeast: 0,
		},
	}
}

// TestNodeExecutorsHonourOwnershipContract runs each executor and checks the
// reference accounting on both sides: the caller must end up owning exactly one
// reference per returned message, and the input's own reference must be
// untouched by anything except that compensation.
func TestNodeExecutorsHonourOwnershipContract(t *testing.T) {
	store := newPipeStorage()
	reg := NewRegistry(store)
	t.Cleanup(reg.Close)

	for _, tc := range ownershipCases() {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := interfaces.GetNodeExecutor(tc.nodeType); !ok && tc.nodeType != "transformation" {
				t.Skipf("no executor registered for %q", tc.nodeType)
			}

			before := message.OverReleaseCount()

			msg := message.AcquireMessage()
			for k, v := range tc.data {
				msg.SetData(k, v)
			}
			// The traversal holds one reference while the node runs; model that
			// exactly so the counts below mean what they mean in production.
			startRefs := refCount(t, msg)

			node := &storage.WorkflowNode{ID: "n-" + tc.name, Type: tc.nodeType, Config: tc.config}
			msgs, _, err := reg.RunWorkflowNode("wf-ownership", node, msg)
			if err != nil {
				// An executor may legitimately reject this configuration; it must
				// still not have leaked or over-released anything.
				t.Logf("executor returned error (allowed): %v", err)
			}

			if len(msgs) < tc.wantAtLeast {
				t.Errorf("returned %d messages, want at least %d", len(msgs), tc.wantAtLeast)
			}

			// Contract: the caller owns one reference per returned message.
			// Releasing each exactly once must not over-release.
			for i, m := range msgs {
				if got := refCount(t, m); got < 1 {
					t.Errorf("returned message %d has refcount %d; the caller owns no reference and "+
						"releasing it will hand a live message back to the pool", i, got)
				}
				m.Release()
			}

			// The input's reference is the traversal's to release. An executor
			// that parks the message in its own state takes one more.
			wantRefs := startRefs
			if tc.retainsInput {
				wantRefs = startRefs + 1
			}
			if got := refCount(t, msg); got != wantRefs {
				t.Errorf("input refcount is %d after the node ran, want %d; the executor "+
					"consumed or added a reference it does not own", got, wantRefs)
			}
			msg.Release()

			if n := message.OverReleaseCount() - before; n != 0 {
				t.Errorf("%d over-release(s) while running this executor", n)
			}
		})
	}
}

// TestRunWorkflowNodePassthroughRetains locks in the compensation that the whole
// contract rests on. If RunWorkflowNode ever stops retaining a passed-through
// input, every executor that returns its input starts under-referencing at once.
func TestRunWorkflowNodePassthroughRetains(t *testing.T) {
	store := newPipeStorage()
	reg := NewRegistry(store)
	t.Cleanup(reg.Close)

	msg := message.AcquireMessage()
	msg.SetData("k", "v")
	start := refCount(t, msg)

	// A node type with no registered executor takes RunWorkflowNode's default
	// path, which returns the input unchanged.
	node := &storage.WorkflowNode{ID: "n-passthrough", Type: "no-such-executor-type"}
	msgs, _, err := reg.RunWorkflowNode("wf-passthrough", node, msg)
	if err != nil {
		t.Fatalf("passthrough returned an error: %v", err)
	}
	if len(msgs) != 1 || msgs[0] != msg {
		t.Fatalf("passthrough should return the input unchanged, got %v", msgs)
	}
	if got := refCount(t, msg); got != start+1 {
		t.Fatalf("passthrough refcount is %d, want %d: the caller was handed a message it "+
			"does not own a reference to", got, start+1)
	}

	msgs[0].Release()
	if got := refCount(t, msg); got != start {
		t.Fatalf("after the caller released, refcount is %d, want %d", got, start)
	}
	msg.Release()
}

// TestNilMessageIntoExecutorsIsSafe is the abuse case: a nil message must never
// reach an executor as a nil dereference. RunWorkflowNode guards it; this makes
// sure the guard stays.
func TestNilMessageIntoExecutorsIsSafe(t *testing.T) {
	store := newPipeStorage()
	reg := NewRegistry(store)
	t.Cleanup(reg.Close)

	for _, tc := range ownershipCases() {
		node := &storage.WorkflowNode{ID: "n-nil", Type: tc.nodeType, Config: tc.config}
		msgs, _, err := reg.RunWorkflowNode("wf-nil", node, nil)
		if err != nil {
			t.Errorf("%s: nil message produced an error rather than being ignored: %v", tc.name, err)
		}
		if len(msgs) != 0 {
			t.Errorf("%s: nil message produced %d messages", tc.name, len(msgs))
		}
	}
}
