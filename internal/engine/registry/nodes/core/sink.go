package core

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
)

type SinkExecutor struct{}

func init() {
	registry.RegisterNodeExecutor("sink", &SinkExecutor{})
}

func (e *SinkExecutor) Execute(ctx context.Context, nctx registry.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	isSeq, _ := node.Config["sequential"].(bool)
	if !isSeq {
		return []hermod.Message{msg}, "", nil
	}

	sink, ok := nctx.GetSink(workflowID, node.ID)
	if !ok {
		return nil, "error", fmt.Errorf("sink not found: %s", node.ID)
	}

	if err := sink.Write(ctx, msg); err != nil {
		nctx.BroadcastLog(workflowID, "ERROR", fmt.Sprintf("Sequential sink %s failed: %v", node.ID, err), msg.ID())
		return []hermod.Message{msg}, "error", err
	}

	return []hermod.Message{msg}, "success", nil
}
