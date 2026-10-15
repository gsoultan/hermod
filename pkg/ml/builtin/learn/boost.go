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
	"errors"
	"fmt"
)

// Gradient boosting's settings: scikit-learn's GradientBoosting defaults,
// 100 stages of depth-3 trees with a learning rate of 0.1, squared error for
// a regression and log loss for a classification (one tree per stage for
// two classes, one per class for more), each leaf set by a Newton step.
const (
	boostStages = 100
	boostDepth  = 3
	boostRate   = 0.1
)

// Boost is a gradient-boosted ensemble: Init, then each stage's trees (Out
// of them, one per output), whose leaves already hold the learning rate's
// share. A regression's sum is its value; a classifier's are log-odds.
type Boost struct {
	Init  []float64 `json:"init"`
	Trees []*Tree   `json:"trees"`
	Out   int       `json:"out"`
	// Classes is 0 for a regression, else the class count; two classes
	// keep one output, the second class's log-odds.
	Classes int `json:"classes,omitempty"`
}

func (b *Boost) check(nNum int, catSize []int, out int) error {
	want := 1
	if out > 2 {
		want = out
	}
	if b.Out != want || len(b.Init) != want || len(b.Trees)%want != 0 {
		return fmt.Errorf("the boosted model has %d outputs, %d initial values and %d trees", b.Out, len(b.Init), len(b.Trees))
	}
	if (out == 1) != (b.Classes == 0) {
		return errors.New("the boosted model's classes do not match its task")
	}
	for _, t := range b.Trees {
		if t == nil {
			return errors.New("the boosted model has an empty tree")
		}
		if err := t.check(nNum, catSize, 1); err != nil {
			return err
		}
	}
	if !finite(b.Init) {
		return errors.New("the boosted model's initial values are not finite")
	}
	return nil
}

func (b *Boost) predict(x *Matrix) [][]float64 {
	out := make([][]float64, x.N)
	z := make([]float64, b.Out)
	for i := range x.N {
		copy(z, b.Init)
		for j, t := range b.Trees {
			z[j%b.Out] += t.leaf(x, i, 1)[0]
		}
		switch {
		case b.Classes == 0:
			out[i] = []float64{z[0]}
		case b.Out == 1:
			p := sigmoid(z[0])
			out[i] = []float64{1 - p, p}
		default:
			out[i] = softmax(z)
		}
	}
	return out
}
