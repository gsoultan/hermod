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

package txsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
)

// The Transactional SQL sink against real servers: the parent's generated id
// reaching the child on each database's own way of handing it back, the parent
// rolling back when the child fails, and a redelivery not duplicating rows.
//
// Run with the servers CI starts:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/hermod_it?sslmode=disable' \
//	MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/hermod_it?parseTime=true' \
//	MSSQL_DSN='sqlserver://sa:Hermod%21Passw0rd@127.0.0.1:1433?database=master&encrypt=disable' \
//	go test -tags=integration ./pkg/comm/sink/txsql/

type dialect struct {
	driver, env string
	// schema creates the two tables; %[1]s is the orders table, %[2]s the items table.
	schema []string
	// parent inserts or upserts the order and hands its id back as {{order.id}}.
	parent string
	// child inserts or upserts one item.
	child string
}

var dialects = []dialect{
	{
		driver: "postgres", env: "POSTGRES_DSN",
		schema: []string{
			`CREATE TABLE %[1]s (id SERIAL PRIMARY KEY, external_id TEXT UNIQUE NOT NULL, customer_id TEXT NOT NULL)`,
			`CREATE TABLE %[2]s (order_id INT NOT NULL REFERENCES %[1]s(id), sku TEXT NOT NULL, qty INT NOT NULL, PRIMARY KEY (order_id, sku))`,
		},
		parent: `INSERT INTO %[1]s (external_id, customer_id) VALUES ({{id}}, {{customer}}) ON CONFLICT (external_id) DO UPDATE SET customer_id = EXCLUDED.customer_id RETURNING id`,
		child:  `INSERT INTO %[2]s (order_id, sku, qty) VALUES ({{order.id}}, {{item.sku}}, {{item.qty}}) ON CONFLICT (order_id, sku) DO UPDATE SET qty = EXCLUDED.qty`,
	},
	{
		driver: "mysql", env: "MYSQL_DSN",
		schema: []string{
			`CREATE TABLE %[1]s (id INT AUTO_INCREMENT PRIMARY KEY, external_id VARCHAR(64) NOT NULL UNIQUE, customer_id VARCHAR(64) NOT NULL) ENGINE=InnoDB`,
			`CREATE TABLE %[2]s (order_id INT NOT NULL, sku VARCHAR(64) NOT NULL, qty INT NOT NULL, PRIMARY KEY (order_id, sku), FOREIGN KEY (order_id) REFERENCES %[1]s(id)) ENGINE=InnoDB`,
		},
		// MySQL has no RETURNING. The sink binds the last-insert id, and
		// id = LAST_INSERT_ID(id) makes an update hand back the existing row's id.
		parent: `INSERT INTO %[1]s (external_id, customer_id) VALUES ({{id}}, {{customer}}) ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id), customer_id = VALUES(customer_id)`,
		child:  `INSERT INTO %[2]s (order_id, sku, qty) VALUES ({{order.id}}, {{item.sku}}, {{item.qty}}) ON DUPLICATE KEY UPDATE qty = VALUES(qty)`,
	},
	{
		driver: "mssql", env: "MSSQL_DSN",
		schema: []string{
			`CREATE TABLE %[1]s (id INT IDENTITY(1,1) PRIMARY KEY, external_id NVARCHAR(64) NOT NULL UNIQUE, customer_id NVARCHAR(64) NOT NULL)`,
			`CREATE TABLE %[2]s (order_id INT NOT NULL REFERENCES %[1]s(id), sku NVARCHAR(64) NOT NULL, qty INT NOT NULL, PRIMARY KEY (order_id, sku))`,
		},
		parent: `MERGE %[1]s WITH (HOLDLOCK) AS t USING (SELECT {{id}} AS external_id, {{customer}} AS customer_id) AS s ON t.external_id = s.external_id ` +
			`WHEN MATCHED THEN UPDATE SET customer_id = s.customer_id ` +
			`WHEN NOT MATCHED THEN INSERT (external_id, customer_id) VALUES (s.external_id, s.customer_id) OUTPUT inserted.id;`,
		child: `MERGE %[2]s WITH (HOLDLOCK) AS t USING (SELECT {{order.id}} AS order_id, {{item.sku}} AS sku, {{item.qty}} AS qty) AS s ON t.order_id = s.order_id AND t.sku = s.sku ` +
			`WHEN MATCHED THEN UPDATE SET qty = s.qty ` +
			`WHEN NOT MATCHED THEN INSERT (order_id, sku, qty) VALUES (s.order_id, s.sku, s.qty);`,
	},
}

type fixture struct {
	snk           *Sink
	db            *sql.DB
	orders, items string
}

func setup(t *testing.T, d dialect, parent, child string) fixture {
	t.Helper()
	dsn := os.Getenv(d.env)
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q %s=%q in CI, where a server is started for exactly this",
				os.Getenv("HERMOD_INTEGRATION"), d.env, dsn)
		}
		t.Skipf("integration: set HERMOD_INTEGRATION=1 and %s to run", d.env)
	}

	suffix := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 10)
	f := fixture{orders: "txsql_orders_" + suffix, items: "txsql_items_" + suffix}

	stmts := fmt.Sprintf(`[{"name":"order","sql":%q},{"name":"items","forEach":"items","as":"item","sql":%q}]`,
		fmt.Sprintf(parent, f.orders, f.items), fmt.Sprintf(child, f.orders, f.items))
	cfg, err := ParseConfig(map[string]string{"driver": d.driver, "statements": stmts})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	snk, err := Open(cfg, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.snk, f.db = snk, snk.db
	ctx := context.Background()
	if err := snk.Ping(ctx); err != nil {
		t.Fatalf("%s names a server that is not reachable: %v", d.env, err)
	}
	for _, q := range d.schema {
		if _, err := f.db.ExecContext(ctx, fmt.Sprintf(q, f.orders, f.items)); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = f.db.ExecContext(context.Background(), "DROP TABLE "+f.items)
		_, _ = f.db.ExecContext(context.Background(), "DROP TABLE "+f.orders)
		_ = snk.Close()
	})
	return f
}

func (f fixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func orderMsg(t *testing.T, msgID, orderID string, items ...map[string]any) hermod.Message {
	return newMsg(t, msgID, map[string]any{"id": orderID, "customer": "C-9", "items": func() []any {
		out := make([]any, len(items))
		for i, it := range items {
			out[i] = it
		}
		return out
	}()})
}

func TestIntegrationParentIDReachesChildren(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.driver, func(t *testing.T) {
			f := setup(t, d, d.parent, d.child)
			msg := orderMsg(t, "m1", "O-1", map[string]any{"sku": "A", "qty": 2}, map[string]any{"sku": "B", "qty": 1})
			if err := f.snk.Write(context.Background(), msg); err != nil {
				t.Fatalf("Write: %v", err)
			}
			var linked int
			q := fmt.Sprintf(`SELECT COUNT(*) FROM %s i JOIN %s o ON o.id = i.order_id WHERE o.external_id = 'O-1'`, f.items, f.orders)
			if err := f.db.QueryRowContext(t.Context(), q).Scan(&linked); err != nil {
				t.Fatal(err)
			}
			if linked != 2 {
				t.Errorf("items linked to the new order = %d, want 2", linked)
			}
		})
	}
}

func TestIntegrationChildFailureRollsBackParent(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.driver, func(t *testing.T) {
			f := setup(t, d, d.parent, d.child)
			// qty is NOT NULL: the second item fails after the order and the
			// first item were written.
			msg := orderMsg(t, "m1", "O-1", map[string]any{"sku": "A", "qty": 2}, map[string]any{"sku": "B", "qty": nil})
			snk := f.snk
			snk.cfg.OnUnresolved = OnUnresolvedNull // let the NULL reach the database
			err := snk.Write(context.Background(), msg)
			if err == nil {
				t.Fatal("Write succeeded with a child the database must refuse")
			}
			if !errors.Is(err, hermod.ErrPermanent) {
				t.Errorf("a NOT NULL violation is not permanent: %v", err)
			}
			if n := f.count(t, f.orders); n != 0 {
				t.Errorf("orders = %d after the child failed, want 0", n)
			}
			if n := f.count(t, f.items); n != 0 {
				t.Errorf("items = %d after the child failed, want 0", n)
			}
		})
	}
}

func TestIntegrationRedeliveryDoesNotDuplicate(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.driver, func(t *testing.T) {
			f := setup(t, d, d.parent, d.child)
			for i := range 2 {
				msg := orderMsg(t, "m1", "O-1", map[string]any{"sku": "A", "qty": 2 + i})
				if err := f.snk.Write(context.Background(), msg); err != nil {
					t.Fatalf("Write #%d: %v", i+1, err)
				}
			}
			if n := f.count(t, f.orders); n != 1 {
				t.Errorf("orders = %d after a redelivery, want 1", n)
			}
			if n := f.count(t, f.items); n != 1 {
				t.Errorf("items = %d after a redelivery, want 1: the second write must reuse the first order's id", n)
			}
		})
	}
}

func TestIntegrationBatchIsOneTransaction(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.driver, func(t *testing.T) {
			f := setup(t, d, d.parent, d.child)
			f.snk.cfg.OnUnresolved = OnUnresolvedNull
			good := orderMsg(t, "m1", "O-1", map[string]any{"sku": "A", "qty": 1})
			bad := orderMsg(t, "m2", "O-2", map[string]any{"sku": "A", "qty": nil})
			err := f.snk.WriteBatch(context.Background(), []hermod.Message{good, bad})
			if err == nil || !errors.Is(err, hermod.ErrPermanent) {
				t.Fatalf("WriteBatch = %v, want a permanent refusal", err)
			}
			if !strings.Contains(err.Error(), "m2") {
				t.Errorf("error %q does not name the failing message", err)
			}
			if n := f.count(t, f.orders); n != 0 {
				t.Errorf("orders = %d, want 0: the good message rolls back with the bad one", n)
			}
		})
	}
}

func TestIntegrationUniqueViolationIsPermanent(t *testing.T) {
	for _, d := range dialects {
		t.Run(d.driver, func(t *testing.T) {
			// A plain insert, no upsert: the second delivery hits the unique key.
			plain := map[string]string{
				"postgres": `INSERT INTO %[1]s (external_id, customer_id) VALUES ({{id}}, {{customer}}) RETURNING id`,
				"mysql":    `INSERT INTO %[1]s (external_id, customer_id) VALUES ({{id}}, {{customer}})`,
				"mssql":    `INSERT INTO %[1]s (external_id, customer_id) OUTPUT inserted.id VALUES ({{id}}, {{customer}})`,
			}[d.driver]
			f := setup(t, d, plain, d.child)
			if err := f.snk.Write(context.Background(), orderMsg(t, "m1", "O-1")); err != nil {
				t.Fatalf("first Write: %v", err)
			}
			err := f.snk.Write(context.Background(), orderMsg(t, "m2", "O-1"))
			if err == nil || !errors.Is(err, hermod.ErrPermanent) {
				t.Fatalf("duplicate Write = %v, want a permanent refusal", err)
			}
		})
	}
}
