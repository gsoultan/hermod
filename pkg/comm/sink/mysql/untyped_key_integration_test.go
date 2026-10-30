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

package mysql

import (
	"fmt"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// A key column mapped without a type used to be created as TEXT PRIMARY KEY,
// which MySQL and MariaDB both refuse ("BLOB/TEXT column used in key
// specification without a key length"). So the plainest possible mapping — mark
// "id" as the key, leave the type empty — could not create its table, and in a
// transactional group that failure aborted every member's write as well.
func TestUntypedKeyCreatesItsTable(t *testing.T) {
	dsn, db, table := requireMappedMySQL(t)

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "id", IsPrimaryKey: true},
		{SourceField: "name", TargetColumn: "name"},
		{SourceField: "city", TargetColumn: "city", IsNullable: true},
	}
	sink := NewMySQLSink(dsn, table, mappings, false, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	if err := sink.WriteBatch(t.Context(), []hermod.Message{
		mappedMsg(t, "a", "Ada", "London", hermod.OpCreate),
		mappedMsg(t, "a", "Ada", "Paris", hermod.OpUpdate),
	}); err != nil {
		t.Fatalf("writing with an untyped key mapping: %v", err)
	}

	var n int
	if err := db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%s has %d rows, want 1: the key must still be a key", table, n)
	}
}
