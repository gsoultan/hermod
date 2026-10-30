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

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/sink/txgroup"
	"github.com/gsoultan/hermod/pkg/engine/twopc"
)

// The runs of a workflow's transactions.
//
// Each group records its runs in the state store as it writes (txgroup's
// KeepRunsIn) and keeps its log there, so this reads both from the store and
// never from a live group: the answer is the same on every worker, and for a
// workflow that is stopped.

// TransactionMember is one database in a transaction.
type TransactionMember struct {
	// ID is what the group calls the member: the node ID for sink nodes that
	// commit together, the sink ID for a Transaction Group node.
	ID       string `json:"id"`
	Name     string `json:"name"`
	SinkID   string `json:"sink_id"`
	SinkType string `json:"sink_type,omitempty"`
}

// TransactionRuns is one transaction of a workflow and what its runs did.
type TransactionRuns struct {
	Name string `json:"name"`
	// Kind is "commit_together" for sink nodes that name a transaction, or
	// "group" for a Transaction Group node.
	Kind string `json:"kind"`
	// NodeID is the Transaction Group node; empty for commit_together.
	NodeID             string              `json:"node_id,omitempty"`
	RequireEveryMember bool                `json:"require_every_member"`
	Members            []TransactionMember `json:"members"`
	Stats              txgroup.Stats       `json:"stats"`
	// Pending is what the log holds now: transactions in flight or left in
	// doubt for recovery or the reaper.
	Pending []twopc.PendingTx `json:"pending"`
	// Problem says why the runs or the log could not be read, or "".
	Problem string `json:"problem,omitempty"`
}

// WorkflowTransactions lists the transactions of a workflow with their runs:
// every set of sink nodes that commit together, and every Transaction Group
// node, in the order the engine reaches them.
func (r *Registry) WorkflowTransactions(ctx context.Context, wf storage.Workflow) ([]TransactionRuns, error) {
	r.mu.RLock()
	ss := r.stateStore
	r.mu.RUnlock()

	out := []TransactionRuns{}
	for _, plan := range groupSinkNodes(reachableSinkNodes(wf)) {
		var tx TransactionRuns
		var id string
		if plan.transaction != "" {
			tx, id = r.commitTogetherTransaction(ctx, plan), commitTogetherSinkID(wf, plan.transaction)
		} else {
			snk, err := r.GetSinkConfig(ctx, plan.nodes[0].RefID)
			if err != nil || snk.Type != "txgroup" {
				continue
			}
			tx, id = r.groupTransaction(ctx, plan.nodes[0], snk), snk.ID
		}
		readTransactionRuns(ctx, ss, id, &tx)
		out = append(out, tx)
	}
	return out, nil
}

// commitTogetherTransaction describes the sink nodes that name one transaction.
func (r *Registry) commitTogetherTransaction(ctx context.Context, plan sinkPlan) TransactionRuns {
	tx := TransactionRuns{
		Name:               plan.transaction,
		Kind:               "commit_together",
		RequireEveryMember: requiresEveryMember(plan.nodes),
	}
	for _, node := range plan.nodes {
		m := TransactionMember{ID: node.ID, Name: nodeLabel(node), SinkID: node.RefID}
		if snk, err := r.GetSinkConfig(ctx, node.RefID); err == nil {
			m.SinkType = snk.Type
		}
		tx.Members = append(tx.Members, m)
	}
	return tx
}

// groupTransaction describes a Transaction Group node, whose members are the
// sinks its record lists.
func (r *Registry) groupTransaction(ctx context.Context, node storage.WorkflowNode, group storage.Sink) TransactionRuns {
	tx := TransactionRuns{Name: nodeLabel(node), Kind: "group", NodeID: node.ID}
	for _, memberID := range splitMemberIDs(group.Config["members"]) {
		m := TransactionMember{ID: memberID, Name: memberID, SinkID: memberID}
		if ms, err := r.GetSinkConfig(ctx, memberID); err == nil {
			if ms.Name != "" {
				m.Name = ms.Name
			}
			m.SinkType = ms.Type
		}
		tx.Members = append(tx.Members, m)
	}
	return tx
}

// readTransactionRuns fills in a transaction's runs and log from the store, and
// says what it could not read rather than failing the whole list.
func readTransactionRuns(ctx context.Context, ss hermod.StateStore, id string, tx *TransactionRuns) {
	tx.Pending = []twopc.PendingTx{}
	if ss == nil {
		tx.Problem = txGroupLogProblem(nil)
		return
	}
	stats, err := txgroup.ReadRuns(ctx, ss, txGroupRunsKey(id))
	if err != nil {
		tx.Problem = fmt.Sprintf("the record of runs could not be read: %v", err)
	}
	if stats.Members == nil {
		stats.Members = []txgroup.MemberStats{}
	}
	if stats.Recent == nil {
		stats.Recent = []txgroup.Run{}
	}
	tx.Stats = stats
	pending, err := twopc.ReadPending(ctx, ss, txGroupIndexKey(id), id)
	if err != nil {
		tx.Problem = fmt.Sprintf("the transaction log could not be read: %v", err)
		return
	}
	tx.Pending = pending
}
