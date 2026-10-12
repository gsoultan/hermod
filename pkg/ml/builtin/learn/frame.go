// Package learn holds the built-in ML engine's algorithms, in pure Go: ridge
// and logistic regression, a random forest, gradient-boosted trees and a
// small multilayer perceptron, over the same preprocessing the hermod-ml
// worker applies (ml/worker/hermod_ml/training.py build_pipeline): a missing
// number gets the training median, numbers are standardised for the linear
// model and the network, and text is one-hot encoded.
//
// Every algorithm is deterministic for a seed: random draws come from a PCG
// seeded with it, and work spread over goroutines is joined in a fixed order.
// Each one checks its context between units of work (an iteration, a tree,
// an epoch), so a training that is cancelled or runs out of time stops.
package learn

import (
	"cmp"
	"math"
	"slices"
)

// The kinds of a feature column.
const (
	KindNumber = "number"
	KindString = "string"
)

// MaxCategories is how many values of a text feature are one-hot encoded:
// the most frequent ones. Any other value encodes like a value never seen in
// training, as all zeros. It bounds the model's width on an ID-like column.
const MaxCategories = 100

// Frame holds the raw features of some rows: a number column in Num (NaN
// where missing), a text column in Str ("" where missing), the other slice
// of the pair nil.
type Frame struct {
	Names []string
	Kinds []string
	Num   [][]float64
	Str   [][]string
	N     int
}

// NewFrame makes a frame of n rows of the given features, numbers all
// missing and text all empty.
func NewFrame(names, kinds []string, n int) *Frame {
	f := &Frame{Names: names, Kinds: kinds, Num: make([][]float64, len(names)), Str: make([][]string, len(names)), N: n}
	for j, k := range kinds {
		if k == KindString {
			f.Str[j] = make([]string, n)
			continue
		}
		f.Num[j] = make([]float64, n)
		for i := range f.Num[j] {
			f.Num[j][i] = math.NaN()
		}
	}
	return f
}

// Take is the frame of the rows idx, in that order.
func (f *Frame) Take(idx []int) *Frame {
	out := &Frame{Names: f.Names, Kinds: f.Kinds, Num: make([][]float64, len(f.Names)), Str: make([][]string, len(f.Names)), N: len(idx)}
	for j := range f.Names {
		if f.Num[j] != nil {
			col := make([]float64, len(idx))
			for i, r := range idx {
				col[i] = f.Num[j][r]
			}
			out.Num[j] = col
		}
		if f.Str[j] != nil {
			col := make([]string, len(idx))
			for i, r := range idx {
				col[i] = f.Str[j][r]
			}
			out.Str[j] = col
		}
	}
	return out
}

// Prep is the preprocessing fitted on the training rows: each number's fill
// value, mean and spread (for scaling), and each text feature's categories.
type Prep struct {
	Features []string `json:"features"`
	Kinds    []string `json:"kinds"`
	// Fill is the training median of each number feature, as a float32
	// value, the worker's rule; 0 when every value was missing.
	Fill  map[string]float64 `json:"fill"`
	Scale bool               `json:"scale"`
	// Mean and Std, per feature (0 and 1 for text), scale the numbers.
	Mean []float64 `json:"mean"`
	Std  []float64 `json:"std"`
	// Categories are the encoded values of each text feature (nil for a
	// number), sorted.
	Categories [][]string `json:"categories"`

	index []map[string]int32
}

// FitPrep fits the preprocessing on the training rows. scale standardises
// the numbers.
func FitPrep(f *Frame, scale bool) *Prep {
	p := &Prep{Features: f.Names, Kinds: f.Kinds, Fill: map[string]float64{}, Scale: scale,
		Mean: make([]float64, len(f.Names)), Std: make([]float64, len(f.Names)), Categories: make([][]string, len(f.Names))}
	for j, name := range f.Names {
		p.Std[j] = 1
		if f.Kinds[j] == KindString {
			p.Categories[j] = topCategories(f.Str[j])
			continue
		}
		fill := float64(float32(median(f.Num[j])))
		p.Fill[name] = fill
		if scale {
			p.Mean[j], p.Std[j] = meanStd(f.Num[j], fill)
		}
	}
	p.buildIndex()
	return p
}

func (p *Prep) buildIndex() {
	p.index = make([]map[string]int32, len(p.Features))
	for j, cats := range p.Categories {
		if cats == nil {
			continue
		}
		m := make(map[string]int32, len(cats))
		for i, c := range cats {
			m[c] = int32(i)
		}
		p.index[j] = m
	}
}

// median of the values that are not NaN; 0 when there are none.
func median(v []float64) float64 {
	vals := make([]float64, 0, len(v))
	for _, x := range v {
		if !math.IsNaN(x) {
			vals = append(vals, x)
		}
	}
	if len(vals) == 0 {
		return 0
	}
	slices.Sort(vals)
	n := len(vals)
	if n%2 == 1 {
		return vals[n/2]
	}
	return (vals[n/2-1] + vals[n/2]) / 2
}

// meanStd of the values with missing ones filled; a constant column's spread
// counts as 1, as StandardScaler does.
func meanStd(v []float64, fill float64) (float64, float64) {
	if len(v) == 0 {
		return 0, 1
	}
	var sum float64
	for _, x := range v {
		sum += orFill(x, fill)
	}
	mean := sum / float64(len(v))
	var ss float64
	for _, x := range v {
		d := orFill(x, fill) - mean
		ss += d * d
	}
	std := math.Sqrt(ss / float64(len(v)))
	if std == 0 {
		std = 1
	}
	return mean, std
}

func orFill(x, fill float64) float64 {
	if math.IsNaN(x) {
		return fill
	}
	return x
}

// topCategories are the MaxCategories most frequent values, sorted. Ties at
// the cut go to the smaller value, so the same rows always encode the same.
func topCategories(v []string) []string {
	counts := map[string]int{}
	for _, s := range v {
		counts[s]++
	}
	cats := make([]string, 0, len(counts))
	for c := range counts {
		cats = append(cats, c)
	}
	slices.SortFunc(cats, func(a, b string) int {
		if c := cmp.Compare(counts[b], counts[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	if len(cats) > MaxCategories {
		cats = cats[:MaxCategories]
	}
	slices.Sort(cats)
	return cats
}

// Matrix is a frame encoded for a model: number features filled (and scaled)
// column by column, and each text feature as the index of its category, -1
// for a value outside them. A text feature stands for one 0/1 column per
// category, which is what a model's weights and splits see.
type Matrix struct {
	N       int
	Num     [][]float64
	Cat     [][]int32
	CatSize []int
}

// Transform encodes a frame with the fitted preprocessing. The frame must
// hold the features the preprocessing was fitted on, in its order.
func (p *Prep) Transform(f *Frame) *Matrix {
	if p.index == nil {
		p.buildIndex()
	}
	m := &Matrix{N: f.N}
	for j, name := range p.Features {
		if p.Kinds[j] == KindString {
			codes := make([]int32, f.N)
			for i, s := range f.Str[j] {
				c, ok := p.index[j][s]
				if !ok {
					c = -1
				}
				codes[i] = c
			}
			m.Cat = append(m.Cat, codes)
			m.CatSize = append(m.CatSize, len(p.Categories[j]))
			continue
		}
		fill, mean, std := p.Fill[name], p.Mean[j], p.Std[j]
		col := make([]float64, f.N)
		for i, x := range f.Num[j] {
			v := float64(float32(orFill(x, fill)))
			if p.Scale {
				v = (v - mean) / std
			}
			col[i] = v
		}
		m.Num = append(m.Num, col)
	}
	return m
}

// Width is how many columns the matrix stands for: one per number feature
// and one per category.
func (m *Matrix) Width() int {
	w := len(m.Num)
	for _, s := range m.CatSize {
		w += s
	}
	return w
}

// Dense writes row i as its Width() columns into out.
func (m *Matrix) Dense(i int, out []float64) {
	clear(out)
	for j, col := range m.Num {
		out[j] = col[i]
	}
	at := len(m.Num)
	for j, codes := range m.Cat {
		if c := codes[i]; c >= 0 {
			out[at+int(c)] = 1
		}
		at += m.CatSize[j]
	}
}

// Rows takes the rows idx of the matrix, in that order.
func (m *Matrix) Rows(idx []int) *Matrix {
	out := &Matrix{N: len(idx), CatSize: m.CatSize}
	for _, col := range m.Num {
		c := make([]float64, len(idx))
		for i, r := range idx {
			c[i] = col[r]
		}
		out.Num = append(out.Num, c)
	}
	for _, col := range m.Cat {
		c := make([]int32, len(idx))
		for i, r := range idx {
			c[i] = col[r]
		}
		out.Cat = append(out.Cat, c)
	}
	return out
}

// Dot is row i's dot product with weights over the Width() columns.
func (m *Matrix) Dot(i int, weights []float64) float64 {
	s := 0.0
	for j, col := range m.Num {
		s += weights[j] * col[i]
	}
	at := len(m.Num)
	for j, codes := range m.Cat {
		if c := codes[i]; c >= 0 {
			s += weights[at+int(c)]
		}
		at += m.CatSize[j]
	}
	return s
}

// AddTo adds g times row i to v, over the Width() columns.
func (m *Matrix) AddTo(i int, v []float64, g float64) {
	for j, col := range m.Num {
		v[j] += g * col[i]
	}
	at := len(m.Num)
	for j, codes := range m.Cat {
		if c := codes[i]; c >= 0 {
			v[at+int(c)] += g
		}
		at += m.CatSize[j]
	}
}
