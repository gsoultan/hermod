package onnxscore

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The fixtures under testdata are written by testdata/gen_fixtures.py, which
// runs the hermod-ml worker's own training and serving code and onnxruntime.
// Every comparison here is against what that code computed.

// tolerance is how far a Go result may be from onnxruntime's: 1e-5, relative
// for values above 1. Both compute in float32, but not always in the same
// order, so the last bits of a sum over hundreds of trees can differ.
const tolerance = 1e-5

func near(got, want float64) bool {
	return math.Abs(got-want) <= tolerance*math.Max(1, math.Abs(want))
}

type tensorJSON struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Shape []int  `json:"shape"`
	Data  []any  `json:"data"`
}

func (tj tensorJSON) tensor(t *testing.T) *Tensor {
	t.Helper()
	out := &Tensor{Shape: tj.Shape}
	for _, v := range tj.Data {
		switch tj.Type {
		case "float":
			out.Type = Float
			out.Floats = append(out.Floats, float32(v.(float64)))
		case "int64":
			out.Type = Int64
			out.Ints = append(out.Ints, int64(v.(float64)))
		default:
			out.Type = String
			out.Strings = append(out.Strings, v.(string))
		}
	}
	return out
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func loadFixture(t testing.TB, dir string) *Model {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "model.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(raw)
	if err != nil {
		t.Fatalf("Load(%s): %v", dir, err)
	}
	return m
}

// TestGlueGraphsMatchOnnxruntime runs the hand-built graphs for the glue
// operators and compares every output tensor with onnxruntime's.
func TestGlueGraphsMatchOnnxruntime(t *testing.T) {
	for _, name := range []string{"glue", "normalizers"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", "graphs", name)
			var c struct {
				Inputs  []tensorJSON `json:"inputs"`
				Outputs []tensorJSON `json:"outputs"`
			}
			readJSON(t, filepath.Join(dir, "cases.json"), &c)
			m := loadFixture(t, dir)

			in := map[string]*Tensor{}
			for _, tj := range c.Inputs {
				in[tj.Name] = tj.tensor(t)
			}
			got, err := m.Run(in)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(got) != len(c.Outputs) {
				t.Fatalf("%d outputs, want %d", len(got), len(c.Outputs))
			}
			for i, tj := range c.Outputs {
				want := tj.tensor(t)
				compareTensor(t, tj.Name, got[i], want)
			}
		})
	}
}

func compareTensor(t *testing.T, name string, got, want *Tensor) {
	t.Helper()
	if got.Type != want.Type || !slices.Equal(got.Shape, want.Shape) {
		t.Fatalf("%s: type %v shape %v, want type %v shape %v", name, got.Type, got.Shape, want.Type, want.Shape)
	}
	switch want.Type {
	case Float:
		for i := range want.Floats {
			if !near(float64(got.Floats[i]), float64(want.Floats[i])) {
				t.Errorf("%s[%d] = %v, want %v", name, i, got.Floats[i], want.Floats[i])
			}
		}
	case Int64:
		if !slices.Equal(got.Ints, want.Ints) {
			t.Errorf("%s = %v, want %v", name, got.Ints, want.Ints)
		}
	default:
		if !slices.Equal(got.Strings, want.Strings) {
			t.Errorf("%s = %v, want %v", name, got.Strings, want.Strings)
		}
	}
}

// trainedFixtures lists the models the worker trained for the fixtures: every
// algorithm it offers, as a binary classifier with string labels, a
// three-class classifier with integer labels, and a regression.
func trainedFixtures(t testing.TB) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "trained"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) != 12 {
		t.Fatalf("found %d trained fixtures, want 12 (4 algorithms x 3 tasks)", len(names))
	}
	return names
}

func fixtureSignature(t testing.TB, dir string) Signature {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sig Signature
	if err := json.Unmarshal(raw, &sig); err != nil {
		t.Fatal(err)
	}
	return sig
}

// TestTrainedModelsScoreLikeTheWorker feeds the rows the worker answered to
// the in-process scorer and compares its predictions with the worker's: the
// same label, and the probability or value within tolerance.
func TestTrainedModelsScoreLikeTheWorker(t *testing.T) {
	for _, name := range trainedFixtures(t) {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", "trained", name)
			raw, err := os.ReadFile(filepath.Join(dir, "model.onnx"))
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewScorer(raw, fixtureSignature(t, dir))
			if err != nil {
				t.Fatalf("NewScorer: %v", err)
			}
			var cases []struct {
				Rows        []map[string]any `json:"rows"`
				Predictions []map[string]any `json:"predictions"`
			}
			readJSON(t, filepath.Join(dir, "cases.json"), &cases)
			for ci, c := range cases {
				got, err := s.Predict(c.Rows)
				if err != nil {
					t.Fatalf("case %d: Predict: %v", ci, err)
				}
				if len(got) != len(c.Predictions) {
					t.Fatalf("case %d: %d predictions, want %d", ci, len(got), len(c.Predictions))
				}
				for i, want := range c.Predictions {
					comparePrediction(t, ci, i, got[i], want)
				}
			}
		})
	}
}

func comparePrediction(t *testing.T, ci, row int, got, want map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("case %d row %d: %v, want %v", ci, row, got, want)
		return
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("case %d row %d: no %q in %v", ci, row, k, got)
			continue
		}
		if k == "label" {
			if g != w {
				t.Errorf("case %d row %d: label %#v, want %#v", ci, row, g, w)
			}
			continue
		}
		gf, ok := g.(float64)
		if !ok || !near(gf, w.(float64)) {
			t.Errorf("case %d row %d: %s = %#v, want %v", ci, row, k, g, w)
		}
	}
}

// TestEveryWorkerExportIsSupported names the operators each algorithm's
// export uses, as listed from the fixtures, so a change in what the worker
// emits shows up here rather than as a silent fallback in production.
func TestEveryWorkerExportIsSupported(t *testing.T) {
	glue := []string{"Concat", "Gather", "Reshape", "ai.onnx.ml.OneHotEncoder"}
	want := map[string][]string{
		"linear_binary":     append([]string{"ai.onnx.ml.LinearClassifier", "ai.onnx.ml.Scaler"}, glue...),
		"linear_multiclass": append([]string{"ai.onnx.ml.LinearClassifier", "ai.onnx.ml.Normalizer", "ai.onnx.ml.Scaler"}, glue...),
		"linear_regression": append([]string{"ai.onnx.ml.LinearRegressor", "ai.onnx.ml.Scaler"}, glue...),
	}
	for _, algo := range []string{"random_forest", "gradient_boosting", "xgboost"} {
		want[algo+"_binary"] = append([]string{"ai.onnx.ml.TreeEnsembleClassifier"}, glue...)
		want[algo+"_multiclass"] = append([]string{"ai.onnx.ml.TreeEnsembleClassifier"}, glue...)
		want[algo+"_regression"] = append([]string{"ai.onnx.ml.TreeEnsembleRegressor"}, glue...)
	}
	for _, name := range trainedFixtures(t) {
		m := loadFixture(t, filepath.Join("testdata", "trained", name))
		w := slices.Clone(want[name])
		slices.Sort(w)
		if got := m.Ops(); !slices.Equal(got, w) {
			t.Errorf("%s uses %v, want %v", name, got, w)
		}
	}
}
