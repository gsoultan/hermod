package control

import (
	"context"
	"encoding/json"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
)

func init() {
	registry.RegisterNodeExecutor("router", &RouterNode{})
}

// RouterNode handles multi-branch routing based on rules.
type RouterNode struct{}

// Execute evaluates rules and returns the label of the first matching rule.
func (n *RouterNode) Execute(ctx context.Context, nctx registry.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	rulesStr, _ := node.Config["rules"].(string)
	var rules []map[string]any
	_ = json.Unmarshal([]byte(rulesStr), &rules)

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
	var ruleConditions []map[string]any
	if condsRaw, ok := rule["conditions"].([]any); ok {
		for _, cr := range condsRaw {
			if condMap, ok := cr.(map[string]any); ok {
				ruleConditions = append(ruleConditions, condMap)
			}
		}
	}

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
