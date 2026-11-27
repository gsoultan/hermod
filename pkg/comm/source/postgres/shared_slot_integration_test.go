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
	"fmt"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// Many workflows on one source database used to mean one replication slot and
// one walsender each. PostgreSQL allows ten of either by default, and every
// slot decodes the whole WAL again, so a thousand CDC workflows on one database
// cannot all stream. Workflows that name the same slot share one reader: each
// gets every change on its tables.
//
// Before the shared reader, the second source found the slot active for the
// first one's walsender and backed off for good, so it never got a message.
func TestTwoCDCWorkflowsOnOneSlotBothReceiveChanges(t *testing.T) {
	first := newCDCFixture(t)
	ctx := t.Context()
	first.start(t, ctx, false)

	second := &cdcFixture{dsn: first.dsn, db: first.db, table: first.table, slot: first.slot, pub: first.pub}
	second.start(t, ctx, false)
	t.Cleanup(second.stop)

	mustExec(t, first.db, fmt.Sprintf(`INSERT INTO %s (name) VALUES ('shared')`, first.table))

	for name, f := range map[string]*cdcFixture{"first": first, "second": second} {
		_, ok := f.collectUntil(t, 20*time.Second, func(m hermod.Message) bool {
			return m.Operation() == hermod.OpCreate
		})
		if !ok {
			t.Errorf("the %s workflow on the shared slot got no insert; %s", name, f.why())
		}
	}
}

// Workflows on one slot that follow different tables each get their own
// table's changes. Before the shared reader, each set the publication to its
// own table list on start, so whichever started last decided what the other
// one could see.
func TestCDCWorkflowsOnOneSlotGetOnlyTheirOwnTables(t *testing.T) {
	first := newCDCFixture(t)
	ctx := t.Context()
	other := first.table + "_b"
	mustExec(t, first.db, "DROP TABLE IF EXISTS "+other)
	mustExec(t, first.db, fmt.Sprintf(`CREATE TABLE %s (id SERIAL PRIMARY KEY, name TEXT NOT NULL)`, other))
	t.Cleanup(func() { mustExec(t, first.db, "DROP TABLE IF EXISTS "+other) })
	first.start(t, ctx, false)

	second := &cdcFixture{dsn: first.dsn, db: first.db, table: other, slot: first.slot, pub: first.pub}
	second.start(t, ctx, false)
	t.Cleanup(second.stop)

	mustExec(t, first.db, fmt.Sprintf(`INSERT INTO %s (name) VALUES ('a')`, first.table))
	mustExec(t, first.db, fmt.Sprintf(`INSERT INTO %s (name) VALUES ('b')`, other))

	for _, f := range []*cdcFixture{first, second} {
		got, ok := f.collectUntil(t, 20*time.Second, func(m hermod.Message) bool {
			return m.Operation() == hermod.OpCreate
		})
		if !ok {
			t.Fatalf("the workflow on %s got no insert; %s", f.table, f.why())
		}
		for _, m := range got {
			if m.Table() != f.table {
				t.Errorf("the workflow on %s got a change on %s", f.table, m.Table())
			}
		}
	}
}
