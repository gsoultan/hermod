// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package core

import (
	"context"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
)

func init() {
	interfaces.RegisterNodeExecutor("validator", &ValidatorNode{})
}

// ValidatorNode handles message validation.
type ValidatorNode struct{}

// Execute runs the validator transformation.
func (n *ValidatorNode) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	res, err := nctx.ApplyTransformation(ctx, msg, "validator", node.Config)
	if err != nil {
		return nil, "", err
	}
	if res == nil {
		return nil, "", nil
	}
	return []hermod.Message{res}, "", nil
}
