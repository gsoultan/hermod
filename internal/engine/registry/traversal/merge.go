// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package traversal

import (
	"maps"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// MergeData folds src into dst the way a node where branches meet combines
// what each branch brought it. It is the one rule for that: the live
// traversal applies it when a second branch delivers into a node's slot
// (resolveEdge), and the editor's simulation applies it when it delivers a
// sample there (simulation.deliver), so a preview cannot show a merge the
// workflow does not do.
//
// strategy is the node's "strategy" setting: "overwrite" or "shallow" copy
// src's keys over dst's; "if_missing" keeps dst's; "deep", the default,
// merges nested objects key by key and otherwise takes src's value.
func MergeData(dst, src map[string]any, strategy string) {
	switch strategy {
	case "overwrite", "shallow":
		maps.Copy(dst, src)
	case "if_missing":
		for k, v := range src {
			if _, ok := dst[k]; !ok {
				dst[k] = v
			}
		}
	default:
		for k, v := range src {
			if srcMap, ok := v.(map[string]any); ok {
				if dstMap, ok := dst[k].(map[string]any); ok {
					MergeData(dstMap, srcMap, "deep")
					continue
				}
			}
			dst[k] = v
		}
	}
}

// MergeStrategy is the merge strategy a node is configured with, "" for the
// default.
func MergeStrategy(node *storage.WorkflowNode) string {
	if node == nil {
		return ""
	}
	s, _ := node.Config["strategy"].(string)
	return s
}

// mergeInto folds src's data into the message already waiting at a node, so
// the node runs once on what every branch brought it. The waiting message's
// cached payload is dropped with it: it was marshalled from the data before
// the merge.
func mergeInto(dst hermod.Message, node *storage.WorkflowNode, src hermod.Message) {
	MergeData(dst.DataRef(), src.Data(), MergeStrategy(node))
	if c, ok := dst.(interface{ ClearCachedPayload() }); ok {
		c.ClearCachedPayload()
	}
}
