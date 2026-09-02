package control

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/Hermod/internal/storage"
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

	// This returns no messages: the message leaves the pipeline and the workflow
	// resumes from the approval record when a human acts on it. With no record
	// there is nothing for anyone to approve and the message is simply gone, so
	// a failure to write it has to surface rather than be discarded.
	store := nctx.Storage()
	if store == nil {
		return nil, "", fmt.Errorf("approval node %s: no storage, so an approval could "+
			"never be recorded or acted on", node.ID)
	}
	if err := store.CreateApproval(ctx, app); err != nil {
		return nil, "", fmt.Errorf("approval node %s: could not record the approval request, "+
			"so nobody could approve it: %w", node.ID, err)
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
