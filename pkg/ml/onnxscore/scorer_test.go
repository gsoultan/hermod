package onnxscore

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func fixtureScorer(t testing.TB, name string) *Scorer {
	t.Helper()
	dir := filepath.Join("testdata", "trained", name)
	raw, err := os.ReadFile(filepath.Join(dir, "model.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewScorer(raw, fixtureSignature(t, dir))
	if err != nil {
		t.Fatalf("NewScorer: %v", err)
	}
	return s
}

// TestPredictHandsBackWhatItCannotReproduce: a value the worker would read
// in a way the scorer does not copy exactly, or would refuse, is not
// guessed at. Predict returns ErrFallback and the caller asks the worker,
// which either scores it or answers with its own error.
func TestPredictHandsBackWhatItCannotReproduce(t *testing.T) {
	s := fixtureScorer(t, "linear_binary")
	base := func() map[string]any { return map[string]any{"x1": 1.0, "x2": 2.0, "city": "Oslo", "flag": true} }
	for name, edit := range map[string]func(map[string]any){
		"nested value in a string feature": func(r map[string]any) { r["city"] = map[string]any{"a": 1} },
		"number with underscores":          func(r map[string]any) { r["x1"] = "1_000" },
		"hexadecimal float":                func(r map[string]any) { r["x1"] = "0x1p3" },
		"not a number":                     func(r map[string]any) { r["x1"] = "abc" },
		"infinity in a string":             func(r map[string]any) { r["x1"] = "inf" },
		"NaN":                              func(r map[string]any) { r["x1"] = math.NaN() },
		"unknown Go type":                  func(r map[string]any) { r["x2"] = struct{}{} },
		"feature in no row":                func(r map[string]any) { delete(r, "x2") },
	} {
		t.Run(name, func(t *testing.T) {
			r := base()
			edit(r)
			if _, err := s.Predict([]map[string]any{r}); !errors.Is(err, ErrFallback) {
				t.Fatalf("Predict = %v, want ErrFallback", err)
			}
		})
	}
}

// TestPredictReadsValuesAsTheWorkerDoes covers the conversions the parity
// fixtures do not: Go types a workflow message holds but JSON never does,
// and numbers fed to a string feature, which the worker renders as text in
// one canonical way (hermod_ml.datasets.to_text).
func TestPredictReadsValuesAsTheWorkerDoes(t *testing.T) {
	s := fixtureScorer(t, "random_forest_binary")
	score := func(r map[string]any) map[string]any {
		t.Helper()
		out, err := s.Predict([]map[string]any{r})
		if err != nil {
			t.Fatalf("Predict(%v): %v", r, err)
		}
		return out[0]
	}
	same := func(a, b map[string]any) {
		t.Helper()
		x, y := score(a), score(b)
		if x["label"] != y["label"] || x["probability"] != y["probability"] {
			t.Errorf("%v scored %v, but %v scored %v", a, x, b, y)
		}
	}
	row := func(x1, x2, city, flag any) map[string]any {
		return map[string]any{"x1": x1, "x2": x2, "city": city, "flag": flag}
	}
	same(row(int64(2), int32(7), "Oslo", true), row(2.0, 7.0, "Oslo", 1.0))
	same(row(float32(0.1), uint8(3), "Bergen", false), row(0.1, 3.0, "Bergen", 0.0))
	same(row(json.Number("1.5"), "4", "Oslo", "1"), row(1.5, 4.0, "Oslo", 1.0))
	same(row(nil, 2.0, nil, nil), row(0.20999999344348907, 2.0, "", 0.0))

	for _, tc := range []struct {
		in   any
		want string
	}{
		{3.0, "3"}, {3.5, "3.5"}, {int64(-12), "-12"}, {true, "true"}, {false, "false"},
		{1e16, "10000000000000000"}, {1e22, "1e+22"}, {0.0001, "0.0001"}, {0.00001, "1e-05"},
		{1.5e-7, "1.5e-07"}, {123456789.125, "123456789.125"}, {2.5e17, "250000000000000000"},
		{float32(0.1), "0.1"}, {math.Copysign(0, -1), "0"}, {json.Number("12"), "12"}, {json.Number("1.50"), "1.5"},
	} {
		got, ok := text(tc.in)
		if !ok || got != tc.want {
			t.Errorf("text(%#v) = %q, %v; want %q", tc.in, got, ok, tc.want)
		}
	}
}

// TestScorerIsSafeForConcurrentUse: one Scorer serves every Predict node and
// request at once.
func TestScorerIsSafeForConcurrentUse(t *testing.T) {
	s := fixtureScorer(t, "gradient_boosting_multiclass")
	rows := []map[string]any{{"x1": 1.0, "x2": 2.0, "city": "Oslo", "flag": true}}
	want, err := s.Predict(rows)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 8)
	for range 8 {
		go func() {
			for range 50 {
				got, err := s.Predict(rows)
				if err != nil {
					done <- err
					return
				}
				if got[0]["label"] != want[0]["label"] || got[0]["probability"] != want[0]["probability"] {
					done <- errors.New("a concurrent Predict scored differently")
					return
				}
			}
			done <- nil
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
