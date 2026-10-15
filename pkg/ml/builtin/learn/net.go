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
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// MLPParams are the network's settings, the worker's (deep.py): a zero
// value takes the default, and WithDefaults checks the same bounds.
type MLPParams struct {
	HiddenLayers []int   `json:"hidden_layers,omitempty"`
	Epochs       int     `json:"epochs,omitempty"`
	BatchSize    int     `json:"batch_size,omitempty"`
	LearningRate float64 `json:"learning_rate,omitempty"`
	Patience     int     `json:"patience,omitempty"`
	// Precision is how the trained network keeps its weights: auto (int8
	// when it scores within the tolerance of float32 on the rows held back,
	// float32 otherwise), float32, or int8 whatever it scores.
	Precision string `json:"precision,omitempty"`
	// Threads bounds the goroutines that compute a batch's gradient: 0 for
	// all the CPUs Go may use but one (at least one). The network trained is
	// the same for any number.
	Threads int `json:"threads,omitempty"`
}

// The network's defaults and bounds, the worker's.
const (
	maxLayers   = 5
	maxUnits    = 1024
	maxEpochs   = 1000
	maxBatch    = 4096
	maxPatience = 1000
	maxThreads  = 256
	// A tenth of the training rows, when there are at least
	// minValidationRows, is held back to stop early.
	validationFraction = 0.1
	minValidationRows  = 10
)

// The precisions a network keeps its weights in.
const (
	PrecisionAuto    = "auto"
	PrecisionFloat32 = "float32"
	PrecisionInt8    = "int8"
)

// Precisions are the precisions MLPParams.Precision takes.
var Precisions = []string{PrecisionAuto, PrecisionFloat32, PrecisionInt8}

// WithDefaults fills the unset settings with the defaults (hidden layers 64
// and 32, 200 epochs, batches of 32, a learning rate of 0.001, a patience of
// 10, precision auto, the default thread cap) and checks them.
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
	if p.Precision == "" {
		p.Precision = PrecisionAuto
	}
	if !slices.Contains(Precisions, p.Precision) {
		return p, fmt.Errorf("'params.precision' must be one of %s", strings.Join(Precisions, ", "))
	}
	if p.Threads < 0 || p.Threads > maxThreads {
		return p, fmt.Errorf("'params.threads' must be an integer from 0 to %d (0 for all the CPUs but one)", maxThreads)
	}
	return p, nil
}

// Net is a multilayer perceptron: ReLU hidden layers, then a softmax (a
// classifier) or a linear output (a regression, which learns the target
// standardised by YMean and YStd).
//
// A network is trained in float32 and kept in Dense, as float32 or int8
// weights (Precision). One saved before that (format 1) has float64 Layers
// instead, and scores in float64 exactly as it always did.
type Net struct {
	Layers  []Layer   `json:"layers,omitempty"`
	Classes int       `json:"classes,omitempty"`
	YMean   float64   `json:"y_mean,omitempty"`
	YStd    float64   `json:"y_std,omitempty"`
	Params  MLPParams `json:"params"`
	// Epochs is how many epochs ran before it stopped; the weights are the
	// ones of the epoch that did best on the rows held back.
	Epochs int `json:"epochs"`

	Dense     []Dense `json:"dense,omitempty"`
	Precision string  `json:"precision,omitempty"`
	// Threads is how many goroutines computed its gradients.
	Threads int `json:"threads,omitempty"`
	// Quantization is what the int8 weights scored against the float32
	// ones on the rows held back, when they were tried.
	Quantization *QuantCheck `json:"quantization,omitempty"`
}

// Layer is one dense layer of a format-1 network: W[out][in] and B[out].
type Layer struct {
	W [][]float64 `json:"w"`
	B []float64   `json:"b"`
}

// check refuses a decoded network whose shapes or values could not score
// width inputs into out outputs, and unpacks the weights of one in Dense.
func (n *Net) check(width, out int) error {
	if out == 1 && !(n.YStd > 0) {
		return errors.New("the network's target scale is not positive")
	}
	if n.Dense != nil {
		return n.checkDense(width, out)
	}
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
	return nil
}

func (n *Net) checkDense(width, out int) error {
	if n.Layers != nil {
		return errors.New("the network holds both float64 and float32 layers")
	}
	if n.Precision != PrecisionFloat32 && n.Precision != PrecisionInt8 {
		return fmt.Errorf("the network's precision is %q", n.Precision)
	}
	if len(n.Dense) < 2 || len(n.Dense) > maxLayers+1 {
		return fmt.Errorf("the network has %d layers", len(n.Dense))
	}
	in := width
	for i := range n.Dense {
		d := &n.Dense[i]
		if d.In != in {
			return fmt.Errorf("layer %d takes %d inputs, not %d", i, d.In, in)
		}
		if d.Out < 1 || d.Out > max(maxUnits, out) {
			return fmt.Errorf("layer %d has %d units", i, d.Out)
		}
		if err := d.unpack(n.Precision == PrecisionInt8); err != nil {
			return fmt.Errorf("layer %d: %w", i, err)
		}
		in = d.Out
	}
	if in != out {
		return fmt.Errorf("the network has %d outputs, not %d", in, out)
	}
	return nil
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

// predict scores a format-1 network, in float64, as it always has.
func (n *Net) predict(x *Matrix) [][]float64 {
	act := make([][]float64, len(n.Layers))
	for i, l := range n.Layers {
		act[i] = make([]float64, len(l.B))
	}
	out := make([][]float64, x.N)
	last := len(n.Layers) - 1
	for i := range x.N {
		n.forward(x, i, act)
		z := act[last]
		if n.Classes == 0 {
			out[i] = []float64{z[0]*n.YStd + n.YMean}
			continue
		}
		out[i] = softmax(z)
	}
	return out
}

// forward computes every layer's activations for row i into act; the last
// is the output before softmax.
func (n *Net) forward(x *Matrix, i int, act [][]float64) {
	for li, l := range n.Layers {
		a := act[li]
		for r := range l.W {
			var z float64
			if li == 0 {
				z = l.B[r] + x.Dot(i, l.W[r])
			} else {
				z = l.B[r] + dot(l.W[r], act[li-1])
			}
			if li < len(n.Layers)-1 && z < 0 {
				z = 0
			}
			a[r] = z
		}
	}
}
