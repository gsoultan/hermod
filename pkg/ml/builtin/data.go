// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package builtin

import (
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/gsoultan/hermod/pkg/ml/builtin/learn"
	"github.com/gsoultan/hermod/pkg/ml/builtin/table"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// The worker's thresholds (precheck.py, selection.py).
const (
	minRows        = 10
	maxAutoClasses = 20
	fewRows        = 100
	mostlyEmpty    = 0.6
	idLike         = 0.95
	leakScore      = 0.98
	leakSample     = 5000
	leakMinRows    = 20
	imbalanceRatio = 0.25
)

// columns resolves a request's target and features against the dataset:
// the features it names, or every column but the target.
func columns(t *table.Table, dataset, target string, named []string) (*table.Column, []string, error) {
	tc := t.Column(target)
	if tc == nil {
		return nil, nil, badRequest("Target column %q is not in dataset %q.", target, dataset)
	}
	features := named
	if len(features) == 0 {
		for _, c := range t.Columns {
			if c.Name != target {
				features = append(features, c.Name)
			}
		}
	}
	for _, f := range features {
		if f == target {
			return nil, nil, badRequest("Column %q is the target and cannot also be a feature.", target)
		}
		if t.Column(f) == nil {
			return nil, nil, badRequest("Feature column %q is not in dataset %q.", f, dataset)
		}
	}
	if len(features) == 0 {
		return nil, nil, badRequest("There are no feature columns to train on.")
	}
	return tc, features, nil
}

// usableRows are the rows with a target value.
func usableRows(t *table.Table, target *table.Column) []int {
	var idx []int
	for i := range t.Rows {
		if !target.Missing(i) {
			idx = append(idx, i)
		}
	}
	return idx
}

func integral(c *table.Column) bool {
	if c.Kind != table.Number {
		return false
	}
	for _, v := range c.Num {
		if v != math.Trunc(v) {
			return false
		}
	}
	return true
}

// detectTask is the task for requested ("auto" or explicit) on a target with
// no missing value: text and yes/no are classes, and so are whole numbers
// with at most maxAutoClasses values.
func detectTask(requested string, c *table.Column) string {
	if requested != "auto" {
		return requested
	}
	if c.Kind != table.Number {
		return learn.TaskClassification
	}
	if integral(c) {
		seen := map[float64]bool{}
		for _, v := range c.Num {
			seen[v] = true
		}
		if len(seen) <= maxAutoClasses {
			return learn.TaskClassification
		}
	}
	return learn.TaskRegression
}

// target is the target as a model learns it: class indices into labels
// (sorted, as the worker's are: text, "false"/"true", or whole numbers), or
// values.
type target struct {
	y      []float64
	labels []any
	// text is each row's label as text, for the checks.
	text []string
}

func (tg target) classes() int { return len(tg.labels) }

// targetValues reads a target with no missing value for task, or refuses a
// target that cannot serve it.
func targetValues(task, name string, c *table.Column) (target, error) {
	n := len(c.Num) + len(c.Str)
	if task == learn.TaskRegression {
		if c.Kind != table.Number {
			return target{}, badRequest("Task regression needs a numeric target; %q is %s.", name, c.Kind)
		}
		return target{y: slices.Clone(c.Num)}, nil
	}
	if c.Kind == table.Number && !integral(c) {
		return target{}, badRequest("Classification needs a string, bool or integer target; %q has fractional numbers.", name)
	}
	tg := target{y: make([]float64, n), text: make([]string, n)}
	if c.Kind == table.Number {
		values := slices.Clone(c.Num)
		slices.Sort(values)
		values = slices.Compact(values)
		for _, v := range values {
			tg.labels = append(tg.labels, v)
		}
		for i, v := range c.Num {
			k, _ := slices.BinarySearch(values, v)
			tg.y[i], tg.text[i] = float64(k), table.ToText(v)
		}
		return tg, nil
	}
	for i := range n {
		if c.Kind == table.Bool {
			tg.text[i] = "false"
			if c.Num[i] == 1 {
				tg.text[i] = "true"
			}
		} else {
			tg.text[i] = c.Str[i]
		}
	}
	values := slices.Clone(tg.text)
	slices.Sort(values)
	values = slices.Compact(values)
	for _, v := range values {
		tg.labels = append(tg.labels, v)
	}
	for i, s := range tg.text {
		k, _ := slices.BinarySearch(values, s)
		tg.y[i] = float64(k)
	}
	return tg, nil
}

// counts are how many rows each class holds.
func counts(y []float64, k int) []int {
	out := make([]int, k)
	for _, v := range y {
		out[int(v)]++
	}
	return out
}

// imbalanced reports whether the rarest class present has at most
// imbalanceRatio rows for each row of the most common one.
func imbalanced(y []float64, k int) bool {
	lo, hi, present := math.MaxInt, 0, 0
	for _, c := range counts(y, k) {
		if c == 0 {
			continue
		}
		present++
		lo, hi = min(lo, c), max(hi, c)
	}
	return present >= 2 && float64(lo) <= imbalanceRatio*float64(hi)
}

// balancedWeights weigh each row by n / (classes * its class's rows), so
// every class counts the same (scikit-learn's "balanced").
func balancedWeights(y []float64, k int) []float64 {
	cnt := counts(y, k)
	present := 0
	for _, c := range cnt {
		if c > 0 {
			present++
		}
	}
	w := make([]float64, len(y))
	for i, v := range y {
		w[i] = float64(len(y)) / float64(present*cnt[int(v)])
	}
	return w
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

// targetWarnings are the notes on a target's usable values, and whether a
// training would accept them (precheck.py target_warnings).
func targetWarnings(task, name string, tg target) ([]worker.CheckWarning, bool) {
	var out []worker.CheckWarning
	ok := true
	rows := len(tg.y)
	if rows < minRows {
		ok = false
		out = append(out, worker.CheckWarning{Code: "few_rows", Column: name,
			Message: fmt.Sprintf("Only %d rows have a value in '%s'. Training needs at least %d.", rows, name, minRows)})
	} else if rows < fewRows {
		out = append(out, worker.CheckWarning{Code: "few_rows", Column: name,
			Message: fmt.Sprintf("Only %d rows have a value in '%s'. A model learned from so few is rough; more rows give better results.", rows, name)})
	}
	if task != learn.TaskClassification {
		if rows > 0 && distinctFloats(tg.y) < 2 {
			out = append(out, worker.CheckWarning{Code: "constant", Column: name,
				Message: fmt.Sprintf("Every row has the same value in '%s', so there is nothing to learn.", name)})
		}
		return out, ok
	}
	k := tg.classes()
	if k < 2 {
		return append(out, worker.CheckWarning{Code: "constant", Column: name,
			Message: fmt.Sprintf("Every row has the same value in '%s'. A yes/no or category needs at least two different values.", name)}), false
	}
	if k > maxAutoClasses {
		out = append(out, worker.CheckWarning{Code: "many_classes", Column: name,
			Message: fmt.Sprintf("'%s' has %d different values to choose between. Each needs enough rows to learn from; "+
				"grouping rare values together usually helps.", name, k)})
	}
	if imbalanced(tg.y, k) {
		cnt := counts(tg.y, k)
		rare := 0
		for c := range cnt {
			if cnt[c] < cnt[rare] {
				rare = c
			}
		}
		out = append(out, worker.CheckWarning{Code: "imbalanced", Column: name,
			Message: fmt.Sprintf("'%s' is lopsided: '%s' is only %s of the rows. Hermod weighs the values "+
				"so the rare ones count, and judges models by balanced accuracy.", name, table.ToText(tg.labels[rare]), pct(float64(cnt[rare])/float64(rows)))})
	}
	return out, ok
}

func distinctFloats(v []float64) int {
	seen := map[float64]bool{}
	for _, x := range v {
		seen[x] = true
	}
	return len(seen)
}

// The words of a column role's warning (precheck.py ROLE_WARNINGS).
var roleWarnings = map[string]string{
	"mostly_empty": "'%s' is mostly empty: %s",
	"constant":     "'%s' has the same value on every row and cannot help predict.",
	"id_like":      "'%s' has a different value on almost every row, like an ID. It cannot help predict and is best left out.",
	"leak_suspect": "'%s' predicts the target almost perfectly on its own. If it is only known after the fact, leave it out.",
}

// featureNotes are each feature's role over the usable rows t, and the
// warnings for those that are not "use". The checks look at a sample of at
// most leakSample rows.
func featureNotes(t *table.Table, features []string, task string, tg target, seed uint64) ([]worker.ColumnCheck, []worker.CheckWarning) {
	rows := make([]int, t.Rows)
	for i := range rows {
		rows[i] = i
	}
	if t.Rows > leakSample {
		rows = sample(t.Rows, leakSample, seed, 23)
	}
	st := t.Take(rows)
	sy := target{y: pick(tg.y, rows), labels: tg.labels}
	var cols []worker.ColumnCheck
	var warns []worker.CheckWarning
	for _, f := range features {
		c := st.Column(f)
		role, note := featureRole(c, task, sy, seed)
		cols = append(cols, worker.ColumnCheck{Name: f, Type: c.Kind, Role: role, Note: note})
		if role != "use" {
			msg := fmt.Sprintf(roleWarnings[role], f)
			if role == "mostly_empty" {
				msg = fmt.Sprintf(roleWarnings[role], f, note)
			}
			warns = append(warns, worker.CheckWarning{Code: role, Message: msg, Column: f})
		}
	}
	return cols, warns
}

// featureRole is a feature column's role, and why in words
// (precheck.py feature_role).
func featureRole(c *table.Column, task string, tg target, seed uint64) (string, string) {
	rows := len(tg.y)
	present, distinct, whole := presence(c, rows)
	empty := 1.0
	if rows > 0 {
		empty = 1 - float64(present)/float64(rows)
	}
	if empty > mostlyEmpty {
		return "mostly_empty", pct(empty) + " of its values are empty, so there is little to learn from."
	}
	if distinct <= 1 {
		return "constant", "Every row has the same value, so it cannot tell rows apart."
	}
	if (c.Kind == table.String || whole) && float64(distinct) >= idLike*float64(rows) {
		return "id_like", "Almost every row has its own value, like an ID. It cannot help predict new rows."
	}
	if rows >= leakMinRows {
		leak := false
		if task == learn.TaskClassification {
			leak = classificationLeak(c, tg, seed)
		} else {
			leak = c.Kind != table.String && regressionLeak(c.Num, tg.y)
		}
		if leak {
			return "leak_suspect", "On its own it predicts the target almost perfectly. It may be the answer in disguise, " +
				"something only known afterwards; a model using it will fail on new rows."
		}
	}
	return "use", ""
}

// presence counts the first rows values of a column that are there (not
// missing, and not "" for text), how many distinct ones there are, and
// whether a number column's are all whole.
func presence(c *table.Column, rows int) (present, distinct int, whole bool) {
	seen := map[string]bool{}
	whole = c.Kind == table.Number
	for i := range rows {
		if c.Missing(i) || c.Kind == table.String && c.Str[i] == "" {
			continue
		}
		present++
		if c.Kind == table.String {
			seen[c.Str[i]] = true
			continue
		}
		seen[strconv.FormatFloat(c.Num[i], 'g', -1, 64)] = true
		whole = whole && c.Num[i] == math.Trunc(c.Num[i])
	}
	return present, len(seen), whole
}

// classificationLeak fits the column alone on half the rows and reports
// whether it predicts the other half with a balanced accuracy of at least
// leakScore: by the most common class of each text value, or by a tree with
// as many leaves as there are classes over a number.
func classificationLeak(c *table.Column, tg target, seed uint64) bool {
	k := tg.classes()
	if distinctFloats(tg.y) < 2 {
		return false
	}
	fit, score := learn.Split(len(tg.y), tg.y, 0.5, seed)
	var pred []float64
	if c.Kind == table.String {
		pred = predictByValue(c, tg.y, k, fit, score)
	} else {
		x := missingBelow(c.Num)
		tree := fitStumps(x, tg.y, fit, k, max(2, k))
		pred = make([]float64, len(score))
		for j, i := range score {
			pred[j] = float64(tree.predict(x[i]))
		}
	}
	return learn.BalancedAccuracy(pick(tg.y, score), pred, k) >= leakScore
}

// predictByValue answers each score row with the most common class of its
// text value among the fit rows, or the most common class of all of them
// for a value they do not hold.
func predictByValue(c *table.Column, y []float64, k int, fit, score []int) []float64 {
	votes := map[string][]int{}
	all := make([]int, k)
	for _, i := range fit {
		v := c.Text(i)
		if votes[v] == nil {
			votes[v] = make([]int, k)
		}
		votes[v][int(y[i])]++
		all[int(y[i])]++
	}
	common := argmaxInt(all)
	pred := make([]float64, len(score))
	for j, i := range score {
		pred[j] = float64(common)
		if v, ok := votes[c.Text(i)]; ok {
			pred[j] = float64(argmaxInt(v))
		}
	}
	return pred
}

// missingBelow is a number column with each missing value made one less
// than the smallest present one, a value of its own.
func missingBelow(num []float64) []float64 {
	x := slices.Clone(num)
	lo := math.Inf(1)
	for _, v := range x {
		if !math.IsNaN(v) {
			lo = min(lo, v)
		}
	}
	if math.IsInf(lo, 1) {
		lo = 1
	}
	for i, v := range x {
		if math.IsNaN(v) {
			x[i] = lo - 1
		}
	}
	return x
}

func argmaxInt(v []int) int {
	best := 0
	for i := range v {
		if v[i] > v[best] {
			best = i
		}
	}
	return best
}

// regressionLeak reports whether a number column correlates with the target
// at least leakScore either way.
func regressionLeak(x, y []float64) bool {
	var xs, ys []float64
	for i := range x {
		if !math.IsNaN(x[i]) && !math.IsInf(x[i], 0) && !math.IsNaN(y[i]) {
			xs, ys = append(xs, x[i]), append(ys, y[i])
		}
	}
	if len(xs) < 3 {
		return false
	}
	mx, my := mean(xs), mean(ys)
	var sxy, sxx, syy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxy, sxx, syy = sxy+dx*dy, sxx+dx*dx, syy+dy*dy
	}
	if sxx == 0 || syy == 0 {
		return false
	}
	return math.Abs(sxy/math.Sqrt(sxx*syy)) >= leakScore
}

func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// intervals is a one-feature classification tree: Cuts split the line into
// len(Cuts)+1 intervals, each answering one class.
type intervals struct {
	cuts    []float64
	answers []int
}

func (t intervals) predict(x float64) int {
	i, _ := slices.BinarySearch(t.cuts, x)
	// BinarySearch finds the first cut >= x; x on a cut goes left.
	return t.answers[i]
}

// featureFrame is the features of t as a model reads them: a text column as
// text ("" when missing), any other as numbers rounded to float32, as the
// worker feeds its ONNX graphs.
func featureFrame(t *table.Table, features []string) (*learn.Frame, map[string]string) {
	kinds := make([]string, len(features))
	types := map[string]string{}
	for j, f := range features {
		kinds[j] = learn.KindNumber
		if t.Column(f).Kind == table.String {
			kinds[j] = learn.KindString
		}
		types[f] = kinds[j]
	}
	fr := learn.NewFrame(features, kinds, t.Rows)
	for j, f := range features {
		c := t.Column(f)
		for i := range t.Rows {
			if kinds[j] == learn.KindString {
				fr.Str[j][i] = c.Text(i)
			} else if !c.Missing(i) {
				fr.Num[j][i] = float64(float32(c.Num[i]))
			}
		}
	}
	return fr, types
}
