package control

import (
	"context"
	"fmt"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/Hermod/internal/storage"
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
	// This returns no messages: the message leaves the pipeline and comes back
	// when the reconciler finds it due. That is only sound if it was actually
	// written down, so a failure here has to surface rather than be discarded.
	//
	// It was discarded, and on every SQL backend the write always failed —
	// suspended_messages was defined in the query set but never created at
	// start-up. A wait longer than thirty seconds destroyed every message that
	// passed through it, and logged "Message suspended" on the way.
	store := nctx.Storage()
	if store == nil {
		return nil, "", fmt.Errorf("wait node %s: no storage, so a message suspended for %v "+
			"could never be resumed", node.ID, d)
	}
	if err := store.CreateSuspendedMessage(ctx, sm); err != nil {
		return nil, "", fmt.Errorf("wait node %s: could not record a message suspended for %v, "+
			"so it would not resume: %w", node.ID, d, err)
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
