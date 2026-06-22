package registry

import (
	"context"

	"github.com/user/hermod"
)

func (r *Registry) BroadcastLiveMessage(workflowID, nodeID string, msg hermod.Message, isError bool, errMsg string) {
	r.broadcastLiveMessageFromHermod(workflowID, nodeID, msg, isError, errMsg)
}

func (r *Registry) BroadcastLog(workflowID, level, msg, msgID string) {
	r.broadcastLogWithData(workflowID, level, msg, msgID)
}

func (r *Registry) ApplyTransformation(ctx context.Context, msg hermod.Message, transType string, config map[string]any) (hermod.Message, error) {
	return r.applyTransformation(ctx, msg, transType, config)
}

func (r *Registry) EvaluateConditions(msg hermod.Message, conditions []map[string]any) bool {
	return r.evaluateConditions(msg, conditions)
}

func (r *Registry) Storage() RegistryStorage {
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
