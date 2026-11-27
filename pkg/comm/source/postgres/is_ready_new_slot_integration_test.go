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

package postgres

import (
	"testing"
	"time"
)

// A CDC source is ready before its replication slot exists: the slot is made
// on the first Read. (The publication is checked, so the test makes it.) The readiness query read the slot's "active" column
// into a bool, and with no slot that column is NULL, so the check failed
// with "cannot scan NULL into *bool" for every new CDC source.
func TestIsReadyBeforeTheSlotExists(t *testing.T) {
	f := newCDCFixture(t)
	mustExec(t, f.db, "CREATE PUBLICATION "+f.pub+" FOR TABLE "+f.table)
	src := NewPostgresSource(f.dsn, f.slot, f.pub, []string{f.table}, true, "", time.Second)
	t.Cleanup(func() { _ = src.Close() })

	if err := src.IsReady(t.Context()); err != nil {
		t.Fatalf("IsReady with no slot yet = %v, want nil", err)
	}
}
