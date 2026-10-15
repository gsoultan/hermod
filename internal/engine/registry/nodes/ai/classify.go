// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package ai holds the workflow node executors that call a language model
// and route on its answer.
package ai

import (
	"context"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
)

func init() {
	interfaces.RegisterNodeExecutor("ai_classify", &ClassifyNode{})
}

// ClassifyNode is semantic routing: the model picks one of the node's labels
// and the message leaves on the edge with that label, or on "unsure" when the
// model is not confident enough.
type ClassifyNode struct{}

// Execute implements interfaces.NodeExecutor.
func (n *ClassifyNode) Execute(ctx context.Context, _ interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	c, err := genai.Classify(genai.WithWorkflow(ctx, workflowID), msg, node.Config)
	if err != nil {
		return []hermod.Message{msg}, "", err
	}
	return []hermod.Message{msg}, c.Label, nil
}
