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
	"path/filepath"
	"strings"
	"testing"
)

const orderSteps = `[
	{"name":"order","table":"orders","action":"upsert","match":["external_id"],
	 "columns":[{"column":"external_id","value":"{{id}}"},{"column":"customer_id","value":"{{customer}}"},{"column":"total","value":"{{total}}"}],
	 "returning":["id"]},
	{"name":"items","table":"order_items","action":"upsert","match":["order_id","sku"],"forEach":"items","as":"item",
	 "columns":[{"column":"order_id","value":"{{order.id}}"},{"column":"sku","value":"{{item.sku}}"},{"column":"qty","value":"{{item.qty}}"}]},
	{"name":"audit","table":"customers","action":"update","match":["id"],
	 "columns":[{"column":"id","value":"{{customer}}"},{"column":"last_order_id","value":"{{order.id}}"}]}
]`

func compileFor(t *testing.T, driver, steps string) []Statement {
	t.Helper()
	stmts, err := CompileGuided(driver, steps)
	if err != nil {
		t.Fatalf("CompileGuided(%s): %v", driver, err)
	}
	return stmts
}

func TestGuidedCompilesAnUpsertPerDatabase(t *testing.T) {
	cases := map[string][]string{
		"postgres": {
			`INSERT INTO "orders" ("external_id", "customer_id", "total") VALUES ({{id}}, {{customer}}, {{total}}) ON CONFLICT ("external_id") DO UPDATE SET "customer_id" = EXCLUDED."customer_id", "total" = EXCLUDED."total" RETURNING "id"`,
			`INSERT INTO "order_items" ("order_id", "sku", "qty") VALUES ({{order.id}}, {{item.sku}}, {{item.qty}}) ON CONFLICT ("order_id", "sku") DO UPDATE SET "qty" = EXCLUDED."qty"`,
			`UPDATE "customers" SET "last_order_id" = {{order.id}} WHERE "id" = {{customer}}`,
		},
		"sqlite": {
			`INSERT INTO "orders" ("external_id", "customer_id", "total") VALUES ({{id}}, {{customer}}, {{total}}) ON CONFLICT ("external_id") DO UPDATE SET "customer_id" = EXCLUDED."customer_id", "total" = EXCLUDED."total" RETURNING "id"`,
		},
		"mysql": {
			"INSERT INTO `orders` (`external_id`, `customer_id`, `total`) VALUES ({{id}}, {{customer}}, {{total}}) ON DUPLICATE KEY UPDATE `customer_id` = VALUES(`customer_id`), `total` = VALUES(`total`), `id` = LAST_INSERT_ID(`id`)",
			"INSERT INTO `order_items` (`order_id`, `sku`, `qty`) VALUES ({{order.id}}, {{item.sku}}, {{item.qty}}) ON DUPLICATE KEY UPDATE `qty` = VALUES(`qty`)",
			"UPDATE `customers` SET `last_order_id` = {{order.id}} WHERE `id` = {{customer}}",
		},
		"mssql": {
			`MERGE INTO [orders] WITH (HOLDLOCK) AS t USING (SELECT {{id}} AS [external_id], {{customer}} AS [customer_id], {{total}} AS [total]) AS s ON t.[external_id] = s.[external_id] WHEN MATCHED THEN UPDATE SET t.[customer_id] = s.[customer_id], t.[total] = s.[total] WHEN NOT MATCHED THEN INSERT ([external_id], [customer_id], [total]) VALUES (s.[external_id], s.[customer_id], s.[total]) OUTPUT INSERTED.[id];`,
			`MERGE INTO [order_items] WITH (HOLDLOCK) AS t USING (SELECT {{order.id}} AS [order_id], {{item.sku}} AS [sku], {{item.qty}} AS [qty]) AS s ON t.[order_id] = s.[order_id] AND t.[sku] = s.[sku] WHEN MATCHED THEN UPDATE SET t.[qty] = s.[qty] WHEN NOT MATCHED THEN INSERT ([order_id], [sku], [qty]) VALUES (s.[order_id], s.[sku], s.[qty]);`,
			`UPDATE [customers] SET [last_order_id] = {{order.id}} WHERE [id] = {{customer}}`,
		},
	}
	for driver, want := range cases {
		stmts := compileFor(t, driver, orderSteps)
		for i, w := range want {
			if stmts[i].SQL != w {
				t.Errorf("%s step %d:\n got %s\nwant %s", driver, i+1, stmts[i].SQL, w)
			}
		}
	}
}

func TestGuidedCarriesNamesLoopsAndChecks(t *testing.T) {
	stmts := compileFor(t, "postgres", orderSteps)
	if stmts[0].Name != "order" || stmts[1].ForEach != "items" || stmts[1].As != "item" {
		t.Fatalf("names and loops not carried: %+v", stmts)
	}
	if stmts[2].Expect != ExpectAffected {
		t.Fatalf("an update must change a row: expect = %q", stmts[2].Expect)
	}
	my := compileFor(t, "mysql", orderSteps)
	if my[0].ReturnIDAs != "id" {
		t.Fatalf("MySQL reads the given-back column from the insert id: %+v", my[0])
	}
}

func TestGuidedCompilesInsertUpdateAndDeleteWithReturning(t *testing.T) {
	steps := `[
		{"name":"a","table":"sales.orders","action":"insert","columns":[{"column":"total","value":"{{total}}"}],"returning":["id","created_at"]},
		{"name":"b","table":"orders","action":"update","match":["id"],"columns":[{"column":"id","value":"{{a.id}}"},{"column":"total","value":"{{total}}"}],"returning":["total"]},
		{"name":"c","table":"orders","action":"delete","match":["id"],"columns":[{"column":"id","value":"{{a.id}}"}],"returning":["id"]}
	]`
	pg := compileFor(t, "postgres", steps)
	want := []string{
		`INSERT INTO "sales"."orders" ("total") VALUES ({{total}}) RETURNING "id", "created_at"`,
		`UPDATE "orders" SET "total" = {{total}} WHERE "id" = {{a.id}} RETURNING "total"`,
		`DELETE FROM "orders" WHERE "id" = {{a.id}} RETURNING "id"`,
	}
	for i, w := range want {
		if pg[i].SQL != w {
			t.Errorf("postgres %d:\n got %s\nwant %s", i, pg[i].SQL, w)
		}
	}
	ms := compileFor(t, "mssql", steps)
	wantMS := []string{
		`INSERT INTO [sales].[orders] ([total]) OUTPUT INSERTED.[id], INSERTED.[created_at] VALUES ({{total}})`,
		`UPDATE [orders] SET [total] = {{total}} OUTPUT INSERTED.[total] WHERE [id] = {{a.id}}`,
		`DELETE FROM [orders] OUTPUT DELETED.[id] WHERE [id] = {{a.id}}`,
	}
	for i, w := range wantMS {
		if ms[i].SQL != w {
			t.Errorf("mssql %d:\n got %s\nwant %s", i, ms[i].SQL, w)
		}
	}
}

func TestGuidedRefusesWhatItCannotCompileSafely(t *testing.T) {
	cases := map[string]string{
		"a table name with SQL in it":        `[{"table":"orders; DROP TABLE x","action":"insert","columns":[{"column":"a","value":"{{a}}"}]}]`,
		"a column name with SQL in it":       `[{"table":"orders","action":"insert","columns":[{"column":"a) VALUES (1); --","value":"{{a}}"}]}]`,
		"a value that is not one field":      `[{"table":"orders","action":"insert","columns":[{"column":"a","value":"1); DELETE FROM orders; --"}]}]`,
		"an upsert with nothing to match on": `[{"table":"orders","action":"upsert","columns":[{"column":"a","value":"{{a}}"}]}]`,
		"a delete with nothing to match on":  `[{"table":"orders","action":"delete","columns":[]}]`,
		"a match column with no value":       `[{"table":"orders","action":"update","match":["id"],"columns":[{"column":"a","value":"{{a}}"}]}]`,
		"an update that sets nothing":        `[{"table":"orders","action":"update","match":["id"],"columns":[{"column":"id","value":"{{id}}"}]}]`,
		"an unknown action":                  `[{"table":"orders","action":"truncate","columns":[{"column":"a","value":"{{a}}"}]}]`,
		"a step with no table":               `[{"action":"insert","columns":[{"column":"a","value":"{{a}}"}]}]`,
		"a column listed twice":              `[{"table":"orders","action":"insert","columns":[{"column":"a","value":"{{a}}"},{"column":"a","value":"{{b}}"}]}]`,
		"no steps":                           `[]`,
	}
	for name, steps := range cases {
		if _, err := CompileGuided("postgres", steps); err == nil {
			t.Errorf("%s: compiled, want a refusal", name)
		}
	}
	mysqlReturning := `[{"table":"orders","action":"update","match":["id"],"columns":[{"column":"id","value":"{{id}}"},{"column":"a","value":"{{a}}"}],"returning":["a"]}]`
	if _, err := CompileGuided("mysql", mysqlReturning); err == nil || !strings.Contains(err.Error(), "MySQL") {
		t.Errorf("MySQL update with give-back: err = %v, want a refusal naming MySQL", err)
	}
}

func TestParseConfigCompilesGuidedMode(t *testing.T) {
	cfg, err := ParseConfig(map[string]string{"driver": "postgres", "mode": "guided", "steps": orderSteps})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Statements) != 3 || !cfg.Statements[0].rows {
		t.Fatalf("statements = %+v", cfg.Statements)
	}
	if _, err := ParseConfig(map[string]string{"driver": "postgres", "mode": "guided"}); err == nil {
		t.Fatal("guided mode with no steps parsed")
	}
	if _, err := ParseConfig(map[string]string{"driver": "postgres", "mode": "drawing", "statements": orderStatements}); err == nil {
		t.Fatal("an unknown mode parsed")
	}
	// A reference to a later step is refused through the same checks SQL mode uses.
	later := `[{"name":"a","table":"t","action":"insert","columns":[{"column":"x","value":"{{b.id}}"}]},{"name":"b","table":"t","action":"insert","columns":[{"column":"x","value":"{{x}}"}],"returning":["id"]}]`
	if _, err := ParseConfig(map[string]string{"driver": "postgres", "mode": "guided", "steps": later}); err == nil || !strings.Contains(err.Error(), "later") {
		t.Fatalf("forward reference: err = %v", err)
	}
}

func TestGuidedUpsertsAreSafeToRedeliverOnSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.db")
	cfg, err := ParseConfig(map[string]string{"driver": "sqlite", "mode": "guided", "steps": orderSteps})
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
	msg := newMsg(t, "g1", order("E-9", map[string]any{"sku": "A", "qty": 2}, map[string]any{"sku": "B", "qty": 1}))
	for range 2 {
		if err := snk.Write(t.Context(), msg); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	db := snk.db
	if n := count(t, db, "SELECT count(*) FROM orders"); n != 1 {
		t.Fatalf("orders = %d after a redelivery, want 1", n)
	}
	if n := count(t, db, "SELECT count(*) FROM order_items oi JOIN orders o ON o.id = oi.order_id"); n != 2 {
		t.Fatalf("linked items = %d, want 2", n)
	}
	if n := count(t, db, "SELECT count(*) FROM customers c JOIN orders o ON o.id = c.last_order_id"); n != 1 {
		t.Fatalf("customer not linked to the order")
	}
}
