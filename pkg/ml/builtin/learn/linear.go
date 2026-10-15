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
	"errors"
	"fmt"
	"math"
)

// The linear model's settings. Regression is ridge regression with a small
// penalty, which leaves the least-squares answer of the worker's
// LinearRegression all but unchanged while keeping it defined when one-hot
// columns are collinear. Classification is L2-penalised logistic regression
// with scikit-learn's default strength (C = 1): binary for two classes,
// multinomial for more.
const (
	ridgeAlpha    = 1e-3
	logisticC     = 1.0
	linearMaxIter = 1000
)

// Linear is a fitted linear model: Out rows of weights over the encoded
// columns, and Out intercepts. A regression has Out 1; a binary classifier
// has one row, the second class's log-odds; a multinomial one a row per
// class.
type Linear struct {
	Weights   [][]float64 `json:"weights"`
	Intercept []float64   `json:"intercept"`
	Classes   int         `json:"classes,omitempty"`
}

func (l *Linear) check(width, out int) error {
	rows := out
	if out == 2 {
		rows = 1
	}
	if len(l.Weights) != rows || len(l.Intercept) != rows {
		return fmt.Errorf("the linear model has %d rows of weights for %d outputs", len(l.Weights), out)
	}
	for _, w := range l.Weights {
		if len(w) != width {
			return fmt.Errorf("the linear model has %d weights for %d columns", len(w), width)
		}
	}
	if !finite(append(l.Weights, l.Intercept)...) {
		return errors.New("the linear model holds a value that is not finite")
	}
	return nil
}

// addGrad adds g times row i (and g for the intercept) to the gradient of
// the output starting at base.
func addGrad(grad []float64, base, width int, x *Matrix, i int, g float64) {
	x.AddTo(i, grad[base:base+width], g)
	grad[base+width] += g
}

func (l *Linear) predict(x *Matrix) [][]float64 {
	out := make([][]float64, x.N)
	z := make([]float64, len(l.Weights))
	for i := range x.N {
		for r, wr := range l.Weights {
			z[r] = l.Intercept[r] + x.Dot(i, wr)
		}
		switch l.Classes {
		case 0:
			out[i] = []float64{z[0]}
		case 2:
			p := sigmoid(z[0])
			out[i] = []float64{1 - p, p}
		default:
			out[i] = softmax(z)
		}
	}
	return out
}

func sigmoid(z float64) float64 {
	if z >= 0 {
		return 1 / (1 + math.Exp(-z))
	}
	e := math.Exp(z)
	return e / (1 + e)
}

// softplus is log(1 + e^z), without overflow.
func softplus(z float64) float64 {
	if z > 30 {
		return z
	}
	return math.Log1p(math.Exp(z))
}

func logSumExp(z []float64) float64 {
	m := math.Inf(-1)
	for _, v := range z {
		m = math.Max(m, v)
	}
	s := 0.0
	for _, v := range z {
		s += math.Exp(v - m)
	}
	return m + math.Log(s)
}

func softmax(z []float64) []float64 {
	lse := logSumExp(z)
	out := make([]float64, len(z))
	for k, v := range z {
		out[k] = math.Exp(v - lse)
	}
	return out
}
