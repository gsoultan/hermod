// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package engine

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/sink/txsql"
)

type collectingDLQ struct {
	mu  sync.Mutex
	ids []string
}

func (d *collectingDLQ) Write(_ context.Context, m hermod.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ids = append(d.ids, m.ID())
	return nil
}
func (d *collectingDLQ) Ping(context.Context) error { return nil }
func (d *collectingDLQ) Close() error               { return nil }

// The Transactional SQL sink rolls a whole batch back when one message in it
// cannot be written, and says so with hermod.ErrPermanent. That relies on the
// engine then writing the batch one message at a time, so the good messages
// commit and only the bad one is parked. This holds the two to that contract.
func TestTransactionalSQLBatchParksOnlyTheMessageThatCannotBeWritten(t *testing.T) {
	cfg, err := txsql.ParseConfig(map[string]string{"driver": "sqlite", "statements": `[
		{"name":"order","sql":"INSERT INTO orders (ref) VALUES ({{ref}}) RETURNING id"},
		{"name":"lines","forEach":"lines","sql":"INSERT INTO lines (order_id, qty) VALUES ({{order.id}}, {{item.qty}})"}
	]`, "on_unresolved": "null"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "orders.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, ref TEXT NOT NULL);
		CREATE TABLE lines (order_id INTEGER NOT NULL REFERENCES orders(id), qty INTEGER NOT NULL);`); err != nil {
		t.Fatal(err)
	}

	snk, err := txsql.Open(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = snk.Close() })
	ctx := context.Background()

	mk := func(id, ref string, qty any) hermod.Message {
		m := message.AcquireMessage()
		t.Cleanup(m.Release)
		m.SetID(id)
		m.SetData("ref", ref)
		m.SetData("lines", []any{map[string]any{"qty": qty}})
		return m
	}
	msgs := []hermod.Message{mk("m1", "A", 1), mk("m2", "B", nil), mk("m3", "C", 3)}

	dlq := &collectingDLQ{}
	eng := engineWithRetries(t, 3)
	eng.SetDeadLetterSink(dlq)

	if err := eng.writeBatchToSink(ctx, snk, msgs, nil, "orders", -1); err != nil {
		t.Fatalf("writeBatchToSink = %v; the bad message was parked, which is the outcome", err)
	}
	if len(dlq.ids) != 1 || dlq.ids[0] != "m2" {
		t.Errorf("parked %v, want only m2", dlq.ids)
	}
	refs := committedRefs(t, db)
	if len(refs) != 2 || refs[0] != "A" || refs[1] != "C" {
		t.Errorf("committed orders %v, want [A C]", refs)
	}
}

func committedRefs(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT ref FROM orders ORDER BY ref`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var refs []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return refs
}
