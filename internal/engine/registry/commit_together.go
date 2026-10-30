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
	"context"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/sink/txgroup"
)

// Sink nodes that commit together.
//
// A sink node may name a transaction in its config ("transaction": "orders").
// Every sink node of the workflow that names the same transaction is a member
// of one two-phase group: the engine sees one sink, drives it through one
// writer, and the rows one event produced for the members — tagged by the
// traversal with the node each came through (hermod.MetaSinkNode) — reach their
// databases in one transaction. This is how a workflow puts different rows in
// different databases atomically; the stand-alone Transaction Group node sends
// the same rows to every member instead.
//
// The member ID is the node ID. It has to be stable across restarts, because
// the transaction log names members by it when a crash leaves a transaction in
// doubt, and a sink record can back two nodes of one workflow.

// nodeTransactionKey is the sink node config key that names the transaction.
const nodeTransactionKey = "transaction"

// nodeTransaction is the transaction a sink node commits in, or "" for none.
func nodeTransaction(node storage.WorkflowNode) string {
	name, _ := node.Config[nodeTransactionKey].(string)
	return strings.TrimSpace(name)
}

// sinkPlan is one engine sink the workflow needs: a single sink node, or the
// sink nodes that commit together in one named transaction.
type sinkPlan struct {
	transaction string
	nodes       []storage.WorkflowNode
}

// planWorkflowSinks walks the graph from its sources, as the engine will, and
// says which engine sinks the reachable sink nodes make. Plain nodes are one
// sink each; the nodes of a transaction are one sink together, placed where its
// first member was reached. A transaction with one member is refused: a single
// database already commits alone, so a lone member is a node that was meant to
// join and did not.
func (r *Registry) planWorkflowSinks(wf storage.Workflow) ([]sinkPlan, error) {
	plans := groupSinkNodes(reachableSinkNodes(wf))
	for _, p := range plans {
		if p.transaction != "" && len(p.nodes) < 2 {
			return nil, fmt.Errorf("transaction %q has one member, sink node %s; a database already commits "+
				"its own rows alone, so add another sink node to the transaction or turn off "+
				"\"Commit together\" on this node", p.transaction, r.getNodeName(p.nodes[0]))
		}
	}
	return plans, nil
}

// reachableSinkNodes lists the sink nodes a message can reach from a source,
// breadth first, in the order the engine's own sink discovery visits them.
func reachableSinkNodes(wf storage.Workflow) []storage.WorkflowNode {
	adj := make(map[string][]string)
	for _, edge := range wf.Edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
	}

	var queue []string
	visited := make(map[string]bool)
	for _, node := range wf.Nodes {
		if node.Type == "source" {
			queue = append(queue, node.ID)
			visited[node.ID] = true
		}
	}

	var sinks []storage.WorkflowNode
	for len(queue) > 0 {
		currID := queue[0]
		queue = queue[1:]
		node := findNodeByID(wf.Nodes, currID)
		if node == nil {
			continue
		}
		if node.Type == "sink" {
			sinks = append(sinks, *node)
		}
		for _, nextID := range adj[currID] {
			if !visited[nextID] {
				visited[nextID] = true
				queue = append(queue, nextID)
			}
		}
	}
	return sinks
}

// groupSinkNodes keeps plain sink nodes one to a plan and gathers the nodes of
// each transaction into one, at the position of its first member.
func groupSinkNodes(nodes []storage.WorkflowNode) []sinkPlan {
	var plans []sinkPlan
	groupAt := make(map[string]int)
	for _, node := range nodes {
		name := nodeTransaction(node)
		if name == "" {
			plans = append(plans, sinkPlan{nodes: []storage.WorkflowNode{node}})
			continue
		}
		i, ok := groupAt[name]
		if !ok {
			i = len(plans)
			groupAt[name] = i
			plans = append(plans, sinkPlan{transaction: name})
		}
		plans[i].nodes = append(plans[i].nodes, node)
	}
	return plans
}

// checkCommitTogether is what saving a workflow checks about its transactions:
// every transaction has at least two connected members, and every member is a
// sink whose type can take part in two-phase commit. Running would refuse the
// same things; checking at save puts the mistake in the editor instead of the
// run log.
func (r *Registry) checkCommitTogether(ctx context.Context, wf storage.Workflow) error {
	plans, err := r.planWorkflowSinks(wf)
	if err != nil {
		return err
	}
	if r.store() == nil {
		return nil
	}
	for _, p := range plans {
		if p.transaction == "" {
			continue
		}
		for _, node := range p.nodes {
			if err := r.checkCommitTogetherMember(ctx, p.transaction, node); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkCommitTogetherMember refuses a member node whose sink cannot prepare a
// transaction, naming the node rather than the record: the node is what the
// person sees on the canvas.
func (r *Registry) checkCommitTogetherMember(ctx context.Context, transaction string, node storage.WorkflowNode) error {
	dbSnk, err := r.GetSinkConfig(ctx, node.RefID)
	if err != nil {
		return fmt.Errorf("transaction %q: sink node %s: failed to get sink %s: %w",
			transaction, r.getNodeName(node), node.RefID, err)
	}
	if !factory.SupportsTwoPhaseCommit(dbSnk.Type) {
		return fmt.Errorf("transaction %q: sink node %s is a %q sink, which cannot commit together with "+
			"other databases; only these can: %s",
			transaction, r.getNodeName(node), dbSnk.Type, strings.Join(factory.TwoPhaseCapableSinkTypes(), ", "))
	}
	return nil
}

// commitTogetherSinkID scopes a transaction's log and names its sink to the
// engine: one workflow, one transaction name, one log.
func commitTogetherSinkID(wf storage.Workflow, transaction string) string {
	return wf.ID + "/" + transaction
}

// buildCommitTogether builds the group for one transaction of a workflow, the
// way createTxGroupSink builds a stand-alone group: every member checked, the
// coordinator over the registry's durable log, then preflight, recovery and
// the reaper. The members are the nodes.
func (r *Registry) buildCommitTogether(ctx context.Context, wf storage.Workflow, plan sinkPlan) (hermod.Sink, factory.SinkConfig, error) {
	label := fmt.Sprintf("transaction %q", plan.transaction)
	cfg := factory.SinkConfig{
		ID:     commitTogetherSinkID(wf, plan.transaction),
		Type:   "txgroup",
		VHost:  wf.VHost,
		Config: map[string]string{nodeTransactionKey: plan.transaction},
	}

	// Every member is checked before any is built, so the refusal names the
	// node at fault and nothing has been connected yet.
	refs := make([]string, 0, len(plan.nodes))
	for _, node := range plan.nodes {
		if err := r.checkCommitTogetherMember(ctx, plan.transaction, node); err != nil {
			return nil, cfg, err
		}
		refs = append(refs, node.RefID)
		if age, _ := node.Config["max_prepared_age"].(string); strings.TrimSpace(age) != "" && cfg.Config["max_prepared_age"] == "" {
			cfg.Config["max_prepared_age"] = strings.TrimSpace(age)
		}
	}
	cfg.Config["members"] = strings.Join(refs, ",")

	coordinator, err := r.newTxCoordinator(cfg)
	if err != nil {
		return nil, cfg, fmt.Errorf("%s: %w", label, err)
	}

	members := make([]txgroup.Member, 0, len(plan.nodes))
	for _, node := range plan.nodes {
		snk, err := r.resolveAndCreateTxGroupMember(ctx, node.RefID)
		if err != nil {
			closeMembers(members)
			return nil, cfg, fmt.Errorf("%s: cannot build sink node %s: %w", label, r.getNodeName(node), err)
		}
		members = append(members, txgroup.Member{ID: node.ID, Sink: snk})
	}

	group, err := r.startTxGroup(ctx, label, members, coordinator)
	if err != nil {
		return nil, cfg, err
	}
	return group, cfg, nil
}
