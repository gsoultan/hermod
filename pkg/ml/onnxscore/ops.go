package onnxscore

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// opFunc adapts a function to op.
type opFunc func(in []*Tensor) ([]*Tensor, error)

func (f opFunc) run(in []*Tensor) ([]*Tensor, error) { return f(in) }

func one(t *Tensor, err error) ([]*Tensor, error) {
	if err != nil {
		return nil, err
	}
	return []*Tensor{t}, nil
}

// ---------------------------------------------------------------------------
// ai.onnx
// ---------------------------------------------------------------------------

func compileIdentity(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 1); err != nil {
		return nil, err
	}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) { return in[:1], nil }), nil
}

// Cast between float and int64. A float is truncated toward zero, as ONNX
// Runtime's static_cast does; one that does not fit, or NaN, is an error
// rather than whatever a platform makes of it.
func compileCast(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 1); err != nil {
		return nil, err
	}
	to, err := c.int("to", 0)
	if err != nil {
		return nil, err
	}
	// saturate (opset 19) only changes casts to 8-bit floats.
	c.ignore("saturate")
	if to != int64(Float) && to != int64(Int64) {
		return nil, unsupported("cast to element type %d", to)
	}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		x := in[0]
		switch {
		case x.Type == DataType(to):
			return []*Tensor{x}, nil
		case x.Type == Int64 && to == int64(Float):
			f, _ := x.floats()
			return []*Tensor{{Type: Float, Shape: x.Shape, Floats: f}}, nil
		case x.Type == Float && to == int64(Int64):
			out := make([]int64, len(x.Floats))
			for i, v := range x.Floats {
				if math.IsNaN(float64(v)) || v >= 9.223372e18 || v < -9.223372e18 {
					return nil, fmt.Errorf("%v does not fit an int64", v)
				}
				out[i] = int64(v)
			}
			return []*Tensor{{Type: Int64, Shape: x.Shape, Ints: out}}, nil
		}
		return nil, fmt.Errorf("cannot cast %s", x.Type)
	}), nil
}

func compileConcat(c *nodeCtx) (op, error) {
	if err := c.arity(1, math.MaxInt32, 1); err != nil {
		return nil, err
	}
	ax, err := c.int("axis", math.MinInt64)
	if err != nil {
		return nil, err
	}
	if ax == math.MinInt64 {
		return nil, errors.New("axis is required")
	}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) { return one(concat(in, ax)) }), nil
}

func concat(in []*Tensor, ax int64) (*Tensor, error) {
	first := in[0]
	a, err := axis(ax, len(first.Shape))
	if err != nil {
		return nil, err
	}
	shape, err := concatShape(in, a)
	if err != nil {
		return nil, err
	}
	outer := 1
	for _, d := range shape[:a] {
		outer *= d
	}
	out := &Tensor{Type: first.Type, Shape: shape}
	switch first.Type {
	case Float:
		out.Floats = make([]float32, 0, out.Len())
	case Int64:
		out.Ints = make([]int64, 0, out.Len())
	case String:
		out.Strings = make([]string, 0, out.Len())
	}
	for o := range outer {
		for _, t := range in {
			block := t.Len() / outer
			out.appendRange(t, o*block, (o+1)*block)
		}
	}
	return out, nil
}

// concatShape is the shape of the inputs joined along axis a, which must
// agree in type, rank and every other dimension.
func concatShape(in []*Tensor, a int) ([]int, error) {
	first := in[0]
	shape := slices.Clone(first.Shape)
	shape[a] = 0
	for _, t := range in {
		if t.Type != first.Type || len(t.Shape) != len(first.Shape) {
			return nil, errors.New("inputs differ in type or rank")
		}
		for d := range t.Shape {
			if d != a && t.Shape[d] != first.Shape[d] {
				return nil, fmt.Errorf("shapes %v and %v differ outside axis %d", first.Shape, t.Shape, a)
			}
		}
		shape[a] += t.Shape[a]
	}
	return shape, nil
}

// appendRange appends t's values lo to hi, of t's own type, to out.
func (out *Tensor) appendRange(t *Tensor, lo, hi int) {
	switch t.Type {
	case Float:
		out.Floats = append(out.Floats, t.Floats[lo:hi]...)
	case Int64:
		out.Ints = append(out.Ints, t.Ints[lo:hi]...)
	case String:
		out.Strings = append(out.Strings, t.Strings[lo:hi]...)
	}
}

func compileGather(c *nodeCtx) (op, error) {
	if err := c.arity(2, 2, 1); err != nil {
		return nil, err
	}
	ax, err := c.int("axis", 0)
	if err != nil {
		return nil, err
	}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) { return one(gather(in[0], in[1], ax)) }), nil
}

func gather(data, idx *Tensor, ax int64) (*Tensor, error) {
	if idx.Type != Int64 {
		return nil, fmt.Errorf("indices are %s, not int64", idx.Type)
	}
	a, err := axis(ax, len(data.Shape))
	if err != nil {
		return nil, err
	}
	dim := data.Shape[a]
	outer, inner := 1, 1
	for _, d := range data.Shape[:a] {
		outer *= d
	}
	for _, d := range data.Shape[a+1:] {
		inner *= d
	}
	shape := append(append(slices.Clone(data.Shape[:a]), idx.Shape...), data.Shape[a+1:]...)
	out := &Tensor{Type: data.Type, Shape: shape}
	for o := range outer {
		for _, i := range idx.Ints {
			if i < 0 {
				i += int64(dim)
			}
			if i < 0 || i >= int64(dim) {
				return nil, fmt.Errorf("index %d is out of range for dimension %d", i, dim)
			}
			lo := (o*dim + int(i)) * inner
			switch data.Type {
			case Float:
				out.Floats = append(out.Floats, data.Floats[lo:lo+inner]...)
			case Int64:
				out.Ints = append(out.Ints, data.Ints[lo:lo+inner]...)
			case String:
				out.Strings = append(out.Strings, data.Strings[lo:lo+inner]...)
			}
		}
	}
	return out, nil
}

// Reshape with a constant shape. 0 copies the input's dimension unless
// allowzero (opset 14) is set; one -1 takes what is left.
func compileReshape(c *nodeCtx) (op, error) {
	if err := c.arity(2, 2, 1); err != nil {
		return nil, err
	}
	allowZero, err := c.int("allowzero", 0)
	if err != nil {
		return nil, err
	}
	spec, err := c.constant(1)
	if err != nil {
		return nil, err
	}
	if spec.Type != Int64 || len(spec.Shape) != 1 {
		return nil, errors.New("the shape must be a one-dimensional int64 tensor")
	}
	want := slices.Clone(spec.Ints)
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		x := in[0]
		shape, err := reshaped(x, want, allowZero != 0)
		if err != nil {
			return nil, err
		}
		out := *x
		out.Shape = shape
		if out.Len() != x.Len() {
			return nil, fmt.Errorf("cannot reshape %v to %v", x.Shape, want)
		}
		return []*Tensor{&out}, nil
	}), nil
}

// reshaped resolves Reshape's shape for x: 0 copies x's dimension unless
// allowZero, and one -1 takes what is left.
func reshaped(x *Tensor, want []int64, allowZero bool) ([]int, error) {
	shape := make([]int, len(want))
	infer, known := -1, 1
	for i, d := range want {
		switch {
		case d == -1 && infer < 0:
			infer = i
			continue
		case d == 0 && !allowZero:
			if i >= len(x.Shape) {
				return nil, fmt.Errorf("shape %v copies a dimension %v does not have", want, x.Shape)
			}
			shape[i] = x.Shape[i]
		case d >= 0:
			shape[i] = int(d)
		default:
			return nil, fmt.Errorf("invalid shape %v", want)
		}
		known *= shape[i]
	}
	if infer >= 0 {
		if known == 0 || x.Len()%known != 0 {
			return nil, fmt.Errorf("cannot reshape %v to %v", x.Shape, want)
		}
		shape[infer] = x.Len() / known
	}
	return shape, nil
}

// Softmax along one axis (opset 13 and later, default the last), or over
// the input flattened to two dimensions at axis (earlier opsets, default 1).
// For the two-dimensional tensors these models carry, with the last axis,
// the two agree.
func compileSoftmax(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 1); err != nil {
		return nil, err
	}
	def := int64(-1)
	if c.version < 13 {
		def = 1
	}
	ax, err := c.int("axis", def)
	if err != nil {
		return nil, err
	}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		x := in[0]
		if x.Type != Float {
			return nil, fmt.Errorf("softmax of %s", x.Type)
		}
		a, err := axis(ax, len(x.Shape))
		if err != nil {
			return nil, err
		}
		if a != len(x.Shape)-1 {
			return nil, fmt.Errorf("softmax over axis %d of shape %v", a, x.Shape)
		}
		width := x.Shape[a]
		out := &Tensor{Type: Float, Shape: x.Shape, Floats: slices.Clone(x.Floats)}
		for lo := 0; lo+width <= len(out.Floats) && width > 0; lo += width {
			softmax(out.Floats[lo : lo+width])
		}
		return []*Tensor{out}, nil
	}), nil
}

func compileArgMax(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 1); err != nil {
		return nil, err
	}
	ax, err := c.int("axis", 0)
	if err != nil {
		return nil, err
	}
	keep, err := c.int("keepdims", 1)
	if err != nil {
		return nil, err
	}
	last, err := c.int("select_last_index", 0)
	if err != nil {
		return nil, err
	}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		return one(argMax(in[0], ax, keep != 0, last != 0))
	}), nil
}

// argMax is the index of the largest value along axis ax: the first of
// equal values, or the last with last set.
func argMax(x *Tensor, ax int64, keep, last bool) (*Tensor, error) {
	v, err := x.floats()
	if err != nil {
		return nil, err
	}
	a, err := axis(ax, len(x.Shape))
	if err != nil {
		return nil, err
	}
	outer, inner := 1, 1
	for _, d := range x.Shape[:a] {
		outer *= d
	}
	for _, d := range x.Shape[a+1:] {
		inner *= d
	}
	dim := x.Shape[a]
	if dim == 0 {
		return nil, errors.New("argmax over an empty axis")
	}
	shape := slices.Clone(x.Shape)
	if keep {
		shape[a] = 1
	} else {
		shape = slices.Delete(shape, a, a+1)
	}
	out := &Tensor{Type: Int64, Shape: shape, Ints: make([]int64, 0, outer*inner)}
	for o := range outer {
		for i := range inner {
			best := 0
			for k := 1; k < dim; k++ {
				cur, top := v[(o*dim+k)*inner+i], v[(o*dim+best)*inner+i]
				if cur > top || (last && cur == top) {
					best = k
				}
			}
			out.Ints = append(out.Ints, int64(best))
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// ai.onnx.ml preprocessing
// ---------------------------------------------------------------------------

// Scaler computes (x - offset) * scale per column, in float32. One offset or
// scale applies to every column.
func compileScaler(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 1); err != nil {
		return nil, err
	}
	offset, err := c.floats("offset")
	if err != nil {
		return nil, err
	}
	scale, err := c.floats("scale")
	if err != nil {
		return nil, err
	}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		x := in[0]
		v, err := x.floats()
		if err != nil {
			return nil, err
		}
		_, cols, err := x.rows()
		if err != nil {
			return nil, err
		}
		pick := func(p []float32, j int, def float32) (float32, error) {
			switch len(p) {
			case 0:
				return def, nil
			case 1:
				return p[0], nil
			case cols:
				return p[j], nil
			}
			return 0, fmt.Errorf("%d parameters for %d columns", len(p), cols)
		}
		out := make([]float32, len(v))
		for i, xv := range v {
			o, err := pick(offset, i%cols, 0)
			if err != nil {
				return nil, err
			}
			s, err := pick(scale, i%cols, 1)
			if err != nil {
				return nil, err
			}
			out[i] = float32(float32(xv-o) * s)
		}
		return []*Tensor{{Type: Float, Shape: x.Shape, Floats: out}}, nil
	}), nil
}

// Normalizer divides each row by its largest value (MAX), the sum of its
// absolute values (L1) or its Euclidean length (L2). A row whose divisor is
// zero is left as it is.
func compileNormalizer(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 1); err != nil {
		return nil, err
	}
	norm, err := c.str("norm", "MAX")
	if err != nil {
		return nil, err
	}
	if norm != "MAX" && norm != "L1" && norm != "L2" {
		return nil, unsupported("norm %q", norm)
	}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		x := in[0]
		v, err := x.floats()
		if err != nil {
			return nil, err
		}
		n, cols, err := x.rows()
		if err != nil {
			return nil, err
		}
		out := slices.Clone(v)
		for r := range n {
			row := out[r*cols : (r+1)*cols]
			d := normOf(norm, row)
			if d == 0 || cols == 0 {
				continue
			}
			for j := range row {
				row[j] /= d
			}
		}
		return []*Tensor{{Type: Float, Shape: x.Shape, Floats: out}}, nil
	}), nil
}

// normOf is the divisor Normalizer uses for one row, in float32 as ONNX
// Runtime computes it.
func normOf(norm string, row []float32) float32 {
	var d float32
	switch norm {
	case "MAX":
		d = float32(math.Inf(-1))
		for _, e := range row {
			d = max(d, e)
		}
	case "L1":
		for _, e := range row {
			d += float32(math.Abs(float64(e)))
		}
	case "L2":
		for _, e := range row {
			d += e * e
		}
		d = float32(math.Sqrt(float64(d)))
	}
	return d
}

// firstIndex maps each value to the index of its first occurrence.
func firstIndex[T comparable](vals []T) map[T]int {
	index := make(map[T]int, len(vals))
	for i, v := range vals {
		if _, dup := index[v]; !dup {
			index[v] = i
		}
	}
	return index
}

// OneHotEncoder appends a dimension of one column per category. An unknown
// value is all zeros when zeros is 1 (the default) and an error otherwise.
func compileOneHotEncoder(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 1); err != nil {
		return nil, err
	}
	cats, err := c.strs("cats_strings")
	if err != nil {
		return nil, err
	}
	ints, err := c.ints("cats_int64s")
	if err != nil {
		return nil, err
	}
	zeros, err := c.int("zeros", 1)
	if err != nil {
		return nil, err
	}
	if (len(cats) == 0) == (len(ints) == 0) {
		return nil, errors.New("exactly one of cats_strings and cats_int64s must be set")
	}
	strIndex, intIndex := firstIndex(cats), firstIndex(ints)
	width := len(cats) + len(ints)
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		x := in[0]
		n := x.Len()
		out := &Tensor{Type: Float, Shape: append(slices.Clone(x.Shape), width), Floats: make([]float32, n*width)}
		for i := range n {
			var (
				j  int
				ok bool
			)
			switch {
			case x.Type == String && len(cats) > 0:
				j, ok = strIndex[x.Strings[i]]
			case x.Type == Int64 && len(ints) > 0:
				j, ok = intIndex[x.Ints[i]]
			default:
				return nil, fmt.Errorf("a %s input for these categories", x.Type)
			}
			if ok {
				out.Floats[i*width+j] = 1
			} else if zeros == 0 {
				return nil, errors.New("a value is not one of the categories")
			}
		}
		return []*Tensor{out}, nil
	}), nil
}

// FeatureVectorizer joins its inputs' columns, each declared in
// inputdimensions, into one float tensor.
func compileFeatureVectorizer(c *nodeCtx) (op, error) {
	if err := c.arity(1, math.MaxInt32, 1); err != nil {
		return nil, err
	}
	dims, err := c.ints("inputdimensions")
	if err != nil {
		return nil, err
	}
	if len(dims) != len(c.node.inputs) {
		return nil, fmt.Errorf("%d input dimensions for %d inputs", len(dims), len(c.node.inputs))
	}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		parts := make([]*Tensor, len(in))
		for i, x := range in {
			v, err := x.floats()
			if err != nil {
				return nil, err
			}
			n, cols, err := x.rows()
			if err != nil {
				return nil, err
			}
			if int64(cols) != dims[i] {
				return nil, fmt.Errorf("input %d has %d columns, declared %d", i, cols, dims[i])
			}
			parts[i] = &Tensor{Type: Float, Shape: []int{n, cols}, Floats: v}
		}
		return one(concat(parts, 1))
	}), nil
}

// ---------------------------------------------------------------------------
// ai.onnx.ml linear models
// ---------------------------------------------------------------------------

// linear is coefficients [targets, features] and one intercept per target.
type linear struct {
	coef       []float32
	intercepts []float32
	targets    int
}

// scores computes x·coefᵀ + intercept for every row, accumulating in float64
// and rounding once to float32.
func (l linear) scores(x *Tensor) (n int, out []float32, err error) {
	v, err := x.floats()
	if err != nil {
		return 0, nil, err
	}
	n, cols, err := x.rows()
	if err != nil {
		return 0, nil, err
	}
	if cols*l.targets != len(l.coef) {
		return 0, nil, fmt.Errorf("%d coefficients for %d features and %d targets", len(l.coef), cols, l.targets)
	}
	out = make([]float32, n*l.targets)
	for r := range n {
		row := v[r*cols : (r+1)*cols]
		for t := range l.targets {
			var s float64
			for j, xv := range row {
				s += float64(xv) * float64(l.coef[t*cols+j])
			}
			if len(l.intercepts) > 0 {
				s += float64(l.intercepts[t])
			}
			out[r*l.targets+t] = float32(s)
		}
	}
	return n, out, nil
}

// LinearClassifier with one coefficient row per class (what skl2onnx emits
// for logistic regression, binary or not): the label is the class with the
// highest score, the first on a tie, and the scores go through
// post_transform. A single coefficient row, which ONNX Runtime treats as a
// special binary case, is refused.
func compileLinearClassifier(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 2); err != nil {
		return nil, err
	}
	coef, err := c.floats("coefficients")
	if err != nil {
		return nil, err
	}
	intercepts, err := c.floats("intercepts")
	if err != nil {
		return nil, err
	}
	labels, err := readLabels(c, "classlabels_strings", "classlabels_ints")
	if err != nil {
		return nil, err
	}
	post, err := c.str("post_transform", "NONE")
	if err != nil {
		return nil, err
	}
	// multi_class is not read by ONNX Runtime either: the coefficient rows
	// already say how many classes there are.
	c.ignore("multi_class")
	classes := len(intercepts)
	if classes < 2 || classes != labels.len() {
		return nil, unsupported("%d intercepts for %d class labels (one coefficient row per class is required)", classes, labels.len())
	}
	if post != "NONE" && post != "LOGISTIC" && post != "SOFTMAX" {
		return nil, unsupported("post_transform %q", post)
	}
	l := linear{coef: coef, intercepts: intercepts, targets: classes}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		n, s, err := l.scores(in[0])
		if err != nil {
			return nil, err
		}
		best := make([]int, n)
		for r := range n {
			row := s[r*classes : (r+1)*classes]
			for k := range row {
				if row[k] > row[best[r]] {
					best[r] = k
				}
			}
			transform(row, post)
		}
		return []*Tensor{labels.pick(best), {Type: Float, Shape: []int{n, classes}, Floats: s}}, nil
	}), nil
}

func compileLinearRegressor(c *nodeCtx) (op, error) {
	if err := c.arity(1, 1, 1); err != nil {
		return nil, err
	}
	coef, err := c.floats("coefficients")
	if err != nil {
		return nil, err
	}
	intercepts, err := c.floats("intercepts")
	if err != nil {
		return nil, err
	}
	targets, err := c.int("targets", 1)
	if err != nil {
		return nil, err
	}
	post, err := c.str("post_transform", "NONE")
	if err != nil {
		return nil, err
	}
	if post != "NONE" {
		return nil, unsupported("post_transform %q", post)
	}
	if targets < 1 || (len(intercepts) != 0 && int64(len(intercepts)) != targets) {
		return nil, fmt.Errorf("%d intercepts for %d targets", len(intercepts), targets)
	}
	l := linear{coef: coef, intercepts: intercepts, targets: int(targets)}
	return opFunc(func(in []*Tensor) ([]*Tensor, error) {
		n, s, err := l.scores(in[0])
		if err != nil {
			return nil, err
		}
		return []*Tensor{{Type: Float, Shape: []int{n, l.targets}, Floats: s}}, nil
	}), nil
}

// classLabels are a classifier's labels, strings or int64s.
type classLabels struct {
	strings []string
	ints    []int64
}

func readLabels(c *nodeCtx, strs, ints string) (classLabels, error) {
	s, err := c.strs(strs)
	if err != nil {
		return classLabels{}, err
	}
	i, err := c.ints(ints)
	if err != nil {
		return classLabels{}, err
	}
	if (len(s) == 0) == (len(i) == 0) {
		return classLabels{}, fmt.Errorf("exactly one of %s and %s must be set", strs, ints)
	}
	return classLabels{strings: s, ints: i}, nil
}

func (l classLabels) len() int { return len(l.strings) + len(l.ints) }

// pick returns the label tensor [n] for the chosen class of each row.
func (l classLabels) pick(classes []int) *Tensor {
	if len(l.strings) > 0 {
		out := make([]string, len(classes))
		for i, k := range classes {
			out[i] = l.strings[k]
		}
		return &Tensor{Type: String, Shape: []int{len(classes)}, Strings: out}
	}
	out := make([]int64, len(classes))
	for i, k := range classes {
		out[i] = l.ints[k]
	}
	return &Tensor{Type: Int64, Shape: []int{len(classes)}, Ints: out}
}

// transform applies a post_transform to one row of scores, in place.
func transform(row []float32, post string) {
	switch post {
	case "LOGISTIC":
		for i, v := range row {
			row[i] = logistic(v)
		}
	case "SOFTMAX":
		softmax(row)
	}
}

// logistic is ONNX Runtime's ComputeLogistic.
func logistic(v float32) float32 {
	p := float32(1 / (1 + math.Exp(-math.Abs(float64(v)))))
	if v < 0 {
		return 1 - p
	}
	return p
}

// softmax is ONNX Runtime's ComputeSoftmax: subtract the largest value,
// exponentiate, divide by the sum, all in float32.
func softmax(row []float32) {
	if len(row) == 0 {
		return
	}
	top := row[0]
	for _, v := range row[1:] {
		top = max(top, v)
	}
	var sum float32
	for i, v := range row {
		row[i] = float32(math.Exp(float64(v - top)))
		sum += row[i]
	}
	for i := range row {
		row[i] /= sum
	}
}
