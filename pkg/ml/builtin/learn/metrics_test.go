// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package learn

import (
	"math"
	"slices"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestClassificationMetricsMatchScikitLearn(t *testing.T) {
	// Values from scikit-learn 1.7 for the same arrays.
	yTrue := []float64{0, 0, 0, 0, 1, 1, 1, 2, 2, 2}
	yPred := []float64{0, 0, 1, 0, 1, 1, 0, 2, 2, 1}
	m := ClassificationMetrics(yTrue, yPred, nil, 3)
	if !near(m["accuracy"], 0.7) {
		t.Errorf("accuracy = %v, want 0.7", m["accuracy"])
	}
	// Recalls 3/4, 2/3, 2/3.
	if !near(m["balanced_accuracy"], (0.75+2.0/3+2.0/3)/3) {
		t.Errorf("balanced_accuracy = %v", m["balanced_accuracy"])
	}
	// Per class F1: 0.75, 0.5714..., 0.8.
	if want := (0.75 + 4.0/7 + 0.8) / 3; !near(m["f1"], want) {
		t.Errorf("f1 = %v, want %v", m["f1"], want)
	}
	if m["score"] != m["accuracy"] {
		t.Errorf("score = %v, want the accuracy", m["score"])
	}
	if _, ok := m["roc_auc"]; ok {
		t.Error("roc_auc reported for three classes")
	}
}

func TestROCAUCOfTwoClasses(t *testing.T) {
	yTrue := []float64{0, 0, 1, 1}
	proba := [][]float64{{0.9, 0.1}, {0.6, 0.4}, {0.65, 0.35}, {0.2, 0.8}}
	m := ClassificationMetrics(yTrue, []float64{0, 0, 0, 1}, proba, 2)
	if !near(m["roc_auc"], 0.75) {
		t.Errorf("roc_auc = %v, want 0.75", m["roc_auc"])
	}
	// Ties count half.
	m = ClassificationMetrics(yTrue, []float64{0, 0, 0, 0}, [][]float64{{1, 0}, {1, 0}, {1, 0}, {1, 0}}, 2)
	if !near(m["roc_auc"], 0.5) {
		t.Errorf("roc_auc with every score tied = %v, want 0.5", m["roc_auc"])
	}
}

func TestRegressionMetrics(t *testing.T) {
	m := RegressionMetrics([]float64{3, -0.5, 2, 7}, []float64{2.5, 0, 2, 8})
	if !near(m["mae"], 0.5) || !near(m["rmse"], math.Sqrt(0.375)) || !near(m["r2"], 0.9486081370449679) {
		t.Errorf("metrics = %v", m)
	}
	if m["score"] != m["r2"] {
		t.Errorf("score = %v, want r2", m["score"])
	}
	if _, ok := RegressionMetrics([]float64{1}, []float64{2})["r2"]; ok {
		t.Error("r2 reported for one row")
	}
}

func TestSplitHoldsBackAShareStratified(t *testing.T) {
	y := make([]float64, 100)
	for i := range y {
		if i%5 == 0 {
			y[i] = 1
		}
	}
	train, test := Split(100, y, 0.2, 42)
	if len(test) != 20 || len(train) != 80 {
		t.Fatalf("split %d/%d, want 80/20", len(train), len(test))
	}
	ones := 0
	for _, i := range test {
		if y[i] == 1 {
			ones++
		}
	}
	if ones != 4 {
		t.Errorf("test holds %d of class 1, want 4 (stratified)", ones)
	}
	all := append(slices.Clone(train), test...)
	slices.Sort(all)
	for i, v := range all {
		if v != i {
			t.Fatalf("the split loses or repeats rows")
		}
	}
	train2, test2 := Split(100, y, 0.2, 42)
	if !slices.Equal(train, train2) || !slices.Equal(test, test2) {
		t.Error("one seed gave two splits")
	}
	if _, other := Split(100, y, 0.2, 43); slices.Equal(test, other) {
		t.Error("another seed gave the same split")
	}
}

func TestFoldsCoverEveryRowOnce(t *testing.T) {
	folds := Folds(10, nil, 3, 1)
	if len(folds) != 3 {
		t.Fatalf("%d folds", len(folds))
	}
	seen := map[int]int{}
	for _, f := range folds {
		for _, i := range f.Score {
			seen[i]++
		}
		if len(f.Fit)+len(f.Score) != 10 {
			t.Errorf("fold covers %d rows", len(f.Fit)+len(f.Score))
		}
	}
	if len(seen) != 10 {
		t.Errorf("scored %d distinct rows, want 10", len(seen))
	}
}
