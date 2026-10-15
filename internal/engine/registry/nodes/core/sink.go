// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
