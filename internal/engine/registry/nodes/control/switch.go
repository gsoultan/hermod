// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package control

import (
	"context"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	interfaces.RegisterNodeExecutor("switch", &SwitchNode{})
}

// SwitchNode handles value-based branching.
type SwitchNode struct{}

// Execute evaluates cases and returns the matching branch label.
func (n *SwitchNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	cases := evaluator.ParseObjectList(node.Config["cases"])

	field, _ := node.Config["field"].(string)

	for i, c := range cases {
		label, _ := c["label"].(string)
		conditions := n.parseCaseConditions(c)

		if len(conditions) > 0 {
			if nctx.EvaluateConditions(msg, conditions) {
				return []hermod.Message{msg}, routeBranch("switch", label, i), nil
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
				return []hermod.Message{msg}, routeBranch("switch", label, i), nil
			}
		}
	}
	return []hermod.Message{msg}, "default", nil
}

func (n *SwitchNode) parseCaseConditions(c map[string]any) []map[string]any {
	return evaluator.ParseObjectList(c["conditions"])
}

// PreviewSafeNode marks this node as runnable from the editor's Test button:
// deciding which case matches reads the message and this node's own config,
// and writes nothing anywhere.
func (n *SwitchNode) PreviewSafeNode() {}
