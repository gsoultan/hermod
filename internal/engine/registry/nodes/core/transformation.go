package core

import (
	"context"
	"encoding/json"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
)

func init() {
	interfaces.RegisterNodeExecutor("transformation", &TransformationNode{})
}

// TransformationNode handles data transformations.
type TransformationNode struct{}

// Execute runs the configured transformation or pipeline.
func (n *TransformationNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	transType, _ := node.Config["transType"].(string)
	if transType == "pipeline" {
		return n.runPipeline(ctx, nctx, node, msg)
	}

	// Optimization: Avoid cloning here as the message is already either a clone
	// or owned by this traversal path. ApplyTransformation will handle its own
	// internal logic.
	res, err := nctx.ApplyTransformation(ctx, msg, transType, node.Config)
	if err != nil {
		nctx.BroadcastLiveMessage(workflowID, node.ID, msg, true, err.Error())
		return nil, "", err
	}
	if res == nil {
		return nil, "", nil
	}
	return []hermod.Message{res}, "", nil
}

func (n *TransformationNode) runPipeline(ctx context.Context, nctx interfaces.NodeContext, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	var steps []map[string]any
	if cached, ok := node.Config["_parsed_steps"].([]map[string]any); ok {
		steps = cached
	} else {
		stepsStr, _ := node.Config["steps"].(string)
		_ = json.Unmarshal([]byte(stepsStr), &steps)
	}

	current := msg
	pctx := nctx.ContextWithPipelineSnapshot(ctx)
	for _, step := range steps {
		st, _ := step["transType"].(string)
		var err error
		current, err = nctx.ApplyTransformation(pctx, current, st, step)
		if err != nil {
			return nil, "", err
		}
		if current == nil {
			return nil, "", nil
		}
	}
	return []hermod.Message{current}, "", nil
}
