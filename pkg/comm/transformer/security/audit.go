// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package security

import (
	"context"
	"os"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/transformer"

	"github.com/gsoultan/hermod"
)

func init() {
	transformer.Register("audit", &AuditTransformer{})
}

type AuditTransformer struct{}

func (t *AuditTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	workflowID, _ := ctx.Value(hermod.WorkflowIDKey).(string)
	nodeID, _ := ctx.Value(hermod.NodeIDKey).(string)
	hostname, _ := os.Hostname()

	prefix, _ := config["prefix"].(string)
	if prefix == "" {
		prefix = "audit_"
	}

	msg.SetData(prefix+"workflow_id", workflowID)
	msg.SetData(prefix+"node_id", nodeID)
	msg.SetData(prefix+"machine_name", hostname)
	msg.SetData(prefix+"timestamp", time.Now().Format(time.RFC3339))
	msg.SetData(prefix+"message_id", msg.ID())

	return msg, nil
}
