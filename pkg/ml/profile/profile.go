// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package profile describes a dataset for someone deciding what to do with
// it: per column its kind, how much is missing, how many different values it
// has, and its numbers, top values or dates with a small chart; the data's
// problems in plain words, each with how to fix it; and the numeric columns
// that move together.
//
// A profile is computed over a sample of the dataset (worker.DatasetSample),
// which either engine gives: Hermod's built-in one and the hermod-ml worker
// keep their datasets apart, and this is the one place both are described,
// so the same data reads the same whichever engine holds it. The thresholds
// follow the training pre-flight check (ml/worker/hermod_ml/precheck.py,
// pkg/ml/builtin/data.go), so a column called an ID here is one training
// leaves out.
package profile

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// The kinds of column, by what they hold.
const (
	KindNumber   = "number"
	KindCategory = "category"
	KindText     = "text"
	KindDate     = "date"
	KindYesNo    = "yes_no"
	KindID       = "id"
	KindEmpty    = "empty"
)

// The warnings' codes.
const (
	CodeFewRows         = "few_rows"
	CodeEmpty           = "empty_column"
	CodeMostlyEmpty     = "mostly_empty"
	CodeConstant        = "constant"
	CodeDuplicateRows   = "duplicate_rows"
	CodeIDLike          = "id_like"
	CodeHighCardinality = "high_cardinality"
	CodeOutliers        = "outliers"
	CodeSkewed          = "skewed"
	CodeImbalanced      = "imbalanced"
	CodeFutureDates     = "future_dates"
	CodeCorrelated      = "correlated"
)

// How much a warning matters: high stops a good model, medium is worth
// fixing, low is worth knowing.
const (
	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"
)

// Thresholds. MinRows, FewRows, MostlyEmpty and IDLike are the training
// pre-flight check's; the others are this package's own.
const (
	MinRows     = 10
	FewRows     = 100
	MostlyEmpty = 0.6
	IDLike      = 0.95
	// idMinRows is the fewest values an ID is called on: in a handful of
	// rows every value is often different by chance.
	idMinRows = 20
	// A string column with more than textDistinct values, over textShare of
	// its values, is free text rather than a category.
	textDistinct = 50
	textShare    = 0.5
	// dateShare of a string column's values must read as dates.
	dateShare = 0.9
	// Values more than outlierIQR interquartile ranges beyond the quartiles
	// are outliers (or, when the quartiles meet, more than outlierStd
	// standard deviations from the mean).
	outlierIQR = 3.0
	outlierStd = 4.0
	// A skewness of skewAt or more is called skewed.
	skewAt = 2.0
	// A column with at most imbalanceClasses values whose most common value
	// is imbalanceShare of the rows is imbalanced.
	imbalanceShare   = 0.9
	imbalanceClasses = 20
	// Correlations: the strongest topPairs pairs at or above minR, measured
	// on at least minPairs rows, over the first maxCorrColumns numeric
	// columns. Pairs at or above sameR are warned about.
	topPairs       = 10
	minR           = 0.3
	minPairs       = 10
	maxCorrColumns = 50
	sameR          = 0.98
	// topValues is how many values a column's top lists; histogramBins how
	// many bins a numeric chart has; monthsMax the longest span charted by
	// month rather than by year.
	topValues     = 10
	histogramBins = 10
	monthsMax     = 36
)

// Profile describes a dataset.
type Profile struct {
	Dataset string `json:"dataset"`
	// Rows is how many rows the dataset holds; SampleRows how many the
	// profile was computed on, all of them unless Sampled. MaxSampleRows is
	// the most a sample takes.
	Rows          int  `json:"rows"`
	ColumnCount   int  `json:"column_count"`
	SampleRows    int  `json:"sample_rows"`
	Sampled       bool `json:"sampled"`
	MaxSampleRows int  `json:"max_sample_rows"`
	// DuplicateRows are the sampled rows that repeat an earlier one exactly.
	DuplicateRows int           `json:"duplicate_rows"`
	Columns       []Column      `json:"columns"`
	Warnings      []Warning     `json:"warnings"`
	Correlations  []Correlation `json:"correlations"`
	ComputedAt    time.Time     `json:"computed_at"`
}

// Column describes one column.
type Column struct {
	Name string `json:"name"`
	// Type is the column's stored type: number, string or bool. Kind is what
	// it holds (KindNumber, KindDate, …).
	Type         string   `json:"type"`
	Kind         string   `json:"kind"`
	Missing      int      `json:"missing"`
	MissingShare float64  `json:"missing_share"`
	Distinct     int      `json:"distinct"`
	Numbers      *Numbers `json:"numbers,omitempty"`
	// Top is the most common values, with the share of the others.
	Top        []Share `json:"top,omitempty"`
	OtherShare float64 `json:"other_share,omitempty"`
	Dates      *Dates  `json:"dates,omitempty"`
	// Histogram is a numeric column's values in equal-width bins.
	Histogram []Bin `json:"histogram,omitempty"`
}

// Numbers summarise a numeric column's values.
type Numbers struct {
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	// Q1 and Q3 are the quartiles: the middle half of the values is
	// between them.
	Q1   float64 `json:"q1"`
	Q3   float64 `json:"q3"`
	Std  float64 `json:"std"`
	Skew float64 `json:"skew"`
	// Integer is true when every value is a whole number.
	Integer  bool `json:"integer"`
	Outliers int  `json:"outliers"`
}

// Share is one value and how many of the column's values it is.
type Share struct {
	Value string  `json:"value"`
	Rows  int     `json:"rows"`
	Share float64 `json:"share"`
}

// Dates are a date column's range and rows per month (or per year, for a
// span longer than three years), every month or year in the range listed.
type Dates struct {
	Min    string   `json:"min"`
	Max    string   `json:"max"`
	Per    string   `json:"per"`
	Counts []Bucket `json:"counts"`
	// Future is how many dates are after the day the profile was computed.
	Future int `json:"future"`
}

// Bucket is one month ("2026-09") or year ("2026") and its rows.
type Bucket struct {
	Label string `json:"label"`
	Rows  int    `json:"rows"`
}

// Bin is the values in (From, To], the first bin [From, To].
type Bin struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
	Rows int     `json:"rows"`
}

// Warning is one problem with the data, what it is and how to fix it.
type Warning struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	// Column is the column it is about, empty for the whole dataset; a
	// correlation's is the second of its two.
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
}

// Correlation is how closely two numeric columns move together: Pearson's
// r, on the Rows rows where both have a value.
type Correlation struct {
	A    string  `json:"a"`
	B    string  `json:"b"`
	R    float64 `json:"r"`
	Rows int     `json:"rows"`
}

// Compute profiles a sample of a dataset; now is when, which dates in the
// future are measured against.
func Compute(s worker.DatasetSample, now time.Time) Profile {
	p := Profile{
		Dataset: s.Name, Rows: s.Rows, ColumnCount: len(s.Columns), SampleRows: s.SampleRows,
		Sampled: s.SampleRows < s.Rows, MaxSampleRows: worker.SampleMaxRows,
		Columns: []Column{}, Warnings: []Warning{}, Correlations: []Correlation{}, ComputedAt: now.UTC(),
	}
	p.Warnings = append(p.Warnings, rowWarnings(s.Rows)...)
	nums := map[string][]float64{}
	for _, c := range s.Columns {
		col, values := describe(c, s.Values[c.Name], s.SampleRows, now)
		p.Columns = append(p.Columns, col)
		p.Warnings = append(p.Warnings, columnWarnings(col)...)
		if col.Kind == KindNumber && col.Distinct > 1 {
			nums[c.Name] = values
		}
	}
	p.DuplicateRows = duplicates(s)
	if p.DuplicateRows > 0 {
		share := float64(p.DuplicateRows) / float64(s.SampleRows)
		sev := SeverityLow
		if share > 0.01 {
			sev = SeverityMedium
		}
		where := ""
		if p.Sampled {
			where = " in the rows checked"
		}
		p.Warnings = append(p.Warnings, Warning{Code: CodeDuplicateRows, Severity: sev,
			Message: fmt.Sprintf("%s (%s) are exact copies of another row%s.", plural(p.DuplicateRows, "row"), percent(share), where),
			Fix: "If each row should be one customer or one order line, remove the copies where the data comes from, " +
				"such as with SELECT DISTINCT. Leave them if they are real repeats."})
	}
	p.Correlations = correlations(s.Columns, nums)
	for _, c := range p.Correlations {
		if math.Abs(c.R) >= sameR {
			p.Warnings = append(p.Warnings, Warning{Code: CodeCorrelated, Severity: SeverityLow, Column: c.B,
				Message: fmt.Sprintf("%s and %s move together almost exactly (r = %.2f).", c.A, c.B, c.R),
				Fix: "One of them is probably enough. If one is worked out from the other, or from what you want to " +
					"predict, leave it out."})
		}
	}
	slices.SortStableFunc(p.Warnings, func(a, b Warning) int { return rank(a.Severity) - rank(b.Severity) })
	return p
}

func rank(sev string) int {
	switch sev {
	case SeverityHigh:
		return 0
	case SeverityMedium:
		return 1
	}
	return 2
}

func rowWarnings(rows int) []Warning {
	switch {
	case rows == 0:
		return []Warning{{Code: CodeFewRows, Severity: SeverityHigh, Message: "The dataset is empty.",
			Fix: "Fill it again: upload a file or read rows from a database."}}
	case rows < MinRows:
		return []Warning{{Code: CodeFewRows, Severity: SeverityHigh,
			Message: fmt.Sprintf("Only %s: training needs at least %d.", plural(rows, "row"), MinRows),
			Fix:     fmt.Sprintf("Add more rows: at least %d, and %d or more for a model you can rely on.", MinRows, FewRows)}}
	case rows < FewRows:
		return []Warning{{Code: CodeFewRows, Severity: SeverityMedium,
			Message: fmt.Sprintf("Only %d rows: a model trained on so few is rough.", rows),
			Fix:     fmt.Sprintf("Add more rows if you can: %d or more, and more is better.", FewRows)}}
	}
	return nil
}

// describe profiles one column, and returns its numbers for correlations.
func describe(c worker.Column, values []any, n int, now time.Time) (Column, []float64) {
	col := Column{Name: c.Name, Type: c.Type}
	t := tallyOf(values, n)
	col.Missing = t.missing
	if n > 0 {
		col.MissingShare = float64(col.Missing) / float64(n)
	}
	col.Distinct = len(t.counts)
	have := n - col.Missing
	switch {
	case have == 0:
		col.Kind = KindEmpty
		return col, t.nums
	case c.Type == "bool":
		col.Kind = KindYesNo
	case c.Type == "number" && len(t.present) == have:
		col.Kind = KindNumber
		col.Numbers = summarise(t.present)
		if col.Numbers.Integer && col.Distinct > 1 && idLike(col.Distinct, have) {
			col.Kind = KindID
		}
		col.Histogram = histogram(t.present, col.Numbers.Min, col.Numbers.Max)
	default:
		if d := dates(t.texts, now); d != nil && float64(d.parsed) >= dateShare*float64(have) {
			col.Kind = KindDate
			col.Dates = d.Dates
			return col, t.nums
		}
		col.Kind = textKind(col.Distinct, have)
	}
	if col.Kind != KindID && (col.Kind != KindNumber || col.Distinct <= imbalanceClasses) {
		col.Top, col.OtherShare = top(t.counts, t.order, have)
	}
	return col, t.nums
}

// idLike says whether distinct values out of have present ones look like an
// ID: nearly one per row, on enough rows to tell.
func idLike(distinct, have int) bool {
	return float64(distinct) >= IDLike*float64(have) && have >= idMinRows
}

// textKind is what a column of text is, by how many values it has.
func textKind(distinct, have int) string {
	switch {
	case idLike(distinct, have):
		return KindID
	case distinct > textDistinct && float64(distinct) > textShare*float64(have):
		return KindText
	}
	return KindCategory
}

// tally is what one pass over a column's values finds.
type tally struct {
	missing int
	// How often each value (as text) appears, and the values in the order
	// they first appear.
	counts map[string]int
	order  []string
	// Every row's number (NaN where it has none), and the numbers and texts
	// present.
	nums    []float64
	present []float64
	texts   []string
}

func tallyOf(values []any, n int) tally {
	t := tally{counts: map[string]int{}, nums: make([]float64, n)}
	for i := range n {
		t.nums[i] = math.NaN()
		var v any
		if i < len(values) {
			v = values[i]
		}
		key, ok := textOf(v)
		if !ok {
			t.missing++
			continue
		}
		if _, seen := t.counts[key]; !seen {
			t.order = append(t.order, key)
		}
		t.counts[key]++
		switch x := v.(type) {
		case float64:
			t.nums[i] = x
			t.present = append(t.present, x)
		case string:
			t.texts = append(t.texts, x)
		}
	}
	return t
}

// textOf is a present value as text, the key its distinct values are
// counted by; false when it is missing.
func textOf(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case float64:
		if math.IsNaN(x) {
			return "", false
		}
		return strconv.FormatFloat(x, 'g', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	case string:
		return x, true
	}
	return fmt.Sprint(v), true
}

func top(counts map[string]int, order []string, have int) ([]Share, float64) {
	keys := slices.Clone(order)
	// Most common first; a tie keeps the order the values first appeared in.
	slices.SortStableFunc(keys, func(a, b string) int { return counts[b] - counts[a] })
	out := []Share{}
	shown := 0
	for _, k := range keys[:min(topValues, len(keys))] {
		out = append(out, Share{Value: k, Rows: counts[k], Share: float64(counts[k]) / float64(have)})
		shown += counts[k]
	}
	return out, float64(have-shown) / float64(have)
}

func summarise(v []float64) *Numbers {
	s := slices.Clone(v)
	slices.Sort(s)
	n := float64(len(s))
	sum, integer := 0.0, true
	for _, x := range s {
		sum += x
		integer = integer && x == math.Trunc(x)
	}
	mean := sum / n
	var m2, m3 float64
	for _, x := range s {
		d := x - mean
		m2 += d * d
		m3 += d * d * d
	}
	std := math.Sqrt(m2 / n)
	q1, q3 := quantile(s, 0.25), quantile(s, 0.75)
	out := &Numbers{Min: s[0], Max: s[len(s)-1], Mean: mean, Median: quantile(s, 0.5), Q1: q1, Q3: q3, Std: std, Integer: integer}
	if std > 0 {
		out.Skew = (m3 / n) / (std * std * std)
	}
	for _, x := range s {
		if iqr := q3 - q1; iqr > 0 {
			if x < q1-outlierIQR*iqr || x > q3+outlierIQR*iqr {
				out.Outliers++
			}
		} else if std > 0 && math.Abs(x-mean) > outlierStd*std {
			out.Outliers++
		}
	}
	return out
}

// quantile of sorted values, interpolated between the two nearest.
func quantile(s []float64, q float64) float64 {
	pos := q * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := min(lo+1, len(s)-1)
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

func histogram(v []float64, lo, hi float64) []Bin {
	if lo == hi {
		return []Bin{{From: lo, To: hi, Rows: len(v)}}
	}
	width := (hi - lo) / histogramBins
	bins := make([]Bin, histogramBins)
	for i := range bins {
		bins[i] = Bin{From: lo + float64(i)*width, To: lo + float64(i+1)*width}
	}
	bins[len(bins)-1].To = hi
	for _, x := range v {
		i := int(math.Ceil((x-lo)/width)) - 1
		bins[min(max(i, 0), len(bins)-1)].Rows++
	}
	return bins
}

// dateLayouts are the ways a date is written that a column is read as dates
// in: ISO 8601 with or without a time, as a database, pandas or Python
// write it.
var dateLayouts = []string{
	time.DateOnly, time.RFC3339Nano, "2006-01-02T15:04:05.999999999", time.DateTime + ".999999999",
	"2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05.999999999Z07:00", "2006/01/02",
}

// looksLikeDate is a cheap first test before the layouts are tried.
var looksLikeDate = regexp.MustCompile(`^\d{4}[-/]\d{2}[-/]\d{2}`)

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if !looksLikeDate.MatchString(s) {
		return time.Time{}, false
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

type dateStats struct {
	*Dates
	parsed int
}

func dates(texts []string, now time.Time) *dateStats {
	if len(texts) == 0 {
		return nil
	}
	var all []time.Time
	for _, s := range texts {
		if t, ok := parseDate(s); ok {
			all = append(all, t.UTC())
		}
	}
	if len(all) == 0 {
		return nil
	}
	lo := slices.MinFunc(all, func(a, b time.Time) int { return a.Compare(b) })
	hi := slices.MaxFunc(all, func(a, b time.Time) int { return a.Compare(b) })
	d := &Dates{Min: lo.Format(time.DateOnly), Max: hi.Format(time.DateOnly), Per: "month"}
	months := (hi.Year()-lo.Year())*12 + int(hi.Month()-lo.Month()) + 1
	label := func(t time.Time) string { return t.Format("2006-01") }
	if months > monthsMax {
		d.Per = "year"
		label = func(t time.Time) string { return strconv.Itoa(t.Year()) }
	}
	rows := map[string]int{}
	tomorrow := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	for _, t := range all {
		rows[label(t)]++
		if !t.Before(tomorrow) {
			d.Future++
		}
	}
	for at := time.Date(lo.Year(), lo.Month(), 1, 0, 0, 0, 0, time.UTC); !at.After(hi); {
		l := label(at)
		d.Counts = append(d.Counts, Bucket{Label: l, Rows: rows[l]})
		if d.Per == "year" {
			at = at.AddDate(1, 0, 0)
		} else {
			at = at.AddDate(0, 1, 0)
		}
	}
	return &dateStats{Dates: d, parsed: len(all)}
}

func columnWarnings(c Column) []Warning {
	var out []Warning
	add := func(code, sev, msg, fix string) {
		out = append(out, Warning{Code: code, Severity: sev, Column: c.Name, Message: msg, Fix: fix})
	}
	if c.Kind == KindEmpty {
		add(CodeEmpty, SeverityMedium, c.Name+" is empty: no row has a value in it.",
			"Leave it out, or check the file or query that filled it.")
		return out
	}
	if c.MissingShare > MostlyEmpty {
		add(CodeMostlyEmpty, SeverityMedium, fmt.Sprintf("%s is %s empty.", c.Name, percent(c.MissingShare)),
			"Fill the gaps where the data comes from, or leave the column out: a model learns little from it.")
	}
	if c.Distinct == 1 {
		what := "the same value"
		if len(c.Top) > 0 {
			what = strconv.Quote(c.Top[0].Value)
		}
		add(CodeConstant, SeverityLow, fmt.Sprintf("%s holds %s on every row.", c.Name, what),
			"Leave it out: a column that never changes cannot tell rows apart.")
		return out
	}
	out = append(out, kindWarnings(c)...)
	if (c.Kind == KindCategory || c.Kind == KindYesNo || c.Kind == KindNumber) && c.Distinct <= imbalanceClasses &&
		len(c.Top) > 0 && c.Top[0].Share >= imbalanceShare {
		add(CodeImbalanced, SeverityLow, fmt.Sprintf("%s is imbalanced: %q is %s of rows.", c.Name, c.Top[0].Value, percent(c.Top[0].Share)),
			"If you predict it, the model sees few examples of the rarer values. Add more of them if you can; "+
				"Hermod weighs them up when it trains.")
	}
	return out
}

// kindWarnings are the warnings particular to the column's kind.
func kindWarnings(c Column) []Warning {
	var out []Warning
	add := func(code, sev, msg, fix string) {
		out = append(out, Warning{Code: code, Severity: sev, Column: c.Name, Message: msg, Fix: fix})
	}
	switch c.Kind {
	case KindID:
		add(CodeIDLike, SeverityLow, c.Name+" looks like an ID: almost every row has its own value.",
			"Do not predict from it; Hermod leaves it out of a prediction model by itself. Keep it to tell who or what "+
				"a row is about, such as the customer on an order.")
	case KindText:
		add(CodeHighCardinality, SeverityLow, fmt.Sprintf("%s has %d different values (%s of rows): free text or very detailed labels.",
			c.Name, c.Distinct, percent(float64(c.Distinct)/float64(max(1, totalOf(c))))),
			"Group the values into a few categories (such as a region instead of an address), or leave the column out.")
	case KindNumber:
		n := c.Numbers
		if n.Outliers > 0 {
			add(CodeOutliers, SeverityLow, fmt.Sprintf("%s of %s %s far outside the usual range (the middle half of the values is %s).",
				plural(n.Outliers, "value"), c.Name, isAre(n.Outliers), middleHalf(c)),
				"Check them for typos or the wrong unit, such as cents instead of euros. Leave them in if they are real.")
		}
		if math.Abs(n.Skew) >= skewAt {
			side := "most values are small and a few are very large"
			if n.Skew < 0 {
				side = "most values are large and a few are very small"
			}
			add(CodeSkewed, SeverityLow, fmt.Sprintf("%s is skewed: %s (median %s, largest %s).", c.Name, side, num(n.Median), num(n.Max)),
				"If you predict it, expect bigger errors on the rare large values. Predicting a range (such as low, "+
					"medium, high) can work better.")
		}
	case KindDate:
		if c.Dates.Future > 0 {
			add(CodeFutureDates, SeverityMedium, fmt.Sprintf("%s in %s %s in the future (the latest is %s).",
				plural(c.Dates.Future, "date"), c.Name, isAre(c.Dates.Future), c.Dates.Max),
				"Usually a wrong date format (day and month swapped) or a placeholder such as 9999-12-31. Fix them where "+
					"the data comes from; write dates as 2026-09-01.")
		}
	}
	return out
}

func totalOf(c Column) int {
	if c.MissingShare >= 1 {
		return 0
	}
	return int(math.Round(float64(c.Missing) / c.MissingShare))
}

func middleHalf(c Column) string {
	if c.Numbers.Q1 == c.Numbers.Q3 {
		return num(c.Numbers.Q1)
	}
	return num(c.Numbers.Q1) + " to " + num(c.Numbers.Q3)
}

func correlations(cols []worker.Column, nums map[string][]float64) []Correlation {
	var names []string
	for _, c := range cols {
		if _, ok := nums[c.Name]; ok && len(names) < maxCorrColumns {
			names = append(names, c.Name)
		}
	}
	out := []Correlation{}
	for i, a := range names {
		for _, b := range names[i+1:] {
			r, n := pearson(nums[a], nums[b])
			if n >= minPairs && !math.IsNaN(r) && math.Abs(r) >= minR {
				out = append(out, Correlation{A: a, B: b, R: r, Rows: n})
			}
		}
	}
	slices.SortStableFunc(out, func(x, y Correlation) int { return cmp.Compare(math.Abs(y.R), math.Abs(x.R)) })
	return out[:min(topPairs, len(out))]
}

func pearson(x, y []float64) (float64, int) {
	var n, sx, sy float64
	for i := range x {
		if !math.IsNaN(x[i]) && !math.IsNaN(y[i]) {
			n++
			sx += x[i]
			sy += y[i]
		}
	}
	if n < 2 {
		return math.NaN(), int(n)
	}
	mx, my := sx/n, sy/n
	var cov, vx, vy float64
	for i := range x {
		if !math.IsNaN(x[i]) && !math.IsNaN(y[i]) {
			dx, dy := x[i]-mx, y[i]-my
			cov += dx * dy
			vx += dx * dx
			vy += dy * dy
		}
	}
	if vx == 0 || vy == 0 {
		return math.NaN(), int(n)
	}
	return math.Max(-1, math.Min(1, cov/math.Sqrt(vx*vy))), int(n)
}

// duplicates counts the sampled rows that repeat an earlier one exactly.
func duplicates(s worker.DatasetSample) int {
	if len(s.Columns) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, s.SampleRows)
	dup := 0
	var b strings.Builder
	for i := range s.SampleRows {
		b.Reset()
		for _, c := range s.Columns {
			var v any
			if vs := s.Values[c.Name]; i < len(vs) {
				v = vs[i]
			}
			t, ok := textOf(v)
			if ok {
				b.WriteByte(1)
				b.WriteString(t)
			}
			b.WriteByte(0)
		}
		k := b.String()
		if _, ok := seen[k]; ok {
			dup++
			continue
		}
		seen[k] = struct{}{}
	}
	return dup
}

func percent(share float64) string {
	p := share * 100
	if p > 0 && p < 1 {
		return "less than 1%"
	}
	return fmt.Sprintf("%.0f%%", p)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func num(f float64) string {
	return strconv.FormatFloat(f, 'g', 6, 64)
}
