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
	"slices"

	"github.com/gsoultan/hermod/internal/storage"
)

// builder accumulates nodes and edges, each once, from records indexed by id.
type builder struct {
	nodes   map[string]*Node
	edges   map[Edge]bool
	sources map[string]storage.Source
	sinks   map[string]storage.Sink
}

func (b *builder) addAsset(a asset, ref *Ref) string {
	id := a.id()
	n, ok := b.nodes[id]
	if !ok {
		n = &Node{ID: id, Kind: a.kind, Label: a.displayLabel(), System: a.system, Locator: a.locator, Resolved: a.kind != KindConnector}
		if a.kind == KindDataset || a.kind == KindModel {
			n.VHost = a.locator
		}
		b.nodes[id] = n
	}
	if ref != nil && !slices.Contains(n.Refs, *ref) {
		n.Refs = append(n.Refs, *ref)
	}
	return id
}

func (b *builder) addEdge(e Edge) { b.edges[e] = true }

// Derive builds the lineage graph of the inputs. It is deterministic: nodes
// are ordered by id and edges by (from, to, relation, node).
func Derive(in Inputs) Graph {
	b := &builder{
		nodes:   map[string]*Node{},
		edges:   map[Edge]bool{},
		sources: make(map[string]storage.Source, len(in.Sources)),
		sinks:   make(map[string]storage.Sink, len(in.Sinks)),
	}
	for _, s := range in.Sources {
		b.sources[s.ID] = s
	}
	for _, s := range in.Sinks {
		b.sinks[s.ID] = s
	}
	for _, wf := range in.Workflows {
		b.addWorkflow(wf)
	}
	// Registered models are assets whether or not a workflow uses them yet; a
	// retraining policy reads its dataset.
	for _, m := range in.Models {
		mid := b.addAsset(modelAsset(m.VHost, m.Name), nil)
		if m.Retrain != nil && m.Retrain.Spec.Dataset != "" {
			did := b.addAsset(datasetAsset(m.VHost, m.Retrain.Spec.Dataset), nil)
			b.addEdge(Edge{From: did, To: mid, Relation: RelTrains})
		}
	}
	return b.graph()
}

// sourceAssets are the assets of a source record, its connector asset when
// none can be derived.
func (b *builder) sourceAssets(s storage.Source) ([]asset, Ref) {
	ref := Ref{Kind: "source", ID: s.ID, Name: s.Name, Type: s.Type}
	as := sourceAssets(s, b.sources)
	if len(as) == 0 {
		as = []asset{connector(s.Type, "source", s.ID, s.Name)}
	}
	return as, ref
}

// sinkAssets are the assets of a sink record, its connector asset when none
// can be derived. A composite sink with no members has none.
func (b *builder) sinkAssets(s storage.Sink) ([]asset, Ref) {
	ref := Ref{Kind: "sink", ID: s.ID, Name: s.Name, Type: s.Type}
	as := sinkAssets(s, b.sinks, map[string]bool{})
	if len(as) == 0 && s.Type != "failover" && s.Type != "txgroup" {
		as = []asset{connector(s.Type, "sink", s.ID, s.Name)}
	}
	return as, ref
}

// addWorkflow adds a workflow node, the assets its nodes and dead-letter
// sink read and write, and the edges between them.
func (b *builder) addWorkflow(wf storage.Workflow) {
	wfID := WorkflowNodeID(wf.ID)
	active := wf.Active
	b.nodes[wfID] = &Node{ID: wfID, Kind: KindWorkflow, Label: cmp.Or(wf.Name, wf.ID), Resolved: true, VHost: wf.VHost, Active: &active}
	reader := nodeReader{vhost: wf.VHost, sources: b.sources}

	for _, n := range wf.Nodes {
		switch n.Type {
		case "source":
			if s, ok := b.sources[n.RefID]; ok {
				as, ref := b.sourceAssets(s)
				b.link(as, &ref, Edge{To: wfID, Relation: RelReads, WorkflowID: wf.ID, NodeID: n.ID})
			}
		case "sink":
			if s, ok := b.sinks[n.RefID]; ok {
				as, ref := b.sinkAssets(s)
				b.link(as, &ref, Edge{From: wfID, Relation: RelWrites, WorkflowID: wf.ID, NodeID: n.ID})
			}
		default:
			for _, io := range reader.node(n) {
				e := Edge{To: wfID, Relation: io.relation, WorkflowID: wf.ID, NodeID: n.ID}
				if io.out {
					e = Edge{From: wfID, Relation: io.relation, WorkflowID: wf.ID, NodeID: n.ID}
				}
				b.link([]asset{io.asset}, nil, e)
			}
		}
	}
}

// link adds each asset and an edge like e to or from it: e names the
// workflow on one end and leaves the other empty for the asset.
func (b *builder) link(as []asset, ref *Ref, e Edge) {
	for _, a := range as {
		id := b.addAsset(a, ref)
		edge := e
		if edge.From == "" {
			edge.From = id
		} else {
			edge.To = id
		}
		b.addEdge(edge)
	}
}

// graph is the accumulated graph, in a deterministic order.
func (b *builder) graph() Graph {
	g := Graph{Nodes: make([]Node, 0, len(b.nodes)), Edges: make([]Edge, 0, len(b.edges))}
	for _, n := range b.nodes {
		slices.SortFunc(n.Refs, func(a, b Ref) int { return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID)) })
		g.Nodes = append(g.Nodes, *n)
	}
	slices.SortFunc(g.Nodes, func(a, b Node) int { return cmp.Compare(a.ID, b.ID) })
	for e := range b.edges {
		g.Edges = append(g.Edges, e)
	}
	slices.SortFunc(g.Edges, func(a, b Edge) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To), cmp.Compare(a.Relation, b.Relation),
			cmp.Compare(a.WorkflowID, b.WorkflowID), cmp.Compare(a.NodeID, b.NodeID))
	})
	return g
}
