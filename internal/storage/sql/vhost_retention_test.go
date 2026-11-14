// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// audit_logs, workflow_versions and approvals were the three tables nothing
// ever purged. A vhost now sets how much of each it keeps, so the purges have
// to be able to act on one vhost, or one workflow, and leave the rest alone.

func newRetentionStorage(t *testing.T) *sqlStorage {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	st, ok := NewSQLStorage(db, "sqlite").(*sqlStorage)
	if !ok {
		t.Fatal("NewSQLStorage did not return *sqlStorage")
	}
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	return st
}

func TestAnAuditRowKeepsTheVHostItIsAbout(t *testing.T) {
	st := newRetentionStorage(t)
	ctx := context.Background()
	if err := st.CreateAuditLog(ctx, storage.AuditLog{Action: "UPDATE", EntityType: "workflow", EntityID: "wf-1", VHost: "sales"}); err != nil {
		t.Fatalf("CreateAuditLog: %v", err)
	}
	rows, _, err := st.ListAuditLogs(ctx, storage.AuditFilter{})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	if len(rows) != 1 || rows[0].VHost != "sales" {
		t.Fatalf("audit rows = %+v, want one row in vhost sales", rows)
	}
}

func TestPurgingOneVHostsAuditLogLeavesEveryOtherRow(t *testing.T) {
	st := newRetentionStorage(t)
	ctx := context.Background()
	old := time.Now().Add(-100 * 24 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	for _, row := range []storage.AuditLog{
		{ID: "sales-old", Timestamp: old, VHost: "sales"},
		{ID: "sales-new", Timestamp: recent, VHost: "sales"},
		{ID: "ops-old", Timestamp: old, VHost: "ops"},
		{ID: "system-old", Timestamp: old},
	} {
		row.Action, row.EntityType, row.EntityID = "UPDATE", "workflow", "wf"
		if err := st.CreateAuditLog(ctx, row); err != nil {
			t.Fatalf("CreateAuditLog(%s): %v", row.ID, err)
		}
	}

	if err := st.PurgeAuditLogsForVHost(ctx, "sales", time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("PurgeAuditLogsForVHost: %v", err)
	}

	rows, _, err := st.ListAuditLogs(ctx, storage.AuditFilter{})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	left := map[string]bool{}
	for _, r := range rows {
		left[r.ID] = true
	}
	for _, id := range []string{"sales-new", "ops-old", "system-old"} {
		if !left[id] {
			t.Errorf("%s was purged; only sales rows older than the cutoff may go", id)
		}
	}
	if left["sales-old"] {
		t.Error("sales-old is still there after the sales vhost's audit log was purged")
	}
}

func TestPurgingWorkflowVersionsKeepsTheNewest(t *testing.T) {
	st := newRetentionStorage(t)
	ctx := context.Background()
	for _, wf := range []string{"wf-a", "wf-b"} {
		for v := 1; v <= 5; v++ {
			if err := st.CreateWorkflowVersion(ctx, storage.WorkflowVersion{
				ID: fmt.Sprintf("%s-%d", wf, v), WorkflowID: wf, Version: v, CreatedAt: time.Now(),
			}); err != nil {
				t.Fatalf("CreateWorkflowVersion: %v", err)
			}
		}
	}

	if err := st.PurgeWorkflowVersions(ctx, "wf-a", 2); err != nil {
		t.Fatalf("PurgeWorkflowVersions: %v", err)
	}

	a, err := st.ListWorkflowVersions(ctx, "wf-a")
	if err != nil {
		t.Fatalf("ListWorkflowVersions: %v", err)
	}
	if len(a) != 2 || a[0].Version != 5 || a[1].Version != 4 {
		t.Errorf("wf-a versions = %+v, want versions 5 and 4", a)
	}
	b, _ := st.ListWorkflowVersions(ctx, "wf-b")
	if len(b) != 5 {
		t.Errorf("wf-b has %d versions, want all 5: another workflow's limit must not touch it", len(b))
	}

	// Fewer versions than the limit, and a limit of nothing, delete nothing.
	if err := st.PurgeWorkflowVersions(ctx, "wf-b", 50); err != nil {
		t.Fatalf("PurgeWorkflowVersions: %v", err)
	}
	if err := st.PurgeWorkflowVersions(ctx, "wf-b", 0); err != nil {
		t.Fatalf("PurgeWorkflowVersions: %v", err)
	}
	if b, _ = st.ListWorkflowVersions(ctx, "wf-b"); len(b) != 5 {
		t.Errorf("wf-b has %d versions, want all 5", len(b))
	}
}

func TestPurgingApprovalsNeverTakesOneStillWaiting(t *testing.T) {
	st := newRetentionStorage(t)
	ctx := context.Background()
	old := time.Now().Add(-200 * 24 * time.Hour)
	for _, a := range []storage.Approval{
		{ID: "decided-old", WorkflowID: "wf-a", CreatedAt: old},
		{ID: "decided-new", WorkflowID: "wf-a", CreatedAt: old},
		{ID: "waiting-old", WorkflowID: "wf-a", CreatedAt: old},
		{ID: "other-old", WorkflowID: "wf-b", CreatedAt: old},
	} {
		a.NodeID, a.MessageID, a.Status = "n", a.ID, "pending"
		if err := st.CreateApproval(ctx, a); err != nil {
			t.Fatalf("CreateApproval(%s): %v", a.ID, err)
		}
	}
	for _, id := range []string{"decided-old", "decided-new", "other-old"} {
		if err := st.UpdateApprovalStatus(ctx, id, "approved", "admin", "", nil); err != nil {
			t.Fatalf("UpdateApprovalStatus(%s): %v", id, err)
		}
	}
	// UpdateApprovalStatus stamps now; age two of the decisions.
	if _, err := st.db.ExecContext(ctx, "UPDATE approvals SET processed_at = ? WHERE id IN ('decided-old','other-old')", old); err != nil {
		t.Fatalf("ageing decisions: %v", err)
	}

	if err := st.PurgeDecidedApprovals(ctx, "wf-a", time.Now().Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("PurgeDecidedApprovals: %v", err)
	}

	for id, want := range map[string]bool{"decided-old": false, "decided-new": true, "waiting-old": true, "other-old": true} {
		_, err := st.GetApproval(ctx, id)
		if got := err == nil; got != want {
			t.Errorf("approval %s present = %v, want %v (err %v)", id, got, want, err)
		}
	}
}

// Rows written before audit_logs had a vhost column are the ones a deployment
// already has too many of. Starting up gives each the vhost of the workflow,
// source or sink it is about, so a vhost's retention reaches them; a row about
// a user, or about something since deleted, stays in no vhost and is kept.
func TestStartingUpGivesOlderAuditRowsTheirVHost(t *testing.T) {
	st := newRetentionStorage(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateWorkflow(ctx, storage.Workflow{ID: "wf-sales", Name: "orders", VHost: "sales"}))
	must(st.CreateWorkflow(ctx, storage.Workflow{ID: "wf-shared", Name: "shared"}))
	must(st.CreateSource(ctx, storage.Source{ID: "src-ops", Name: "pg", Type: "postgres", VHost: "ops"}))
	must(st.CreateSink(ctx, storage.Sink{ID: "snk-ops", Name: "kafka", Type: "kafka", VHost: "ops"}))
	for id, entity := range map[string][2]string{
		"a-wf":      {"workflow", "wf-sales"},
		"a-shared":  {"workflow", "wf-shared"},
		"a-src":     {"source", "src-ops"},
		"a-snk":     {"sink", "snk-ops"},
		"a-deleted": {"workflow", "wf-gone"},
		"a-user":    {"user", "wf-sales"},
	} {
		must(st.CreateAuditLog(ctx, storage.AuditLog{ID: id, Action: "UPDATE", EntityType: entity[0], EntityID: entity[1]}))
	}
	must(st.CreateAuditLog(ctx, storage.AuditLog{ID: "a-stamped", Action: "UPDATE", EntityType: "workflow", EntityID: "wf-sales", VHost: "archive"}))

	must(st.Init(ctx))

	rows, _, err := st.ListAuditLogs(ctx, storage.AuditFilter{})
	must(err)
	got := map[string]string{}
	for _, r := range rows {
		got[r.ID] = r.VHost
	}
	for id, want := range map[string]string{
		"a-wf": "sales", "a-shared": "default", "a-src": "ops", "a-snk": "ops",
		"a-deleted": "", "a-user": "", "a-stamped": "archive",
	} {
		if got[id] != want {
			t.Errorf("audit row %s is in vhost %q, want %q", id, got[id], want)
		}
	}
}

// The same purges against real PostgreSQL, where placeholders are rewritten
// and a timestamp is compared as one rather than as text.
func TestVHostRetentionPurgesOnPostgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to enable")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := NewSQLStorage(db, "pgx").(*sqlStorage)
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.Init(ctx))

	// Unique per run, as the database outlives the test.
	p := fmt.Sprintf("ret%d-", time.Now().UnixNano())
	sales, ops, wf := p+"sales", p+"ops", p+"wf"
	old := time.Now().Add(-200 * 24 * time.Hour)

	must(st.CreateWorkflow(ctx, storage.Workflow{ID: wf, Name: wf, VHost: sales}))
	for id, row := range map[string]storage.AuditLog{
		"sales-old": {Timestamp: old, VHost: sales},
		"sales-new": {Timestamp: time.Now(), VHost: sales},
		"ops-old":   {Timestamp: old, VHost: ops},
		"legacy":    {Timestamp: time.Now(), EntityType: "workflow", EntityID: wf},
	} {
		row.ID, row.Action = p+id, "UPDATE"
		if row.EntityType == "" {
			row.EntityType, row.EntityID = "user", "u"
		}
		must(st.CreateAuditLog(ctx, row))
	}
	must(st.Init(ctx)) // the backfill: "legacy" takes its workflow's vhost
	must(st.PurgeAuditLogsForVHost(ctx, sales, time.Now().Add(-30*24*time.Hour)))

	inVHost := func(vhost string) map[string]bool {
		rows, _, err := st.ListAuditLogs(ctx, storage.AuditFilter{CommonFilter: storage.CommonFilter{VHost: vhost}})
		must(err)
		ids := map[string]bool{}
		for _, r := range rows {
			ids[r.ID] = true
		}
		return ids
	}
	if got := inVHost(sales); len(got) != 2 || !got[p+"sales-new"] || !got[p+"legacy"] {
		t.Errorf("audit rows left in the purged vhost = %v, want sales-new and the backfilled legacy row", got)
	}
	if got := inVHost(ops); !got[p+"ops-old"] {
		t.Errorf("audit rows left in the other vhost = %v, want ops-old untouched", got)
	}

	for v := 1; v <= 5; v++ {
		must(st.CreateWorkflowVersion(ctx, storage.WorkflowVersion{ID: fmt.Sprintf("%sv%d", p, v), WorkflowID: wf, Version: v, CreatedAt: time.Now()}))
	}
	must(st.PurgeWorkflowVersions(ctx, wf, 2))
	versions, err := st.ListWorkflowVersions(ctx, wf)
	must(err)
	if len(versions) != 2 || versions[0].Version != 5 || versions[1].Version != 4 {
		t.Errorf("versions left = %+v, want 5 and 4", versions)
	}

	for _, id := range []string{"decided-old", "decided-new", "waiting"} {
		must(st.CreateApproval(ctx, storage.Approval{ID: p + id, WorkflowID: wf, NodeID: "n", MessageID: id, Status: "pending", CreatedAt: old}))
	}
	must(st.UpdateApprovalStatus(ctx, p+"decided-old", "approved", "admin", "", nil))
	must(st.UpdateApprovalStatus(ctx, p+"decided-new", "rejected", "admin", "", nil))
	_, err = st.exec(ctx, "UPDATE approvals SET processed_at = ? WHERE id = ?", old, p+"decided-old")
	must(err)
	must(st.PurgeDecidedApprovals(ctx, wf, time.Now().Add(-90*24*time.Hour)))
	for id, want := range map[string]bool{"decided-old": false, "decided-new": true, "waiting": true} {
		_, err := st.GetApproval(ctx, p+id)
		if got := err == nil; got != want {
			t.Errorf("approval %s present = %v, want %v (err %v)", id, got, want, err)
		}
	}
}
