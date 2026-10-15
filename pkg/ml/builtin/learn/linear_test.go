// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package learn

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"
)

func TestLinearRegressionRecoversTheCoefficients(t *testing.T) {
	x, y := linearData(400, 0, 7)
	m := fit(t, AlgorithmLinear, TaskRegression, x, y, 0)
	probe := numericFrame([][]float64{{0, 0}, {1, 0}, {0, 1}, {2, -1}})
	got := m.Predict(probe)
	want := []float64{5, 8, 3, 13}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 0.01 {
			t.Errorf("row %d: predicted %.4f, want %.4f", i, got[i], want[i])
		}
	}
}

func TestLogisticRegressionSeparatesSeparableData(t *testing.T) {
	x, y := separable(300, 11)
	m := fit(t, AlgorithmLinear, TaskClassification, x, y, 2)
	if acc := accuracy(t, m, x, y); acc < 0.97 {
		t.Fatalf("accuracy %.3f on separable data, want at least 0.97", acc)
	}
	p := m.Proba(numericFrame([][]float64{{2, 2}, {-2, -2}}))
	if p[0][1] < 0.9 || p[1][0] < 0.9 {
		t.Errorf("probabilities far from the margin are %v, want confident", p)
	}
	for _, row := range p {
		if s := row[0] + row[1]; math.Abs(s-1) > 1e-9 {
			t.Errorf("probabilities sum to %v", s)
		}
	}
}

func TestLogisticRegressionTakesThreeClasses(t *testing.T) {
	rows := [][]float64{}
	y := []float64{}
	for i := range 90 {
		c := i % 3
		rows = append(rows, []float64{float64(c)*4 + float64(i%7)*0.1, float64(i%5) * 0.1})
		y = append(y, float64(c))
	}
	x := numericFrame(rows)
	m := fit(t, AlgorithmLinear, TaskClassification, x, y, 3)
	if acc := accuracy(t, m, x, y); acc < 0.95 {
		t.Fatalf("accuracy %.3f, want at least 0.95", acc)
	}
}

func TestStringFeaturesAreOneHotEncodedAndMissingNumbersFilled(t *testing.T) {
	f := NewFrame([]string{"city", "size"}, []string{KindString, KindNumber}, 8)
	cities := []string{"oslo", "bergen", "oslo", "bergen", "oslo", "bergen", "", "oslo"}
	sizes := []float64{1, 2, 1, 2, math.NaN(), 2, 3, 1}
	y := make([]float64, 8)
	for i := range y {
		f.Str[0][i], f.Num[1][i] = cities[i], sizes[i]
		if cities[i] == "oslo" {
			y[i] = 100
		} else {
			y[i] = 50
		}
	}
	m := fit(t, AlgorithmLinear, TaskRegression, f, y, 0)
	if m.Prep.Fill["size"] != 2 {
		t.Errorf("fill for size = %v, want the median 2", m.Prep.Fill["size"])
	}
	probe := NewFrame([]string{"city", "size"}, []string{KindString, KindNumber}, 2)
	probe.Str[0][0], probe.Num[1][0] = "oslo", math.NaN()
	probe.Str[0][1], probe.Num[1][1] = "never-seen", 2
	got := m.Predict(probe)
	if math.Abs(got[0]-100) > 2 {
		t.Errorf("oslo predicted %.2f, want about 100", got[0])
	}
	if math.IsNaN(got[1]) {
		t.Error("an unseen category gave NaN")
	}
}

func TestTrainingIsDeterministicBySeed(t *testing.T) {
	x, y := separable(200, 5)
	for _, alg := range []string{AlgorithmLinear, AlgorithmRandomForest, AlgorithmGradientBoosting, AlgorithmMLP} {
		a, b := fit(t, alg, TaskClassification, x, y, 2), fit(t, alg, TaskClassification, x, y, 2)
		ea, err := a.Encode()
		if err != nil {
			t.Fatal(err)
		}
		eb, _ := b.Encode()
		if !bytes.Equal(ea, eb) {
			t.Errorf("%s: two trainings with one seed differ", alg)
		}
	}
}

func TestEncodeDecodeKeepsThePredictions(t *testing.T) {
	x, y := separable(150, 9)
	for _, alg := range []string{AlgorithmLinear, AlgorithmRandomForest, AlgorithmGradientBoosting, AlgorithmMLP} {
		m := fit(t, alg, TaskClassification, x, y, 2)
		raw, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		back, err := Decode(raw)
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		pa, pb := m.Proba(x), back.Proba(x)
		for i := range pa {
			for k := range pa[i] {
				if pa[i][k] != pb[i][k] {
					t.Fatalf("%s: row %d differs after a round trip: %v vs %v", alg, i, pa[i], pb[i])
				}
			}
		}
	}
}

func TestDecodeRefusesWhatIsNotAModel(t *testing.T) {
	for name, raw := range map[string][]byte{
		"empty":   nil,
		"garbage": []byte("not gzip"),
	} {
		if _, err := Decode(raw); !errors.Is(err, ErrInvalidModel) {
			t.Errorf("%s: err = %v, want ErrInvalidModel", name, err)
		}
	}
	m := fit(t, AlgorithmLinear, TaskRegression, numericFrame([][]float64{{1}, {2}, {3}}), []float64{1, 2, 3}, 0)
	m.Format = 99
	raw, _ := m.Encode()
	if _, err := Decode(raw); !errors.Is(err, ErrInvalidModel) {
		t.Errorf("format 99: err = %v, want ErrInvalidModel", err)
	}
}

func TestFitStopsWhenTheContextEnds(t *testing.T) {
	x, y := separable(2000, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, alg := range []string{AlgorithmLinear, AlgorithmRandomForest, AlgorithmGradientBoosting, AlgorithmMLP} {
		_, err := Fit(ctx, Spec{Task: TaskClassification, Algorithm: alg, Classes: 2, Seed: 1}, x, y, nil)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", alg, err)
		}
	}
}
