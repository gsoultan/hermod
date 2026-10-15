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
	"math"
	"math/rand/v2"
	"testing"
)

// numericFrame is a frame of numeric features x0..x(d-1), one row per row.
func numericFrame(rows [][]float64) *Frame {
	d := 0
	if len(rows) > 0 {
		d = len(rows[0])
	}
	names := make([]string, d)
	kinds := make([]string, d)
	for j := range names {
		names[j], kinds[j] = "x"+string(rune('0'+j)), KindNumber
	}
	f := NewFrame(names, kinds, len(rows))
	for i, r := range rows {
		for j, v := range r {
			f.Num[j][i] = v
		}
	}
	return f
}

// linearData is y = 3*x0 - 2*x1 + 5 with a little noise.
func linearData(n int, noise float64, seed uint64) (*Frame, []float64) {
	rng := rand.New(rand.NewPCG(seed, 1))
	rows := make([][]float64, n)
	y := make([]float64, n)
	for i := range rows {
		x0, x1 := rng.NormFloat64()*2, rng.NormFloat64()*3
		rows[i] = []float64{x0, x1}
		y[i] = 3*x0 - 2*x1 + 5 + noise*rng.NormFloat64()
	}
	return numericFrame(rows), y
}

// separable is two classes split by x0 + x1 > 0, with a margin.
func separable(n int, seed uint64) (*Frame, []float64) {
	rng := rand.New(rand.NewPCG(seed, 2))
	rows := make([][]float64, 0, n)
	y := make([]float64, 0, n)
	for len(rows) < n {
		x0, x1 := rng.Float64()*4-2, rng.Float64()*4-2
		s := x0 + x1
		if math.Abs(s) < 0.3 {
			continue
		}
		rows = append(rows, []float64{x0, x1})
		if s > 0 {
			y = append(y, 1)
		} else {
			y = append(y, 0)
		}
	}
	return numericFrame(rows), y
}

// xor is the four corners of a square, labelled by their XOR, jittered.
func xor(n int, seed uint64) (*Frame, []float64) {
	rng := rand.New(rand.NewPCG(seed, 3))
	rows := make([][]float64, n)
	y := make([]float64, n)
	for i := range rows {
		a, b := rng.IntN(2), rng.IntN(2)
		rows[i] = []float64{float64(a) + rng.Float64()*0.4 - 0.2, float64(b) + rng.Float64()*0.4 - 0.2}
		y[i] = float64(a ^ b)
	}
	return numericFrame(rows), y
}

func accuracy(t *testing.T, m *Model, x *Frame, y []float64) float64 {
	t.Helper()
	pred := m.Predict(x)
	right := 0
	for i := range y {
		if pred[i] == y[i] {
			right++
		}
	}
	return float64(right) / float64(len(y))
}

func fit(t *testing.T, algorithm, task string, x *Frame, y []float64, k int) *Model {
	t.Helper()
	m, err := Fit(context.Background(), Spec{Task: task, Algorithm: algorithm, Classes: k, Seed: 42}, x, y, nil)
	if err != nil {
		t.Fatalf("fitting %s: %v", algorithm, err)
	}
	return m
}
