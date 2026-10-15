// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package learn

import (
	"math"
	"testing"
)

func TestTreesAndTheNetworkSolveXOR(t *testing.T) {
	x, y := xor(400, 4)
	for _, alg := range []string{AlgorithmRandomForest, AlgorithmGradientBoosting, AlgorithmMLP} {
		m := fit(t, alg, TaskClassification, x, y, 2)
		probe, want := numericFrame([][]float64{{0, 0}, {0, 1}, {1, 0}, {1, 1}}), []float64{0, 1, 1, 0}
		got := m.Predict(probe)
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: XOR of corner %d predicted %v, want %v", alg, i, got[i], want[i])
			}
		}
	}
}

func TestLinearCannotSolveXOR(t *testing.T) {
	// The control for the test above: XOR is not linearly separable, so a
	// passing test there is the trees' and the network's doing.
	x, y := xor(400, 4)
	if acc := accuracy(t, fit(t, AlgorithmLinear, TaskClassification, x, y, 2), x, y); acc > 0.8 {
		t.Fatalf("logistic regression scored %.2f on XOR", acc)
	}
}

func TestRegressionTreesFitAStepFunction(t *testing.T) {
	rows := make([][]float64, 300)
	y := make([]float64, 300)
	for i := range rows {
		v := float64(i) / 30
		rows[i] = []float64{v}
		y[i] = 10
		if v > 5 {
			y[i] = 40
		}
	}
	x := numericFrame(rows)
	for _, alg := range []string{AlgorithmRandomForest, AlgorithmGradientBoosting} {
		m := fit(t, alg, TaskRegression, x, y, 0)
		got := m.Predict(numericFrame([][]float64{{1}, {9}}))
		if math.Abs(got[0]-10) > 1.5 || math.Abs(got[1]-40) > 1.5 {
			t.Errorf("%s predicted %v, want about [10 40]", alg, got)
		}
	}
}

func TestTreesSplitOnACategory(t *testing.T) {
	f := NewFrame([]string{"plan"}, []string{KindString}, 120)
	y := make([]float64, 120)
	plans := []string{"free", "pro", "team"}
	for i := range y {
		f.Str[0][i] = plans[i%3]
		if plans[i%3] == "pro" {
			y[i] = 1
		}
	}
	for _, alg := range []string{AlgorithmRandomForest, AlgorithmGradientBoosting, AlgorithmMLP} {
		if acc := accuracy(t, fit(t, alg, TaskClassification, f, y, 2), f, y); acc != 1 {
			t.Errorf("%s: accuracy %.3f on a column that decides the class, want 1", alg, acc)
		}
	}
}

func TestMLPLearnsARegression(t *testing.T) {
	x, y := linearData(400, 0.1, 3)
	m := fit(t, AlgorithmMLP, TaskRegression, x, y, 0)
	pred := m.Predict(x)
	var ss, tot, mean float64
	for _, v := range y {
		mean += v / float64(len(y))
	}
	for i := range y {
		ss += (y[i] - pred[i]) * (y[i] - pred[i])
		tot += (y[i] - mean) * (y[i] - mean)
	}
	if r2 := 1 - ss/tot; r2 < 0.95 {
		t.Fatalf("R² %.3f on linear data, want at least 0.95", r2)
	}
}

func TestMLPParamsAreChecked(t *testing.T) {
	bad := []MLPParams{
		{HiddenLayers: []int{0}}, {HiddenLayers: []int{1, 2, 3, 4, 5, 6}}, {Epochs: 1001}, {BatchSize: 5000},
		{LearningRate: 2}, {Patience: -1},
	}
	for _, p := range bad {
		if _, err := p.WithDefaults(); err == nil {
			t.Errorf("%+v was accepted", p)
		}
	}
	p, err := MLPParams{}.WithDefaults()
	if err != nil || len(p.HiddenLayers) != 2 || p.Epochs != 200 || p.BatchSize != 32 || p.LearningRate != 0.001 || p.Patience != 10 {
		t.Errorf("defaults = %+v, %v; want the worker's 64,32 / 200 / 32 / 0.001 / 10", p, err)
	}
}
