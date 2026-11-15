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

// A statement that ends or replaces the sink's transaction breaks the one
// promise the sink makes: a COMMIT halfway down the list keeps the parent when
// the child then fails. On MySQL a procedure call can commit inside itself.
// These are refused when the sink is saved, as schema changes are, so neither
// a live write nor a Test can run them.
func TestParseConfigRefusesStatementsThatControlTheTransaction(t *testing.T) {
	cases := []struct {
		driver, sql, want string
	}{
		{"postgres", "COMMIT", "COMMIT"},
		{"postgres", "rollback", "ROLLBACK"},
		{"postgres", "BEGIN", "BEGIN"},
		{"mysql", "START TRANSACTION", "transaction"},
		{"postgres", "SAVEPOINT a", "SAVEPOINT"},
		{"mssql", "BEGIN TRANSACTION", "transaction"},
		{"mssql", "COMMIT TRANSACTION", "COMMIT"},
		{"mysql", "SET autocommit = 1", "AUTOCOMMIT"},
		{"mysql", "CALL archive_orders({{id}})", "CALL"},
		{"mysql", "LOCK TABLES orders WRITE", "LOCK"},
	}
	for _, tc := range cases {
		stmts := `[{"name":"a","sql":"` + tc.sql + `"}]`
		_, err := ParseConfig(map[string]string{"driver": tc.driver, "statements": stmts})
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
			t.Errorf("%s %q: err = %v, want a refusal naming %s", tc.driver, tc.sql, err, tc.want)
		}
	}
}

// The words alone are not enough: inside a string or a column they are data.
func TestParseConfigAllowsTheWordsAsData(t *testing.T) {
	for _, sql := range []string{
		`UPDATE t SET note = 'COMMIT' WHERE id = {{id}}`,
		`UPDATE t SET \"commit\" = 1 WHERE id = {{id}}`,
		`INSERT INTO audit (action) VALUES ({{rollback}})`,
	} {
		stmts := `[{"name":"a","sql":"` + sql + `"}]`
		if _, err := ParseConfig(map[string]string{"driver": "postgres", "statements": stmts}); err != nil {
			t.Errorf("%q was refused: %v", sql, err)
		}
	}
}
