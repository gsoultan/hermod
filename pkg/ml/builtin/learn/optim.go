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
	"context"
	"math"
)

// fitLinear fits a ridge regression (squared error plus ridgeAlpha times
// the weights' squared norm) or a logistic regression (log loss plus 1/C of
// it, binary or multinomial), as scikit-learn's Ridge and LogisticRegression
// do, by L-BFGS.
func fitLinear(ctx context.Context, x *Matrix, y, w []float64, spec Spec) (*Linear, error) {
	o := &linearObjective{x: x, y: y, w: w, width: x.Width(), rows: 1, regression: spec.Task == TaskRegression, penalty: ridgeAlpha}
	if spec.Classes > 2 {
		o.rows = spec.Classes
	}
	if !o.regression {
		o.penalty = 1 / logisticC
	}
	o.z = make([]float64, o.rows)
	theta, err := lbfgs(ctx, o.eval, make([]float64, o.rows*(o.width+1)), linearMaxIter)
	if err != nil {
		return nil, err
	}
	l := &Linear{Classes: spec.Classes}
	for r := range o.rows {
		base := r * (o.width + 1)
		l.Weights = append(l.Weights, theta[base:base+o.width])
		l.Intercept = append(l.Intercept, theta[base+o.width])
	}
	return l, nil
}

// linearObjective is a linear model's loss and gradient. theta holds each
// output's weights then its intercept; the intercepts are not penalised.
type linearObjective struct {
	x          *Matrix
	y, w       []float64
	width      int
	rows       int // outputs: 1, or the classes when there are more than two
	regression bool
	penalty    float64
	z          []float64
}

func (o *linearObjective) eval(theta, grad []float64) float64 {
	clear(grad)
	loss := 0.0
	for i := range o.x.N {
		for r := range o.rows {
			base := r * (o.width + 1)
			o.z[r] = theta[base+o.width] + o.x.Dot(i, theta[base:base+o.width])
		}
		wi := 1.0
		if o.w != nil {
			wi = o.w[i]
		}
		loss += o.row(i, wi, grad)
	}
	for r := range o.rows {
		base := r * (o.width + 1)
		for j := range o.width {
			loss += 0.5 * o.penalty * theta[base+j] * theta[base+j]
			grad[base+j] += o.penalty * theta[base+j]
		}
	}
	return loss
}

// row is row i's loss, with weight wi, its gradient added to grad.
func (o *linearObjective) row(i int, wi float64, grad []float64) float64 {
	z := o.z
	switch {
	case o.regression:
		d := z[0] - o.y[i]
		addGrad(grad, 0, o.width, o.x, i, wi*d)
		return 0.5 * wi * d * d
	case o.rows == 1:
		// Binary: z is the log-odds of class 1.
		t := o.y[i]
		addGrad(grad, 0, o.width, o.x, i, wi*(sigmoid(z[0])-t))
		return wi * (softplus(z[0]) - t*z[0])
	}
	lse := logSumExp(z)
	for r := range o.rows {
		p := math.Exp(z[r] - lse)
		if r == int(o.y[i]) {
			p--
		}
		addGrad(grad, r*(o.width+1), o.width, o.x, i, wi*p)
	}
	return wi * (lse - z[int(o.y[i])])
}

// L-BFGS's settings: the pairs it remembers, and when it has converged.
const (
	lbfgsMemory = 10
	lbfgsGtol   = 1e-9
	lbfgsFtol   = 1e-12
)

// lbfgsState is L-BFGS's position and memory.
type lbfgsState struct {
	f      func(x, grad []float64) float64
	x, g   []float64
	fx     float64
	s, yv  [][]float64
	rho    []float64
	d      []float64
	alpha  []float64
	xn, gn []float64
}

// lbfgs minimises f from x0 with limited-memory BFGS and a backtracking
// (Armijo) line search, for at most maxIter iterations. f returns the value
// at x and writes the gradient into grad. It checks ctx every iteration.
func lbfgs(ctx context.Context, f func(x, grad []float64) float64, x0 []float64, maxIter int) ([]float64, error) {
	n := len(x0)
	st := &lbfgsState{f: f, x: append([]float64(nil), x0...), g: make([]float64, n), d: make([]float64, n),
		alpha: make([]float64, lbfgsMemory), xn: make([]float64, n), gn: make([]float64, n)}
	st.fx = f(st.x, st.g)
	for range maxIter {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if norm(st.g) <= lbfgsGtol*math.Max(1, norm(st.x)) || !st.step() {
			break
		}
	}
	return st.x, nil
}

// direction sets d to -H g by the two-loop recursion, or to -g when that is
// not a descent direction, and returns the slope along d.
func (st *lbfgsState) direction() float64 {
	copy(st.d, st.g)
	for k := len(st.s) - 1; k >= 0; k-- {
		st.alpha[k] = st.rho[k] * dot(st.s[k], st.d)
		axpy(-st.alpha[k], st.yv[k], st.d)
	}
	if len(st.s) > 0 {
		last := len(st.s) - 1
		scale(dot(st.s[last], st.yv[last])/dot(st.yv[last], st.yv[last]), st.d)
	}
	for k := range st.s {
		b := st.rho[k] * dot(st.yv[k], st.d)
		axpy(st.alpha[k]-b, st.s[k], st.d)
	}
	scale(-1, st.d)
	slope := dot(st.g, st.d)
	if slope >= 0 {
		// Not a descent direction: restart from the gradient.
		copy(st.d, st.g)
		scale(-1, st.d)
		slope = dot(st.g, st.d)
		st.s, st.yv, st.rho = nil, nil, nil
	}
	return slope
}

// step takes one iteration, and reports whether to go on: false once the
// value stops falling.
func (st *lbfgsState) step() bool {
	slope := st.direction()
	step := 1.0
	if len(st.s) == 0 {
		step = math.Min(1, 1/math.Max(norm(st.g), 1e-12))
	}
	var fNew float64
	for tries := 0; ; tries++ {
		for j := range st.x {
			st.xn[j] = st.x[j] + step*st.d[j]
		}
		fNew = st.f(st.xn, st.gn)
		if fNew <= st.fx+1e-4*step*slope || tries == 50 {
			break
		}
		step /= 2
	}
	if fNew > st.fx {
		return false
	}
	n := len(st.x)
	sk, yk := make([]float64, n), make([]float64, n)
	for j := range st.x {
		sk[j], yk[j] = st.xn[j]-st.x[j], st.gn[j]-st.g[j]
	}
	done := math.Abs(st.fx-fNew) <= lbfgsFtol*math.Max(1, math.Abs(st.fx))
	copy(st.x, st.xn)
	copy(st.g, st.gn)
	st.fx = fNew
	if sy := dot(sk, yk); sy > 1e-12 {
		if len(st.s) == lbfgsMemory {
			st.s, st.yv, st.rho = st.s[1:], st.yv[1:], st.rho[1:]
		}
		st.s, st.yv, st.rho = append(st.s, sk), append(st.yv, yk), append(st.rho, 1/sy)
	}
	return !done
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func norm(a []float64) float64 { return math.Sqrt(dot(a, a)) }

func axpy(a float64, x, y []float64) {
	for i := range x {
		y[i] += a * x[i]
	}
}

func scale(a float64, x []float64) {
	for i := range x {
		x[i] *= a
	}
}
