// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package subflow

import (
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// Caller is one Run sub-flow step that runs a sub-flow.
type Caller struct {
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	NodeID       string `json:"node_id"`
	// Step is the step's label, or its ID when it has none.
	Step string `json:"step"`
	// Version is the saved version the step pins; 0 when it runs the latest
	// save or its pin cannot be read.
	Version int `json:"version"`
	// Latest is set when the step runs whatever the sub-flow was saved as last,
	// so a change to the sub-flow reaches it on its next start.
	Latest bool `json:"latest"`
}

// Callers returns every Run sub-flow step in wfs that runs the sub-flow id, in
// the order of wfs and of each workflow's steps.
func Callers(wfs []storage.Workflow, id string) []Caller {
	var out []Caller
	for _, wf := range wfs {
		for _, n := range wf.Nodes {
			if n.Type != TypeCall {
				continue
			}
			if sid, _ := n.Config["subflow_id"].(string); strings.TrimSpace(sid) != id {
				continue
			}
			c := Caller{WorkflowID: wf.ID, WorkflowName: wf.Name, NodeID: n.ID, Step: stepLabel(n)}
			if v, err := pinnedVersion(n.Config["version"]); err == nil {
				c.Version, c.Latest = v, v == 0
			}
			out = append(out, c)
		}
	}
	return out
}

func stepLabel(n storage.WorkflowNode) string {
	if l, ok := n.Config["label"].(string); ok && strings.TrimSpace(l) != "" {
		return l
	}
	return n.ID
}
