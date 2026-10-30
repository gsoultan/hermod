// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

//go:build integration

package registry

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// ---------------------------------------------------------------------------
// Reachability: sink nodes that commit together, built the way a workflow
// builds them.
//
// Branch X builds a row for PostgreSQL and branch Y a row for MySQL, and the
// two sink nodes name the same transaction. The registry must turn them into
// one group whose members are the nodes, so that the row tagged with node PG
// reaches only PostgreSQL, the row tagged with node MY reaches only MySQL, and
// the two commit or roll back as one. Everything starts from stored sink
// records and the workflow graph, as in
// txgroup_mixed_reachability_integration_test.go.
//
//	HERMOD_INTEGRATION=1 POSTGRES_DSN='postgres://...' \
//	MYSQL_DSN='root:root@tcp(127.0.0.1:3307)/hermod_test' \
//	go test -tags=integration -run CommitTogether ./internal/engine/registry/
// ---------------------------------------------------------------------------

// commitTogetherWorkflow is S → PG and S → MY, both sink nodes in transaction t1.
func commitTogetherWorkflow(id string) storage.Workflow {
	return storage.Workflow{
		ID: id,
		Nodes: []storage.WorkflowNode{
			{ID: "S", Type: "source", RefID: "src"},
			sinkNode("PG", "sink-pg", "t1"),
			sinkNode("MY", "sink-my", "t1"),
		},
		Edges: edges("S", "PG", "S", "MY"),
	}
}

// buildCommitTogetherSinks builds the workflow's sinks as StartWorkflow does
// and returns the one group both nodes map to.
func buildCommitTogetherSinks(t *testing.T, sinks map[string]storage.Sink, wf storage.Workflow) hermod.BatchSink {
	t.Helper()
	r := registryWithSinks(sinks)
	built, cfgs, nodeToIndex, err := r.discoverWorkflowSinks(context.Background(), wf, &multiSource{})
	if err != nil {
		t.Fatalf("building the workflow's sinks: %v", err)
	}
	t.Cleanup(func() {
		for _, s := range built {
			_ = s.Close()
		}
	})
	if len(built) != 1 {
		t.Fatalf("two sink nodes in one transaction became %d engine sinks, want 1", len(built))
	}
	if nodeToIndex["PG"] != 0 || nodeToIndex["MY"] != 0 {
		t.Fatalf("sink node → sink index = %v, want both nodes at 0", nodeToIndex)
	}
	if cfgs[0].Type != "txgroup" {
		t.Errorf("the engine was told the sink is a %q, want txgroup", cfgs[0].Type)
	}
	batch, ok := built[0].(hermod.BatchSink)
	if !ok {
		t.Fatalf("the group is %T, which is not a hermod.BatchSink", built[0])
	}
	return batch
}

// rowThrough is a row as the traversal hands it to the engine: it says which
// sink node it came through.
func rowThrough(node, id, val string) hermod.Message {
	m := reachabilityMessage(id, val)
	m.SetMetadata(hermod.MetaSinkNode, node)
	return m
}

func TestCommitTogetherGivesEachDatabaseItsOwnRows(t *testing.T) {
	pgDSN := reachabilityDSN(t)
	p := reachabilityPool(t, pgDSN)
	requirePrepared(t, p)
	myDSN, my := mixedMySQL(t)

	pgTable := makeReachabilityTable(t, p, "ct_pg")
	myTable := makeMySQLTable(t, my, "ct_my", "")

	group := buildCommitTogetherSinks(t, map[string]storage.Sink{
		"sink-pg": storedPostgresSink("sink-pg", pgDSN, pgTable),
		"sink-my": storedMySQLSink("sink-my", myDSN, myTable),
	}, commitTogetherWorkflow("wf-ct"))

	pgBefore := reachabilityPreparedCount(t, p)
	// One event: branch X produced a row for PostgreSQL, branch Y one for MySQL.
	if err := group.WriteBatch(context.Background(), []hermod.Message{
		rowThrough("PG", "x-1", "for postgres"),
		rowThrough("MY", "y-1", "for mysql"),
	}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	if got := reachabilityRowCount(t, p, pgTable); got != 1 {
		t.Errorf("PostgreSQL has %d rows, want only its own one", got)
	}
	var pgVal string
	if err := p.QueryRow(context.Background(), "SELECT val FROM "+pgTable).Scan(&pgVal); err == nil && pgVal != "for postgres" {
		t.Errorf("PostgreSQL got the row %q, want the one built for it", pgVal)
	}
	if got := mysqlRowCount(t, my, myTable); got != 1 {
		t.Errorf("MySQL has %d rows, want only its own one", got)
	}
	var myVal string
	if err := my.QueryRowContext(context.Background(), "SELECT val FROM "+myTable).Scan(&myVal); err == nil && myVal != "for mysql" {
		t.Errorf("MySQL got the row %q, want the one built for it", myVal)
	}
	if got := reachabilityPreparedCount(t, p); got != pgBefore {
		t.Errorf("pg_prepared_xacts went from %d to %d; a transaction is still holding locks", pgBefore, got)
	}
	if left := mysqlInDoubt(t, my); len(left) != 0 {
		t.Errorf("XA RECOVER still lists %v after a clean commit", left)
	}
}

// When MySQL refuses its row, PostgreSQL keeps nothing: the event's rows were
// one transaction.
func TestCommitTogetherRollsBackEveryDatabaseWhenOneRefuses(t *testing.T) {
	pgDSN := reachabilityDSN(t)
	p := reachabilityPool(t, pgDSN)
	requirePrepared(t, p)
	myDSN, my := mixedMySQL(t)

	pgTable := makeReachabilityTable(t, p, "ct_atomic_pg")
	myTable := makeMySQLTable(t, my, "ct_atomic_my", "val = 'nothing writes this'")

	group := buildCommitTogetherSinks(t, map[string]storage.Sink{
		"sink-pg": storedPostgresSink("sink-pg", pgDSN, pgTable),
		"sink-my": storedMySQLSink("sink-my", myDSN, myTable),
	}, commitTogetherWorkflow("wf-ct-atomic"))

	pgBefore := reachabilityPreparedCount(t, p)
	if err := group.WriteBatch(context.Background(), []hermod.Message{
		rowThrough("PG", "x-1", "for postgres"),
		rowThrough("MY", "y-1", "for mysql"),
	}); err == nil {
		t.Fatal("a write MySQL refused reported success")
	}
	if got := reachabilityRowCount(t, p, pgTable); got != 0 {
		t.Errorf("PostgreSQL kept %d rows after MySQL refused; the event was not one transaction", got)
	}
	if got := reachabilityPreparedCount(t, p); got != pgBefore {
		t.Errorf("pg_prepared_xacts went from %d to %d; a failed write left a transaction in doubt", pgBefore, got)
	}
	if left := mysqlInDoubt(t, my); len(left) != 0 {
		t.Errorf("XA RECOVER lists %v after a failed write", left)
	}
}

// Two workflows that both call their transaction "t1" get separate logs, because
// the log is scoped by workflow and transaction name — not by a sink record's
// ID, which these groups do not have.
func TestEachTransactionHasItsOwnLog(t *testing.T) {
	pgDSN := reachabilityDSN(t)
	p := reachabilityPool(t, pgDSN)
	requirePrepared(t, p)
	myDSN, my := mixedMySQL(t)

	pgTable := makeReachabilityTable(t, p, "ct_log_pg")
	myTable := makeMySQLTable(t, my, "ct_log_my", "")
	r := registryWithSinks(map[string]storage.Sink{
		"sink-pg": storedPostgresSink("sink-pg", pgDSN, pgTable),
		"sink-my": storedMySQLSink("sink-my", myDSN, myTable),
	})

	ids := map[string]bool{}
	for _, wfID := range []string{"wf-a", "wf-b"} {
		built, cfgs, _, err := r.discoverWorkflowSinks(context.Background(), commitTogetherWorkflow(wfID), &multiSource{})
		if err != nil {
			t.Fatalf("%s: %v", wfID, err)
		}
		t.Cleanup(func() { _ = built[0].Close() })
		ids[cfgs[0].ID] = true
	}
	if len(ids) != 2 {
		t.Errorf("two workflows' transactions share the sink ID %v, so they would share one transaction log", ids)
	}
}
