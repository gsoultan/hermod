package control

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
)

func init() {
	interfaces.RegisterNodeExecutor("approval", &ApprovalNode{})
}

// ApprovalNode handles human-in-the-loop approvals.
type ApprovalNode struct{}

// Execute halts the workflow and creates an approval request.
func (n *ApprovalNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	app := n.createApprovalModel(workflowID, node, msg)

	// Support custom forms
	if formRaw, ok := node.Config["form"]; ok {
		if formDef, ok := formRaw.(map[string]any); ok {
			app.FormDefinition = formDef
		}
	}

	if store := nctx.Storage(); store != nil {
		_ = store.CreateApproval(ctx, app)
	}

	nctx.BroadcastLog(workflowID, "INFO", "Approval requested at node "+node.ID, msg.ID())

	// Halt the message until approved (no forward routing)
	return nil, "pending", nil
}

func (n *ApprovalNode) createApprovalModel(workflowID string, node *storage.WorkflowNode, msg hermod.Message) storage.Approval {
	return storage.Approval{
		ID:         uuid.New().String(),
		WorkflowID: workflowID,
		NodeID:     node.ID,
		MessageID:  msg.ID(),
		Payload:    msg.Payload(),
		Metadata:   msg.Metadata(),
		Data:       msg.Data(),
		Status:     "pending",
		CreatedAt:  time.Now(),
	}
}
