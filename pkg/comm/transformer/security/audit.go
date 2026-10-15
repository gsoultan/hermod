// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
