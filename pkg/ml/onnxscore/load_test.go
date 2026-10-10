package onnxscore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadRefusesWhatItCannotScore: a graph with an operator outside the
// supported set, or with ZipMap (whose output is not a tensor), is refused
// with ErrUnsupported naming the operator, so the caller falls back to the
// worker instead of scoring it wrong.
func TestLoadRefusesWhatItCannotScore(t *testing.T) {
	for _, tc := range []struct{ file, mention string }{
		{"unsupported_op.onnx", "Abs"},
		{"unsupported_zipmap.onnx", "ZipMap"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "graphs", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Load(raw)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("Load = %v, want ErrUnsupported", err)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not name %s", err, tc.mention)
			}
		})
	}
}

// TestLoadRefusesMalformedBytes: anything that is not a well-formed model is
// an error, never a panic.
func TestLoadRefusesMalformedBytes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "trained", "random_forest_binary", "model.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{
		"empty":     nil,
		"garbage":   []byte("not a protobuf at all"),
		"truncated": raw[:len(raw)/2],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(b); err == nil {
				t.Fatal("Load accepted malformed bytes")
			}
		})
	}
}

// TestNewScorerRefusesAModelItsSignatureDoesNotDescribe: the worker's meta
// and the graph must agree on the inputs, or the scorer would feed the wrong
// columns.
func TestNewScorerRefusesAModelItsSignatureDoesNotDescribe(t *testing.T) {
	dir := filepath.Join("testdata", "trained", "linear_binary")
	raw, err := os.ReadFile(filepath.Join(dir, "model.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*Signature){
		"missing feature": func(s *Signature) { s.Features = s.Features[:len(s.Features)-1] },
		"wrong type":      func(s *Signature) { s.FeatureTypes["city"] = "number" },
		"unknown task":    func(s *Signature) { s.Task = "forecast" },
		"no labels":       func(s *Signature) { s.Labels = nil },
	} {
		t.Run(name, func(t *testing.T) {
			sig := fixtureSignature(t, dir)
			edit(&sig)
			if _, err := NewScorer(raw, sig); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("NewScorer = %v, want ErrUnsupported", err)
			}
		})
	}
}
