package core

import (
	"context"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
)

func init() {
	registry.RegisterNodeExecutor("validator", &ValidatorNode{})
}

// ValidatorNode handles message validation.
type ValidatorNode struct{}

// Execute runs the validator transformation.
func (n *ValidatorNode) Execute(ctx context.Context, nctx registry.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	res, err := nctx.ApplyTransformation(ctx, msg, "validator", node.Config)
	if err != nil {
		return nil, "", err
	}
	if res == nil {
		return nil, "", nil
	}
	return []hermod.Message{res}, "", nil
}
