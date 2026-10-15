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
	"fmt"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/storage"
)

type SinkExecutor struct{}

func init() {
	interfaces.RegisterNodeExecutor("sink", &SinkExecutor{})
}

func (e *SinkExecutor) Execute(ctx context.Context, nctx interfaces.NodeContext, workflowID string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	isSeq, _ := node.Config["sequential"].(bool)
	if !isSeq {
		return []hermod.Message{msg}, "", nil
	}

	sink, ok := nctx.GetSink(workflowID, node.ID)
	if !ok {
		return nil, "error", fmt.Errorf("sink not found: %s", node.ID)
	}

	if err := sink.Write(ctx, msg); err != nil {
		nctx.BroadcastLog(workflowID, "ERROR", fmt.Sprintf("Sequential sink %s failed: %v", node.ID, err), msg.ID())
		return []hermod.Message{msg}, "error", err
	}

	return []hermod.Message{msg}, "success", nil
}
