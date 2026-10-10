package onnxscore

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// benchRows are n request rows, cycled from a fixture's first case.
func benchRows(b *testing.B, fixture string, n int) []map[string]any {
	b.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "trained", fixture, "cases.json"))
	if err != nil {
		b.Fatal(err)
	}
	var cases []struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		b.Fatal(err)
	}
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = cases[0].Rows[i%len(cases[0].Rows)]
	}
	return out
}

// BenchmarkInProcess is the latency of one Predict call scored in-process,
// for one row (a Predict node scoring each message) and for a batch of 100.
func BenchmarkInProcess(b *testing.B) {
	for _, fixture := range []string{"linear_binary", "random_forest_binary", "gradient_boosting_multiclass", "xgboost_regression"} {
		s := fixtureScorer(b, fixture)
		for _, n := range []int{1, 100} {
			rows := benchRows(b, fixture, n)
			b.Run(fmt.Sprintf("%s/rows=%d", fixture, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Predict(rows); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkWorkerVsInProcess scores the same model version both ways: over
// HTTP on a running hermod-ml worker, as Predict does by default, and
// in-process from the graph the worker hands out. It needs a worker: set
// HERMOD_ML_BENCH_WORKER_URL (and HERMOD_ML_BENCH_WORKER_TOKEN if it has a
// token). It trains one model per algorithm on 200 rows in vhost "bench".
func BenchmarkWorkerVsInProcess(b *testing.B) {
	url := os.Getenv("HERMOD_ML_BENCH_WORKER_URL")
	if url == "" {
		b.Skip("set HERMOD_ML_BENCH_WORKER_URL to a running hermod-ml worker")
	}
	ctx := context.Background()
	w := worker.New(url, os.Getenv("HERMOD_ML_BENCH_WORKER_TOKEN"), nil)
	rng := rand.New(rand.NewPCG(3, 3))
	train := make([]map[string]any, 200)
	for i := range train {
		x1, x2 := rng.Float64()*6-3, float64(rng.IntN(10))
		city := []string{"Oslo", "Bergen", "Tromso"}[rng.IntN(3)]
		flag := rng.IntN(2) == 1
		score := x1 + x2/3
		train[i] = map[string]any{
			"x1": x1, "x2": x2, "city": city, "flag": flag,
			"churned": score > 1, "band": min(2, max(0, int(score+1)/2)), "amount": 3*x1 + 2*x2,
		}
	}
	if _, err := w.AppendRows(ctx, "bench", "rows", train, true); err != nil {
		b.Fatal(err)
	}
	client := inference.NewClient(nil)
	for _, tc := range []struct{ algorithm, task, target, rows string }{
		{"linear", "classification", "churned", "linear_binary"},
		{"random_forest", "classification", "churned", "random_forest_binary"},
		{"gradient_boosting", "classification", "band", "gradient_boosting_multiclass"},
		{"xgboost", "regression", "amount", "xgboost_regression"},
	} {
		model := "bench_" + tc.algorithm
		v, err := w.Train(ctx, "bench", model, worker.TrainSpec{
			Dataset: "rows", Target: tc.target, Features: []string{"x1", "x2", "city", "flag"},
			Task: tc.task, Algorithm: tc.algorithm,
		})
		if err != nil {
			b.Fatal(err)
		}
		graph, err := w.ModelFile(ctx, "bench", model, v.Version)
		if err != nil {
			b.Fatal(err)
		}
		s, err := NewScorer(graph, Signature{Task: v.Task, Features: v.Features, FeatureTypes: v.FeatureTypes, Fill: v.Fill, Labels: v.Labels})
		if err != nil {
			b.Fatal(err)
		}
		target := inference.Target{
			Backend: inference.BackendOIP, URL: w.ServingURL("bench"), Model: model, Version: v.Version,
			Token: w.Token, Timeout: 10 * time.Second,
		}
		// Both ways must answer the same before their speed is compared.
		rows := benchRows(b, tc.rows, 25)
		fromWorker, err := client.Predict(ctx, target, rows)
		if err != nil {
			b.Fatal(err)
		}
		inProcess, err := s.Predict(rows)
		if err != nil {
			b.Fatal(err)
		}
		for i := range rows {
			for k, want := range fromWorker[i] {
				got := inProcess[i][k]
				if wf, ok := want.(float64); ok && k != "label" {
					if gf, ok := got.(float64); !ok || !near(gf, wf) {
						b.Fatalf("%s row %d: %s = %v in-process, %v on the worker", tc.algorithm, i, k, got, want)
					}
				} else if got != want {
					b.Fatalf("%s row %d: %s = %v in-process, %v on the worker", tc.algorithm, i, k, got, want)
				}
			}
		}
		for _, n := range []int{1, 100} {
			rows := benchRows(b, tc.rows, n)
			b.Run(fmt.Sprintf("%s/rows=%d/worker", tc.algorithm, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := client.Predict(ctx, target, rows); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(fmt.Sprintf("%s/rows=%d/in_process", tc.algorithm, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Predict(rows); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
