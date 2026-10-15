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
	"context"
	"math/rand/v2"
	"testing"
)

// benchFrame is 10,000 rows of eight numbers and two text features, and a
// yes/no target that depends on some of them.
func benchFrame() (*Frame, []float64) {
	const n = 10_000
	names := []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7", "s0", "s1"}
	kinds := []string{KindNumber, KindNumber, KindNumber, KindNumber, KindNumber, KindNumber, KindNumber, KindNumber, KindString, KindString}
	f := NewFrame(names, kinds, n)
	y := make([]float64, n)
	rng := rand.New(rand.NewPCG(5, 6))
	for i := range n {
		for j := range 8 {
			f.Num[j][i] = rng.NormFloat64()
		}
		f.Str[8][i] = []string{"a", "b", "c", "d"}[rng.IntN(4)]
		f.Str[9][i] = []string{"x", "y"}[rng.IntN(2)]
		if f.Num[0][i]+f.Num[1][i]*f.Num[2][i] > 0 || f.Str[8][i] == "a" {
			y[i] = 1
		}
	}
	return f, y
}

func benchFit(b *testing.B, alg string) {
	x, y := benchFrame()
	spec := Spec{Task: TaskClassification, Algorithm: alg, Classes: 2, Seed: 1}
	if alg == AlgorithmMLP {
		spec.MLP, _ = MLPParams{Epochs: 20}.WithDefaults()
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Fit(context.Background(), spec, x, y, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFitLinear10k(b *testing.B)           { benchFit(b, AlgorithmLinear) }
func BenchmarkFitRandomForest10k(b *testing.B)     { benchFit(b, AlgorithmRandomForest) }
func BenchmarkFitGradientBoosting10k(b *testing.B) { benchFit(b, AlgorithmGradientBoosting) }
func BenchmarkFitMLP10kTwentyEpochs(b *testing.B)  { benchFit(b, AlgorithmMLP) }

func BenchmarkPredictRandomForest10k(b *testing.B) {
	x, y := benchFrame()
	m, err := Fit(context.Background(), Spec{Task: TaskClassification, Algorithm: AlgorithmRandomForest, Classes: 2, Seed: 1}, x, y, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		m.Proba(x)
	}
}
