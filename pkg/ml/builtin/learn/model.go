package learn

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// The tasks and the algorithms of this package. The names are the worker's
// where it has the same algorithm, so a training spec reads the same on
// either engine; AlgorithmMLP is the built-in network.
const (
	TaskClassification = "classification"
	TaskRegression     = "regression"

	AlgorithmLinear           = "linear"
	AlgorithmRandomForest     = "random_forest"
	AlgorithmGradientBoosting = "gradient_boosting"
	AlgorithmMLP              = "mlp"
)

// Algorithms are the algorithms Fit trains.
var Algorithms = []string{AlgorithmLinear, AlgorithmRandomForest, AlgorithmGradientBoosting, AlgorithmMLP}

// FormatVersion is the version of an encoded model (Encode), and FormatNet
// that of one holding a network kept as float32 or int8 weights (Net.Dense),
// which an engine from before it cannot read. Every other model stays
// FormatVersion, so that it can. Decode refuses any other format.
const (
	FormatVersion = 1
	FormatNet     = 2
)

// MaxEncodedBytes bounds an encoded model, compressed, and maxDecodedBytes
// what it may expand to.
const (
	MaxEncodedBytes = 64 << 20
	maxDecodedBytes = 512 << 20
)

// ErrInvalidModel is returned by Decode for bytes that are not a model this
// package encoded.
var ErrInvalidModel = errors.New("not a valid built-in model")

// Spec says what to fit: the task, the algorithm, the number of classes of a
// classification (whose targets are class indices 0..Classes-1), the seed,
// the network's settings, and how many goroutines a forest may use (0 for
// one per CPU, at most the trees) and a network at most (0 for no cap
// beyond its own).
type Spec struct {
	Task      string
	Algorithm string
	Classes   int
	Seed      uint64
	MLP       MLPParams
	Workers   int
}

// Model is a fitted model with its preprocessing, as it is saved. Exactly one
// of Linear, Forest, Boost and Net is set.
type Model struct {
	Format    int    `json:"format"`
	Task      string `json:"task"`
	Algorithm string `json:"algorithm"`
	Classes   int    `json:"classes,omitempty"`
	Prep      *Prep  `json:"prep"`

	Linear *Linear `json:"linear,omitempty"`
	Forest *Forest `json:"forest,omitempty"`
	Boost  *Boost  `json:"boost,omitempty"`
	Net    *Net    `json:"net,omitempty"`
}

// Fit trains a model on x and y: class indices for a classification, values
// for a regression. w weighs the rows (nil weighs them all 1).
func Fit(ctx context.Context, spec Spec, x *Frame, y, w []float64) (*Model, error) {
	if x.N == 0 || len(y) != x.N {
		return nil, fmt.Errorf("training needs rows, and one target per row (%d rows, %d targets)", x.N, len(y))
	}
	if w != nil && len(w) != x.N {
		return nil, fmt.Errorf("%d weights for %d rows", len(w), x.N)
	}
	switch spec.Task {
	case TaskClassification:
		if spec.Classes < 2 {
			return nil, errors.New("a classification needs at least two classes")
		}
	case TaskRegression:
		spec.Classes = 0
	default:
		return nil, fmt.Errorf("unknown task %q", spec.Task)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scale := spec.Algorithm == AlgorithmLinear || spec.Algorithm == AlgorithmMLP
	m := &Model{Format: FormatVersion, Task: spec.Task, Algorithm: spec.Algorithm, Classes: spec.Classes, Prep: FitPrep(x, scale)}
	var err error
	if spec.Algorithm == AlgorithmMLP {
		// The network reads a float32 copy of the rows, never a float64 one.
		m.Format = FormatNet
		if m.Net, err = fitNet(ctx, m.Prep.transform32(x), y, w, spec); err != nil {
			return nil, err
		}
		return m, nil
	}
	mx := m.Prep.Transform(x)
	switch spec.Algorithm {
	case AlgorithmLinear:
		m.Linear, err = fitLinear(ctx, mx, y, w, spec)
	case AlgorithmRandomForest:
		m.Forest, err = fitForest(ctx, mx, y, w, spec)
	case AlgorithmGradientBoosting:
		m.Boost, err = fitBoost(ctx, mx, y, w, spec)
	default:
		return nil, fmt.Errorf("unknown algorithm %q", spec.Algorithm)
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// raw is the model's output for each row: one value per class (before
// normalising, for some algorithms) or the value of a regression.
func (m *Model) raw(x *Frame) [][]float64 {
	if m.Net != nil && m.Net.Dense != nil {
		return m.Net.predict32(m.Prep.transform32(x))
	}
	mx := m.Prep.Transform(x)
	switch {
	case m.Linear != nil:
		return m.Linear.predict(mx)
	case m.Forest != nil:
		return m.Forest.predict(mx)
	case m.Boost != nil:
		return m.Boost.predict(mx)
	case m.Net != nil:
		return m.Net.predict(mx)
	}
	return make([][]float64, x.N)
}

// Proba is a classifier's probability of each class, per row. A regression
// gives each row its value as a one-element row.
func (m *Model) Proba(x *Frame) [][]float64 { return m.raw(x) }

// Predict is each row's class index (the most probable class, the first on a
// tie) or value.
func (m *Model) Predict(x *Frame) []float64 {
	out := m.raw(x)
	pred := make([]float64, len(out))
	for i, r := range out {
		if m.Task == TaskRegression {
			pred[i] = r[0]
			continue
		}
		pred[i] = float64(argmax(r))
	}
	return pred
}

func argmax(v []float64) int {
	best := 0
	for k := range v {
		if v[k] > v[best] {
			best = k
		}
	}
	return best
}

// Encode writes the model as gzip-compressed JSON: {"format": 1, ...}. A
// network's weights are its packed bytes (Dense), base64 in the JSON.
func (m *Model) Encode() ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(zw).Encode(m); err != nil {
		return nil, fmt.Errorf("encoding the model: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > MaxEncodedBytes {
		return nil, fmt.Errorf("the model is %d MB, over the %d MB a built-in model may be: train on fewer rows, "+
			"or with another algorithm", buf.Len()>>20, MaxEncodedBytes>>20)
	}
	return buf.Bytes(), nil
}

// Decode reads a model Encode wrote, checking it is whole: its format, its
// one algorithm, and every shape that indexes another.
func Decode(raw []byte) (*Model, error) {
	if len(raw) > MaxEncodedBytes {
		return nil, fmt.Errorf("%w: it is %d bytes, over the %d-byte limit", ErrInvalidModel, len(raw), MaxEncodedBytes)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidModel, err)
	}
	var m Model
	dec := json.NewDecoder(io.LimitReader(zr, maxDecodedBytes))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidModel, err)
	}
	if err := m.check(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidModel, err)
	}
	m.Prep.buildIndex()
	return &m, nil
}

// check refuses a decoded model that could index out of range or answer
// nonsense.
func (m *Model) check() error {
	switch {
	case m.Format != FormatVersion && m.Format != FormatNet:
		return fmt.Errorf("format %d is not format %d or %d", m.Format, FormatVersion, FormatNet)
	case (m.Format == FormatNet) != (m.Net != nil && m.Net.Dense != nil):
		return fmt.Errorf("format %d does not hold the model it says", m.Format)
	}
	if m.Task != TaskClassification && m.Task != TaskRegression {
		return fmt.Errorf("task %q", m.Task)
	}
	if m.Task == TaskClassification && m.Classes < 2 {
		return errors.New("a classifier with fewer than two classes")
	}
	nNum, width, catSize, err := m.Prep.shape()
	if err != nil {
		return err
	}
	out := 1
	if m.Task == TaskClassification {
		out = m.Classes
	}
	set := 0
	if m.Linear != nil {
		set++
		err = m.Linear.check(width, out)
	}
	if m.Forest != nil {
		set++
		err = errors.Join(err, m.Forest.check(nNum, catSize, out))
	}
	if m.Boost != nil {
		set++
		err = errors.Join(err, m.Boost.check(nNum, catSize, out))
	}
	if m.Net != nil {
		set++
		err = errors.Join(err, m.Net.check(width, out))
	}
	if set != 1 {
		return fmt.Errorf("it holds %d models, not one", set)
	}
	return err
}

// finite reports whether every value is a finite number.
func finite(v ...[]float64) bool {
	for _, s := range v {
		for _, x := range s {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return false
			}
		}
	}
	return true
}
