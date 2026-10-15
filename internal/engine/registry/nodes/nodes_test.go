// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
