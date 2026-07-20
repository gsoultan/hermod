package control

import (
	"context"
	"encoding/json"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/storage"
)

func init() {
	interfaces.RegisterNodeExecutor("switch", &SwitchNode{})
}

// SwitchNode handles value-based branching.
type SwitchNode struct{}

// Execute evaluates cases and returns the matching branch label.
func (n *SwitchNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	casesStr, _ := node.Config["cases"].(string)
	var cases []map[string]any
	_ = json.Unmarshal([]byte(casesStr), &cases)

	field, _ := node.Config["field"].(string)

	for _, c := range cases {
		label, _ := c["label"].(string)
		conditions := n.parseCaseConditions(c)

		if len(conditions) > 0 {
			if nctx.EvaluateConditions(msg, conditions) {
				return []hermod.Message{msg}, label, nil
			}
		} else {
			operator, ok := c["operator"].(string)
			if !ok || operator == "" {
				operator = "="
			}
			value := c["value"]

			// Use nctx.EvaluateConditions to handle the comparison.
			// This automatically supports regex, contains, templates in value, and expressions in field.
			caseCond := map[string]any{
				"field":    field,
				"operator": operator,
				"value":    value,
			}
			if nctx.EvaluateConditions(msg, []map[string]any{caseCond}) {
				return []hermod.Message{msg}, label, nil
			}
		}
	}
	return []hermod.Message{msg}, "default", nil
}

func (n *SwitchNode) parseCaseConditions(c map[string]any) []map[string]any {
	var caseConditions []map[string]any
	if condsRaw, ok := c["conditions"].([]any); ok {
		for _, cr := range condsRaw {
			if condMap, ok := cr.(map[string]any); ok {
				caseConditions = append(caseConditions, condMap)
			}
		}
	}
	return caseConditions
}
