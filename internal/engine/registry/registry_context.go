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

func (r *Registry) SetNodeState(key string, val any) {
	r.nodeStatesMu.Lock()
	defer r.nodeStatesMu.Unlock()
	r.nodeStates[key] = val
}

func (r *Registry) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	return r.storage.UpdateNodeState(ctx, workflowID, nodeID, state)
}

func (r *Registry) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	return r.storage.GetNodeStates(ctx, workflowID)
}
