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
	"strings"
	"testing"
)

func cfgMap(statements string) map[string]string {
	return map[string]string{"driver": "sqlite", "statements": statements}
}

func TestParseConfigAcceptsDependentStatements(t *testing.T) {
	cfg, err := ParseConfig(cfgMap(`[
		{"name":"order","sql":"INSERT INTO orders (customer) VALUES ({{customer}}) RETURNING id"},
		{"name":"items","forEach":"items","as":"item","sql":"INSERT INTO order_items (order_id, sku) VALUES ({{order.id}}, {{item.sku}})"}
	]`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Driver != "sqlite" {
		t.Errorf("Driver = %q, want sqlite", cfg.Driver)
	}
	if cfg.OnUnresolved != OnUnresolvedFail {
		t.Errorf("OnUnresolved = %q, want %q: a NULL foreign key is what this sink exists to prevent", cfg.OnUnresolved, OnUnresolvedFail)
	}
	if len(cfg.Statements) != 2 || cfg.Statements[1].As != "item" || cfg.Statements[1].ForEach != "items" {
		t.Fatalf("statements = %+v", cfg.Statements)
	}
}

func TestParseConfigNamesUnnamedStatements(t *testing.T) {
	cfg, err := ParseConfig(cfgMap(`[{"sql":"UPDATE t SET a = 1"},{"sql":"DELETE FROM t WHERE a = 2"}]`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Statements[0].Name != "step1" || cfg.Statements[1].Name != "step2" {
		t.Errorf("names = %q, %q; want step1, step2", cfg.Statements[0].Name, cfg.Statements[1].Name)
	}
}

func TestParseConfigDefaultsForEachAliasToItem(t *testing.T) {
	cfg, err := ParseConfig(cfgMap(`[{"name":"lines","forEach":"lines","sql":"INSERT INTO l (v) VALUES ({{item.v}})"}]`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Statements[0].As != "item" {
		t.Errorf("As = %q, want item", cfg.Statements[0].As)
	}
}

func TestParseConfigRefuses(t *testing.T) {
	long := make([]string, MaxStatements+1)
	for i := range long {
		long[i] = `{"sql":"UPDATE t SET a = 1"}`
	}
	cases := []struct {
		name string
		cfg  map[string]string
		want string
	}{
		{"no driver", map[string]string{"statements": `[{"sql":"UPDATE t SET a=1"}]`}, "driver"},
		{"unsupported driver", map[string]string{"driver": "clickhouse", "statements": `[{"sql":"UPDATE t SET a=1"}]`}, "clickhouse"},
		{"oracle not yet", map[string]string{"driver": "oracle", "statements": `[{"sql":"UPDATE t SET a=1"}]`}, "oracle"},
		{"no statements", cfgMap(``), "at least one statement"},
		{"empty list", cfgMap(`[]`), "at least one statement"},
		{"not json", cfgMap(`INSERT INTO t VALUES (1)`), "statements"},
		{"too many", cfgMap("[" + strings.Join(long, ",") + "]"), "at most"},
		{"empty sql", cfgMap(`[{"name":"a","sql":"  "}]`), "no SQL"},
		{"bad name", cfgMap(`[{"name":"Order-1","sql":"UPDATE t SET a=1"}]`), "Order-1"},
		{"duplicate name", cfgMap(`[{"name":"a","sql":"UPDATE t SET a=1"},{"name":"a","sql":"UPDATE t SET a=2"}]`), "more than once"},
		{"forward reference", cfgMap(`[{"name":"a","sql":"UPDATE t SET x={{b.id}}"},{"name":"b","sql":"INSERT INTO t (a) VALUES (1) RETURNING id"}]`), "runs later"},
		{"self reference", cfgMap(`[{"name":"a","sql":"INSERT INTO t (x) VALUES ({{a.id}}) RETURNING id"}]`), "its own"},
		{"reference into forEach", cfgMap(`[{"name":"a","forEach":"xs","sql":"INSERT INTO t (x) VALUES ({{item}}) RETURNING id"},{"name":"b","sql":"UPDATE t SET y={{a.id}}"}]`), "runs once per item"},
		{"alias shadows statement", cfgMap(`[{"name":"a","sql":"INSERT INTO t (x) VALUES (1) RETURNING id"},{"name":"b","forEach":"xs","as":"a","sql":"INSERT INTO u (x) VALUES ({{a.id}})"}]`), "alias"},
		{"ddl", cfgMap(`[{"sql":"DROP TABLE orders"}]`), "DROP"},
		{"ddl lower case", cfgMap(`[{"sql":"  truncate orders"}]`), "TRUNCATE"},
		{"two statements in one", cfgMap(`[{"sql":"UPDATE t SET a=1; DELETE FROM t"}]`), "one statement"},
		{"bad expect", cfgMap(`[{"sql":"UPDATE t SET a=1","expect":"many"}]`), "expect"},
		{"bad on_unresolved", map[string]string{"driver": "sqlite", "on_unresolved": "skip", "statements": `[{"sql":"UPDATE t SET a=1"}]`}, "on_unresolved"},
		{"bad isolation", map[string]string{"driver": "postgres", "isolation": "chaos", "statements": `[{"sql":"UPDATE t SET a=1"}]`}, "isolation"},
		{"bad timeout", map[string]string{"driver": "sqlite", "statement_timeout": "soon", "statements": `[{"sql":"UPDATE t SET a=1"}]`}, "statement_timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig(tc.cfg)
			if err == nil {
				t.Fatalf("ParseConfig accepted %v", tc.cfg)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestParseConfigAllowsSemicolonInsideLiteralsAndTrailing(t *testing.T) {
	_, err := ParseConfig(cfgMap(`[{"sql":"UPDATE t SET note = 'a;b' -- c;d\n WHERE id = 1;"}]`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
}

func TestReturnsRows(t *testing.T) {
	cases := map[string]bool{
		"INSERT INTO t (a) VALUES (1) RETURNING id":                                                  true,
		"insert into t (a) output inserted.id values (1)":                                            true,
		"SELECT id FROM t WHERE a = 1":                                                               true,
		"UPDATE t SET a = 'returning' WHERE id = 1":                                                  false,
		"INSERT INTO t (a) VALUES (1)":                                                               false,
		"DELETE FROM t WHERE id = 1 -- returning nothing":                                            false,
		"WITH x AS (SELECT 1 AS v) SELECT v FROM x":                                                  true,
		"MERGE INTO t USING s ON t.k = s.k WHEN MATCHED THEN UPDATE SET a = s.a OUTPUT inserted.id;": true,
	}
	for sqlText, want := range cases {
		if got := returnsRows(sqlText); got != want {
			t.Errorf("returnsRows(%q) = %v, want %v", sqlText, got, want)
		}
	}
}
