// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// An explode node without an array path fails every record it is given; the
// workflow says so before it is started, as it does for foreach.
func TestExplodeNodeNeedsAnArrayPath(t *testing.T) {
	wf := storage.Workflow{Nodes: []storage.WorkflowNode{
		{ID: "x1", Type: "explode", Config: map[string]any{}},
		{ID: "x2", Type: "explode", Config: map[string]any{"arrayPath": "lines"}},
	}}
	issues, _, _ := nodeConfigIssues(wf)
	if len(issues) != 1 || issues[0].NodeID != "x1" || issues[0].Severity != "error" ||
		!strings.Contains(issues[0].Message, "arrayPath") {
		t.Fatalf("issues = %+v, want one error for x1 naming arrayPath", issues)
	}
}
