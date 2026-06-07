package registry

import (
	"context"

	"github.com/user/hermod"
)

// NodeContext provides services to node executors.
type NodeContext interface {
	BroadcastLiveMessage(workflowID, nodeID string, msg hermod.Message, isError bool, errMsg string)
	BroadcastLog(workflowID, level, msg, msgID string)
	ApplyTransformation(ctx context.Context, msg hermod.Message, transType string, config map[string]any) (hermod.Message, error)
	EvaluateConditions(msg hermod.Message, conditions []map[string]any) bool
	Storage() RegistryStorage
	StateStore() hermod.StateStore
	GetNodeState(key string) (any, bool)
	SetNodeState(key string, val any)
	GetSink(workflowID, nodeID string) (hermod.Sink, bool)
}
