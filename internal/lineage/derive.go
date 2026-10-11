package lineage

import (
	"cmp"
	"slices"

	"github.com/gsoultan/hermod/internal/storage"
)

// builder accumulates nodes and edges, each once.
type builder struct {
	nodes map[string]*Node
	edges map[Edge]bool
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
	b := &builder{nodes: map[string]*Node{}, edges: map[Edge]bool{}}

	sources := make(map[string]storage.Source, len(in.Sources))
	for _, s := range in.Sources {
		sources[s.ID] = s
	}
	sinks := make(map[string]storage.Sink, len(in.Sinks))
	for _, s := range in.Sinks {
		sinks[s.ID] = s
	}

	// srcAssets and snkAssets are the assets of a record, its connector
	// asset when none can be derived.
	srcAssets := func(s storage.Source) ([]asset, Ref) {
		ref := Ref{Kind: "source", ID: s.ID, Name: s.Name, Type: s.Type}
		as := sourceAssets(s, sources)
		if len(as) == 0 {
			as = []asset{connector(s.Type, "source", s.ID, s.Name)}
		}
		return as, ref
	}
	snkAssets := func(s storage.Sink) ([]asset, Ref) {
		ref := Ref{Kind: "sink", ID: s.ID, Name: s.Name, Type: s.Type}
		as := sinkAssets(s, sinks, map[string]bool{})
		if len(as) == 0 && s.Type != "failover" && s.Type != "txgroup" {
			as = []asset{connector(s.Type, "sink", s.ID, s.Name)}
		}
		return as, ref
	}

	for _, wf := range in.Workflows {
		wfID := WorkflowNodeID(wf.ID)
		active := wf.Active
		b.nodes[wfID] = &Node{ID: wfID, Kind: KindWorkflow, Label: cmp.Or(wf.Name, wf.ID), Resolved: true, VHost: wf.VHost, Active: &active}
		reader := nodeReader{vhost: wf.VHost, sources: sources}

		for _, n := range wf.Nodes {
			switch n.Type {
			case "source":
				if s, ok := sources[n.RefID]; ok {
					as, ref := srcAssets(s)
					for _, a := range as {
						id := b.addAsset(a, &ref)
						b.addEdge(Edge{From: id, To: wfID, Relation: RelReads, WorkflowID: wf.ID, NodeID: n.ID})
					}
				}
			case "sink":
				if s, ok := sinks[n.RefID]; ok {
					as, ref := snkAssets(s)
					for _, a := range as {
						id := b.addAsset(a, &ref)
						b.addEdge(Edge{From: wfID, To: id, Relation: RelWrites, WorkflowID: wf.ID, NodeID: n.ID})
					}
				}
			default:
				for _, io := range reader.node(n) {
					id := b.addAsset(io.asset, nil)
					e := Edge{From: id, To: wfID, Relation: io.relation, WorkflowID: wf.ID, NodeID: n.ID}
					if io.out {
						e.From, e.To = wfID, id
					}
					b.addEdge(e)
				}
			}
		}

		if s, ok := sinks[wf.DeadLetterSinkID]; ok && wf.DeadLetterSinkID != "" {
			as, ref := snkAssets(s)
			for _, a := range as {
				id := b.addAsset(a, &ref)
				b.addEdge(Edge{From: wfID, To: id, Relation: RelDeadLetter, WorkflowID: wf.ID})
			}
		}
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
