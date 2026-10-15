// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package learn

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fitClassifier(t *testing.T, x *Frame, y []float64, p MLPParams) *Model {
	t.Helper()
	p, err := p.WithDefaults()
	if err != nil {
		t.Fatal(err)
	}
	m, err := Fit(context.Background(), Spec{Task: TaskClassification, Algorithm: AlgorithmMLP, Classes: 2, Seed: 42, MLP: p}, x, y, nil)
	if err != nil {
		t.Fatalf("fitting the network: %v", err)
	}
	return m
}

func TestQuantizingALayerMovesEachWeightByAtMostHalfAStep(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	w := make([]float32, 64*33)
	for i := range w {
		w[i] = float32(rng.NormFloat64() * 0.3)
	}
	w[5] = -1.7 // the largest magnitude sets the step
	q, scale := quantize(w)
	if want := float32(1.7 / 127); math.Abs(float64(scale-want)) > 1e-9 {
		t.Fatalf("scale = %v, want the largest magnitude over 127 (%v)", scale, want)
	}
	if q[5] != -127 {
		t.Errorf("the largest weight quantized to %d, want -127", q[5])
	}
	for i, v := range w {
		if d := math.Abs(float64(float32(q[i])*scale - v)); d > float64(scale)/2+1e-7 {
			t.Fatalf("weight %d: %v came back as %v, off by %v, more than half a step (%v)", i, v, float32(q[i])*scale, d, scale/2)
		}
	}
	if q, scale := quantize(make([]float32, 4)); scale != 1 || q[0] != 0 {
		t.Errorf("all-zero weights: scale %v, q %v; want 1 and zeros", scale, q)
	}
}

func TestTheNetworkTrainsTheSameModelOnAnyNumberOfThreads(t *testing.T) {
	// Batches big enough to be worth three goroutines (minWorkPerThread).
	x, y := xor(3000, 9)
	var models [][]byte
	for _, threads := range []int{1, 3, 1} {
		m := fitClassifier(t, x, y, MLPParams{Epochs: 5, BatchSize: 2048, Threads: threads})
		if m.Net.Threads != threads {
			t.Errorf("trained on %d threads, want %d", m.Net.Threads, threads)
		}
		m.Net.Threads, m.Net.Params.Threads = 0, 0 // recorded, and allowed to differ
		raw, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		models = append(models, raw)
	}
	if !bytes.Equal(models[0], models[2]) {
		t.Error("one seed trained two different networks")
	}
	if !bytes.Equal(models[0], models[1]) {
		t.Error("3 threads trained a different network than 1 thread")
	}
}

func TestTheThreadCapBoundsTheGoroutinesAtWork(t *testing.T) {
	for _, threads := range []int{1, 2, 3} {
		p := newPool(threads)
		var busy, peak atomic.Int32
		var mu sync.Mutex
		seen := make([]bool, 50)
		p.run(len(seen), len(seen)*minWorkPerThread, func(lo, hi int) {
			n := busy.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			for i := lo; i < hi; i++ {
				seen[i] = true
			}
			mu.Unlock()
			busy.Add(-1)
		})
		if got := peak.Load(); got > int32(threads) {
			t.Errorf("cap %d: %d goroutines worked at once", threads, got)
		}
		for i, ok := range seen {
			if !ok {
				t.Fatalf("cap %d: item %d was never worked on", threads, i)
			}
		}
	}
}

func TestTooLittleWorkStaysOnOneGoroutine(t *testing.T) {
	p := newPool(4)
	var calls atomic.Int32
	p.run(32, minWorkPerThread-1, func(lo, hi int) {
		calls.Add(1)
		if lo != 0 || hi != 32 {
			t.Errorf("one goroutine got [%d, %d), want all 32", lo, hi)
		}
	})
	if calls.Load() != 1 {
		t.Errorf("%d goroutines for less work than one is worth", calls.Load())
	}
	calls.Store(0)
	p.run(32, 2*minWorkPerThread, func(int, int) { calls.Add(1) })
	if calls.Load() != 2 {
		t.Errorf("%d goroutines for two goroutines' worth of work, want 2", calls.Load())
	}
}

func TestTheDefaultThreadCapLeavesACPUFree(t *testing.T) {
	want := max(1, runtime.GOMAXPROCS(0)-1)
	if got := threadCap(0, 0); got != want {
		t.Errorf("threadCap(0, 0) = %d, want GOMAXPROCS-1 = %d", got, want)
	}
	if got := threadCap(0, 1); got != 1 {
		t.Errorf("with the engine's workers at 1: %d threads, want 1", got)
	}
	if got := threadCap(6, 2); got != 2 {
		t.Errorf("6 asked for, the engine allows 2: %d, want 2", got)
	}
	if got := threadCap(2, 0); got != 2 {
		t.Errorf("2 asked for: %d, want 2", got)
	}
}

func TestPrecisionAutoKeepsInt8WeightsWhenTheyScoreAsWell(t *testing.T) {
	x, y := separable(800, 4)
	m := fitClassifier(t, x, y, MLPParams{Epochs: 30})
	n := m.Net
	if n.Precision != PrecisionInt8 {
		t.Fatalf("precision = %q, want int8 on an easy problem (check %+v)", n.Precision, n.Quantization)
	}
	q := n.Quantization
	if q == nil || q.Metric != "accuracy" || q.Int8 < q.Float32-q.Tolerance {
		t.Fatalf("quantization check = %+v, want accuracy within the tolerance", q)
	}
	if acc := accuracy(t, m, x, y); acc < 0.97 {
		t.Errorf("int8 network accuracy %.3f on separable data", acc)
	}

	f32 := fitClassifier(t, x, y, MLPParams{Epochs: 30, Precision: PrecisionFloat32})
	if f32.Net.Precision != PrecisionFloat32 {
		t.Fatalf("precision float32 asked for, got %q", f32.Net.Precision)
	}
	small, _ := m.Encode()
	big, _ := f32.Encode()
	if len(small) >= len(big) {
		t.Errorf("the int8 model is %d bytes, the float32 one %d: want it smaller", len(small), len(big))
	}
}

func TestInt8IsKeptOnlyWithinTheTolerance(t *testing.T) {
	for _, tc := range []struct {
		requested     string
		task          string
		float32, int8 float64
		want          string
	}{
		{PrecisionAuto, TaskClassification, 0.90, 0.897, PrecisionInt8},
		{PrecisionAuto, TaskClassification, 0.90, 0.89, PrecisionFloat32},
		{PrecisionAuto, TaskRegression, 1.00, 1.009, PrecisionInt8},
		{PrecisionAuto, TaskRegression, 1.00, 1.02, PrecisionFloat32},
		{PrecisionFloat32, TaskClassification, 0.90, 0.90, PrecisionFloat32},
		{PrecisionInt8, TaskClassification, 0.90, 0.50, PrecisionInt8},
	} {
		check := newQuantCheck(tc.task, tc.float32, tc.int8)
		if got := check.choose(tc.requested); got != tc.want {
			t.Errorf("%s %s float32 %v int8 %v: chose %s, want %s", tc.requested, tc.task, tc.float32, tc.int8, got, tc.want)
		}
	}
}

func TestPrecisionAutoFallsBackToFloat32WhenInt8LosesTheMetric(t *testing.T) {
	// One input feeds two units: a large weight the classes do not depend
	// on, and a weight so small that one int8 step of the layer (the large
	// weight over 127) rounds it to zero, which is the one that decides.
	x := numericFrame([][]float64{{-1}, {1}, {-2}, {2}, {-0.5}, {0.5}, {-3}, {3}, {-1.5}, {1.5}, {-0.2}, {0.2}})
	y := []float64{0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1}
	prep := FitPrep(x, true)
	mx := prep.transform32(x)
	n := &Net{Classes: 2, YStd: 1, Dense: []Dense{
		{In: 1, Out: 2, w32: []float32{0.003, 100}, b32: []float32{0, -1000}},
		{In: 2, Out: 2, w32: []float32{-1000, 0, 1000, 0}, b32: []float32{0, 0}},
	}}
	rows := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	got := n.settlePrecision(PrecisionAuto, mx, y, nil, rows, newPool(1))
	if got != PrecisionFloat32 || n.Quantization == nil || n.Quantization.Int8 >= n.Quantization.Float32 {
		t.Fatalf("precision %q with check %+v; want float32, int8 having lost accuracy", got, n.Quantization)
	}
	if n.Dense[0].w8 != nil {
		t.Error("the float32 network kept int8 weights")
	}
}

func TestMLPEfficiencySettingsAreChecked(t *testing.T) {
	for _, p := range []MLPParams{{Precision: "fp16"}, {Threads: -1}, {Threads: maxThreads + 1}} {
		if _, err := p.WithDefaults(); err == nil {
			t.Errorf("%+v was accepted", p)
		}
	}
	p, err := MLPParams{}.WithDefaults()
	if err != nil || p.Precision != PrecisionAuto || p.Threads != 0 {
		t.Errorf("defaults = %+v, %v; want precision auto and threads 0 (the default cap)", p, err)
	}
}

func TestANetworkModelIsFormat2AndOthersStayFormat1(t *testing.T) {
	x, y := separable(200, 5)
	for alg, want := range map[string]int{AlgorithmMLP: 2, AlgorithmLinear: 1} {
		m := fit(t, alg, TaskClassification, x, y, 2)
		raw, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		back, err := Decode(raw)
		if err != nil {
			t.Fatalf("%s: decoding: %v", alg, err)
		}
		if back.Format != want {
			t.Errorf("%s: format %d, want %d", alg, back.Format, want)
		}
		a, b := m.Proba(x), back.Proba(x)
		for i := range a {
			for k := range a[i] {
				if a[i][k] != b[i][k] {
					t.Fatalf("%s: row %d scores %v before saving and %v after", alg, i, a[i], b[i])
				}
			}
		}
	}
}

func TestADecodedNetworkWithBrokenWeightsIsRefused(t *testing.T) {
	x, y := separable(200, 6)
	m := fitClassifier(t, x, y, MLPParams{Epochs: 5, Precision: PrecisionFloat32})
	m.Net.Dense[0].W = m.Net.Dense[0].W[:len(m.Net.Dense[0].W)-1]
	raw, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(raw); err == nil {
		t.Error("a network with a weight cut short decoded")
	}
}

// legacyProbe is what testdata/mlp_format1_*.json holds: rows to score and
// what the model scored for them when it was saved, before networks were
// kept as float32 or int8.
type legacyProbe struct {
	Num     [][]float64 `json:"num"`
	Missing []int       `json:"missing_b"`
	Plan    []string    `json:"plan"`
	Proba   [][]float64 `json:"proba"`
}

func TestNetworksSavedBeforeFloat32ScoreExactlyAsBefore(t *testing.T) {
	for _, task := range []string{TaskClassification, TaskRegression} {
		raw, err := os.ReadFile("testdata/mlp_format1_" + task + ".gz")
		if err != nil {
			t.Fatal(err)
		}
		js, err := os.ReadFile("testdata/mlp_format1_" + task + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var p legacyProbe
		if err := json.Unmarshal(js, &p); err != nil {
			t.Fatal(err)
		}
		m, err := Decode(raw)
		if err != nil {
			t.Fatalf("%s: a format-1 network no longer decodes: %v", task, err)
		}
		f := NewFrame([]string{"a", "b", "plan"}, []string{KindNumber, KindNumber, KindString}, len(p.Plan))
		copy(f.Num[0], p.Num[0])
		copy(f.Num[1], p.Num[1])
		copy(f.Str[2], p.Plan)
		for _, i := range p.Missing {
			f.Num[1][i] = math.NaN()
		}
		got := m.Proba(f)
		for i := range p.Proba {
			for k := range p.Proba[i] {
				if got[i][k] != p.Proba[i][k] {
					t.Fatalf("%s: row %d scores %v, saved as %v", task, i, got[i], p.Proba[i])
				}
			}
		}
	}
}
