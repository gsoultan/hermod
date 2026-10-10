// Package onnxscore evaluates small tabular ONNX models in pure Go: the
// linear models and tree ensembles scikit-learn and XGBoost export through
// skl2onnx and onnxmltools, with the preprocessing glue those exports carry.
//
// Hermod is built without cgo, so it cannot link ONNX Runtime; models are
// normally scored by the hermod-ml worker over the network. For a model whose
// graph uses only the operators below, this package scores it in-process
// instead, which takes microseconds rather than a network round trip.
//
// Supported operators (ai.onnx.ml opset 1 to 3, ai.onnx opset 1 to 23):
//
//	ai.onnx.ml  LinearClassifier, LinearRegressor, TreeEnsembleClassifier,
//	            TreeEnsembleRegressor, Scaler, Normalizer, OneHotEncoder,
//	            FeatureVectorizer
//	ai.onnx     Concat, Gather, Reshape, Identity, Cast, Softmax, ArgMax
//
// Correctness comes before coverage. Load refuses, with ErrUnsupported, any
// graph holding another operator, an attribute it does not know, or a
// combination of settings whose result it has not been checked against ONNX
// Runtime (see testdata/gen_fixtures.py). ZipMap is refused too: its output
// is a sequence of maps, which the worker never exports. A refused model is
// still served by the worker; it is never scored by a guess.
package onnxscore

import (
	"errors"
	"fmt"
)

var (
	// ErrUnsupported is returned by Load and NewScorer for a model this
	// package cannot score exactly. The error names what it could not.
	ErrUnsupported = errors.New("the model cannot be scored in-process")
	// ErrFallback is returned by Scorer.Predict for rows it cannot score
	// exactly as the worker would. The caller sends them to the worker.
	ErrFallback = errors.New("the rows need the ML worker")
)

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrUnsupported}, args...)...)
}

// DataType is a tensor's element type, numbered as ONNX numbers them
// (TensorProto.DataType).
type DataType int32

// The element types this package computes with.
const (
	Float  DataType = 1
	Int64  DataType = 7
	String DataType = 8
)

func (d DataType) String() string {
	switch d {
	case Float:
		return "float"
	case Int64:
		return "int64"
	case String:
		return "string"
	}
	return fmt.Sprintf("type %d", int32(d))
}

// Tensor is a dense tensor in row-major order. Only the slice of its Type is
// used. Operators never modify a tensor they are given, so one may be shared.
type Tensor struct {
	Type    DataType
	Shape   []int
	Floats  []float32
	Ints    []int64
	Strings []string
}

// Len is the number of elements the shape holds.
func (t *Tensor) Len() int {
	n := 1
	for _, d := range t.Shape {
		n *= d
	}
	return n
}

// count is the number of elements the tensor's data holds.
func (t *Tensor) count() int {
	switch t.Type {
	case Float:
		return len(t.Floats)
	case Int64:
		return len(t.Ints)
	case String:
		return len(t.Strings)
	}
	return -1
}

// valid reports whether the data fills the shape exactly.
func (t *Tensor) valid() bool {
	for _, d := range t.Shape {
		if d < 0 {
			return false
		}
	}
	return t.count() == t.Len()
}

// floats returns the tensor's values as float32, converting int64; a string
// tensor is an error.
func (t *Tensor) floats() ([]float32, error) {
	switch t.Type {
	case Float:
		return t.Floats, nil
	case Int64:
		out := make([]float32, len(t.Ints))
		for i, v := range t.Ints {
			out[i] = float32(v)
		}
		return out, nil
	}
	return nil, fmt.Errorf("expected a numeric tensor, got %s", t.Type)
}

// rows views a rank-1 or rank-2 tensor as rows of columns: [C] is one row.
func (t *Tensor) rows() (n, c int, err error) {
	switch len(t.Shape) {
	case 1:
		return 1, t.Shape[0], nil
	case 2:
		return t.Shape[0], t.Shape[1], nil
	}
	return 0, 0, fmt.Errorf("expected a tensor of rank 1 or 2, got shape %v", t.Shape)
}
