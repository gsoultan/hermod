package control

import (
	"context"
	"fmt"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	interfaces.RegisterNodeExecutor("condition", &ConditionNode{})
}

// ConditionNode handles boolean branching.
type ConditionNode struct{}

// Execute evaluates conditions and returns the branch name ("true" or "false").
func (n *ConditionNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	conditions := n.parseConditions(node)

	// A regex that does not compile is not a condition that matches nothing:
	// EvaluateConditions starts `match` at false and swallows the compile
	// error, so it rejects *every* message. That used to happen silently —
	// no error, no log — which makes a typo in a filter indistinguishable
	// from "nothing matched" while the workflow drops all of its traffic and
	// still reports healthy.
	//
	// Failing here instead hands the message to the engine's normal node
	// failure path, so it is dead-lettered where a DLQ exists and loudly
	// reported where one does not.
	if err := evaluator.ValidateConditions(conditions); err != nil {
		var msgID string
		if msg != nil {
			msgID = msg.ID()
		}
		nctx.BroadcastLog(workflowID, "ERROR", fmt.Sprintf("Node %s rejects every message: %v", node.ID, err), msgID)
		return nil, "", fmt.Errorf("node %s: %w", node.ID, err)
	}

	if nctx.EvaluateConditions(msg, conditions) {
		return []hermod.Message{msg}, "true", nil
	}
	return []hermod.Message{msg}, "false", nil
}

// parseConditions delegates to the evaluator so the validator that checks a
// saved workflow and the node that runs it read the config the same way.
func (n *ConditionNode) parseConditions(node *storage.WorkflowNode) []map[string]any {
	return evaluator.ParseConditions(node.Config)
}

// PreviewSafeNode marks this node as runnable from the editor's Test button:
// deciding whether the condition holds reads the message and this node's own config,
// and writes nothing anywhere.
func (n *ConditionNode) PreviewSafeNode() {}
