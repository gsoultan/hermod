// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package table

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/tealeg/xlsx"
)

func TestParseXLSXReadsTheFirstSheet(t *testing.T) {
	f := xlsx.NewFile()
	sh, err := f.AddSheet("data")
	if err != nil {
		t.Fatal(err)
	}
	head := sh.AddRow()
	for _, h := range []string{"qty", "ok", "sku"} {
		head.AddCell().SetString(h)
	}
	for i, row := range []struct {
		qty float64
		ok  bool
		sku string
	}{{3, true, "a-1"}, {4.5, false, "b-2"}} {
		r := sh.AddRow()
		r.AddCell().SetFloat(row.qty)
		r.AddCell().SetBool(row.ok)
		if i == 0 {
			r.AddCell().SetString(row.sku)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	tb, err := ParseXLSX(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if tb.Rows != 2 {
		t.Fatalf("rows = %d", tb.Rows)
	}
	if q := tb.Column("qty"); q.Kind != Number || q.Num[1] != 4.5 {
		t.Errorf("qty = %+v", q)
	}
	if o := tb.Column("ok"); o.Kind != Bool || o.Num[0] != 1 || o.Num[1] != 0 {
		t.Errorf("ok = %+v", o)
	}
	if s := tb.Column("sku"); s.Kind != String || s.Str[0] != "a-1" || !s.Null[1] {
		t.Errorf("sku = %+v", s)
	}
	if _, err := ParseXLSX([]byte("not a workbook")); err == nil {
		t.Error("a file that is not a workbook was accepted")
	}
}

func TestFromRecordsTypesColumnsAsTheWorkerDoes(t *testing.T) {
	tb, err := FromRecords([]map[string]any{
		{"age": 30.0, "name": "ann", "vip": true, "mixed": 1.0, "obj": map[string]any{"a": 1.0}},
		{"age": nil, "name": nil, "vip": false, "mixed": "x"},
		{"age": 41.5, "vip": nil, "mixed": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tb.Rows != 3 {
		t.Fatalf("rows = %d, want 3", tb.Rows)
	}
	want := map[string]string{"age": Number, "name": String, "vip": Bool, "mixed": String, "obj": String}
	if got := len(tb.Columns); got != len(want) {
		t.Fatalf("got %d columns, want %d", got, len(want))
	}
	// A map keeps no order: the keys new in a row are sorted.
	if got := strings.Join(names(tb), ","); got != "age,mixed,name,obj,vip" {
		t.Fatalf("columns = %s", got)
	}
	for _, c := range tb.Columns {
		if c.Kind != want[c.Name] {
			t.Errorf("column %q is %s, want %s", c.Name, c.Kind, want[c.Name])
		}
	}
	age := tb.Column("age")
	if age.Num[0] != 30 || !math.IsNaN(age.Num[1]) || age.Num[2] != 41.5 {
		t.Errorf("age = %v", age.Num)
	}
	mixed := tb.Column("mixed")
	if got := mixed.Str; got[0] != "1" || got[1] != "x" || got[2] != "true" {
		t.Errorf("mixed = %q, want 1, x, true rendered as text", got)
	}
	if obj := tb.Column("obj"); obj.Str[0] != `{"a":1}` || !obj.Null[1] {
		t.Errorf("obj = %q null %v", obj.Str, obj.Null)
	}
	if name := tb.Column("name"); !name.Null[1] || !name.Null[2] || name.Null[0] {
		t.Errorf("name nulls = %v", name.Null)
	}
}

func TestFromJSONKeepsTheColumnsInTheOrderTheRowsNameThem(t *testing.T) {
	tb, err := FromJSON([]byte(`[{"z": 1, "a": "x"}, {"m": true, "z": 2}]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(tb), ","); got != "z,a,m" {
		t.Fatalf("columns = %s, want z,a,m", got)
	}
	for _, bad := range []string{`{"a": 1}`, `[1, 2]`, `[{"a": NaN}]`} {
		if _, err := FromJSON([]byte(bad)); err == nil {
			t.Errorf("FromJSON(%s) was accepted", bad)
		}
	}
}

func TestFromRecordsRefusesRowsWithoutColumns(t *testing.T) {
	if _, err := FromRecords([]map[string]any{{}, {}}); err == nil {
		t.Fatal("empty rows were accepted")
	}
}

func TestToTextRendersAsPython(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{3.0, "3"}, {-2.0, "-2"}, {0.1, "0.1"}, {1234567.5, "1234567.5"}, {1e16, "1e+16"},
		{1.5e-7, "1.5e-07"}, {true, "true"}, {false, "false"}, {"x", "x"}, {[]any{1.0, "a"}, `[1,"a"]`},
	}
	for _, c := range cases {
		if got := ToText(c.in); got != c.want {
			t.Errorf("ToText(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRecordsRoundTrip(t *testing.T) {
	tb, _ := FromRecords([]map[string]any{{"n": 2.0, "s": "a", "b": true}, {"n": 2.5, "s": nil, "b": nil}})
	got := tb.Records(0, 2)
	if got[0]["n"] != 2.0 || got[0]["s"] != "a" || got[0]["b"] != true {
		t.Errorf("row 0 = %v", got[0])
	}
	if got[1]["s"] != nil || got[1]["b"] != nil || got[1]["n"] != 2.5 {
		t.Errorf("row 1 = %v", got[1])
	}
}

func TestParseCSVInfersTypesLikePandas(t *testing.T) {
	csv := "\xef\xbb\xbfid,price,vip,city,note\n1,9.5,True,Oslo,\n2,NA,False,,n/a\n3,12,,Bergen,hi\n"
	tb, err := ParseCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, c := range tb.Columns {
		kinds[c.Name] = c.Kind
	}
	want := map[string]string{"id": Number, "price": Number, "vip": Bool, "city": String, "note": String}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("%s is %s, want %s", k, kinds[k], v)
		}
	}
	if p := tb.Column("price"); !math.IsNaN(p.Num[1]) || p.Num[2] != 12 {
		t.Errorf("price = %v", p.Num)
	}
	if v := tb.Column("vip"); v.Num[0] != 1 || v.Num[1] != 0 || !math.IsNaN(v.Num[2]) {
		t.Errorf("vip = %v", v.Num)
	}
	if c := tb.Column("city"); !c.Null[1] || c.Str[2] != "Bergen" {
		t.Errorf("city = %q %v", c.Str, c.Null)
	}
	if n := tb.Column("note"); !n.Null[0] || !n.Null[1] || n.Str[2] != "hi" {
		t.Errorf("note = %q %v", n.Str, n.Null)
	}
}

func TestParseCSVRefusesBadFiles(t *testing.T) {
	for name, body := range map[string]string{
		"empty":     "  \n",
		"duplicate": "a,a\n1,2\n",
		"ragged":    "a,b\n1,2,3\n",
	} {
		if _, err := ParseCSV(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func names(tb *Table) []string {
	out := make([]string, len(tb.Columns))
	for i, c := range tb.Columns {
		out[i] = c.Name
	}
	return out
}
