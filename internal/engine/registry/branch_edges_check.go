// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"fmt"
	"slices"

	"github.com/gsoultan/hermod/internal/engine/registry/nodes/control"
	"github.com/gsoultan/hermod/internal/storage"
)

// unroutedBranches describes every switch case and router rule that no edge
// carries, and every edge out of one whose label names no case or rule.
//
// A message that matches a case with no edge stops at the node, and the engine
// acknowledges it as filtered: nothing fails and nothing is logged. The usual
// cause is renaming a case after drawing its edge, which leaves the edge with
// the old name. Checking once when the workflow starts names the case in the
// log, where it can be found, without costing every message a check.
func unroutedBranches(wf storage.Workflow) []string {
	var problems []string
	for _, node := range wf.Nodes {
		names := control.BranchNames(node.Type, node.Config)
		if len(names) == 0 {
			continue
		}
		var labels []string
		unlabelled := false
		for _, edge := range wf.Edges {
			if edge.SourceID != node.ID {
				continue
			}
			label := edgeLabel(edge)
			if label == "" {
				unlabelled = true
				continue
			}
			labels = append(labels, label)
			if label != "default" && !slices.Contains(names, label) {
				problems = append(problems, fmt.Sprintf(
					"%s %q: the edge to %s is labelled %q, which matches no %s, so it never carries a message. Was the %s renamed?",
					node.Type, nodeLabel(node), edge.TargetID, label, branchKind(node.Type), branchKind(node.Type)))
			}
		}
		// An unlabelled edge takes every branch, so no case is left without one.
		if unlabelled {
			continue
		}
		for _, name := range names {
			if !slices.Contains(labels, name) {
				problems = append(problems, fmt.Sprintf(
					"%s %q: %s %q has no edge, so messages that match it stop here.",
					node.Type, nodeLabel(node), branchKind(node.Type), name))
			}
		}
	}
	return problems
}

func branchKind(nodeType string) string {
	if nodeType == "router" {
		return "rule"
	}
	return "case"
}

func nodeLabel(node storage.WorkflowNode) string {
	if label, ok := node.Config["label"].(string); ok && label != "" {
		return label
	}
	return node.ID
}
