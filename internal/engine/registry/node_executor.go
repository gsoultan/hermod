package registry

import (
	"context"
	"sync"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
)

// NodeExecutor defines the interface for executing a workflow node.
type NodeExecutor interface {
	Execute(ctx context.Context, nctx NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error)
}

var (
	executorsMu sync.RWMutex
	executors   = make(map[string]NodeExecutor)
)

// RegisterNodeExecutor registers a node executor for a given node type.
func RegisterNodeExecutor(nodeType string, executor NodeExecutor) {
	executorsMu.Lock()
	defer executorsMu.Unlock()
	executors[nodeType] = executor
}

// GetNodeExecutor retrieves a node executor for a given node type.
func GetNodeExecutor(nodeType string) (NodeExecutor, bool) {
	executorsMu.RLock()
	defer executorsMu.RUnlock()
	e, ok := executors[nodeType]
	return e, ok
}
