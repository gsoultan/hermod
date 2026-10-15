// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package lineage

import (
	"cmp"
	"errors"
	"slices"
)

// ErrUnknownNode is returned for an id the graph does not hold.
var ErrUnknownNode = errors.New("lineage: no such asset or workflow in this scope")

// Impacted is one node downstream of the root.
type Impacted struct {
	Node Node `json:"node"`
	// Depth is the number of edges on the shortest path from the root.
	Depth int `json:"depth"`
	// Path is the ids from the root to Node, both included.
	Path []string `json:"path"`
	// Relation is the relation of the last edge on Path.
	Relation string `json:"relation"`
}

// Impact is everything downstream of one asset or workflow.
type Impact struct {
	Root      Node       `json:"root"`
	Impacted  []Impacted `json:"impacted"`
	Workflows int        `json:"workflows"`
	Assets    int        `json:"assets"`
}

// Downstream answers "what breaks if this changes": every node reachable
// from id along the direction data flows, nearest first, each with the
// shortest path to it. Breadth-first over edges sorted by target id, so the
// path chosen among equally short ones is stable.
func Downstream(g Graph, id string) (Impact, error) {
	root, ok := g.Node(id)
	if !ok {
		return Impact{}, ErrUnknownNode
	}
	byID := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	seen, order := walk(g.Edges, id)

	imp := Impact{Root: root, Impacted: make([]Impacted, 0, len(order))}
	for _, nid := range order {
		v := seen[nid]
		path := []string{nid}
		for p := v.parent; p != ""; p = seen[p].parent {
			path = append(path, p)
			if p == id {
				break
			}
		}
		slices.Reverse(path)
		n := byID[nid]
		imp.Impacted = append(imp.Impacted, Impacted{Node: n, Depth: v.depth, Path: path, Relation: v.relation})
		if n.Kind == KindWorkflow {
			imp.Workflows++
		} else {
			imp.Assets++
		}
	}
	slices.SortStableFunc(imp.Impacted, func(a, b Impacted) int { return cmp.Compare(a.Depth, b.Depth) })
	return imp, nil
}

// visit is how the walk first reached a node.
type visit struct {
	parent   string
	relation string
	depth    int
}

// walk visits every node reachable from id breadth-first, following edges in
// (target, relation) order. It returns how each was reached and the order
// they were reached in, id excluded.
func walk(edges []Edge, id string) (map[string]visit, []string) {
	out := map[string][]Edge{}
	for _, e := range edges {
		out[e.From] = append(out[e.From], e)
	}
	for k := range out {
		slices.SortFunc(out[k], func(a, b Edge) int { return cmp.Or(cmp.Compare(a.To, b.To), cmp.Compare(a.Relation, b.Relation)) })
	}
	seen := map[string]visit{id: {}}
	queue := []string{id}
	var order []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range out[cur] {
			if _, done := seen[e.To]; done {
				continue
			}
			seen[e.To] = visit{parent: cur, relation: e.Relation, depth: seen[cur].depth + 1}
			queue = append(queue, e.To)
			order = append(order, e.To)
		}
	}
	return seen, order
}
