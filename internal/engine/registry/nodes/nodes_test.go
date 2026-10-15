// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
