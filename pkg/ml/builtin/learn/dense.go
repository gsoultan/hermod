package learn

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Matrix32 is a frame encoded for the network, in float32 and row by row:
// each row's NumW numbers (filled and scaled, as Transform does), and the
// index of each text feature's category, -1 for a value outside them. A
// text feature stands for its categories' columns, from CatOff on, as in
// Matrix; the network adds the weights of the category a row has instead
// of multiplying a one-hot vector.
type Matrix32 struct {
	N, NumW int
	Num     []float32
	Cat     []int32
	CatOff  []int
	CatSize []int
	Width   int
}

func (m *Matrix32) num(i int) []float32 { return m.Num[i*m.NumW : (i+1)*m.NumW] }

func (m *Matrix32) cat(i int) []int32 {
	k := len(m.CatOff)
	return m.Cat[i*k : (i+1)*k]
}

// transform32 encodes a frame for the network: the values Transform gives,
// as float32, in one block of rows rather than a column per feature.
func (p *Prep) transform32(f *Frame) *Matrix32 {
	if p.index == nil {
		p.buildIndex()
	}
	var nums, cats []int
	for j, k := range p.Kinds {
		if k == KindString {
			cats = append(cats, j)
		} else {
			nums = append(nums, j)
		}
	}
	m := &Matrix32{N: f.N, NumW: len(nums), Num: make([]float32, f.N*len(nums)), Cat: make([]int32, f.N*len(cats))}
	at := len(nums)
	for _, j := range cats {
		m.CatOff = append(m.CatOff, at)
		m.CatSize = append(m.CatSize, len(p.Categories[j]))
		at += len(p.Categories[j])
	}
	m.Width = at
	for c, j := range nums {
		fill, mean, std := p.Fill[p.Features[j]], p.Mean[j], p.Std[j]
		for i, x := range f.Num[j] {
			v := float64(float32(orFill(x, fill)))
			if p.Scale {
				v = (v - mean) / std
			}
			m.Num[i*m.NumW+c] = float32(v)
		}
	}
	for c, j := range cats {
		for i, s := range f.Str[j] {
			code, ok := p.index[j][s]
			if !ok {
				code = -1
			}
			m.Cat[i*len(cats)+c] = code
		}
	}
	return m
}

// Dense is one dense layer of a network: Out units over In inputs. W holds
// the weights input by input (In rows of Out), each a little-endian float32,
// or with int8 precision a signed byte worth Scale (one scale for the
// layer). B holds the Out biases as float32, at either precision.
//
// The rows of W run by input so that a row's input adds its weights to the
// units, which skips every input that is zero: a ReLU that did not fire, or
// a category the row does not have.
type Dense struct {
	In    int     `json:"in"`
	Out   int     `json:"out"`
	W     []byte  `json:"w"`
	Scale float32 `json:"scale,omitempty"`
	B     []byte  `json:"b"`

	w32 []float32
	w8  []int8
	b32 []float32
}

// addRow adds a times input j's weights to z.
func (d *Dense) addRow(z []float32, j int, a float32) {
	if d.w8 != nil {
		a *= d.Scale
		for r, q := range d.w8[j*d.Out : (j+1)*d.Out] {
			z[r] += a * float32(q)
		}
		return
	}
	axpy32(a, d.w32[j*d.Out:(j+1)*d.Out], z)
}

// quantize is w in int8, symmetric, with one scale: the largest magnitude
// over 127. Each weight comes back within half a step of what it was. A
// layer of zeros takes a scale of 1.
func quantize(w []float32) ([]int8, float32) {
	var top float32
	for _, v := range w {
		top = max(top, float32(math.Abs(float64(v))))
	}
	q := make([]int8, len(w))
	if top == 0 {
		return q, 1
	}
	scale := top / 127
	for i, v := range w {
		q[i] = int8(max(-127, min(127, math.Round(float64(v/scale)))))
	}
	return q, scale
}

// pack writes the layer's weights into W and B for saving, as int8 when it
// has int8 weights.
func (d *Dense) pack() {
	d.B = f32bytes(d.b32)
	if d.w8 == nil {
		d.W, d.Scale = f32bytes(d.w32), 0
		return
	}
	d.W = make([]byte, len(d.w8))
	for i, q := range d.w8 {
		d.W[i] = byte(q)
	}
}

// unpack reads W and B back, checking their sizes and values.
func (d *Dense) unpack(int8s bool) error {
	if len(d.B) != 4*d.Out {
		return fmt.Errorf("%d bytes of biases for %d units", len(d.B), d.Out)
	}
	d.b32 = f32s(d.B)
	if !finite32(d.b32) {
		return errors.New("the network holds a value that is not finite")
	}
	d.w32, d.w8 = nil, nil
	if int8s {
		if len(d.W) != d.In*d.Out {
			return fmt.Errorf("%d bytes of int8 weights for %d inputs and %d units", len(d.W), d.In, d.Out)
		}
		if !(d.Scale > 0) || math.IsInf(float64(d.Scale), 0) {
			return fmt.Errorf("the int8 scale %v is not a positive number", d.Scale)
		}
		d.w8 = make([]int8, len(d.W))
		for i, b := range d.W {
			d.w8[i] = int8(b)
		}
		return nil
	}
	if len(d.W) != 4*d.In*d.Out {
		return fmt.Errorf("%d bytes of float32 weights for %d inputs and %d units", len(d.W), d.In, d.Out)
	}
	d.w32 = f32s(d.W)
	if !finite32(d.w32) {
		return errors.New("the network holds a value that is not finite")
	}
	return nil
}

func f32bytes(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(x))
	}
	return out
}

func f32s(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

func finite32(v []float32) bool {
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
	}
	return true
}

// axpy32 adds a times x to y.
func axpy32(a float32, x, y []float32) {
	y = y[:len(x)]
	i := 0
	for ; i+4 <= len(x); i += 4 {
		y[i] += a * x[i]
		y[i+1] += a * x[i+1]
		y[i+2] += a * x[i+2]
		y[i+3] += a * x[i+3]
	}
	for ; i < len(x); i++ {
		y[i] += a * x[i]
	}
}

// dot32 is the dot product of a and b.
func dot32(a, b []float32) float32 {
	b = b[:len(a)]
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	return (s0 + s1) + (s2 + s3)
}

// forward32 computes every layer's activations for row i of x into act; the
// last is the output before softmax.
func (n *Net) forward32(x *Matrix32, i int, act [][]float32) {
	last := len(n.Dense) - 1
	for li := range n.Dense {
		d, z := &n.Dense[li], act[li]
		copy(z, d.b32)
		if li == 0 {
			d.addInput(z, x, i)
		} else {
			d.addActs(z, act[li-1])
		}
		if li < last {
			relu32(z)
		}
	}
}

// addInput adds row i of x times the first layer's weights to z: a row of
// weights for each nonzero number and each category present.
func (d *Dense) addInput(z []float32, x *Matrix32, i int) {
	for j, v := range x.num(i) {
		if v != 0 {
			d.addRow(z, j, v)
		}
	}
	for c, code := range x.cat(i) {
		if code >= 0 {
			d.addRow(z, x.CatOff[c]+int(code), 1)
		}
	}
}

// addActs adds the previous layer's activations times the weights to z,
// skipping the zeros ReLU left.
func (d *Dense) addActs(z, prev []float32) {
	for j, v := range prev {
		if v != 0 {
			d.addRow(z, j, v)
		}
	}
}

func relu32(z []float32) {
	for r, v := range z {
		if v < 0 {
			z[r] = 0
		}
	}
}

func (n *Net) newActs() [][]float32 {
	act := make([][]float32, len(n.Dense))
	for i := range n.Dense {
		act[i] = make([]float32, n.Dense[i].Out)
	}
	return act
}

// output turns the last layer's z into the model's answer: each class's
// probability, or the value in the target's units.
func (n *Net) output(z []float32) []float64 {
	if n.Classes == 0 {
		return []float64{float64(z[0])*n.YStd + n.YMean}
	}
	v := make([]float64, len(z))
	for k, x := range z {
		v[k] = float64(x)
	}
	return softmax(v)
}

func (n *Net) predict32(x *Matrix32) [][]float64 {
	act := n.newActs()
	out := make([][]float64, x.N)
	for i := range x.N {
		n.forward32(x, i, act)
		out[i] = n.output(act[len(act)-1])
	}
	return out
}
