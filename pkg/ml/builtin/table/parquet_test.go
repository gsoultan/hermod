// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package table

import (
	"math"
	"os"
	"strings"
	"testing"
)

// testdata/flat.parquet was written by pyarrow, as pandas' to_parquet writes
// a file: one column of each type a dataset is likely to hold.
func TestParseParquetTypesColumnsAsTheWorkerDoes(t *testing.T) {
	raw, err := os.ReadFile("testdata/flat.parquet")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := ParseParquet(raw)
	if err != nil {
		t.Fatal(err)
	}
	if tb.Rows != 3 {
		t.Fatalf("rows = %d, want 3", tb.Rows)
	}
	var names []string
	for _, c := range tb.Columns {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "qty,small,price,ok,sku,day,at,at_utc,amount,plan,empty" {
		t.Errorf("columns = %s: the file's order is kept", got)
	}
	num := func(name string, want ...float64) {
		t.Helper()
		c := tb.Column(name)
		if c == nil || c.Kind != Number {
			t.Fatalf("%s = %+v, want a number column", name, c)
		}
		for i, w := range want {
			if math.IsNaN(w) != math.IsNaN(c.Num[i]) || (!math.IsNaN(w) && math.Abs(c.Num[i]-w) > 1e-9) {
				t.Errorf("%s[%d] = %v, want %v", name, i, c.Num[i], w)
			}
		}
	}
	text := func(name string, want ...string) {
		t.Helper()
		c := tb.Column(name)
		if c == nil || c.Kind != String {
			t.Fatalf("%s = %+v, want a string column", name, c)
		}
		for i, w := range want {
			if got := c.Text(i); got != w {
				t.Errorf("%s[%d] = %q, want %q", name, i, got, w)
			}
		}
	}
	nan := math.NaN()
	num("qty", 3, nan, 7)
	num("small", 1, 2, 3)
	num("price", 1.5, 2.25, nan)
	num("amount", 12.5, -3.05, nan)
	if ok := tb.Column("ok"); ok.Kind != Bool || ok.Num[0] != 1 || ok.Num[1] != 0 || !ok.Missing(2) {
		t.Errorf("ok = %+v", ok)
	}
	text("sku", "a-1", "", "c-3")
	// Dates and times as the worker's isoformat writes them.
	text("day", "2026-09-01", "2026-09-02", "")
	text("at", "2026-09-01T10:30:00", "2026-09-01T10:30:00.250000", "")
	text("at_utc", "2026-09-01T10:30:00+00:00", "", "")
	text("plan", "basic", "pro", "basic")
	if e := tb.Column("empty"); e == nil || e.Kind != Number || !e.Missing(0) {
		t.Errorf("empty = %+v: a column with no value is held as number, as from JSON", e)
	}
}

func TestParseParquetRefusesWhatItCannotRead(t *testing.T) {
	nested, err := os.ReadFile("testdata/nested.parquet")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  []byte
		want string
	}{
		{"empty", nil, "the Parquet file is empty"},
		{"not parquet", []byte("customer,amount\nC-1,10\n"), "could not read the Parquet file"},
		{"nested", nested, `column "tags" is a list, map or nested group`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseParquet(tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one saying %q", err, tc.want)
			}
		})
	}
}
