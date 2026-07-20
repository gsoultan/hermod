package control

import (
	"context"
	"fmt"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
)

func init() {
	interfaces.RegisterNodeExecutor("wait", &WaitNode{})
}

// WaitNode handles time-based pauses in workflows.
type WaitNode struct{}

// Execute waits for a configured duration before continuing.
func (n *WaitNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	durationStr, _ := node.Config["duration"].(string)
	if durationStr == "" {
		return []hermod.Message{msg}, "", nil
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return []hermod.Message{msg}, "", fmt.Errorf("invalid duration: %w", err)
	}

	if duration > 30*time.Second {
		return n.suspendMessage(ctx, nctx, workflowID, node, msg, duration)
	}

	return n.waitForDuration(ctx, nctx, workflowID, duration, msg)
}

func (n *WaitNode) suspendMessage(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message, d time.Duration) ([]hermod.Message, string, error) {
	sm := storage.SuspendedMessage{
		ID:         msg.ID(),
		WorkflowID: workflowID,
		NodeID:     node.ID,
		Payload:    msg.Payload(),
		Metadata:   msg.Metadata(),
		Data:       msg.Data(),
		ResumeAt:   time.Now().Add(d),
		CreatedAt:  time.Now(),
	}
	if store := nctx.Storage(); store != nil {
		_ = store.CreateSuspendedMessage(ctx, sm)
	}
	nctx.BroadcastLog(workflowID, "INFO", fmt.Sprintf("Message suspended for %v", d), msg.ID())
	return nil, "suspended", nil
}

func (n *WaitNode) waitForDuration(ctx context.Context, nctx interfaces.NodeContext, workflowID string, d time.Duration, msg hermod.Message) ([]hermod.Message, string, error) {
	nctx.BroadcastLog(workflowID, "INFO", fmt.Sprintf("Waiting for %v", d), msg.ID())
	select {
	case <-time.After(d):
		return []hermod.Message{msg}, "", nil
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
}
