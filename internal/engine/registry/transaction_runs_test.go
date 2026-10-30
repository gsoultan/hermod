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
	"encoding/json"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/sink/txgroup"
)

// A workflow's transactions and their runs, as the run view reads them.

func TestWorkflowTransactionsListsEachTransactionWithItsRunsAndLog(t *testing.T) {
	r := registryWithSinks(map[string]storage.Sink{
		"sink-pg": {ID: "sink-pg", Type: "postgres"},
		"sink-my": {ID: "sink-my", Type: "mysql"},
		"plain":   {ID: "plain", Type: "kafka"},
	})
	wf := storage.Workflow{
		ID: "wf",
		Nodes: []storage.WorkflowNode{
			{ID: "S", Type: "source", RefID: "src"},
			sinkNode("PG", "sink-pg", "order"),
			sinkNode("K", "plain", ""),
			sinkNode("MY", "sink-my", "order"),
		},
		Edges: edges("S", "PG", "S", "K", "S", "MY"),
	}
	wf.Nodes[3].Config[nodeEveryMemberKey] = true

	ss := r.stateStore.(*fakeStateStore)
	runs, _ := json.Marshal(txgroup.Stats{
		Committed: 7, RolledBack: 1, Refused: 2,
		Recent: []txgroup.Run{{At: time.Unix(100, 0), Outcome: txgroup.OutcomeCommitted}},
	})
	ss.kv["twopc-runs/wf/order"] = runs
	ss.kv["twopc-index/wf/order"] = []byte(`["twopc/wf/order/tx-1"]`)
	ss.kv["twopc/wf/order/tx-1"] = []byte(`{"tx_id":"tx-1","state":"committing","order":["PG","MY"],"tx_ids":{"MY":"x"}}`)

	got, err := r.WorkflowTransactions(context.Background(), wf)
	if err != nil {
		t.Fatalf("WorkflowTransactions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d transactions, want the one named order: %+v", len(got), got)
	}
	tx := got[0]
	if tx.Name != "order" || tx.Kind != "commit_together" || !tx.RequireEveryMember {
		t.Errorf("transaction = %+v, want order, commit_together, every member required", tx)
	}
	if len(tx.Members) != 2 || tx.Members[0].ID != "PG" || tx.Members[0].Name != "DB PG" ||
		tx.Members[0].SinkType != "postgres" || tx.Members[1].SinkType != "mysql" {
		t.Errorf("members = %+v", tx.Members)
	}
	if tx.Stats.Committed != 7 || tx.Stats.Refused != 2 || len(tx.Stats.Recent) != 1 {
		t.Errorf("stats = %+v, want what the group recorded", tx.Stats)
	}
	if len(tx.Pending) != 1 || tx.Pending[0].State != "committing" || len(tx.Pending[0].Participants) != 1 ||
		tx.Pending[0].Participants[0] != "MY" {
		t.Errorf("pending = %+v, want the commit still owed to MY", tx.Pending)
	}
	if tx.Problem != "" {
		t.Errorf("problem %q", tx.Problem)
	}
}

func TestWorkflowTransactionsIncludesTransactionGroupNodes(t *testing.T) {
	r := registryWithSinks(map[string]storage.Sink{
		"grp": {ID: "grp", Type: "txgroup", Config: map[string]string{"members": "a,b"}},
		"a":   {ID: "a", Name: "Orders DB", Type: "postgres"},
		"b":   {ID: "b", Type: "mysql"},
	})
	wf := storage.Workflow{
		ID:    "wf",
		Nodes: []storage.WorkflowNode{{ID: "S", Type: "source"}, sinkNode("G", "grp", "")},
		Edges: edges("S", "G"),
	}

	got, err := r.WorkflowTransactions(context.Background(), wf)
	if err != nil || len(got) != 1 {
		t.Fatalf("WorkflowTransactions = %+v, %v; want the group", got, err)
	}
	tx := got[0]
	if tx.Kind != "group" || tx.NodeID != "G" || tx.Name != "DB G" || tx.RequireEveryMember {
		t.Errorf("transaction = %+v", tx)
	}
	if len(tx.Members) != 2 || tx.Members[0].Name != "Orders DB" || tx.Members[1].Name != "b" {
		t.Errorf("members = %+v, want a by its name and b by its ID", tx.Members)
	}
	if tx.Stats.Recent == nil || tx.Pending == nil {
		t.Errorf("a transaction that never ran should list no runs, not null: %+v", tx)
	}
}

func TestWorkflowTransactionsWithoutAStateStoreSaysSo(t *testing.T) {
	r := registryWithSinks(map[string]storage.Sink{"x": {ID: "x", Type: "postgres"}, "y": {ID: "y", Type: "mysql"}})
	r.stateStore = nil
	wf := storage.Workflow{
		ID:    "wf",
		Nodes: []storage.WorkflowNode{{ID: "S", Type: "source"}, sinkNode("X", "x", "t"), sinkNode("Y", "y", "t")},
		Edges: edges("S", "X", "S", "Y"),
	}
	got, err := r.WorkflowTransactions(context.Background(), wf)
	if err != nil || len(got) != 1 || got[0].Problem == "" {
		t.Errorf("WorkflowTransactions = %+v, %v; want the transaction with the reason its runs are unknown", got, err)
	}
}

func TestEveryDatabaseMustGetARowIsOnWhenAnyMemberSaysSo(t *testing.T) {
	cases := []struct {
		name   string
		values []any
		want   bool
	}{
		{"unset", []any{nil, nil}, false},
		{"one member", []any{nil, true}, true},
		{"as text", []any{"true", nil}, true},
		{"off", []any{false, "false"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var nodes []storage.WorkflowNode
			for i, v := range c.values {
				n := sinkNode(string(rune('A'+i)), "s", "t")
				if v != nil {
					n.Config[nodeEveryMemberKey] = v
				}
				nodes = append(nodes, n)
			}
			if got := requiresEveryMember(nodes); got != c.want {
				t.Errorf("requiresEveryMember = %v, want %v", got, c.want)
			}
		})
	}
}
