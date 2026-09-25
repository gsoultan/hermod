package control

import (
	"context"
	"strconv"

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

	for i, rule := range rules {
		label, _ := rule["label"].(string)
		conditions := n.parseRuleConditions(rule)

		if len(conditions) > 0 {
			if nctx.EvaluateConditions(msg, conditions) {
				return []hermod.Message{msg}, routeBranch("router", label, i), nil
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

// routeBranch is the branch a router rule or switch case sends a message down:
// its label, or, when it has none, the id the editor gives its handle (rule_0,
// case_2), which is the label on any edge drawn from that handle.
//
// It used to be the label alone. An unnamed rule's empty label is an empty
// branch, and an empty branch takes every edge (traversal.TakesEdge), so a
// message matching an unnamed rule went down every route out of the node --
// live, not only in the editor's preview. testdata/branch_names.json is the
// contract with the editor's branchHandleId; both sides test against it.
func routeBranch(nodeType, label string, index int) string {
	if label != "" {
		return label
	}
	prefix := "case"
	if nodeType == "router" {
		prefix = "rule"
	}
	return prefix + "_" + strconv.Itoa(index)
}
