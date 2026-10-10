package onnxscore

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Signature is what the hermod-ml worker records with a model version
// (meta.json): what it predicts and how it reads its inputs.
type Signature struct {
	// Task is "classification" or "regression".
	Task string `json:"task"`
	// Features are the graph's inputs, one [n, 1] tensor each, and
	// FeatureTypes says which are "number" (float) and which "string".
	Features     []string          `json:"features"`
	FeatureTypes map[string]string `json:"feature_types"`
	// Fill is what a null numeric feature becomes: its training median.
	Fill map[string]float64 `json:"fill"`
	// Labels are a classifier's classes, strings or (as JSON numbers)
	// integers.
	Labels []any `json:"labels"`
}

// The worker's feature types (hermod_ml.datasets).
const (
	featureNumber = "number"
	featureString = "string"
)

// Scorer scores rows the way the hermod-ml worker's ModelServer does for the
// same model: it coerces each value as the worker would, runs the graph and
// builds the same prediction — {"label", "probability"} for a classifier,
// {"value"} for a regression — as the Open Inference Protocol client reads
// it back. It is safe for concurrent use.
type Scorer struct {
	model    *Model
	sig      Signature
	intLabel bool
	labels   map[any]int
}

// NewScorer loads the model and checks that the signature describes it. A
// model it cannot score exactly is refused with ErrUnsupported.
func NewScorer(raw []byte, sig Signature) (*Scorer, error) {
	m, err := Load(raw)
	if err != nil {
		return nil, err
	}
	s := &Scorer{model: m, sig: sig}
	switch sig.Task {
	case "classification":
		if err := s.readLabels(); err != nil {
			return nil, err
		}
		if len(m.outputs) < 2 {
			return nil, unsupported("a classifier with %d outputs", len(m.outputs))
		}
	case "regression":
	default:
		return nil, unsupported("task %q", sig.Task)
	}
	if err := checkFeatures(m, sig); err != nil {
		return nil, err
	}

	// One row of every kind of value proves the graph runs end to end, so a
	// shape it cannot handle is found here rather than on live traffic.
	probe := map[string]any{}
	for _, f := range sig.Features {
		probe[f] = nil
	}
	if _, err := s.Predict([]map[string]any{probe, probe}); err != nil {
		return nil, unsupported("a trial prediction failed: %v", err)
	}
	return s, nil
}

// readLabels indexes a classifier's labels: all integers (as JSON numbers)
// or all strings.
func (s *Scorer) readLabels() error {
	labels := s.sig.Labels
	if len(labels) == 0 {
		return unsupported("a classifier without labels")
	}
	s.labels = make(map[any]int, len(labels))
	strs := 0
	for i, l := range labels {
		switch v := l.(type) {
		case string:
			strs++
		case float64:
			if v != math.Trunc(v) || math.Abs(v) > 1<<53 {
				return unsupported("class label %v", v)
			}
		default:
			return unsupported("class label %v", l)
		}
		if _, dup := s.labels[l]; !dup {
			s.labels[l] = i
		}
	}
	if strs != 0 && strs != len(labels) {
		return unsupported("class labels mix strings and numbers")
	}
	s.intLabel = strs == 0
	return nil
}

// checkFeatures checks the graph takes one input per feature, of the type
// the signature gives it.
func checkFeatures(m *Model, sig Signature) error {
	if len(sig.Features) == 0 || len(sig.Features) != len(m.inputs) {
		return unsupported("the signature lists %d features; the graph takes %d inputs", len(sig.Features), len(m.inputs))
	}
	for _, f := range sig.Features {
		typ, ok := m.InputType(f)
		if !ok {
			return unsupported("the graph has no input for feature %q", f)
		}
		switch sig.FeatureTypes[f] {
		case featureNumber:
			ok = typ == Float
		case featureString:
			ok = typ == String
		default:
			ok = false
		}
		if !ok {
			return unsupported("feature %q is %q, but the graph takes %s", f, sig.FeatureTypes[f], typ)
		}
	}
	return nil
}

// Ops lists the operators the model's graph uses (Model.Ops).
func (s *Scorer) Ops() []string { return s.model.Ops() }

func fallback(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrFallback}, args...)...)
}

// Predict scores the rows and returns one prediction per row. Rows it cannot
// score exactly as the worker would — a value the worker reads in a way this
// package does not copy, or would refuse — get ErrFallback, and nothing is
// guessed: the caller sends them to the worker instead.
func (s *Scorer) Predict(rows []map[string]any) ([]map[string]any, error) {
	n := len(rows)
	if n == 0 {
		return nil, nil
	}
	inputs := make(map[string]*Tensor, len(s.sig.Features))
	for _, f := range s.sig.Features {
		t, err := s.input(f, rows)
		if err != nil {
			return nil, err
		}
		inputs[f] = t
	}
	outs, err := s.model.Run(inputs)
	if err != nil {
		return nil, fallback("%v", err)
	}
	if s.sig.Task == "regression" {
		return values(outs[0], n)
	}
	return s.classes(outs[0], outs[1], n)
}

// input is feature f of every row as the graph's [n, 1] input, read as the
// worker reads it.
func (s *Scorer) input(f string, rows []map[string]any) (*Tensor, error) {
	n := len(rows)
	present := false
	t := &Tensor{Shape: []int{n, 1}}
	numeric := s.sig.FeatureTypes[f] == featureNumber
	if numeric {
		t.Type, t.Floats = Float, make([]float32, n)
	} else {
		t.Type, t.Strings = String, make([]string, n)
	}
	for i, r := range rows {
		v, has := r[f]
		present = present || has
		ok := false
		if numeric {
			var x float64
			x, ok = number(v, s.sig.Fill[f])
			t.Floats[i] = float32(x)
		} else {
			t.Strings[i], ok = text(v)
		}
		if !ok {
			return nil, fallback("feature %q of row %d", f, i)
		}
	}
	if !present {
		// The worker refuses a request that leaves a feature out.
		return nil, fallback("no row has feature %q", f)
	}
	return t, nil
}

// values are a regressor's predictions, {"value": v} per row.
func values(v *Tensor, n int) ([]map[string]any, error) {
	if v.Type != Float || len(v.Floats) != n {
		return nil, fallback("the model returned %d values for %d rows", v.count(), n)
	}
	preds := make([]map[string]any, n)
	for i := range preds {
		preds[i] = map[string]any{"value": float64(v.Floats[i])}
	}
	return preds, nil
}

// classes are a classifier's predictions: the label of each row and the
// probability the model gives it.
func (s *Scorer) classes(label, probs *Tensor, n int) ([]map[string]any, error) {
	if probs.Type != Float || len(probs.Shape) != 2 || probs.Shape[0] != n || label.count() != n {
		return nil, fallback("the model's outputs do not match %d rows", n)
	}
	k := probs.Shape[1]
	preds := make([]map[string]any, n)
	for i := range preds {
		l, err := s.label(label, i)
		if err != nil {
			return nil, fallback("%v", err)
		}
		j, ok := s.labels[l]
		if !ok || j >= k {
			return nil, fallback("the model predicted %v, which is not one of its labels", l)
		}
		preds[i] = map[string]any{"label": l, "probability": float64(probs.Floats[i*k+j])}
	}
	return preds, nil
}

// label reads row i of the label output the way the worker reports it: an
// integer label as a number (a JSON number decodes to float64), anything
// else as a string.
func (s *Scorer) label(t *Tensor, i int) (any, error) {
	switch {
	case t.Type == Int64 && s.intLabel:
		return float64(t.Ints[i]), nil
	case t.Type == Int64:
		return strconv.FormatInt(t.Ints[i], 10), nil
	case t.Type == String && s.intLabel:
		v, err := strconv.ParseInt(t.Strings[i], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("label %q is not an integer", t.Strings[i])
		}
		return float64(v), nil
	case t.Type == String:
		return t.Strings[i], nil
	}
	return nil, errors.New("the label output is not int64 or string")
}

// A numeric string the worker's float() and Go's ParseFloat read the same
// way: plain decimal, optionally with an exponent. Python also accepts
// underscores, "inf", "nan" and non-ASCII digits; those go to the worker.
var plainNumber = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

// number is the worker's _number for one value of a numeric feature, as it
// arrives after Hermod's client has sent it as JSON: null is the fill value,
// a boolean 1 or 0, a number itself, and a string the number it spells.
func number(v any, fill float64) (float64, bool) {
	switch x := v.(type) {
	case nil:
		return fill, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case string:
		return numberText(x)
	}
	return numberJSON(v, fill)
}

// numberText is the number a string spells, when Python's float() and Go
// read it the same way.
func numberText(x string) (float64, bool) {
	s := strings.TrimSpace(x)
	if !plainNumber.MatchString(s) {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil && !math.IsInf(f, 0)
}

// numberJSON reads any other value as the worker receives it: whatever its
// JSON encoding is.
func numberJSON(v any, fill float64) (float64, bool) {
	tok, ok := jsonToken(v)
	if !ok {
		return 0, false
	}
	var decoded any
	if err := json.Unmarshal(tok, &decoded); err != nil {
		return 0, false
	}
	if _, isNumber := decoded.(float64); isNumber {
		f, err := strconv.ParseFloat(string(tok), 64)
		return f, err == nil && !math.IsInf(f, 0)
	}
	switch decoded.(type) {
	case nil, bool, string:
		return number(decoded, fill)
	}
	return 0, false
}

// text is the worker's _text for one value of a string feature, after the
// value has crossed JSON: null is "", a string itself, a boolean "true" or
// "false", and a number rendered by hermod_ml.datasets.to_text — an
// integral value without a decimal point, anything else as Python's repr.
// An object or array would be rendered by Python's json.dumps, which this
// does not copy, so it is refused.
func text(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	}
	tok, ok := jsonToken(v)
	if !ok {
		return "", false
	}
	var decoded any
	if err := json.Unmarshal(tok, &decoded); err != nil {
		return "", false
	}
	switch d := decoded.(type) {
	case float64:
		return pythonText(string(tok))
	case nil, string, bool:
		return text(d)
	}
	return "", false
}

// jsonToken is how Hermod's client sends a value: its JSON encoding.
func jsonToken(v any) ([]byte, bool) {
	tok, err := json.Marshal(v)
	return tok, err == nil
}

// pythonText renders a JSON number token as to_text renders what Python's
// json module parses it into: an int (a token without '.', 'e' or 'E') as
// its digits, and a float as its integer digits when integral and below
// 2**53, else as repr.
func pythonText(tok string) (string, bool) {
	if !strings.ContainsAny(tok, ".eE") {
		// Python's str(int(tok)): the same digits, and "-0" is "0".
		digits := strings.TrimPrefix(tok, "-")
		if digits == "" || strings.Trim(digits, "0123456789") != "" {
			return "", false
		}
		if strings.Trim(digits, "0") == "" {
			return "0", true
		}
		return tok, true
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil || math.IsInf(f, 0) {
		return "", false
	}
	if f == 0 {
		return "0", true // str(int(-0.0)) is "0"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1<<53 {
		return strconv.FormatFloat(f, 'f', 0, 64), true
	}
	return pythonRepr(f), true
}

// pythonRepr is Python's repr of a float: the shortest digits that read back
// as the same value, in positional notation when the decimal point falls
// within 16 digits of them, else in exponent notation with a signed exponent
// of at least two digits.
func pythonRepr(f float64) string {
	e := strconv.FormatFloat(f, 'e', -1, 64) // "-1.2345e+06"
	neg := strings.HasPrefix(e, "-")
	e = strings.TrimPrefix(e, "-")
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	point := x + 1 // digits are 0.d1d2... × 10^point
	var out string
	switch {
	case point > 16 || point <= -4:
		out = digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		es := "+"
		if x < 0 {
			es, x = "-", -x
		}
		out += fmt.Sprintf("e%s%02d", es, x)
	case point <= 0:
		out = "0." + strings.Repeat("0", -point) + digits
	case point >= len(digits):
		out = digits + strings.Repeat("0", point-len(digits)) + ".0"
	default:
		out = digits[:point] + "." + digits[point:]
	}
	if neg {
		out = "-" + out
	}
	return out
}
