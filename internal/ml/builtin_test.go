// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package ml

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/builtin"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// builtinService is a Service whose engine is Hermod's built-in one, with a
// dataset of 200 customers whose plan decides whether they churn.
func builtinService(t *testing.T) *Service {
	t.Helper()
	return NewService(func() any { return newMemStore() }, nil, nil).WithWorker(builtinEngine(t).Client())
}

// builtinEngine is Hermod's built-in engine with the customers dataset.
func builtinEngine(t *testing.T) *builtin.Engine {
	t.Helper()
	e, err := builtin.New(builtin.Config{Dir: t.TempDir(), Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]map[string]any, 200)
	for i := range rows {
		plan := []string{"basic", "pro"}[i%2]
		churned := map[string]string{"basic": "yes", "pro": "no"}[plan]
		rows[i] = map[string]any{"age": float64(20 + i%40), "plan": plan, "churned": churned}
	}
	if _, err := e.Client().AppendRows(t.Context(), "v", "customers", rows, true); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestConnectingOrDisconnectingTheWorkerNeverBreaksAModel(t *testing.T) {
	store := newMemStore()
	e := builtinEngine(t)
	f := &fakeWorker{score: 0.9, versions: map[string][]worker.Version{}, rows: map[string]int{}}
	remote := worker.New(f.server(t).URL, "worker-token", nil)
	svc := NewService(func() any { return store }, nil, nil).WithWorker(e.Route(remote))
	ctx := t.Context()

	// A built-in algorithm trains in-process although a worker is connected;
	// one only the worker trains goes to it, with the dataset copied over.
	mine, err := svc.Train(ctx, "v", "churn", worker.TrainSpec{Dataset: "customers", Target: "churned", Algorithm: "linear"},
		GoLive{Mode: GoLiveAlways}, "ada")
	if err != nil || mine.Version.Version != "b1" || mine.Version.Engine != worker.EngineBuiltin {
		t.Fatalf("linear with a worker connected = %+v, %v; want b1, in-process", mine.Version, err)
	}
	theirs, err := svc.Train(ctx, "v", "xg", worker.TrainSpec{Dataset: "customers", Target: "churned", Algorithm: "xgboost"},
		GoLive{Mode: GoLiveAlways}, "ada")
	if err != nil || theirs.Version.Version != "1" || theirs.Version.Engine != worker.EngineWorker {
		t.Fatalf("xgboost = %+v, %v; want version 1 on the worker", theirs.Version, err)
	}
	if f.rows["v/customers"] != 200 {
		t.Errorf("the worker got %d rows of customers, want 200", f.rows["v/customers"])
	}
	predict := func(model string) ([]inference.Row, error) {
		return svc.Predict(ctx, "v", model, []inference.Row{{"age": 30, "plan": "basic"}, {"age": 50, "plan": "pro"}})
	}
	for _, model := range []string{"churn", "xg"} {
		if _, err := predict(model); err != nil {
			t.Errorf("with the worker connected, Predict(%s): %v", model, err)
		}
	}

	svc.WithWorker(e.Route(nil)) // the worker is disconnected
	if out, err := predict("churn"); err != nil || out[0]["label"] != "yes" || out[1]["label"] != "no" {
		t.Errorf("without the worker, the built-in model predicts %v, %v", out, err)
	}
	_, err = predict("xg")
	if !errors.Is(err, ErrNoWorker) || !strings.Contains(err.Error(), `"xg"`) || !strings.Contains(err.Error(), builtin.NeedsWorker) {
		t.Errorf("without the worker, the worker's model: %v; want ErrNoWorker naming the model and %q", err, builtin.NeedsWorker)
	}
	if st, err := svc.Scoring(ctx, "v", "churn"); err != nil || !st.InProcess {
		t.Errorf("scoring of the built-in model = %+v, %v", st, err)
	}

	svc.WithWorker(e.Route(remote)) // and connected again
	if _, err := predict("xg"); err != nil {
		t.Errorf("with the worker back, Predict(xg): %v", err)
	}
}

func TestATrainingOnlyTheWorkerRunsWaitsForNoWorkerWhenOneIsConnected(t *testing.T) {
	e := builtinEngine(t)
	f := &fakeWorker{score: 0.9, versions: map[string][]worker.Version{}, rows: map[string]int{}}
	svc := NewService(func() any { return newMemStore() }, nil, nil).
		WithWorker(e.Route(worker.New(f.server(t).URL, "worker-token", nil)))
	if err := svc.checkEngine(t.Context(), worker.TrainSpec{Dataset: "customers", Target: "churned", Algorithm: "keras_mlp"}); err != nil {
		t.Errorf("checkEngine refused keras_mlp with a worker connected: %v", err)
	}
	if err := svc.checkEngine(t.Context(), worker.TrainSpec{Scenario: "visits", Data: map[string]map[string]string{"visits": {"dataset": "customers"}}}); err != nil {
		t.Errorf("checkEngine refused a scenario with a worker connected: %v", err)
	}
}

func TestTheBuiltinEngineTrainsAndPredictsWithNoWorker(t *testing.T) {
	store := newMemStore()
	svc := builtinService(t)
	svc.store = func() any { return store }

	res, err := svc.Train(t.Context(), "v", "churn", worker.TrainSpec{Dataset: "customers", Target: "churned", Algorithm: "mlp"},
		GoLive{Mode: GoLiveAlways}, "ada")
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if !res.Live || res.Version.Algorithm != "mlp" || res.Version.Metrics["accuracy"] < 0.99 {
		t.Fatalf("result = %+v", res)
	}
	out, err := svc.Predict(t.Context(), "v", "churn", []inference.Row{{"age": 30, "plan": "basic"}, {"age": 50, "plan": "pro"}})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if out[0]["label"] != "yes" || out[1]["label"] != "no" {
		t.Errorf("predictions = %v", out)
	}
	st, err := svc.Scoring(t.Context(), "v", "churn")
	if err != nil {
		t.Fatal(err)
	}
	if !st.InProcess || st.Scoring != storage.MLScoringInProcess || st.Version != "b1" {
		t.Errorf("scoring = %+v, want in-process", st)
	}
}

func TestTheBuiltinEngineSendsWorkerOnlyTrainingsToTheWorker(t *testing.T) {
	svc := builtinService(t)
	for _, spec := range []worker.TrainSpec{
		{Dataset: "customers", Target: "churned", Algorithm: "xgboost"},
		{Dataset: "customers", Target: "churned", Algorithm: "keras_mlp"},
	} {
		_, err := svc.Train(t.Context(), "v", "m", spec, GoLive{}, "ada")
		if !errors.Is(err, ErrNoWorker) {
			t.Errorf("Train(%s) = %v, want ErrNoWorker", spec.Algorithm, err)
		}
		if err != nil && (!strings.Contains(err.Error(), spec.Algorithm) || !strings.Contains(err.Error(), builtin.NeedsWorker)) {
			t.Errorf("the refusal does not say why: %v", err)
		}
	}
}

func TestABuiltinModelWithNoLiveVersionIsRefused(t *testing.T) {
	store := newMemStore()
	svc := builtinService(t)
	svc.store = func() any { return store }
	if _, err := svc.Train(t.Context(), "v", "churn", worker.TrainSpec{Dataset: "customers", Target: "churned", Algorithm: "linear"},
		GoLive{Mode: GoLiveNever}, "ada"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Predict(t.Context(), "v", "churn", []inference.Row{{"age": 1, "plan": "pro"}}); !errors.Is(err, ErrNoLiveVersion) {
		t.Errorf("Predict = %v, want ErrNoLiveVersion", err)
	}
}
