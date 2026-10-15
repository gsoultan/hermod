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
	"context"
	"math"
)

// booster fits a Boost: the rows, their raw predictions per output so far,
// and the residuals and probabilities each stage works from.
type booster struct {
	x       *Matrix
	bins    *binned
	y, w    []float64
	classes int // 0 for a regression
	k       int // outputs: 1, or the classes when there are more than two
	raw     [][]float64
	prob    [][]float64
	resid   []float64
	rows    []int
}

func fitBoost(ctx context.Context, x *Matrix, y, w []float64, spec Spec) (*Boost, error) {
	b := newBooster(x, y, w, spec.Classes)
	m := &Boost{Out: b.k, Classes: spec.Classes, Init: b.init()}
	for c := range b.k {
		for i := range b.raw[c] {
			b.raw[c][i] = m.Init[c]
		}
	}
	rng := newRand(spec.Seed, 0)
	for range boostStages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b.probabilities()
		for c := range b.k {
			b.residuals(c)
			t := growTree(b.bins, b.resid, w, b.rows, treeParams{maxDepth: boostDepth, minLeaf: 1, out: 1, leafValue: b.leaf}, rng)
			for i := range b.raw[c] {
				b.raw[c][i] += t.leaf(x, i, 1)[0]
			}
			m.Trees = append(m.Trees, t)
		}
	}
	return m, nil
}

func newBooster(x *Matrix, y, w []float64, classes int) *booster {
	b := &booster{x: x, bins: binMatrix(x), y: y, w: w, classes: classes, k: 1, resid: make([]float64, x.N), rows: make([]int, x.N)}
	if classes > 2 {
		b.k = classes
	}
	b.raw, b.prob = make([][]float64, b.k), make([][]float64, b.k)
	for c := range b.k {
		b.raw[c], b.prob[c] = make([]float64, x.N), make([]float64, x.N)
	}
	for i := range b.rows {
		b.rows[i] = i
	}
	return b
}

func (b *booster) weight(i int) float64 {
	if b.w == nil {
		return 1
	}
	return b.w[i]
}

// init is the initial raw predictions: the mean, the log-odds of class 1,
// or the log of each class's share.
func (b *booster) init() []float64 {
	tot, mean := 0.0, 0.0
	share := make([]float64, max(b.classes, 1))
	for i := range b.y {
		wi := b.weight(i)
		tot += wi
		if b.classes > 0 {
			share[int(b.y[i])] += wi
		} else {
			mean += wi * b.y[i]
		}
	}
	out := make([]float64, b.k)
	switch {
	case b.classes == 0:
		out[0] = mean / tot
	case b.k == 1:
		p := math.Min(math.Max(share[1]/tot, 1e-12), 1-1e-12)
		out[0] = math.Log(p / (1 - p))
	default:
		for c := range b.k {
			out[c] = math.Log(math.Max(share[c]/tot, 1e-12))
		}
	}
	return out
}

// probabilities sets each row's class probabilities from its raw
// predictions: a sigmoid for two classes, a softmax for more.
func (b *booster) probabilities() {
	if b.classes == 0 {
		return
	}
	if b.k == 1 {
		for i, r := range b.raw[0] {
			b.prob[0][i] = sigmoid(r)
		}
		return
	}
	z := make([]float64, b.k)
	for i := range b.x.N {
		for c := range b.k {
			z[c] = b.raw[c][i]
		}
		p := softmax(z)
		for c := range b.k {
			b.prob[c][i] = p[c]
		}
	}
}

// residuals are the negative gradients of output c's loss: the error of a
// regression, or the class indicator less its probability.
func (b *booster) residuals(c int) {
	for i := range b.resid {
		switch {
		case b.classes == 0:
			b.resid[i] = b.y[i] - b.raw[0][i]
		case b.k == 1:
			b.resid[i] = b.y[i] - b.prob[0][i]
		default:
			t := 0.0
			if int(b.y[i]) == c {
				t = 1
			}
			b.resid[i] = t - b.prob[c][i]
		}
	}
}

// leaf is a leaf's value: one Newton step on its rows, times the learning
// rate ((K-1)/K of it for K classes, as scikit-learn scales it).
func (b *booster) leaf(idx []int) []float64 {
	num, den := 0.0, 0.0
	for _, i := range idx {
		wi, r := b.weight(i), b.resid[i]
		num += wi * r
		switch {
		case b.classes == 0:
			den += wi
		case b.k == 1:
			den += wi * b.prob[0][i] * (1 - b.prob[0][i])
		default:
			a := math.Abs(r)
			den += wi * a * (1 - a)
		}
	}
	v := 0.0
	if math.Abs(den) > 1e-150 {
		v = num / den
	}
	if b.k > 1 {
		v *= float64(b.k-1) / float64(b.k)
	}
	return []float64{boostRate * v}
}
