package core

import (
	"context"
	"encoding/json"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
)

func init() {
	registry.RegisterNodeExecutor("transformation", &TransformationNode{})
}

// TransformationNode handles data transformations.
type TransformationNode struct{}

// Execute runs the configured transformation or pipeline.
func (n *TransformationNode) Execute(ctx context.Context, nctx registry.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	transType, _ := node.Config["transType"].(string)
	if transType == "pipeline" {
		return n.runPipeline(ctx, nctx, node, msg)
	}

	res, err := nctx.ApplyTransformation(ctx, msg.Clone(), transType, node.Config)
	if err != nil {
		nctx.BroadcastLiveMessage(workflowID, node.ID, msg, true, err.Error())
		return nil, "", err
	}
	if res == nil {
		return nil, "", nil
	}
	return []hermod.Message{res}, "", nil
}

func (n *TransformationNode) runPipeline(ctx context.Context, nctx registry.NodeContext, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	stepsStr, _ := node.Config["steps"].(string)
	var steps []map[string]any
	_ = json.Unmarshal([]byte(stepsStr), &steps)

	current := msg.Clone()
	for _, step := range steps {
		st, _ := step["transType"].(string)
		var err error
		current, err = nctx.ApplyTransformation(ctx, current, st, step)
		if err != nil {
			return nil, "", err
		}
		if current == nil {
			return nil, "", nil
		}
	}
	return []hermod.Message{current}, "", nil
}
