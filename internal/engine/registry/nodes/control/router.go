package control

import (
	"context"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	interfaces.RegisterNodeExecutor("router", &RouterNode{})
}

// RouterNode handles multi-branch routing based on rules.
type RouterNode struct{}

// Execute evaluates rules and returns the label of the first matching rule.
func (n *RouterNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	rules := evaluator.ParseObjectList(node.Config["rules"])

	for _, rule := range rules {
		label, _ := rule["label"].(string)
		conditions := n.parseRuleConditions(rule)

		if len(conditions) > 0 {
			if nctx.EvaluateConditions(msg, conditions) {
				return []hermod.Message{msg}, label, nil
			}
		}
	}
	return []hermod.Message{msg}, "default", nil
}

func (n *RouterNode) parseRuleConditions(rule map[string]any) []map[string]any {
	ruleConditions := evaluator.ParseObjectList(rule["conditions"])

	if len(ruleConditions) == 0 {
		field, _ := rule["field"].(string)
		op, _ := rule["operator"].(string)
		val := rule["value"]
		if field != "" && op != "" {
			ruleConditions = append(ruleConditions, map[string]any{
				"field":    field,
				"operator": op,
				"value":    val,
			})
		}
	}
	return ruleConditions
}

// PreviewSafeNode marks this node as runnable from the editor's Test button:
// deciding which route matches reads the message and this node's own config,
// and writes nothing anywhere.
func (n *RouterNode) PreviewSafeNode() {}
