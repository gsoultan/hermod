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
	"errors"
	"math"
	"math/rand/v2"
)

// The tolerance int8 weights are kept within: an accuracy at most half a
// point below float32's, or an RMSE at most 1% above it, on the rows held
// back.
const (
	accuracyTolerance = 0.005
	rmseTolerance     = 0.01
)

// QuantCheck is what a network's int8 weights scored against its float32
// ones on the rows held back to stop early (all its training rows when too
// few were held back): accuracy for a classifier, RMSE for a regression.
// Tolerance is the accuracy points int8 may lose, or the share of RMSE it
// may add.
type QuantCheck struct {
	Metric    string  `json:"metric"`
	Float32   float64 `json:"float32"`
	Int8      float64 `json:"int8"`
	Tolerance float64 `json:"tolerance"`
}

func newQuantCheck(task string, f32, i8 float64) QuantCheck {
	if task == TaskRegression {
		return QuantCheck{Metric: "rmse", Float32: f32, Int8: i8, Tolerance: rmseTolerance}
	}
	return QuantCheck{Metric: "accuracy", Float32: f32, Int8: i8, Tolerance: accuracyTolerance}
}

// choose is the precision to keep for requested: float32 and int8 as asked,
// and for auto int8 when it scored within the tolerance.
func (q QuantCheck) choose(requested string) string {
	switch requested {
	case PrecisionFloat32, PrecisionInt8:
		return requested
	}
	const slack = 1e-12
	if q.Metric == "rmse" && q.Int8 <= q.Float32*(1+q.Tolerance)+slack ||
		q.Metric == "accuracy" && q.Int8 >= q.Float32-q.Tolerance-slack {
		return PrecisionInt8
	}
	return PrecisionFloat32
}

// netTrainer trains a network in float32 with Adam (beta1 0.9, beta2 0.999,
// epsilon 1e-8, as PyTorch's and Keras's defaults) on mini-batches.
//
// A batch is two parallel passes. The first runs each row forward and its
// error back through the layers, a row to an item. The second sums the
// rows' gradient into each input's weights (and each layer's biases), an
// input to an item, in the batch's order, and takes the Adam step on them.
// Every value is computed the same way whichever goroutine computes it, so
// the network trained does not depend on the number of threads.
type netTrainer struct {
	net  *Net
	x    *Matrix32
	y, w []float64
	pool *pool

	// act and delta are each batch row's activations and errors, by layer.
	act, delta [][][]float32
	rows       []int
	grads      []gradLayer
	m1, m2     []gradLayer
	units      []gradUnit
	// weights is how many weights the network has: about the multiply-adds
	// a row costs each way.
	weights int
	// steps is Adam's steps so far; step and c2 are this one's rate over
	// the first moment's bias correction, and one over the root of the
	// second's (PyTorch's form); scale makes the summed gradient a mean.
	steps    int
	step, c2 float32
	scale    float32
}

type gradLayer struct{ w, b []float32 }

// gradUnit is one item of the second pass: input j's weights of layer
// (kind unitInput), text feature j's categories' weights of the first
// layer (unitCategory), or the layer's biases (unitBias).
type gradUnit struct {
	kind, layer, j int
}

const (
	unitInput = iota
	unitCategory
	unitBias
)

func newNetTrainer(net *Net, x *Matrix32, y, w []float64, batch int, p *pool) *netTrainer {
	t := &netTrainer{net: net, x: x, y: y, w: w, pool: p,
		act: make([][][]float32, batch), delta: make([][][]float32, batch)}
	for s := range batch {
		t.act[s], t.delta[s] = net.newActs(), net.newActs()
	}
	zero := func() []gradLayer {
		g := make([]gradLayer, len(net.Dense))
		for i, d := range net.Dense {
			g[i] = gradLayer{w: make([]float32, d.In*d.Out), b: make([]float32, d.Out)}
		}
		return g
	}
	t.grads, t.m1, t.m2 = zero(), zero(), zero()
	for li, d := range net.Dense {
		t.weights += d.In * d.Out
		inputs := d.In
		if li == 0 {
			inputs = x.NumW
			for c := range x.CatOff {
				t.units = append(t.units, gradUnit{unitCategory, 0, c})
			}
		}
		for j := range inputs {
			t.units = append(t.units, gradUnit{unitInput, li, j})
		}
		t.units = append(t.units, gradUnit{unitBias, li, 0})
	}
	return t
}

// initDense makes dense layers of the given sizes over in inputs, with
// PyTorch's default initialisation: uniform within one over the root of a
// layer's inputs, drawn unit by unit.
func initDense(in int, sizes []int, rng *rand.Rand) []Dense {
	var out []Dense
	for _, units := range sizes {
		bound := 1 / math.Sqrt(float64(max(in, 1)))
		d := Dense{In: in, Out: units, w32: make([]float32, in*units), b32: make([]float32, units)}
		for r := range units {
			for c := range in {
				d.w32[c*units+r] = float32((rng.Float64()*2 - 1) * bound)
			}
			d.b32[r] = float32((rng.Float64()*2 - 1) * bound)
		}
		out = append(out, d)
		in = units
	}
	return out
}

func fitNet(ctx context.Context, x *Matrix32, y, w []float64, spec Spec) (*Net, error) {
	params, err := spec.MLP.WithDefaults()
	if err != nil {
		return nil, err
	}
	rng := newRand(spec.Seed, 7)
	out := 1
	if spec.Classes > 0 {
		out = spec.Classes
	}
	threads := threadCap(params.Threads, spec.Workers)
	net := &Net{Classes: spec.Classes, Params: params, YStd: 1, Threads: threads}
	net.Dense = initDense(x.Width, append(append([]int(nil), params.HiddenLayers...), out), rng)

	target := net.standardise(y)
	fitRows, validRows := holdBack(rng.Perm(x.N))

	p := newPool(threads)
	t := newNetTrainer(net, x, target, w, min(params.BatchSize, len(fitRows)), p)
	best, bestLoss, stale := net.snapshot(), math.Inf(1), 0
	for epoch := 1; epoch <= params.Epochs; epoch++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rng.Shuffle(len(fitRows), func(i, j int) { fitRows[i], fitRows[j] = fitRows[j], fitRows[i] })
		for at := 0; at < len(fitRows); at += params.BatchSize {
			t.batch(fitRows[at:min(at+params.BatchSize, len(fitRows))], float32(params.LearningRate))
		}
		net.Epochs = epoch
		if validRows == nil {
			continue
		}
		if loss := t.loss(validRows); loss < bestLoss-1e-12 {
			bestLoss, stale, best = loss, 0, net.snapshot()
		} else if stale++; stale >= params.Patience {
			break
		}
	}
	if validRows != nil {
		net.restore(best)
	}
	if !net.finite() {
		return nil, errors.New("the network diverged: try a smaller learning rate")
	}
	checkRows := validRows
	if checkRows == nil {
		checkRows = fitRows
	}
	net.settlePrecision(params.Precision, x, target, w, checkRows, p)
	return net, nil
}

// finite reports whether every weight and bias is a finite number.
func (n *Net) finite() bool {
	for _, d := range n.Dense {
		if !finite32(d.w32) || !finite32(d.b32) {
			return false
		}
	}
	return true
}

// standardise returns a regression's targets in standard units, recording
// their mean and spread on n, and a classifier's as they are.
func (n *Net) standardise(y []float64) []float64 {
	if n.Classes > 0 {
		return y
	}
	n.YMean, n.YStd = meanStd(y, 0)
	target := make([]float64, len(y))
	for i, v := range y {
		target[i] = (v - n.YMean) / n.YStd
	}
	return target
}

func (n *Net) snapshot() []gradLayer {
	out := make([]gradLayer, len(n.Dense))
	for i, d := range n.Dense {
		out[i] = gradLayer{w: append([]float32(nil), d.w32...), b: append([]float32(nil), d.b32...)}
	}
	return out
}

func (n *Net) restore(s []gradLayer) {
	for i := range n.Dense {
		copy(n.Dense[i].w32, s[i].w)
		copy(n.Dense[i].b32, s[i].b)
	}
}

// batch takes one Adam step on the weighted mean gradient of rows.
func (t *netTrainer) batch(rows []int, rate float32) {
	tot := 0.0
	for _, i := range rows {
		tot += t.weight(i)
	}
	if tot <= 0 {
		return
	}
	t.rows = rows
	work := len(rows) * t.weights
	t.pool.run(len(rows), work, func(lo, hi int) {
		for s := lo; s < hi; s++ {
			t.backprop(s, rows[s])
		}
	})
	const b1, b2 = 0.9, 0.999
	t.steps++
	k := float64(t.steps)
	t.scale = float32(1 / tot)
	t.step = float32(float64(rate) / (1 - math.Pow(b1, k)))
	t.c2 = float32(1 / math.Sqrt(1-math.Pow(b2, k)))
	t.pool.run(len(t.units), work, func(lo, hi int) {
		for _, u := range t.units[lo:hi] {
			t.update(u)
		}
	})
}

func (t *netTrainer) weight(i int) float64 {
	if t.w == nil {
		return 1
	}
	return t.w[i]
}

// backprop runs row i forward into batch slot s and its error, weighted,
// back through every layer into the slot's deltas.
func (t *netTrainer) backprop(s, i int) {
	n, act, delta := t.net, t.act[s], t.delta[s]
	n.forward32(t.x, i, act)
	last := len(n.Dense) - 1
	outputDelta(n, act[last], t.y[i], delta[last])
	if wi := float32(t.weight(i)); wi != 1 {
		for r := range delta[last] {
			delta[last][r] *= wi
		}
	}
	for li := last; li > 0; li-- {
		d, prev, a := &n.Dense[li], delta[li-1], act[li-1]
		for j := range prev {
			if a[j] <= 0 {
				prev[j] = 0
				continue
			}
			prev[j] = dot32(d.w32[j*d.Out:(j+1)*d.Out], delta[li])
		}
	}
}

// outputDelta is the loss's gradient with respect to the output z: the
// softmax's probabilities less the true class, or the error.
func outputDelta(n *Net, z []float32, y float64, d []float32) {
	if n.Classes == 0 {
		d[0] = z[0] - float32(y)
		return
	}
	p := n.output(z)
	for k := range d {
		d[k] = float32(p[k])
	}
	d[int(y)]--
}

// update sums the batch's gradient for one unit and takes its Adam step.
func (t *netTrainer) update(u gradUnit) {
	d, g := &t.net.Dense[u.layer], &t.grads[u.layer]
	switch u.kind {
	case unitBias:
		clear(g.b)
		for s := range t.rows {
			axpy32(1, t.delta[s][u.layer], g.b)
		}
		t.adam(d.b32, g.b, t.m1[u.layer].b, t.m2[u.layer].b)
	case unitCategory:
		lo, hi := t.x.CatOff[u.j]*d.Out, (t.x.CatOff[u.j]+t.x.CatSize[u.j])*d.Out
		clear(g.w[lo:hi])
		for s, i := range t.rows {
			if code := t.x.cat(i)[u.j]; code >= 0 {
				at := (t.x.CatOff[u.j] + int(code)) * d.Out
				axpy32(1, t.delta[s][0], g.w[at:at+d.Out])
			}
		}
		t.adam(d.w32[lo:hi], g.w[lo:hi], t.m1[0].w[lo:hi], t.m2[0].w[lo:hi])
	default:
		lo, hi := u.j*d.Out, (u.j+1)*d.Out
		gw := g.w[lo:hi]
		clear(gw)
		for s, i := range t.rows {
			var a float32
			if u.layer == 0 {
				a = t.x.num(i)[u.j]
			} else {
				a = t.act[s][u.layer-1][u.j]
			}
			if a != 0 {
				axpy32(a, t.delta[s][u.layer], gw)
			}
		}
		t.adam(d.w32[lo:hi], gw, t.m1[u.layer].w[lo:hi], t.m2[u.layer].w[lo:hi])
	}
}

// adam takes the Adam step on p for the gradient g, scaled to the batch's
// mean, with the moments m and v.
//
// A moment that decays below tiny is set to zero: left alone, the moments of
// a weight whose gradient stays zero (a category the batches do not have, a
// unit that does not fire) decay into float32's subnormal range, where
// each operation costs the CPU tens of times more; they then took over half
// of a 1M-row training. A weight whose gradient and moments are all zero
// does not move, and is skipped.
func (t *netTrainer) adam(p, g, m, v []float32) {
	const b1, b2, eps, tiny = 0.9, 0.999, 1e-8, 1e-30
	for j := range p {
		gj := g[j] * t.scale
		if gj == 0 && m[j] == 0 && v[j] == 0 {
			continue
		}
		mj := b1*m[j] + (1-b1)*gj
		vj := b2*v[j] + (1-b2)*gj*gj
		if mj < tiny && mj > -tiny {
			mj = 0
		}
		if vj < tiny {
			vj = 0
		}
		m[j], v[j] = mj, vj
		p[j] -= t.step * mj / (float32(math.Sqrt(float64(vj)))*t.c2 + eps)
	}
}

// loss is the weighted mean loss over rows, each row's computed in
// parallel and summed in order.
func (t *netTrainer) loss(rows []int) float64 {
	per := make([]float64, len(rows))
	t.pool.run(len(rows), len(rows)*t.weights, func(lo, hi int) {
		act := t.net.newActs()
		for k := lo; k < hi; k++ {
			t.net.forward32(t.x, rows[k], act)
			per[k] = rowLoss(t.net, act[len(act)-1], t.y[rows[k]])
		}
	})
	sum, tot := 0.0, 0.0
	for k, i := range rows {
		wi := t.weight(i)
		sum += wi * per[k]
		tot += wi
	}
	if tot == 0 {
		return 0
	}
	return sum / tot
}

func rowLoss(n *Net, z []float32, y float64) float64 {
	if n.Classes == 0 {
		e := float64(z[0]) - y
		return 0.5 * e * e
	}
	p := n.output(z)
	return -math.Log(math.Max(p[int(y)], 1e-300))
}

// settlePrecision decides which precision the network keeps for
// requested, measuring its float32 and int8 weights on rows unless float32
// was asked for, and packs the weights to save. y is the target as the
// network learns it (class indices, or standardised values).
func (n *Net) settlePrecision(requested string, x *Matrix32, y, w []float64, rows []int, p *pool) string {
	n.Precision, n.Quantization = PrecisionFloat32, nil
	if requested != PrecisionFloat32 {
		f32 := n.score(x, y, w, rows, p)
		for i := range n.Dense {
			d := &n.Dense[i]
			d.w8, d.Scale = quantize(d.w32)
		}
		check := newQuantCheck(taskOf(n), f32, n.score(x, y, w, rows, p))
		n.Quantization = &check
		n.Precision = check.choose(requested)
	}
	for i := range n.Dense {
		d := &n.Dense[i]
		if n.Precision == PrecisionInt8 {
			d.w32 = nil
		} else {
			d.w8, d.Scale = nil, 0
		}
		d.pack()
	}
	return n.Precision
}

func taskOf(n *Net) string {
	if n.Classes == 0 {
		return TaskRegression
	}
	return TaskClassification
}

// rowScore is a row's squared error in the target's units, or 1 when the
// class with the highest output is the row's and 0 otherwise.
func (n *Net) rowScore(z []float32, y float64) float64 {
	if n.Classes == 0 {
		e := (float64(z[0]) - y) * n.YStd
		return e * e
	}
	best := 0
	for c := range z {
		if z[c] > z[best] {
			best = c
		}
	}
	if float64(best) == y {
		return 1
	}
	return 0
}

// score is the network's weighted accuracy, or its RMSE in the target's
// units, on rows.
func (n *Net) score(x *Matrix32, y, w []float64, rows []int, p *pool) float64 {
	per := make([]float64, len(rows))
	weights := 0
	for _, d := range n.Dense {
		weights += d.In * d.Out
	}
	p.run(len(rows), len(rows)*weights, func(lo, hi int) {
		act := n.newActs()
		for k := lo; k < hi; k++ {
			i := rows[k]
			n.forward32(x, i, act)
			per[k] = n.rowScore(act[len(act)-1], y[i])
		}
	})
	sum, tot := 0.0, 0.0
	for k, i := range rows {
		wi := 1.0
		if w != nil {
			wi = w[i]
		}
		sum += wi * per[k]
		tot += wi
	}
	if tot == 0 {
		return 0
	}
	if n.Classes == 0 {
		return math.Sqrt(sum / tot)
	}
	return sum / tot
}
