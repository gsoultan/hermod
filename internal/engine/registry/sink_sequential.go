package registry

import (
	"strconv"

	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/storage"
)

// resolveSinkNodeSequential makes the sequential-sink flag reachable.
//
// SinkExecutor reads node.Config["sequential"] to decide between two very
// different execution models: when false the sink node is a pass-through and
// the write happens asynchronously through the sink writers; when true the sink
// writes inline and exposes success/error branches
// (internal/engine/registry/nodes/core/sink.go).
//
// The UI, however, only ever wrote the flag onto the shared sink entity, and
// nothing carried it to the node — so the documented "Sequential Control Flow"
// feature could not be switched on from the UI.
//
// Precedence is node-first by design. Execution semantics belong to the
// workflow node: a sink entity is a reusable connection that many workflows may
// share, and letting it dictate execution order would mean editing one
// connection silently changed the behaviour of every workflow using it. The
// entity value is honoured only as a fallback so existing setups keep working
// until the control moves onto the node in the editor.
func resolveSinkNodeSequential(node *storage.WorkflowNode, entityConfig hermod.StringMap) {
	if node == nil || node.Type != "sink" {
		return
	}
	if _, ok := node.Config["sequential"].(bool); ok {
		// The node already has an explicit decision; leave it alone.
		return
	}
	raw, ok := entityConfig["sequential"]
	if !ok || raw == "" {
		return
	}
	seq, err := strconv.ParseBool(raw)
	if err != nil {
		return
	}
	if node.Config == nil {
		node.Config = map[string]any{}
	}
	node.Config["sequential"] = seq
}
