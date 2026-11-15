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
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func oracleCfg(statements string, extra map[string]string) map[string]string {
	m := map[string]string{"driver": "oracle", "statements": statements}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestOracleIsAccepted(t *testing.T) {
	cfg, err := ParseConfig(oracleCfg(`[{"sql":"UPDATE t SET a = {{a}} WHERE id = {{id}}"}]`, nil))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := cfg.canonicalDriver(); got != "oracle" {
		t.Errorf("driver = %q, want oracle", got)
	}
}

// Oracle has no RETURNING result set: a write hands its columns back through
// OUT binds named in an INTO clause. The sink writes that clause, so a
// statement reads the same on Oracle as on PostgreSQL.
func TestOracleReturningBecomesOutBinds(t *testing.T) {
	cfg, err := ParseConfig(oracleCfg(`[
		{"name":"order","sql":"INSERT INTO orders (external_id) VALUES ({{id}}) RETURNING id, \"Created_At\""}
	]`, nil))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	st := cfg.Statements[0]
	if st.rows {
		t.Error("an Oracle RETURNING statement runs as a query; it has to run as an exec with OUT binds")
	}
	if want := []string{"id", "created_at"}; !reflect.DeepEqual(st.outCols, want) {
		t.Errorf("outCols = %v, want %v", st.outCols, want)
	}
	if st.Expect != ExpectOne {
		t.Errorf("expect = %q, want %q", st.Expect, ExpectOne)
	}
	if got, want := withInto(st.SQL, 1, len(st.outCols)), `INSERT INTO orders (external_id) VALUES ({{id}}) RETURNING id, "Created_At" INTO :2, :3`; got != want {
		t.Errorf("withInto = %q, want %q", got, want)
	}
}

func TestOracleTrailingSemicolonIsDropped(t *testing.T) {
	cfg, err := ParseConfig(oracleCfg(`[{"sql":"UPDATE t SET a = 1 WHERE id = 2;  "}]`, nil))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := cfg.Statements[0].SQL; got != "UPDATE t SET a = 1 WHERE id = 2" {
		t.Errorf("SQL = %q; Oracle refuses a trailing semicolon outside PL/SQL", got)
	}
}

func TestOracleRefuses(t *testing.T) {
	cases := []struct {
		name  string
		cfg   map[string]string
		wants string
	}{
		{"own INTO", oracleCfg(`[{"sql":"INSERT INTO t (a) VALUES (1) RETURNING id INTO :x"}]`, nil), "INTO"},
		{"expression", oracleCfg(`[{"sql":"INSERT INTO t (a) VALUES (1) RETURNING id + 1"}]`, nil), "column names"},
		{"many rows", oracleCfg(`[{"sql":"UPDATE t SET a = 1 RETURNING id","expect":"affected"}]`, nil), "exactly one row"},
		{"repeatable read", oracleCfg(`[{"sql":"UPDATE t SET a = 1"}]`, map[string]string{"isolation": "repeatable_read"}), "isolation"},
		{"read uncommitted", oracleCfg(`[{"sql":"UPDATE t SET a = 1"}]`, map[string]string{"isolation": "read_uncommitted"}), "isolation"},
		{"procedure call", oracleCfg(`[{"sql":"CALL archive_order({{id}})"}]`, nil), "CALL"},
		// A block is refused by its first word, before its semicolons are counted.
		{"anonymous block", oracleCfg(`[{"sql":"BEGIN archive_order({{id}}) END"}]`, nil), "BEGIN"},
		{"ddl", oracleCfg(`[{"sql":"CREATE TABLE x (a NUMBER)"}]`, nil), "CREATE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig(tc.cfg)
			if err == nil {
				t.Fatalf("ParseConfig accepted %v", tc.cfg)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q does not mention %q", err, tc.wants)
			}
		})
	}
}

func TestOracleIsolation(t *testing.T) {
	for iso, want := range map[string]sql.IsolationLevel{
		"":               sql.LevelDefault,
		"read_committed": sql.LevelDefault, // Oracle's default, and go-ora takes no other
		"serializable":   sql.LevelSerializable,
	} {
		cfg, err := ParseConfig(oracleCfg(`[{"sql":"UPDATE t SET a = 1"}]`, map[string]string{"isolation": iso}))
		if err != nil {
			t.Fatalf("isolation %q: %v", iso, err)
		}
		if cfg.Isolation != want {
			t.Errorf("isolation %q = %v, want %v", iso, cfg.Isolation, want)
		}
		if got := cfg.txOptions().Isolation; got != sql.LevelDefault {
			t.Errorf("isolation %q: the driver is asked for %v; go-ora refuses anything but the default", iso, got)
		}
	}
}

func TestGuidedCompilesForOracle(t *testing.T) {
	stmts := compileFor(t, "oracle", orderSteps)
	type want struct{ name, sql, expect string }
	wants := []want{
		{"order_merge", `MERGE INTO "ORDERS" t USING (SELECT {{id}} AS "EXTERNAL_ID", {{customer}} AS "CUSTOMER_ID", {{total}} AS "TOTAL" FROM dual) s ON (t."EXTERNAL_ID" = s."EXTERNAL_ID") WHEN MATCHED THEN UPDATE SET t."CUSTOMER_ID" = s."CUSTOMER_ID", t."TOTAL" = s."TOTAL" WHEN NOT MATCHED THEN INSERT ("EXTERNAL_ID", "CUSTOMER_ID", "TOTAL") VALUES (s."EXTERNAL_ID", s."CUSTOMER_ID", s."TOTAL")`, ""},
		{"order", `SELECT "ID" FROM "ORDERS" WHERE "EXTERNAL_ID" = {{id}}`, ExpectOne},
		{"items", `MERGE INTO "ORDER_ITEMS" t USING (SELECT {{order.id}} AS "ORDER_ID", {{item.sku}} AS "SKU", {{item.qty}} AS "QTY" FROM dual) s ON (t."ORDER_ID" = s."ORDER_ID" AND t."SKU" = s."SKU") WHEN MATCHED THEN UPDATE SET t."QTY" = s."QTY" WHEN NOT MATCHED THEN INSERT ("ORDER_ID", "SKU", "QTY") VALUES (s."ORDER_ID", s."SKU", s."QTY")`, ""},
		{"audit", `UPDATE "CUSTOMERS" SET "LAST_ORDER_ID" = {{order.id}} WHERE "ID" = {{customer}}`, ExpectAffected},
	}
	if len(stmts) != len(wants) {
		t.Fatalf("compiled %d statements, want %d: %+v", len(stmts), len(wants), stmts)
	}
	for i, w := range wants {
		if stmts[i].Name != w.name || stmts[i].SQL != w.sql || stmts[i].Expect != w.expect {
			t.Errorf("statement %d =\n  %q %q %q\nwant\n  %q %q %q", i+1, stmts[i].Name, stmts[i].SQL, stmts[i].Expect, w.name, w.sql, w.expect)
		}
	}
	if stmts[2].ForEach != "items" || stmts[2].As != "item" {
		t.Errorf("the items step lost its forEach: %+v", stmts[2])
	}

	// The whole list parses as an Oracle sink's config.
	if _, err := ParseConfig(map[string]string{"driver": "oracle", "mode": "guided", "steps": orderSteps}); err != nil {
		t.Fatalf("ParseConfig(guided oracle): %v", err)
	}
}

func TestGuidedOracleNamesAndOtherActions(t *testing.T) {
	stmts := compileFor(t, "oracle", `[
		{"name":"a","table":"app.Orders","action":"insert","columns":[{"column":"Total","value":"{{t}}"}],"returning":["id"]},
		{"name":"b","table":"orders","action":"delete","match":["id"],"columns":[{"column":"id","value":"{{a.id}}"}],"returning":["total"]},
		{"name":"c","table":"orders","action":"upsert","match":["id"],"columns":[{"column":"id","value":"{{x}}"}]}
	]`)
	wants := []string{
		// A name with no capitals is folded, as Oracle folds an unquoted one;
		// one that has capitals is taken as written.
		`INSERT INTO "APP"."Orders" ("Total") VALUES ({{t}}) RETURNING "ID"`,
		`DELETE FROM "ORDERS" WHERE "ID" = {{a.id}} RETURNING "TOTAL"`,
		// Nothing to update: insert-only MERGE, which Oracle allows.
		`MERGE INTO "ORDERS" t USING (SELECT {{x}} AS "ID" FROM dual) s ON (t."ID" = s."ID") WHEN NOT MATCHED THEN INSERT ("ID") VALUES (s."ID")`,
	}
	for i, w := range wants {
		if stmts[i].SQL != w {
			t.Errorf("statement %d = %q, want %q", i+1, stmts[i].SQL, w)
		}
	}
}

func TestOracleRowKeysAreLowerCase(t *testing.T) {
	got := lowerKeys(map[string]any{"ID": 1, "Created_At": "x"})
	if want := map[string]any{"id": 1, "created_at": "x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lowerKeys = %v, want %v", got, want)
	}
}
