// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package nodeclean decides what a saved workflow node may hold, and removes
// the rest.
//
// The editor saves a node's config as its React Flow data, whatever is in it.
// Setting up a source or sink from the editor used to spread the whole
// connection the drawer returned over the node -- its config with the
// password in it, its CDC cursor, its sample row, which worker held it -- so
// a copy of every connection, credentials in plain text, sat in each workflow
// row, in each version of it, in exports and in what promotion sent to
// production. The connection's own row keeps those, encrypted; the node needs
// none of them because the engine loads the connection by RefID.
//
// Storage runs every workflow and version through Nodes on the way in and on
// the way out, so nothing written before this rule reaches the engine, an
// export or a promotion either.
package nodeclean

import (
	"maps"
	"slices"

	"github.com/gsoultan/hermod/internal/storage"
)

// connectionCopyKeys are the fields of a storage.Source or storage.Sink. On a
// source or sink node they are a copy of the connection, never a setting of
// the node: "config" holds the credentials, "state" the CDC cursor, "sample"
// a row out of someone's database.
var connectionCopyKeys = []string{
	"id", "name", "vhost", "active", "status", "worker_id", "workspace_id",
	"config", "sample", "state", "created_at", "updated_at",
}

// editorOnlyKeys never belong in a saved node of any type. lastSample and
// testResult are data the editor captured; ref_id repeats the node's own
// RefID, and an import that gives a node a new RefID left this copy pointing
// at the old one.
var editorOnlyKeys = []string{"lastSample", "testResult", "ref_id"}

// IsConnection reports a node that stands for a stored source or sink.
func IsConnection(n storage.WorkflowNode) bool {
	return n.Type == "source" || n.Type == "sink"
}

// Nodes returns nodes holding only what a saved node may hold, and whether
// anything was removed. The nodes passed in are not modified: a node whose
// config changes gets its own copy of the map.
func Nodes(nodes []storage.WorkflowNode) ([]storage.WorkflowNode, bool) {
	var out []storage.WorkflowNode
	for i, n := range nodes {
		cfg, changed := clean(n)
		if !changed {
			continue
		}
		if out == nil {
			out = slices.Clone(nodes)
		}
		out[i].Config = cfg
	}
	if out == nil {
		return nodes, false
	}
	return out, true
}

func clean(n storage.WorkflowNode) (map[string]any, bool) {
	var cfg map[string]any // a copy, made on the first change
	own := func() map[string]any {
		if cfg == nil {
			cfg = maps.Clone(n.Config)
		}
		return cfg
	}
	drop := func(keys []string) {
		for _, k := range keys {
			if _, ok := n.Config[k]; ok {
				delete(own(), k)
			}
		}
	}
	if IsConnection(n) {
		// The editor names a node by its label. A node built through the API
		// may only have the connection's name; keep it as the label.
		name, _ := n.Config["name"].(string)
		if label, _ := n.Config["label"].(string); label == "" && name != "" {
			own()["label"] = name
		}
		drop(connectionCopyKeys)
	}
	drop(editorOnlyKeys)
	return cfg, cfg != nil
}
