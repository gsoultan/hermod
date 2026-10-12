package learn

import (
	"math"
	"math/rand/v2"
	"slices"
	"sort"
)

// ClassificationMetrics are the worker's classification metrics
// (insights.py): accuracy, balanced_accuracy (the mean recall of the classes
// in yTrue), f1 (macro, over the classes in yTrue or yPred), roc_auc (two
// classes, from proba's second column, when yTrue holds both), and score,
// which is the accuracy. Classes are indices 0..k-1.
func ClassificationMetrics(yTrue, yPred []float64, proba [][]float64, k int) map[string]float64 {
	n := len(yTrue)
	out := map[string]float64{}
	if n == 0 {
		return out
	}
	tp := make([]float64, k)
	trueN := make([]float64, k)
	predN := make([]float64, k)
	right := 0.0
	for i := range yTrue {
		t, p := int(yTrue[i]), int(yPred[i])
		trueN[t]++
		predN[p]++
		if t == p {
			tp[t]++
			right++
		}
	}
	out["accuracy"] = right / float64(n)
	recall, present := 0.0, 0
	f1, labels := 0.0, 0
	for c := range k {
		if trueN[c] > 0 {
			recall += tp[c] / trueN[c]
			present++
		}
		if trueN[c] > 0 || predN[c] > 0 {
			labels++
			if d := trueN[c] + predN[c]; d > 0 {
				f1 += 2 * tp[c] / d
			}
		}
	}
	out["balanced_accuracy"] = recall / float64(max(present, 1))
	out["f1"] = f1 / float64(max(labels, 1))
	if proba != nil && k == 2 && trueN[0] > 0 && trueN[1] > 0 {
		scores := make([]float64, n)
		for i := range proba {
			scores[i] = proba[i][1]
		}
		out["roc_auc"] = rocAUC(yTrue, scores)
	}
	out["score"] = out["accuracy"]
	return finiteOnly(out)
}

// BalancedAccuracy is the mean recall of the classes yTrue holds, the score
// auto and permutation importance judge a classifier by.
func BalancedAccuracy(yTrue, yPred []float64, k int) float64 {
	return ClassificationMetrics(yTrue, yPred, nil, k)["balanced_accuracy"]
}

// rocAUC is the area under the ROC curve of scores for class 1: the share of
// (positive, negative) pairs the scores order right, ties counting half.
func rocAUC(yTrue, scores []float64) float64 {
	idx := make([]int, len(scores))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] < scores[idx[b]] })
	ranks := make([]float64, len(scores))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && scores[idx[j+1]] == scores[idx[i]] {
			j++
		}
		r := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			ranks[idx[k]] = r
		}
		i = j + 1
	}
	var pos, neg, sum float64
	for i, t := range yTrue {
		if t == 1 {
			pos++
			sum += ranks[i]
		} else {
			neg++
		}
	}
	return (sum - pos*(pos+1)/2) / (pos * neg)
}

// RegressionMetrics are the worker's regression metrics: rmse, mae, r2 (with
// two rows or more) and score, which is r2.
func RegressionMetrics(yTrue, yPred []float64) map[string]float64 {
	n := float64(len(yTrue))
	out := map[string]float64{}
	if n == 0 {
		return out
	}
	var se, ae, mean float64
	for i := range yTrue {
		d := yTrue[i] - yPred[i]
		se += d * d
		ae += math.Abs(d)
		mean += yTrue[i]
	}
	out["rmse"] = math.Sqrt(se / n)
	out["mae"] = ae / n
	if len(yTrue) >= 2 {
		out["r2"] = R2(yTrue, yPred)
		out["score"] = out["r2"]
	}
	return finiteOnly(out)
}

// R2 is the coefficient of determination; a constant target scores 1 when
// predicted exactly and 0 otherwise, as scikit-learn does.
func R2(yTrue, yPred []float64) float64 {
	if len(yTrue) < 2 {
		return math.NaN()
	}
	mean := 0.0
	for _, v := range yTrue {
		mean += v
	}
	mean /= float64(len(yTrue))
	var res, tot float64
	for i, v := range yTrue {
		res += (v - yPred[i]) * (v - yPred[i])
		tot += (v - mean) * (v - mean)
	}
	if tot == 0 {
		if res == 0 {
			return 1
		}
		return 0
	}
	return 1 - res/tot
}

func finiteOnly(m map[string]float64) map[string]float64 {
	for k, v := range m {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			delete(m, k)
		}
	}
	return m
}

// Split holds back ceil(testSize * n) rows to test on, at random with the
// seed, stratified by class when y (class indices) is given and every class
// has two rows or more. Both lists are in ascending order.
func Split(n int, y []float64, testSize float64, seed uint64) (train, test []int) {
	rng := newRand(seed, 11)
	nTest := int(math.Ceil(testSize * float64(n)))
	nTest = min(max(nTest, 0), n)
	groups := stratify(n, y, 2)
	if groups == nil {
		perm := rng.Perm(n)
		test, train = perm[:nTest], perm[nTest:]
	} else {
		take := allocate(groups, nTest, rng)
		for g, rows := range groups {
			rng.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
			test = append(test, rows[:take[g]]...)
			train = append(train, rows[take[g]:]...)
		}
	}
	slices.Sort(train)
	slices.Sort(test)
	return train, test
}

// stratify groups row indices by class, or returns nil when y is nil or a
// class has fewer than least rows.
func stratify(n int, y []float64, least int) [][]int {
	if y == nil {
		return nil
	}
	k := 0
	for _, v := range y {
		k = max(k, int(v)+1)
	}
	groups := make([][]int, k)
	for i := range n {
		c := int(y[i])
		groups[c] = append(groups[c], i)
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		if len(g) < least {
			return nil
		}
		out = append(out, g)
	}
	return out
}

// allocate shares total among the groups in proportion to their sizes, the
// remainder going to the largest fractions (ties at random).
func allocate(groups [][]int, total int, rng *rand.Rand) []int {
	n := 0
	for _, g := range groups {
		n += len(g)
	}
	take := make([]int, len(groups))
	frac := make([]float64, len(groups))
	given := 0
	for i, g := range groups {
		exact := float64(total) * float64(len(g)) / float64(n)
		take[i] = int(math.Floor(exact))
		frac[i] = exact - float64(take[i])
		given += take[i]
	}
	order := rng.Perm(len(groups))
	sort.SliceStable(order, func(a, b int) bool { return frac[order[a]] > frac[order[b]] })
	for _, g := range order {
		if given >= total {
			break
		}
		if take[g] < len(groups[g]) {
			take[g]++
			given++
		}
	}
	return take
}

// Fold is one fold of cross-validation: rows to fit on and rows to score.
type Fold struct {
	Fit, Score []int
}

// Folds cuts n rows into k folds at random with the seed, stratified by
// class when y is given and every class has k rows or more.
func Folds(n int, y []float64, k int, seed uint64) []Fold {
	k = max(2, min(k, n))
	rng := newRand(seed, 13)
	assign := make([]int, n)
	if groups := stratify(n, y, k); groups != nil {
		at := 0
		for _, g := range groups {
			rng.Shuffle(len(g), func(i, j int) { g[i], g[j] = g[j], g[i] })
			for _, i := range g {
				assign[i] = at % k
				at++
			}
		}
	} else {
		for pos, i := range rng.Perm(n) {
			assign[i] = pos * k / n
		}
	}
	folds := make([]Fold, k)
	for i := range n {
		for f := range folds {
			if assign[i] == f {
				folds[f].Score = append(folds[f].Score, i)
			} else {
				folds[f].Fit = append(folds[f].Fit, i)
			}
		}
	}
	return folds
}
