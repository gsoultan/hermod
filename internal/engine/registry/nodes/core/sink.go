// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
