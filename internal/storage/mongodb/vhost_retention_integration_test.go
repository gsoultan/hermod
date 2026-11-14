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
// +build integration

package mongodb

import (
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// A vhost's retention on a live MongoDB, as internal/storage/sql covers it for
// the SQL store: each purge takes what its vhost or workflow asked for and
// nothing else.
func TestMongoVHostRetentionPurges(t *testing.T) {
	s, _ := newTraceMongo(t)
	ms := s.(*mongoStorage)
	ctx := t.Context()
	old := time.Now().Add(-200 * 24 * time.Hour)

	for _, row := range []storage.AuditLog{
		{ID: "sales-old", Timestamp: old, VHost: "sales"},
		{ID: "sales-new", Timestamp: time.Now(), VHost: "sales"},
		{ID: "ops-old", Timestamp: old, VHost: "ops"},
		{ID: "system-old", Timestamp: old},
	} {
		if err := s.CreateAuditLog(ctx, row); err != nil {
			t.Fatalf("CreateAuditLog(%s): %v", row.ID, err)
		}
	}
	if err := ms.PurgeAuditLogsForVHost(ctx, "sales", time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("PurgeAuditLogsForVHost: %v", err)
	}
	rows, _, err := s.ListAuditLogs(ctx, storage.AuditFilter{})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	left := map[string]string{}
	for _, r := range rows {
		left[r.ID] = r.VHost
	}
	if _, gone := left["sales-old"]; gone || len(left) != 3 {
		t.Errorf("audit rows left = %v, want sales-new, ops-old and system-old", left)
	}
	if left["sales-new"] != "sales" {
		t.Errorf("sales-new reads back in vhost %q, want sales", left["sales-new"])
	}

	for _, wf := range []string{"wf-a", "wf-b"} {
		for v := 1; v <= 5; v++ {
			if err := s.CreateWorkflowVersion(ctx, storage.WorkflowVersion{
				ID: fmt.Sprintf("%s-%d", wf, v), WorkflowID: wf, Version: v, CreatedAt: time.Now(),
			}); err != nil {
				t.Fatalf("CreateWorkflowVersion: %v", err)
			}
		}
	}
	if err := ms.PurgeWorkflowVersions(ctx, "wf-a", 2); err != nil {
		t.Fatalf("PurgeWorkflowVersions: %v", err)
	}
	if err := ms.PurgeWorkflowVersions(ctx, "wf-b", 50); err != nil {
		t.Fatalf("PurgeWorkflowVersions: %v", err)
	}
	for wf, want := range map[string]int64{"wf-a": 2, "wf-b": 5} {
		n, err := ms.db.Collection("workflow_versions").CountDocuments(ctx, ofWorkflow(wf))
		if err != nil || n != want {
			t.Errorf("%s has %d versions (err %v), want %d", wf, n, err, want)
		}
	}
	for _, v := range []int{4, 5} {
		if _, err := s.GetWorkflowVersion(ctx, "wf-a", v); err != nil {
			t.Errorf("wf-a version %d is gone, and the newest two must stay: %v", v, err)
		}
	}

	for _, a := range []storage.Approval{
		{ID: "decided-old", WorkflowID: "wf-a"},
		{ID: "decided-new", WorkflowID: "wf-a"},
		{ID: "waiting-old", WorkflowID: "wf-a"},
		{ID: "other-old", WorkflowID: "wf-b"},
	} {
		a.NodeID, a.MessageID, a.Status, a.CreatedAt = "n", a.ID, "pending", old
		if err := s.CreateApproval(ctx, a); err != nil {
			t.Fatalf("CreateApproval(%s): %v", a.ID, err)
		}
	}
	for _, id := range []string{"decided-old", "decided-new", "other-old"} {
		if err := s.UpdateApprovalStatus(ctx, id, "approved", "admin", "", nil); err != nil {
			t.Fatalf("UpdateApprovalStatus(%s): %v", id, err)
		}
	}
	if _, err := ms.db.Collection("approvals").UpdateMany(ctx,
		map[string]any{"id": map[string]any{"$in": []string{"decided-old", "other-old"}}},
		map[string]any{"$set": map[string]any{"processed_at": old}}); err != nil {
		t.Fatalf("ageing decisions: %v", err)
	}
	if err := ms.PurgeDecidedApprovals(ctx, "wf-a", time.Now().Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("PurgeDecidedApprovals: %v", err)
	}
	for id, want := range map[string]bool{"decided-old": false, "decided-new": true, "waiting-old": true, "other-old": true} {
		_, err := s.GetApproval(ctx, id)
		if got := err == nil; got != want {
			t.Errorf("approval %s present = %v, want %v (err %v)", id, got, want, err)
		}
	}
}
