// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package storage

// WorkflowExportBundle packages a workflow along with its referenced dependencies
// (like sources and sinks) for easy export and import between Hermod instances.
type WorkflowExportBundle struct {
	Workflow Workflow `json:"workflow"`
	Sources  []Source `json:"sources,omitempty" omitzero:"true"`
	Sinks    []Sink   `json:"sinks,omitempty" omitzero:"true"`

	// MissingRefs names the dependencies the workflow points at that no longer
	// exist on the exporting instance. They were previously dropped from the
	// bundle in silence, so an export of a workflow with a dangling reference
	// looked complete and imported into a workflow that could not start.
	MissingRefs []MissingRef `json:"missing_refs,omitempty" omitzero:"true"`
}

// MissingRef is one dependency an export could not resolve.
type MissingRef struct {
	Kind   string `json:"kind"` // "source" or "sink"
	ID     string `json:"id"`
	NodeID string `json:"node_id,omitempty"`
}

// NodeConfigSourceKeys are the config keys through which a node names a source
// without being a source node. db_lookup writes "sourceId"; the enrichment SQL
// node (SQLConfig.tsx) reads and writes both spellings.
var NodeConfigSourceKeys = []string{"sourceId", "sourceID"}

// WorkflowQueriesSource reports whether a workflow aims SQL at this source
// without holding it in a source node's RefID: a db_lookup's sourceId, or the
// delegate of a batch_sql source it does hold.
//
// Walking nodes for `type == "source"` alone misses both, which is how a source
// that a running workflow depends on could be edited or deleted out from under
// it. delegateOf resolves a source id to its batch_sql delegate, or "" when it
// has none; callers that cannot resolve sources may pass nil.
func WorkflowQueriesSource(wf Workflow, sourceID string, delegateOf func(id string) string) bool {
	if sourceID == "" {
		return false
	}
	for _, node := range wf.Nodes {
		for _, key := range NodeConfigSourceKeys {
			if id, ok := node.Config[key].(string); ok && id == sourceID {
				return true
			}
		}
		if node.Type == "source" && node.RefID != "" && delegateOf != nil {
			if delegateOf(node.RefID) == sourceID {
				return true
			}
		}
	}
	return false
}
