// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
