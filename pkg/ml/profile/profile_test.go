// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package profile

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

var now = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// sample builds a dataset sample from columns of values, typed as given.
func sample(rows int, cols ...col) worker.DatasetSample {
	s := worker.DatasetSample{Name: "d", Rows: rows, Values: map[string][]any{}}
	for _, c := range cols {
		s.Columns = append(s.Columns, worker.Column{Name: c.name, Type: c.typ})
		s.Values[c.name] = c.values
		s.SampleRows = len(c.values)
	}
	return s
}

type col struct {
	name, typ string
	values    []any
}

func numbers(name string, vs ...float64) col {
	out := make([]any, len(vs))
	for i, v := range vs {
		if math.IsNaN(v) {
			continue
		}
		out[i] = v
	}
	return col{name, "number", out}
}

func texts(name string, vs ...string) col {
	out := make([]any, len(vs))
	for i, v := range vs {
		if v != "" {
			out[i] = v
		}
	}
	return col{name, "string", out}
}

func repeat[T any](v T, n int) []T {
	out := make([]T, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func seq(n int, f func(i int) float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

func column(t *testing.T, p Profile, name string) Column {
	t.Helper()
	for _, c := range p.Columns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no column %q in %+v", name, p.Columns)
	return Column{}
}

func warning(p Profile, code, column string) *Warning {
	for i, w := range p.Warnings {
		if w.Code == code && w.Column == column {
			return &p.Warnings[i]
		}
	}
	return nil
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestNumbersSummaryAndHistogram(t *testing.T) {
	p := Compute(sample(5, numbers("amount", 1, 2, 3, 4, math.NaN())), now)
	c := column(t, p, "amount")
	if c.Kind != KindNumber || c.Missing != 1 || !near(c.MissingShare, 0.2) || c.Distinct != 4 {
		t.Fatalf("amount = %+v", c)
	}
	n := c.Numbers
	if n == nil || n.Min != 1 || n.Max != 4 || !near(n.Mean, 2.5) || !near(n.Median, 2.5) || !near(n.Std, math.Sqrt(1.25)) || !n.Integer {
		t.Fatalf("numbers = %+v", n)
	}
	total := 0
	for _, b := range c.Histogram {
		total += b.Rows
	}
	if total != 4 || c.Histogram[0].From != 1 || c.Histogram[len(c.Histogram)-1].To != 4 {
		t.Errorf("histogram = %+v, want the 4 values from 1 to 4", c.Histogram)
	}
	if p.Rows != 5 || p.ColumnCount != 1 || p.SampleRows != 5 || p.Sampled {
		t.Errorf("profile = %+v", p)
	}
}

func TestTextTopValuesWithShares(t *testing.T) {
	p := Compute(sample(6, texts("plan", "basic", "pro", "basic", "team", "basic", "pro")), now)
	c := column(t, p, "plan")
	if c.Kind != KindCategory || c.Distinct != 3 {
		t.Fatalf("plan = %+v", c)
	}
	if len(c.Top) != 3 || c.Top[0].Value != "basic" || c.Top[0].Rows != 3 || !near(c.Top[0].Share, 0.5) || c.Top[1].Value != "pro" {
		t.Errorf("top = %+v", c.Top)
	}
}

func TestDatesRangeAndRowsPerMonth(t *testing.T) {
	p := Compute(sample(4, texts("order_date", "2026-08-30", "2026-09-01T10:00:00+00:00", "2026-09-15", "2026-07-01 08:00:00")), now)
	c := column(t, p, "order_date")
	if c.Kind != KindDate || c.Dates == nil {
		t.Fatalf("order_date = %+v", c)
	}
	d := c.Dates
	if d.Min != "2026-07-01" || d.Max != "2026-09-15" || d.Per != "month" {
		t.Errorf("dates = %+v", d)
	}
	var got []string
	for _, b := range d.Counts {
		got = append(got, fmt.Sprintf("%s=%d", b.Label, b.Rows))
	}
	if strings.Join(got, ",") != "2026-07=1,2026-08=1,2026-09=2" {
		t.Errorf("per month = %v; a month without rows is still shown", got)
	}
}

func TestWarnings(t *testing.T) {
	ids := make([]string, 40)
	for i := range ids {
		ids[i] = fmt.Sprintf("C-%03d", i)
	}
	sparse := repeat("", 40)
	sparse[0], sparse[1] = "x", "y"
	free := make([]string, 40)
	for i := range free {
		free[i] = fmt.Sprintf("note %d", i%30)
	}
	skewed := seq(40, func(i int) float64 { return 1 })
	skewed[39] = 1000
	churn := repeat("no", 40)
	churn[0] = "yes"
	when := repeat("2026-09-01", 40)
	when[3] = "2031-01-01"
	p := Compute(sample(40,
		texts("customer_id", ids...),
		texts("comment", sparse...),
		texts("country", repeat("NL", 40)...),
		texts("note", free...),
		numbers("amount", skewed...),
		texts("churned", churn...),
		texts("ordered", when...),
	), now)

	for _, tc := range []struct{ code, column, says string }{
		{CodeIDLike, "customer_id", "looks like an ID"},
		{CodeMostlyEmpty, "comment", "95% empty"},
		{CodeConstant, "country", `"NL" on every row`},
		{CodeOutliers, "amount", "far outside the usual range"},
		{CodeSkewed, "amount", "skewed"},
		{CodeImbalanced, "churned", `"no" is 98% of rows`},
		{CodeFutureDates, "ordered", "1 date in ordered is in the future"},
		{CodeFewRows, "", "Only 40 rows"},
	} {
		w := warning(p, tc.code, tc.column)
		if w == nil {
			t.Errorf("no %s warning on %q; warnings: %+v", tc.code, tc.column, p.Warnings)
			continue
		}
		if !strings.Contains(w.Message, tc.says) || w.Fix == "" {
			t.Errorf("%s on %q = %+v, want it to say %q and how to fix it", tc.code, tc.column, w, tc.says)
		}
	}
	if c := column(t, p, "note"); c.Kind != KindCategory {
		t.Errorf("note = %s: 30 values in 40 rows is still a category", c.Kind)
	}
	if w := warning(p, CodeIDLike, "ordered"); w != nil {
		t.Errorf("a date column was called an ID: %+v", w)
	}
}

func TestHighCardinalityText(t *testing.T) {
	addr := make([]string, 200)
	for i := range addr {
		addr[i] = fmt.Sprintf("%d Main Street", i%150)
	}
	p := Compute(sample(200, texts("address", addr...)), now)
	if c := column(t, p, "address"); c.Kind != KindText {
		t.Errorf("address = %s, want text", c.Kind)
	}
	if w := warning(p, CodeHighCardinality, "address"); w == nil || !strings.Contains(w.Message, "150 different values") {
		t.Errorf("warning = %+v", w)
	}
}

func TestDuplicateRows(t *testing.T) {
	p := Compute(sample(4, texts("a", "x", "x", "y", "x"), numbers("b", 1, 1, 2, 1)), now)
	if p.DuplicateRows != 2 {
		t.Errorf("duplicates = %d, want 2", p.DuplicateRows)
	}
	if w := warning(p, CodeDuplicateRows, ""); w == nil || !strings.Contains(w.Message, "2 rows (50%)") {
		t.Errorf("warning = %+v", w)
	}
}

func TestCorrelationsStrongestFirst(t *testing.T) {
	// Not whole numbers: 50 different whole numbers in 50 rows are an ID.
	x := seq(50, func(i int) float64 { return float64(i) + 0.5 })
	twice := seq(50, func(i int) float64 { return 2*float64(i) + 1.5 })
	noisy := seq(50, func(i int) float64 { return float64(i) + float64((i*7)%11)*3 + 0.5 })
	down := seq(50, func(i int) float64 { return -float64(i % 10) })
	p := Compute(sample(50, numbers("x", x...), numbers("twice", twice...), numbers("noisy", noisy...), numbers("down", down...)), now)
	if len(p.Correlations) == 0 {
		t.Fatal("no correlations")
	}
	top := p.Correlations[0]
	if top.A != "x" || top.B != "twice" || !near(top.R, 1) {
		t.Errorf("strongest = %+v, want x and twice at 1", top)
	}
	for i := 1; i < len(p.Correlations); i++ {
		if math.Abs(p.Correlations[i].R) > math.Abs(p.Correlations[i-1].R) {
			t.Errorf("correlations are not strongest first: %+v", p.Correlations)
		}
	}
	if w := warning(p, CodeCorrelated, "twice"); w == nil {
		t.Errorf("x and twice move together exactly; warnings: %+v", p.Warnings)
	}
}

func TestBoolAndEmptyColumns(t *testing.T) {
	p := Compute(sample(3, col{"ok", "bool", []any{true, false, true}}, col{"blank", "number", []any{nil, nil, nil}}), now)
	ok := column(t, p, "ok")
	if ok.Kind != KindYesNo || len(ok.Top) != 2 || ok.Top[0].Value != "true" || ok.Top[0].Rows != 2 {
		t.Errorf("ok = %+v", ok)
	}
	if b := column(t, p, "blank"); b.Kind != KindEmpty || b.Missing != 3 {
		t.Errorf("blank = %+v", b)
	}
	if w := warning(p, CodeEmpty, "blank"); w == nil {
		t.Errorf("no warning on an empty column: %+v", p.Warnings)
	}
}

func TestSampledProfileSaysSo(t *testing.T) {
	s := sample(1_000_000, numbers("a", 1, 2, 3))
	p := Compute(s, now)
	if !p.Sampled || p.Rows != 1_000_000 || p.SampleRows != 3 {
		t.Errorf("profile = %+v, want it marked as computed on a sample", p)
	}
	if w := warning(p, CodeFewRows, ""); w != nil {
		t.Errorf("a million rows were called few: %+v", w)
	}
}

func TestEmptyDataset(t *testing.T) {
	p := Compute(worker.DatasetSample{Name: "d"}, now)
	if p.Rows != 0 || len(p.Columns) != 0 {
		t.Errorf("profile = %+v", p)
	}
	if w := warning(p, CodeFewRows, ""); w == nil || w.Severity != SeverityHigh {
		t.Errorf("an empty dataset has no warning: %+v", p.Warnings)
	}
}

func TestAConstantDateColumnIsCalledConstant(t *testing.T) {
	p := Compute(sample(3, texts("day", "2026-09-01", "2026-09-01", "")), now)
	if w := warning(p, CodeConstant, "day"); w == nil || !strings.Contains(w.Message, "the same value on every row") {
		t.Errorf("warning = %+v", w)
	}
}
