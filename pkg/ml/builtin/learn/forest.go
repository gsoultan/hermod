// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
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
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
)

// The random forest's settings: scikit-learn's defaults (100 trees grown
// until their leaves are pure, each on a bootstrap sample; a classifier
// tries the square root of the columns at each split, a regression all of
// them), except that a leaf holds at least one row in maxForestLeaves of the
// training rows, which bounds a model trained on a large table.
const (
	forestTrees     = 100
	maxForestLeaves = 10000
)

// Forest is a random forest: its trees' leaves hold class probabilities, or
// values, which a prediction averages.
type Forest struct {
	Trees []*Tree `json:"trees"`
	Out   int     `json:"out"`
}

func (f *Forest) check(nNum int, catSize []int, out int) error {
	if f.Out != out || len(f.Trees) == 0 {
		return fmt.Errorf("the forest has %d outputs and %d trees", f.Out, len(f.Trees))
	}
	for _, t := range f.Trees {
		if t == nil {
			return errors.New("the forest has an empty tree")
		}
		if err := t.check(nNum, catSize, out); err != nil {
			return err
		}
	}
	return nil
}

func fitForest(ctx context.Context, x *Matrix, y, w []float64, spec Spec) (*Forest, error) {
	b := binMatrix(x)
	out := max(1, spec.Classes)
	p := treeParams{minLeaf: max(1, (x.N+maxForestLeaves-1)/maxForestLeaves), classes: spec.Classes, out: out}
	if spec.Classes > 0 {
		p.mtry = max(1, int(math.Sqrt(float64(x.Width()))))
	}
	p.leafValue = func(rows []int) []float64 { return meanLeaf(rows, y, w, spec.Classes) }

	trees := make([]*Tree, forestTrees)
	workers := spec.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, forestTrees)
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for t := range next {
				// Each tree draws from its own stream, so the forest is the
				// same however the trees are spread over goroutines.
				rng := newRand(spec.Seed, uint64(t)+1)
				rows := make([]int, x.N)
				for i := range rows {
					rows[i] = rng.IntN(x.N)
				}
				trees[t] = growTree(b, y, w, rows, p, rng)
			}
		})
	}
	var err error
	for t := range forestTrees {
		if err = ctx.Err(); err != nil {
			break
		}
		next <- t
	}
	close(next)
	wg.Wait()
	if err != nil {
		return nil, err
	}
	return &Forest{Trees: trees, Out: out}, nil
}

// meanLeaf is a leaf's weighted class shares, or weighted mean value.
func meanLeaf(rows []int, y, w []float64, classes int) []float64 {
	if classes > 0 {
		v := make([]float64, classes)
		tot := 0.0
		for _, i := range rows {
			wi := 1.0
			if w != nil {
				wi = w[i]
			}
			v[int(y[i])] += wi
			tot += wi
		}
		if tot > 0 {
			for k := range v {
				v[k] /= tot
			}
		}
		return v
	}
	sum, tot := 0.0, 0.0
	for _, i := range rows {
		wi := 1.0
		if w != nil {
			wi = w[i]
		}
		sum += wi * y[i]
		tot += wi
	}
	if tot == 0 {
		return []float64{0}
	}
	return []float64{sum / tot}
}

func (f *Forest) predict(x *Matrix) [][]float64 {
	out := make([][]float64, x.N)
	for i := range x.N {
		acc := make([]float64, f.Out)
		for _, t := range f.Trees {
			for k, v := range t.leaf(x, i, f.Out) {
				acc[k] += v
			}
		}
		for k := range acc {
			acc[k] /= float64(len(f.Trees))
		}
		out[i] = acc
	}
	return out
}
