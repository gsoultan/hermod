// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package pebble

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

func TestPebblePurgesOneVHostsAuditLogAndLeavesTheRest(t *testing.T) {
	s := newTracePebble(t)
	ctx := context.Background()
	old := time.Now().Add(-100 * 24 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	for _, row := range []storage.AuditLog{
		{ID: "sales-old", Timestamp: old, VHost: "sales"},
		{ID: "sales-new", Timestamp: recent, VHost: "sales"},
		{ID: "ops-old", Timestamp: old, VHost: "ops"},
		{ID: "system-old", Timestamp: old},
	} {
		if err := s.CreateAuditLog(ctx, row); err != nil {
			t.Fatalf("CreateAuditLog(%s): %v", row.ID, err)
		}
	}

	purger, ok := s.(storage.VHostAuditPurger)
	if !ok {
		t.Fatal("the pebble log store cannot purge one vhost's audit log")
	}
	if err := purger.PurgeAuditLogsForVHost(ctx, "sales", time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("PurgeAuditLogsForVHost: %v", err)
	}

	rows, _, err := s.ListAuditLogs(ctx, storage.AuditFilter{})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	left := map[string]bool{}
	for _, r := range rows {
		left[r.ID] = true
	}
	for id, want := range map[string]bool{"sales-old": false, "sales-new": true, "ops-old": true, "system-old": true} {
		if left[id] != want {
			t.Errorf("audit row %s present = %v, want %v", id, left[id], want)
		}
	}
}
