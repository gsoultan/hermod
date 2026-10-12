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
	c := e.Client()
	if _, err := c.AppendRows(t.Context(), "v", "customers", rows, true); err != nil {
		t.Fatal(err)
	}
	return NewService(func() any { return newMemStore() }, nil, nil).WithWorker(c)
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
	if !st.InProcess || st.Scoring != storage.MLScoringInProcess || st.Version != "1" {
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
