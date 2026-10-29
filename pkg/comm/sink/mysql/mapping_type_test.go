// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package mysql

import (
	"testing"

	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

func TestMappingColumnDefTypes(t *testing.T) {
	cases := []struct {
		name string
		m    sqlutil.ColumnMapping
		want string
	}{
		// MySQL cannot index TEXT without a prefix length, so an untyped key
		// takes the type the sink's own default table uses for its key.
		{"untyped key", sqlutil.ColumnMapping{TargetColumn: "id", IsPrimaryKey: true}, "`id` VARCHAR(255) PRIMARY KEY"},
		{"typed key", sqlutil.ColumnMapping{TargetColumn: "id", DataType: "BIGINT", IsPrimaryKey: true}, "`id` BIGINT PRIMARY KEY"},
		{"untyped column", sqlutil.ColumnMapping{TargetColumn: "name"}, "`name` TEXT NOT NULL"},
		{"untyped nullable", sqlutil.ColumnMapping{TargetColumn: "city", IsNullable: true}, "`city` TEXT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mappingColumnDef(c.m); got != c.want {
				t.Errorf("mappingColumnDef = %q, want %q", got, c.want)
			}
		})
	}
}
