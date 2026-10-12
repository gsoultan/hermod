package learn

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
)

// MLPParams are the network's settings, the worker's (deep.py): a zero
// value takes the default, and WithDefaults checks the same bounds.
type MLPParams struct {
	HiddenLayers []int   `json:"hidden_layers,omitempty"`
	Epochs       int     `json:"epochs,omitempty"`
	BatchSize    int     `json:"batch_size,omitempty"`
	LearningRate float64 `json:"learning_rate,omitempty"`
	Patience     int     `json:"patience,omitempty"`
}

// The network's defaults and bounds, the worker's.
const (
	maxLayers   = 5
	maxUnits    = 1024
	maxEpochs   = 1000
	maxBatch    = 4096
	maxPatience = 1000
	// A tenth of the training rows, when there are at least
	// minValidationRows, is held back to stop early.
	validationFraction = 0.1
	minValidationRows  = 10
)

// WithDefaults fills the unset settings with the defaults (hidden layers 64
// and 32, 200 epochs, batches of 32, a learning rate of 0.001, a patience of
// 10) and checks them.
func (p MLPParams) WithDefaults() (MLPParams, error) {
	if p.HiddenLayers == nil {
		p.HiddenLayers = []int{64, 32}
	}
	if len(p.HiddenLayers) < 1 || len(p.HiddenLayers) > maxLayers {
		return p, fmt.Errorf("'params.hidden_layers' must list 1 to %d layer sizes, each an integer from 1 to %d", maxLayers, maxUnits)
	}
	for _, u := range p.HiddenLayers {
		if u < 1 || u > maxUnits {
			return p, fmt.Errorf("'params.hidden_layers' must list 1 to %d layer sizes, each an integer from 1 to %d", maxLayers, maxUnits)
		}
	}
	type bound struct {
		name     string
		v        *int
		def, max int
	}
	for _, b := range []bound{
		{"epochs", &p.Epochs, 200, maxEpochs}, {"batch_size", &p.BatchSize, 32, maxBatch}, {"patience", &p.Patience, 10, maxPatience},
	} {
		if *b.v == 0 {
			*b.v = b.def
		}
		if *b.v < 1 || *b.v > b.max {
			return p, fmt.Errorf("'params.%s' must be an integer from 1 to %d", b.name, b.max)
		}
	}
	if p.LearningRate == 0 {
		p.LearningRate = 0.001
	}
	if !(p.LearningRate > 0 && p.LearningRate <= 1) {
		return p, errors.New("'params.learning_rate' must be a number above 0 and at most 1")
	}
	return p, nil
}

// Net is a multilayer perceptron: ReLU hidden layers, then a softmax (a
// classifier) or a linear output (a regression, which learns the target
// standardised by YMean and YStd).
type Net struct {
	Layers  []Layer   `json:"layers"`
	Classes int       `json:"classes,omitempty"`
	YMean   float64   `json:"y_mean,omitempty"`
	YStd    float64   `json:"y_std,omitempty"`
	Params  MLPParams `json:"params"`
	// Epochs is how many epochs ran before it stopped; the weights are the
	// ones of the epoch that did best on the rows held back.
	Epochs int `json:"epochs"`
}

// Layer is one dense layer: W[out][in] and B[out].
type Layer struct {
	W [][]float64 `json:"w"`
	B []float64   `json:"b"`
}

func (n *Net) check(width, out int) error {
	if len(n.Layers) < 2 || len(n.Layers) > maxLayers+1 {
		return fmt.Errorf("the network has %d layers", len(n.Layers))
	}
	in := width
	for i, l := range n.Layers {
		if len(l.W) != len(l.B) || len(l.W) == 0 || len(l.W) > max(maxUnits, out) {
			return fmt.Errorf("layer %d has %d rows of weights and %d biases", i, len(l.W), len(l.B))
		}
		for _, row := range l.W {
			if len(row) != in {
				return fmt.Errorf("layer %d takes %d inputs, not %d", i, len(row), in)
			}
			if !finite(row) {
				return errors.New("the network holds a value that is not finite")
			}
		}
		if !finite(l.B) {
			return errors.New("the network holds a value that is not finite")
		}
		in = len(l.W)
	}
	if in != out {
		return fmt.Errorf("the network has %d outputs, not %d", in, out)
	}
	if out == 1 && !(n.YStd > 0) {
		return errors.New("the network's target scale is not positive")
	}
	return nil
}

// netState is the network being trained, with its gradients and Adam's
// moments.
type netState struct {
	net        *Net
	grads, m1  []Layer
	m2         []Layer
	act, delta [][]float64
	step       int
}

func zeroLike(ls []Layer) []Layer {
	out := make([]Layer, len(ls))
	for i, l := range ls {
		out[i] = Layer{W: make([][]float64, len(l.W)), B: make([]float64, len(l.B))}
		for r := range l.W {
			out[i].W[r] = make([]float64, len(l.W[r]))
		}
	}
	return out
}

func cloneLayers(ls []Layer) []Layer {
	out := zeroLike(ls)
	for i, l := range ls {
		copy(out[i].B, l.B)
		for r := range l.W {
			copy(out[i].W[r], l.W[r])
		}
	}
	return out
}

// initLayers makes dense layers of the given sizes over in inputs, with
// PyTorch's default initialisation: uniform within one over the root of a
// layer's inputs.
func initLayers(in int, sizes []int, rng *rand.Rand) []Layer {
	var out []Layer
	for _, units := range sizes {
		bound := 1 / math.Sqrt(float64(max(in, 1)))
		l := Layer{W: make([][]float64, units), B: make([]float64, units)}
		for r := range l.W {
			l.W[r] = make([]float64, in)
			for c := range l.W[r] {
				l.W[r][c] = (rng.Float64()*2 - 1) * bound
			}
			l.B[r] = (rng.Float64()*2 - 1) * bound
		}
		out = append(out, l)
		in = units
	}
	return out
}

// holdBack splits rows, in a random order, into the rows to fit on and a
// tenth to stop early by; with fewer than minValidationRows, none is held
// back.
func holdBack(order []int) (fit, valid []int) {
	if len(order) < minValidationRows {
		return order, nil
	}
	nv := max(1, int(math.Round(validationFraction*float64(len(order)))))
	return order[nv:], order[:nv]
}

func newNetState(net *Net) *netState {
	st := &netState{net: net, grads: zeroLike(net.Layers), m1: zeroLike(net.Layers), m2: zeroLike(net.Layers),
		act: make([][]float64, len(net.Layers)), delta: make([][]float64, len(net.Layers))}
	for i, l := range net.Layers {
		st.act[i] = make([]float64, len(l.B))
		st.delta[i] = make([]float64, len(l.B))
	}
	return st
}

func fitNet(ctx context.Context, x *Matrix, y, w []float64, spec Spec) (*Net, error) {
	params, err := spec.MLP.WithDefaults()
	if err != nil {
		return nil, err
	}
	rng := newRand(spec.Seed, 7)
	out := 1
	if spec.Classes > 0 {
		out = spec.Classes
	}
	net := &Net{Classes: spec.Classes, Params: params, YStd: 1}
	net.Layers = initLayers(x.Width(), append(append([]int(nil), params.HiddenLayers...), out), rng)

	target := y
	if spec.Classes == 0 {
		net.YMean, net.YStd = meanStd(y, 0)
		target = make([]float64, len(y))
		for i, v := range y {
			target[i] = (v - net.YMean) / net.YStd
		}
	}
	fitRows, validRows := holdBack(rng.Perm(x.N))

	st := newNetState(net)
	best, bestLoss, stale := cloneLayers(net.Layers), math.Inf(1), 0
	for epoch := 1; epoch <= params.Epochs; epoch++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rng.Shuffle(len(fitRows), func(i, j int) { fitRows[i], fitRows[j] = fitRows[j], fitRows[i] })
		for at := 0; at < len(fitRows); at += params.BatchSize {
			st.batch(x, target, w, fitRows[at:min(at+params.BatchSize, len(fitRows))], params.LearningRate)
		}
		net.Epochs = epoch
		if validRows == nil {
			continue
		}
		if loss := st.loss(x, target, w, validRows); loss < bestLoss-1e-12 {
			bestLoss, stale, best = loss, 0, cloneLayers(net.Layers)
		} else if stale++; stale >= params.Patience {
			break
		}
	}
	if validRows != nil {
		net.Layers = best
	}
	return net, diverged(net.Layers)
}

// diverged refuses a network whose weights are no longer finite.
func diverged(layers []Layer) error {
	for _, l := range layers {
		if !finite(l.B) || !finite(l.W...) {
			return errors.New("the network diverged: try a smaller learning rate")
		}
	}
	return nil
}

// forward computes every layer's activations for row i into st.act; the
// last is the output before softmax.
func (st *netState) forward(x *Matrix, i int) {
	n := st.net
	for li, l := range n.Layers {
		a := st.act[li]
		for r := range l.W {
			var z float64
			if li == 0 {
				z = l.B[r] + x.Dot(i, l.W[r])
			} else {
				z = l.B[r] + dot(l.W[r], st.act[li-1])
			}
			if li < len(n.Layers)-1 && z < 0 {
				z = 0
			}
			a[r] = z
		}
	}
}

// rowLoss is row i's loss and, into the last delta, its gradient with
// respect to the output, after a forward pass.
func (st *netState) rowLoss(y float64) float64 {
	last := len(st.net.Layers) - 1
	z, d := st.act[last], st.delta[last]
	if st.net.Classes == 0 {
		e := z[0] - y
		d[0] = e
		return 0.5 * e * e
	}
	p := softmax(z)
	for k := range d {
		d[k] = p[k]
	}
	d[int(y)]--
	return -math.Log(math.Max(p[int(y)], 1e-300))
}

// batch takes one Adam step on the weighted mean gradient of rows.
func (st *netState) batch(x *Matrix, y, w []float64, rows []int, rate float64) {
	for _, g := range st.grads {
		clear(g.B)
		for r := range g.W {
			clear(g.W[r])
		}
	}
	tot := 0.0
	for _, i := range rows {
		wi := 1.0
		if w != nil {
			wi = w[i]
		}
		tot += wi
		st.forward(x, i)
		st.rowLoss(y[i])
		st.backward(x, i, wi)
	}
	if tot <= 0 {
		return
	}
	st.adam(1/tot, rate)
}

// backward adds row i's gradient, weighted wi, to st.grads, from the
// output's delta back through each layer.
func (st *netState) backward(x *Matrix, i int, wi float64) {
	for li := len(st.net.Layers) - 1; li >= 0; li-- {
		d, g := st.delta[li], st.grads[li]
		for r := range d {
			gr := wi * d[r]
			g.B[r] += gr
			if li == 0 {
				x.AddTo(i, g.W[r], gr)
			} else {
				axpy(gr, st.act[li-1], g.W[r])
			}
		}
		if li == 0 {
			return
		}
		prev := st.delta[li-1]
		clear(prev)
		for r, row := range st.net.Layers[li].W {
			axpy(d[r], row, prev)
		}
		for k, a := range st.act[li-1] {
			if a <= 0 {
				prev[k] = 0
			}
		}
	}
}

// adam takes one Adam step (beta1 0.9, beta2 0.999, epsilon 1e-8, as
// PyTorch's and Keras's defaults) with the gradients scaled by s.
func (st *netState) adam(s, rate float64) {
	const b1, b2, eps = 0.9, 0.999, 1e-8
	st.step++
	c1 := 1 - math.Pow(b1, float64(st.step))
	c2 := 1 - math.Pow(b2, float64(st.step))
	upd := func(p, g, m, v []float64) {
		for j := range p {
			gj := g[j] * s
			m[j] = b1*m[j] + (1-b1)*gj
			v[j] = b2*v[j] + (1-b2)*gj*gj
			p[j] -= rate * (m[j] / c1) / (math.Sqrt(v[j]/c2) + eps)
		}
	}
	for li, l := range st.net.Layers {
		upd(l.B, st.grads[li].B, st.m1[li].B, st.m2[li].B)
		for r := range l.W {
			upd(l.W[r], st.grads[li].W[r], st.m1[li].W[r], st.m2[li].W[r])
		}
	}
}

// loss is the weighted mean loss over rows.
func (st *netState) loss(x *Matrix, y, w []float64, rows []int) float64 {
	sum, tot := 0.0, 0.0
	for _, i := range rows {
		wi := 1.0
		if w != nil {
			wi = w[i]
		}
		st.forward(x, i)
		sum += wi * st.rowLoss(y[i])
		tot += wi
	}
	if tot == 0 {
		return 0
	}
	return sum / tot
}

func (n *Net) predict(x *Matrix) [][]float64 {
	st := &netState{net: n, act: make([][]float64, len(n.Layers))}
	for i, l := range n.Layers {
		st.act[i] = make([]float64, len(l.B))
	}
	out := make([][]float64, x.N)
	last := len(n.Layers) - 1
	for i := range x.N {
		st.forward(x, i)
		z := st.act[last]
		if n.Classes == 0 {
			out[i] = []float64{z[0]*n.YStd + n.YMean}
			continue
		}
		out[i] = softmax(z)
	}
	return out
}
