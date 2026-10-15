// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package nodes_test

import (
	"testing"

	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes"
)

// Importing this package is how the binary gets its node types; a type that
// is missing here fails at runtime, on the first message, not at build time.
func TestImportingNodesRegistersTheAINodes(t *testing.T) {
	for _, nodeType := range []string{"ai_classify", "ai_agent", "approval", "sink"} {
		if _, ok := interfaces.GetNodeExecutor(nodeType); !ok {
			t.Errorf("node type %q is not registered", nodeType)
		}
	}
	ex, _ := interfaces.GetNodeExecutor("ai_agent")
	if _, ok := ex.(interfaces.ApprovalResumer); !ok {
		t.Error("ai_agent must resume from its own approvals")
	}
}
