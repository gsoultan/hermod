package registry

import (
	"context"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/engine/registry/nodes/reliability"

	"github.com/gsoultan/Hermod/internal/engine/registry/interfaces"
)

func (r *Registry) BroadcastLiveMessage(workflowID, nodeID string, msg hermod.Message, isError bool, errMsg string) {
	r.broadcastLiveMessageFromHermod(workflowID, nodeID, msg, isError, errMsg)
}

func (r *Registry) ApplyTransformation(ctx context.Context, msg hermod.Message, transType string, config map[string]any) (hermod.Message, error) {
	return r.applyTransformation(ctx, msg, transType, config)
}

// ContextWithPipelineSnapshot returns a new context with a shared snapshot pointer
// for optimizing transformation pipelines by reducing redundant ToMap() calls.
func (r *Registry) ContextWithPipelineSnapshot(ctx context.Context) context.Context {
	var lastSnapshot map[string]any
	return context.WithValue(ctx, hermod.LastTraceSnapshotKey, &lastSnapshot)
}

func (r *Registry) EvaluateConditions(msg hermod.Message, conditions []map[string]any) bool {
	return r.evaluateConditions(msg, conditions)
}

func (r *Registry) Storage() interfaces.RegistryStorage {
	return r.storage
}

func (r *Registry) StateStore() hermod.StateStore {
	return r.stateStore
}

func (r *Registry) GetNodeState(key string) (any, bool) {
	r.nodeStatesMu.Lock()
	defer r.nodeStatesMu.Unlock()
	val, ok := r.nodeStates[key]
	return val, ok
}

// maxNodeStates bounds the in-memory node state map so it cannot grow without
// limit. Node state is best-effort scratch storage; when the cap is reached the
// oldest-style growth is curbed by dropping an arbitrary existing entry.
const maxNodeStates = 10000

func (r *Registry) SetNodeState(key string, val any) {
	r.nodeStatesMu.Lock()
	defer r.nodeStatesMu.Unlock()
	if _, exists := r.nodeStates[key]; !exists && len(r.nodeStates) >= maxNodeStates {
		for k := range r.nodeStates {
			delete(r.nodeStates, k)
			break
		}
	}
	r.nodeStates[key] = val
}

func (r *Registry) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	if r.storage == nil {
		return nil
	}
	return r.storage.UpdateNodeState(ctx, workflowID, nodeID, state)
}

func (r *Registry) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	if r.storage == nil {
		return make(map[string]any), nil
	}
	return r.storage.GetNodeStates(ctx, workflowID)
}

func (r *Registry) GetSink(workflowID, nodeID string) (hermod.Sink, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ae, ok := r.engines[workflowID]
	if !ok {
		return nil, false
	}
	idx, ok := ae.sinkNodeToIndex[nodeID]
	if !ok || idx < 0 || idx >= len(ae.sinks) {
		return nil, false
	}
	return ae.sinks[idx], true
}

// RecordCircuitBreakerFailure counts a downstream failure against a circuit
// breaker node.
//
// The breaker reads this count and opens once it passes its threshold. Nothing
// incremented it before, so a node offered in the editor as "Stop flow on
// failure threshold" always reported success however broken the downstream was
// — a control that cannot fire, which is worse than no control because someone
// believes it is there.
//
// The registry is the natural place for this: it already holds node state and
// is what the traversal has a handle on.
func (r *Registry) RecordCircuitBreakerFailure(workflowID, breakerNodeID string) {
	if breakerNodeID == "" {
		return
	}
	(&reliability.CircuitBreakerExecutor{}).RecordFailure(r, breakerNodeID)
	r.BroadcastLog(workflowID, "WARN",
		"Circuit breaker "+breakerNodeID+" recorded a downstream failure", "")
}
