// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package builtin

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

func TestATrainingIsStoppedMidwayByItsContext(t *testing.T) {
	e, c := newEngine(t)
	appendRows(t, c, "lin", linearRows(2000))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	req := &trainRequest{dataset: "lin", target: "y", task: "auto", algorithm: "mlp", testSize: 0.2, seed: 1}
	req.params.Epochs, req.params.BatchSize, req.params.Patience = 1000, 1, 1000
	req.params, _ = req.params.WithDefaults()
	_, err := e.trainTabular(ctx, "v", "m", req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("trainTabular = %v, want context.DeadlineExceeded", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the training stopped %s after its deadline", took)
	}
}

func TestTrainTimeoutReachesTheEngine(t *testing.T) {
	e, c := newEngine(t)
	appendRows(t, c, "lin", linearRows(2000))
	c.TrainTimeout = 100 * time.Millisecond
	p := &worker.TrainParams{Epochs: 1000, BatchSize: 1, Patience: 1000}
	if _, err := c.Train(context.Background(), "v", "m", worker.TrainSpec{Dataset: "lin", Target: "y", Algorithm: "mlp", Params: p}); err == nil {
		t.Fatal("a training past the train timeout succeeded")
	}
	// The slot is given back.
	release, err := e.slot()
	if err != nil {
		t.Fatalf("the slot was not given back: %v", err)
	}
	release()
}

func TestConfigFromEnvKeepsNothingOnDiskUnlessADirectoryIsGiven(t *testing.T) {
	t.Setenv("HERMOD_ML_BUILTIN", "")
	t.Setenv("HERMOD_ML_BUILTIN_DIR", "")
	cfg, on := ConfigFromEnv()
	if !on || cfg.Dir != "" {
		t.Errorf("by default: on = %v, dir = %q; want on, with no directory (Hermod's store keeps the data)", on, cfg.Dir)
	}
	t.Setenv("HERMOD_ML_BUILTIN_DIR", " /data/ml ")
	if cfg, _ := ConfigFromEnv(); cfg.Dir != "/data/ml" {
		t.Errorf("dir = %q, want /data/ml", cfg.Dir)
	}
	t.Setenv("HERMOD_ML_BUILTIN", "false")
	if _, on := ConfigFromEnv(); on {
		t.Error("HERMOD_ML_BUILTIN=false did not turn the engine off")
	}
}

func TestNumericStatsAreNumpyDeciles(t *testing.T) {
	st := numericStats([]float64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1, math.NaN()})
	want := []float64{1.9, 2.8, 3.7, 4.6, 5.5, 6.4, 7.3, 8.2, 9.1}
	for i := range want {
		if math.Abs(st.Edges[i]-want[i]) > 1e-9 {
			t.Fatalf("edges = %v, want %v", st.Edges, want)
		}
	}
	for _, f := range st.Fractions {
		if math.Abs(f-0.1) > 1e-12 {
			t.Fatalf("fractions = %v", st.Fractions)
		}
	}
	if st.Count != 11 || math.Abs(st.NullFraction-1.0/11) > 1e-12 || *st.Mean != 5.5 || *st.Min != 1 || *st.Max != 10 {
		t.Errorf("stats = %+v", st)
	}
	// Repeated values merge their edges.
	if st := numericStats([]float64{1, 1, 1, 1, 2}); len(st.Edges) != 3 || st.Edges[0] != 1 ||
		math.Abs(st.Edges[1]-1.2) > 1e-9 || math.Abs(st.Edges[2]-1.6) > 1e-9 || len(st.Fractions) != 4 {
		t.Errorf("edges of repeated values = %v, fractions %v", st.Edges, st.Fractions)
	}
}

func TestCategoricalStatsRankByCountThenValue(t *testing.T) {
	st := categoricalStats([]string{"b", "a", "b", "", "c", "a"})
	if len(st.Top) != 3 || st.Top[0].Value != "a" || st.Top[1].Value != "b" || st.Top[2].Value != "c" {
		t.Errorf("top = %v", st.Top)
	}
	if math.Abs(st.Top[0].Fraction-0.4) > 1e-12 || math.Abs(st.NullFraction-1.0/6) > 1e-12 || st.OtherFraction > 1e-12 {
		t.Errorf("stats = %+v", st)
	}
}

func TestTargetWarningsUseTheWorkersWords(t *testing.T) {
	y := make([]float64, 50)
	for i := range 5 {
		y[i] = 1
	}
	tg := target{y: y, labels: []any{"no", "yes"}}
	warns, ok := targetWarnings("classification", "churn", tg)
	if !ok || len(warns) != 2 {
		t.Fatalf("warnings = %v, ok %v", warns, ok)
	}
	if warns[0].Message != "Only 50 rows have a value in 'churn'. A model learned from so few is rough; more rows give better results." {
		t.Errorf("few_rows = %q", warns[0].Message)
	}
	if warns[1].Code != "imbalanced" || warns[1].Message != "'churn' is lopsided: 'yes' is only 10% of the rows. Hermod weighs the values "+
		"so the rare ones count, and judges models by balanced accuracy." {
		t.Errorf("imbalanced = %+v", warns[1])
	}
}

func TestFitStumpsFindsTheCuts(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	y := []float64{0, 0, 0, 1, 1, 1, 2, 2, 2}
	tree := fitStumps(x, y, []int{0, 1, 2, 3, 4, 5, 6, 7, 8}, 3, 3)
	if !slices.Equal(tree.cuts, []float64{3.5, 6.5}) || !slices.Equal(tree.answers, []int{0, 1, 2}) {
		t.Errorf("tree = %+v", tree)
	}
	for i, v := range x {
		if tree.predict(v) != int(y[i]) {
			t.Errorf("predict(%v) = %d", v, tree.predict(v))
		}
	}
}

func TestANetworkVersionRecordsHowItIsKept(t *testing.T) {
	_, c := newEngine(t)
	ctx := context.Background()
	appendRows(t, c, "churn", churnRows(400))
	v, err := c.Train(ctx, "v", "net", worker.TrainSpec{Dataset: "churn", Target: "churn", Algorithm: "mlp",
		Params: &worker.TrainParams{Epochs: 20, Threads: 2}})
	if err != nil {
		t.Fatal(err)
	}
	ef := v.Efficiency
	if ef == nil {
		t.Fatal("the network's version records no efficiency")
	}
	if ef.Requested != "auto" || (ef.Precision != "int8" && ef.Precision != "float32") || ef.Threads != 2 || ef.Metric != "accuracy" {
		t.Errorf("efficiency = %+v; want auto asked for, int8 or float32 kept, 2 threads, checked on accuracy", ef)
	}
	if ef.Float32 == nil || ef.Int8 == nil {
		t.Errorf("efficiency = %+v; want both precisions' scores", ef)
	}
	f32, err := c.Train(ctx, "v", "net32", worker.TrainSpec{Dataset: "churn", Target: "churn", Algorithm: "mlp",
		Params: &worker.TrainParams{Epochs: 20, Precision: "float32"}})
	if err != nil {
		t.Fatal(err)
	}
	if f32.Efficiency == nil || f32.Efficiency.Precision != "float32" || f32.Efficiency.Requested != "float32" {
		t.Errorf("float32 asked for: efficiency = %+v", f32.Efficiency)
	}
	forest, err := c.Train(ctx, "v", "forest", worker.TrainSpec{Dataset: "churn", Target: "churn", Algorithm: "random_forest"})
	if err != nil {
		t.Fatal(err)
	}
	if forest.Efficiency != nil {
		t.Errorf("a forest records efficiency %+v; only a network has one", forest.Efficiency)
	}
}
