package control

import (
	"context"
	"encoding/json"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
)

func init() {
	interfaces.RegisterNodeExecutor("condition", &ConditionNode{})
}

// ConditionNode handles boolean branching.
type ConditionNode struct{}

// Execute evaluates conditions and returns the branch name ("true" or "false").
func (n *ConditionNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	conditions := n.parseConditions(node)
	if nctx.EvaluateConditions(msg, conditions) {
		return []hermod.Message{msg}, "true", nil
	}
	return []hermod.Message{msg}, "false", nil
}

func (n *ConditionNode) parseConditions(node *storage.WorkflowNode) []map[string]any {
	conditionsStr, _ := node.Config["conditions"].(string)
	var conditions []map[string]any
	if conditionsStr != "" {
		_ = json.Unmarshal([]byte(conditionsStr), &conditions)
	}

	if len(conditions) == 0 {
		field, _ := node.Config["field"].(string)
		op, _ := node.Config["operator"].(string)
		val, _ := node.Config["value"].(string)
		if field != "" {
			conditions = append(conditions, map[string]any{
				"field":    field,
				"operator": op,
				"value":    val,
			})
		}
	}
	return conditions
}
