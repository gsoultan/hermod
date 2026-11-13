// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advisor

import (
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

// Flow is what the rules need to know about a flow's design.
type Flow struct {
	ID              string
	TraceSampleRate float64
	// MaxInflight is the engine's cap on messages in flight. A sink batch
	// larger than it can never fill on count (pkg/engine/batch_sizing.go).
	MaxInflight int
	Nodes       []Node
	Edges       []Edge
	// Sinks is keyed by sink connection ID, the key of a frame's SinkFill.
	Sinks map[string]SinkSettings
}

// Node is one step of the flow.
type Node struct {
	ID string
	// Type is the step's kind: the transformation it runs for a
	// transformation node (db_lookup, set, ...), the node type otherwise.
	Type   string
	Label  string
	RefID  string
	Config map[string]any
}

// Edge joins two steps.
type Edge struct {
	ID       string
	From, To string
}

// SinkSettings are a sink connection's settings the rules read.
type SinkSettings struct {
	Name         string
	Type         string
	BatchSize    int
	BatchTimeout string
	ShardCount   int
}

// FlowFrom describes a stored workflow. sinks holds the workflow's sink
// connections by ID; a missing one is described by its ID alone.
func FlowFrom(wf storage.Workflow, sinks map[string]storage.Sink) Flow {
	f := Flow{
		ID:              wf.ID,
		TraceSampleRate: wf.TraceSampleRate,
		MaxInflight:     config.DefaultConfig().MaxInflight,
		Sinks:           map[string]SinkSettings{},
	}
	for _, n := range wf.Nodes {
		label, _ := n.Config["label"].(string)
		f.Nodes = append(f.Nodes, Node{ID: n.ID, Type: nodeType(n), Label: label, RefID: n.RefID, Config: n.Config})
		if n.Type == "sink" && n.RefID != "" {
			s, ok := sinks[n.RefID]
			if !ok {
				f.Sinks[n.RefID] = SinkSettings{Name: n.RefID}
				continue
			}
			f.Sinks[n.RefID] = SinkSettings{
				Name:         s.Name,
				Type:         s.Type,
				BatchSize:    atoi(s.Config["batch_size"]),
				BatchTimeout: s.Config["batch_timeout"],
				ShardCount:   atoi(s.Config["shard_count"]),
			}
		}
	}
	for _, e := range wf.Edges {
		f.Edges = append(f.Edges, Edge{ID: e.ID, From: e.SourceID, To: e.TargetID})
	}
	return f
}

func nodeType(n storage.WorkflowNode) string {
	if n.Type == "transformation" {
		if tt, _ := n.Config["transType"].(string); tt != "" {
			return tt
		}
	}
	return n.Type
}

// atoi reads a numeric connector setting; anything else is unset.
func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (f Flow) node(id string) (Node, bool) {
	for _, n := range f.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// name is how a person knows the step: its label, else its kind and ID.
func (f Flow) name(id string) string {
	if n, ok := f.node(id); ok {
		if n.Label != "" {
			return n.Label
		}
		if n.Type != "" {
			return n.Type + " (" + id + ")"
		}
	}
	return id
}

func (f Flow) sinkName(id string) string {
	if s, ok := f.Sinks[id]; ok && s.Name != "" {
		return s.Name
	}
	return id
}

// lookupCacheOff reports whether a lookup step was told not to cache: an
// explicit ttl of zero. Unset means the lookup's own default, which caches.
func lookupCacheOff(n Node) bool {
	if !isLookup(n.Type) {
		return false
	}
	ttl, _ := n.Config["ttl"].(string)
	ttl = strings.TrimSpace(ttl)
	if ttl == "" {
		return false
	}
	if ttl == "0" {
		return true
	}
	d, err := time.ParseDuration(ttl)
	return err == nil && d == 0
}

// isLookup is a lookup whose ttl setting controls a result cache.
func isLookup(t string) bool { return t == "db_lookup" || t == "api_lookup" }
