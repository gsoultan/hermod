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
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/gsoultan/hermod/pkg/ml/builtin/learn"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// What a version reports about itself beyond its predictions, as the
// worker's insights.py and stats.py measure it: metrics, a baseline,
// permutation importance and the training rows' feature stats.

// trainError is a failed fit as the caller sees it: a cancellation as it
// is, anything else as a refusal in words.
func trainError(algorithm string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return badRequest("Algorithm %q could not be trained on these rows: %v.", algorithm, err)
}

func pick(v []float64, idx []int) []float64 {
	out := make([]float64, len(idx))
	for i, r := range idx {
		out[i] = v[r]
	}
	return out
}

// scoreModel is the worker's metrics of a model's predictions on the test
// rows (insights.py metrics).
func scoreModel(task string, k int, pred []float64, proba [][]float64, y []float64) map[string]float64 {
	if task == learn.TaskClassification {
		if k != 2 {
			proba = nil
		}
		return learn.ClassificationMetrics(y, pred, proba, k)
	}
	return learn.RegressionMetrics(y, pred)
}

// baseline is the metrics of a model that knows nothing: the most common
// class of the training rows (the first on a tie), or their mean.
func baseline(task string, k int, yTrain, yTest []float64) map[string]float64 {
	pred := make([]float64, len(yTest))
	if task == learn.TaskClassification {
		cnt := counts(yTrain, k)
		common := float64(argmaxInt(cnt))
		prior := make([]float64, k)
		for c := range cnt {
			prior[c] = float64(cnt[c]) / float64(len(yTrain))
		}
		proba := make([][]float64, len(yTest))
		for i := range pred {
			pred[i], proba[i] = common, prior
		}
		return scoreModel(task, k, pred, proba, yTest)
	}
	m := mean(yTrain)
	for i := range pred {
		pred[i] = m
	}
	return scoreModel(task, k, pred, nil, yTest)
}

// The permutation importance's settings (insights.py).
const (
	topFeatures       = 20
	importanceRepeats = 5
	// importanceRows bounds the test rows importance is measured on, so a
	// large test split does not cost features x repeats full predictions.
	importanceRows = 10_000
)

// judge is the score auto and the importance judge a model by: balanced
// accuracy for a classifier, R² for a regression.
func judge(task string, k int, y, pred []float64) float64 {
	if task == learn.TaskClassification {
		return learn.BalancedAccuracy(y, pred, k)
	}
	return learn.R2(y, pred)
}

// importance is each feature's permutation importance on the test rows:
// the drop in score when its values are shuffled, the mean of
// importanceRepeats shuffles, at least zero, normalised to sum to 1,
// heaviest first (ties by name), at most topFeatures. Empty when no feature
// changes the score.
func importance(m *learn.Model, x *learn.Frame, y []float64, task string, k int, seed uint64) []worker.FeatureWeight {
	if x.N < 2 {
		return nil
	}
	if x.N > importanceRows {
		keep := sample(x.N, importanceRows, seed, 29)
		x, y = x.Take(keep), pick(y, keep)
	}
	rng := newRand(seed, 31)
	base := judge(task, k, y, m.Predict(x))
	weights := make([]float64, len(x.Names))
	total := 0.0
	for j := range x.Names {
		drop := 0.0
		for range importanceRepeats {
			drop += base - judge(task, k, y, m.Predict(shuffleColumn(x, j, rng.Perm(x.N))))
		}
		weights[j] = max(0, drop/importanceRepeats)
		if math.IsNaN(weights[j]) { // max keeps a NaN
			weights[j] = 0
		}
		total += weights[j]
	}
	if total <= 0 {
		return nil
	}
	return ranked(x.Names, weights, total)
}

// shuffleColumn is x with column j's values in the order perm gives.
func shuffleColumn(x *learn.Frame, j int, perm []int) *learn.Frame {
	shuffled := *x
	shuffled.Num, shuffled.Str = slices.Clone(x.Num), slices.Clone(x.Str)
	if x.Num[j] != nil {
		shuffled.Num[j] = pick(x.Num[j], perm)
		return &shuffled
	}
	col := make([]string, x.N)
	for i, r := range perm {
		col[i] = x.Str[j][r]
	}
	shuffled.Str[j] = col
	return &shuffled
}

// sample is n of the indices 0..total-1, drawn with the seed and stream,
// in ascending order.
func sample(total, n int, seed, stream uint64) []int {
	keep := newRand(seed, stream).Perm(total)[:n]
	slices.Sort(keep)
	return keep
}

// newRand is a generator seeded with seed and stream, so a training repeats.
func newRand(seed, stream uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, stream)) //nolint:gosec // G404: seeded on purpose, so a training repeats; nothing relies on it being unpredictable.
}

// ranked are the features with weight, normalised by total, heaviest first
// (ties by name), at most topFeatures.
func ranked(names []string, weights []float64, total float64) []worker.FeatureWeight {
	var out []worker.FeatureWeight
	for j, f := range names {
		if weights[j] > 0 {
			out = append(out, worker.FeatureWeight{Feature: f, Weight: weights[j] / total})
		}
	}
	slices.SortFunc(out, func(a, b worker.FeatureWeight) int {
		if a.Weight != b.Weight {
			if a.Weight > b.Weight {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Feature, b.Feature)
	})
	if len(out) > topFeatures {
		out = out[:topFeatures]
	}
	return out
}

// The stats' settings (stats.py).
const (
	statBins = 10
	statTopK = 20
)

// featureStats describe the training rows per feature, before missing
// numbers are filled (stats.py): decile bins of a number, the most frequent
// values of text.
func featureStats(x *learn.Frame) map[string]worker.FeatureStats {
	out := map[string]worker.FeatureStats{}
	for j, f := range x.Names {
		if x.Num[j] != nil {
			out[f] = numericStats(x.Num[j])
		} else {
			out[f] = categoricalStats(x.Str[j])
		}
	}
	return out
}

func share(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func numericStats(col []float64) worker.FeatureStats {
	var v []float64
	for _, x := range col {
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			v = append(v, x)
		}
	}
	st := worker.FeatureStats{Kind: worker.StatsNumeric, Count: len(col), NullFraction: share(len(col)-len(v), len(col)),
		Edges: []float64{}, Fractions: []float64{}}
	if len(v) == 0 {
		return st
	}
	slices.Sort(v)
	m := mean(v)
	ss := 0.0
	for _, x := range v {
		ss += (x - m) * (x - m)
	}
	sd, lo, hi := math.Sqrt(ss/float64(len(v))), v[0], v[len(v)-1]
	st.Mean, st.Std, st.Min, st.Max = &m, &sd, &lo, &hi
	for b := 1; b < statBins; b++ {
		st.Edges = append(st.Edges, quantile(v, float64(b)/statBins))
	}
	st.Edges = slices.Compact(st.Edges)
	cnt := make([]int, len(st.Edges)+1)
	for _, x := range v {
		i, _ := slices.BinarySearch(st.Edges, x)
		cnt[i]++
	}
	for _, c := range cnt {
		st.Fractions = append(st.Fractions, float64(c)/float64(len(v)))
	}
	return st
}

// quantile is numpy's linear quantile of sorted values.
func quantile(sorted []float64, q float64) float64 {
	h := q * float64(len(sorted)-1)
	lo := int(math.Floor(h))
	if lo+1 >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[lo] + (h-float64(lo))*(sorted[lo+1]-sorted[lo])
}

func categoricalStats(col []string) worker.FeatureStats {
	cnt := map[string]int{}
	n := 0
	for _, s := range col {
		if s != "" {
			cnt[s]++
			n++
		}
	}
	type kv struct {
		v string
		c int
	}
	var ranked []kv
	for v, c := range cnt {
		ranked = append(ranked, kv{v, c})
	}
	slices.SortFunc(ranked, func(a, b kv) int {
		if a.c != b.c {
			return b.c - a.c
		}
		return strings.Compare(a.v, b.v)
	})
	st := worker.FeatureStats{Kind: worker.StatsCategorical, Count: len(col), NullFraction: share(len(col)-n, len(col)), Top: []worker.CategoryShare{}}
	other := 1.0
	for i, r := range ranked {
		if i == statTopK {
			break
		}
		f := float64(r.c) / float64(n)
		st.Top = append(st.Top, worker.CategoryShare{Value: r.v, Fraction: f})
		other -= f
	}
	if n > 0 {
		st.OtherFraction = max(0, other)
	}
	return st
}
