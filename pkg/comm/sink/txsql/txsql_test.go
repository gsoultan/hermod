// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package txsql

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

const sqliteSchema = `
CREATE TABLE customers (id TEXT PRIMARY KEY, last_order_id INTEGER);
CREATE TABLE orders (id INTEGER PRIMARY KEY AUTOINCREMENT, external_id TEXT UNIQUE, customer_id TEXT NOT NULL, total INTEGER);
CREATE TABLE order_items (order_id INTEGER NOT NULL REFERENCES orders(id), sku TEXT NOT NULL, qty INTEGER NOT NULL, PRIMARY KEY (order_id, sku));
INSERT INTO customers (id) VALUES ('C-9');
`

// newSQLiteSink opens a fresh database file with the order schema and a sink
// over it built from statements.
func newSQLiteSink(t *testing.T, statements string, extra map[string]string) (*Sink, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tx.db")
	conf := map[string]string{"driver": "sqlite", "statements": statements}
	for k, v := range extra {
		conf[k] = v
	}
	cfg, err := ParseConfig(conf)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	snk, err := Open(cfg, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = snk.Close() })
	if _, err := snk.db.ExecContext(t.Context(), sqliteSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return snk, snk.db
}

func newMsg(t *testing.T, id string, fields map[string]any) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	for k, v := range fields {
		m.SetData(k, v)
	}
	return m
}

func order(id string, items ...map[string]any) map[string]any {
	list := make([]any, len(items))
	for i, it := range items {
		list[i] = it
	}
	return map[string]any{"id": id, "customer": "C-9", "total": 120, "items": list}
}

const orderStatements = `[
	{"name":"order","sql":"INSERT INTO orders (external_id, customer_id, total) VALUES ({{id}}, {{customer}}, {{total}}) ON CONFLICT (external_id) DO UPDATE SET total = excluded.total RETURNING id"},
	{"name":"items","forEach":"items","as":"item","sql":"INSERT INTO order_items (order_id, sku, qty) VALUES ({{order.id}}, {{item.sku}}, {{item.qty}}) ON CONFLICT (order_id, sku) DO UPDATE SET qty = excluded.qty"},
	{"name":"audit","sql":"UPDATE customers SET last_order_id = {{order.id}} WHERE id = {{customer}}","expect":"affected"}
]`

func count(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(), q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestWriteInsertsParentThenChildrenWithTheReturnedID(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, nil)
	msg := newMsg(t, "m1", order("O-1", map[string]any{"sku": "A", "qty": 2}, map[string]any{"sku": "B", "qty": 1}))

	if err := snk.Write(context.Background(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var orderID int
	if err := db.QueryRowContext(t.Context(), `SELECT id FROM orders WHERE external_id = 'O-1'`).Scan(&orderID); err != nil {
		t.Fatalf("order not written: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM order_items WHERE order_id = `+itoa(orderID)); n != 2 {
		t.Errorf("order_items for the new order = %d, want 2", n)
	}
	var last sql.NullInt64
	_ = db.QueryRowContext(t.Context(), `SELECT last_order_id FROM customers WHERE id = 'C-9'`).Scan(&last)
	if !last.Valid || int(last.Int64) != orderID {
		t.Errorf("customers.last_order_id = %v, want %d", last, orderID)
	}
}

func TestWriteRollsBackTheParentWhenAChildFails(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, nil)
	// qty is NOT NULL; a child with no qty fails after the parent was inserted.
	msg := newMsg(t, "m1", order("O-1", map[string]any{"sku": "A", "qty": 2}, map[string]any{"sku": "B"}))

	err := snk.Write(context.Background(), msg)
	if err == nil {
		t.Fatal("Write succeeded with a child that cannot be inserted")
	}
	if !errors.Is(err, hermod.ErrPermanent) {
		t.Errorf("error %v is not permanent: another attempt gets the same answer", err)
	}
	if !strings.Contains(err.Error(), "items") {
		t.Errorf("error %q does not name the statement that failed", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM orders`); n != 0 {
		t.Errorf("orders = %d after a failed child, want 0: the parent must roll back too", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM order_items`); n != 0 {
		t.Errorf("order_items = %d, want 0", n)
	}
}

func TestWriteIsIdempotentWithUpserts(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, nil)
	body := order("O-1", map[string]any{"sku": "A", "qty": 2})
	for i := range 2 {
		if err := snk.Write(context.Background(), newMsg(t, "m1", body)); err != nil {
			t.Fatalf("Write #%d: %v", i+1, err)
		}
	}
	if n := count(t, db, `SELECT COUNT(*) FROM orders`); n != 1 {
		t.Errorf("orders = %d after a redelivery, want 1", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM order_items`); n != 1 {
		t.Errorf("order_items = %d after a redelivery, want 1", n)
	}
}

func TestWriteFailsOnAnUnresolvedVariableByDefault(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, nil)
	body := order("O-1", map[string]any{"sku": "A", "qty": 2})
	delete(body, "total")

	err := snk.Write(context.Background(), newMsg(t, "m1", body))
	if err == nil || !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("Write = %v, want a permanent refusal", err)
	}
	if !strings.Contains(err.Error(), "total") {
		t.Errorf("error %q does not name the missing variable", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM orders`); n != 0 {
		t.Errorf("orders = %d, want 0", n)
	}
}

func TestWriteBindsNullWhenAskedTo(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, map[string]string{"on_unresolved": "null"})
	body := order("O-1", map[string]any{"sku": "A", "qty": 2})
	delete(body, "total")
	if err := snk.Write(context.Background(), newMsg(t, "m1", body)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM orders WHERE total IS NULL`); n != 1 {
		t.Errorf("orders with NULL total = %d, want 1", n)
	}
}

func TestExpectOneRefusesAnEmptyReturningResult(t *testing.T) {
	// The ON CONFLICT DO NOTHING trap: on a conflict RETURNING gives no row,
	// and without the check the child would be written with a NULL parent.
	stmts := `[
		{"name":"order","sql":"INSERT INTO orders (external_id, customer_id) VALUES ({{id}}, {{customer}}) ON CONFLICT (external_id) DO NOTHING RETURNING id"},
		{"name":"items","forEach":"items","sql":"INSERT INTO order_items (order_id, sku, qty) VALUES ({{order.id}}, {{item.sku}}, {{item.qty}})"}
	]`
	snk, db := newSQLiteSink(t, stmts, nil)
	if err := snk.Write(context.Background(), newMsg(t, "m1", order("O-1", map[string]any{"sku": "A", "qty": 1}))); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	err := snk.Write(context.Background(), newMsg(t, "m2", order("O-1", map[string]any{"sku": "B", "qty": 1})))
	if err == nil || !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("second Write = %v, want a permanent refusal", err)
	}
	if !strings.Contains(err.Error(), "order") || !strings.Contains(err.Error(), "0 rows") {
		t.Errorf("error %q should name the statement and say it returned 0 rows", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM order_items`); n != 1 {
		t.Errorf("order_items = %d, want only the first message's 1", n)
	}
}

func TestExpectAffectedRefusesAnUpdateThatMatchedNothing(t *testing.T) {
	stmts := `[{"name":"touch","sql":"UPDATE customers SET last_order_id = 1 WHERE id = {{customer}}","expect":"affected"}]`
	snk, _ := newSQLiteSink(t, stmts, nil)
	err := snk.Write(context.Background(), newMsg(t, "m1", map[string]any{"customer": "nobody"}))
	if err == nil || !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("Write = %v, want a permanent refusal", err)
	}
	if err := snk.Write(context.Background(), newMsg(t, "m2", map[string]any{"customer": "C-9"})); err != nil {
		t.Fatalf("Write for an existing customer: %v", err)
	}
}

func TestDeleteStatementsRun(t *testing.T) {
	stmts := `[
		{"name":"order","sql":"INSERT INTO orders (external_id, customer_id) VALUES ({{id}}, {{customer}}) RETURNING id"},
		{"name":"cleanup","sql":"DELETE FROM orders WHERE id = {{order.id}}","expect":"one"}
	]`
	snk, db := newSQLiteSink(t, stmts, nil)
	if err := snk.Write(context.Background(), newMsg(t, "m1", order("O-1"))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM orders`); n != 0 {
		t.Errorf("orders = %d, want 0 after the delete", n)
	}
}

func TestForEachOverAnEmptyListRunsNothing(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, nil)
	if err := snk.Write(context.Background(), newMsg(t, "m1", order("O-1"))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM orders`); n != 1 {
		t.Errorf("orders = %d, want 1", n)
	}
}

func TestForEachRefusesAFieldThatIsNotAList(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, nil)
	body := order("O-1")
	body["items"] = "A,B"
	err := snk.Write(context.Background(), newMsg(t, "m1", body))
	if err == nil || !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("Write = %v, want a permanent refusal", err)
	}
	if !strings.Contains(err.Error(), "not a list") {
		t.Errorf("error %q should say the field is not a list", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM orders`); n != 0 {
		t.Errorf("orders = %d, want 0", n)
	}
}

func TestForEachRefusesAMissingListByDefault(t *testing.T) {
	snk, _ := newSQLiteSink(t, orderStatements, nil)
	body := order("O-1")
	delete(body, "items")
	if err := snk.Write(context.Background(), newMsg(t, "m1", body)); err == nil || !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("Write = %v, want a permanent refusal", err)
	}
}

func TestForEachBindsScalarItems(t *testing.T) {
	stmts := `[
		{"name":"order","sql":"INSERT INTO orders (external_id, customer_id) VALUES ({{id}}, {{customer}}) RETURNING id"},
		{"name":"items","forEach":"skus","as":"sku","sql":"INSERT INTO order_items (order_id, sku, qty) VALUES ({{order.id}}, {{sku}}, 1)"}
	]`
	snk, db := newSQLiteSink(t, stmts, nil)
	body := order("O-1")
	body["skus"] = []any{"A", "B", "C"}
	if err := snk.Write(context.Background(), newMsg(t, "m1", body)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM order_items`); n != 3 {
		t.Errorf("order_items = %d, want 3", n)
	}
}

func TestWriteBatchIsAllOrNothing(t *testing.T) {
	snk, db := newSQLiteSink(t, orderStatements, nil)
	good := newMsg(t, "m1", order("O-1", map[string]any{"sku": "A", "qty": 1}))
	bad := newMsg(t, "m2", order("O-2", map[string]any{"sku": "A"}))

	err := snk.WriteBatch(context.Background(), []hermod.Message{good, bad})
	if err == nil || !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("WriteBatch = %v, want a permanent refusal so the engine retries each message alone", err)
	}
	if !strings.Contains(err.Error(), "m2") {
		t.Errorf("error %q does not name the message that failed", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM orders`); n != 0 {
		t.Errorf("orders = %d, want 0: the batch is one transaction", n)
	}

	if err := snk.WriteBatch(context.Background(), []hermod.Message{good}); err != nil {
		t.Fatalf("WriteBatch of the good message alone: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM orders`); n != 1 {
		t.Errorf("orders = %d, want 1", n)
	}
}

func TestStatementResultsAreScopedToTheirMessage(t *testing.T) {
	// Each message's {{order.id}} must be its own order, not the batch's first.
	snk, db := newSQLiteSink(t, orderStatements, nil)
	a := newMsg(t, "m1", order("O-1", map[string]any{"sku": "A", "qty": 1}))
	b := newMsg(t, "m2", order("O-2", map[string]any{"sku": "B", "qty": 1}))
	if err := snk.WriteBatch(context.Background(), []hermod.Message{a, b}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM order_items i JOIN orders o ON o.id = i.order_id WHERE (o.external_id = 'O-1' AND i.sku = 'A') OR (o.external_id = 'O-2' AND i.sku = 'B')`); n != 2 {
		t.Errorf("correctly linked items = %d, want 2", n)
	}
}

func TestStatementNamesShadowMessageFields(t *testing.T) {
	// A message field called "order" must not stand in for the statement.
	snk, db := newSQLiteSink(t, orderStatements, nil)
	body := order("O-1", map[string]any{"sku": "A", "qty": 1})
	body["order"] = map[string]any{"id": 999}
	if err := snk.Write(context.Background(), newMsg(t, "m1", body)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM order_items WHERE order_id = 999`); n != 0 {
		t.Errorf("a child was linked to the message field, not the inserted row")
	}
}

func TestEmptyBatchIsANoOp(t *testing.T) {
	snk, _ := newSQLiteSink(t, orderStatements, nil)
	if err := snk.WriteBatch(context.Background(), nil); err != nil {
		t.Fatalf("WriteBatch(nil): %v", err)
	}
}

func TestPing(t *testing.T) {
	snk, _ := newSQLiteSink(t, orderStatements, nil)
	if err := snk.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
