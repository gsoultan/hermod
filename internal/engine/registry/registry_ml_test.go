package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/ml"
)

// The Predict node end to end: a model registered in the vhost's registry is
// called through the engine's transformation path, and its answer lands on the
// message.
func TestMLPredictCallsTheRegisteredModel(t *testing.T) {
	reg := newSimRegistry(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Records []map[string]float64 `json:"dataframe_records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"predictions": []float64{body.Records[0]["amount"] / 1000}})
	}))
	t.Cleanup(srv.Close)

	models, ok := reg.store().(storage.MLModelStore)
	if !ok {
		t.Fatal("the registry's store holds no models")
	}
	if err := models.PutMLModel(t.Context(), storage.MLModel{
		VHost: "default", Name: "fraud", Backend: inference.BackendMLflow, URL: srv.URL,
	}); err != nil {
		t.Fatalf("PutMLModel: %v", err)
	}

	got := transform(t, reg,
		map[string]any{"total": 250.0, "customer": "C-1"},
		map[string]any{
			"transType":   "ml_predict",
			"model":       "fraud",
			"inputs":      `{"amount":"total"}`,
			"outputField": "fraud_score",
		})

	if got["fraud_score"] != 0.25 {
		t.Errorf("fraud_score = %v, want 0.25 from the model", got["fraud_score"])
	}
	if got["customer"] != "C-1" {
		t.Errorf("customer = %v, the record lost a field", got["customer"])
	}
}

// fakeTrainingWorker trains instantly and keeps the dataset rows it is sent.
func fakeTrainingWorker(t *testing.T) (*worker.Client, *[]string, *[]map[string]any) {
	t.Helper()
	var calls []string
	var rows []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/rows"):
			var body struct {
				Rows []map[string]any `json:"rows"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			rows = append(rows, body.Rows...)
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": len(rows)})
		case strings.HasSuffix(r.URL.Path, "/train"):
			_ = json.NewEncoder(w).Encode(worker.Version{Model: "churn", Version: "1", Task: "classification",
				Dataset: "customers", Target: "churned", Features: []string{"age"},
				Metrics: map[string]float64{"score": 0.9}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return worker.New(srv.URL, "", nil), &calls, &rows
}

// The Train Model node end to end: it refills the dataset from a database
// source of its vhost, trains, registers the model and writes the result.
func TestMLTrainRefillsFromTheSourceTrainsAndRegisters(t *testing.T) {
	reg := newSimRegistry(t)
	w, calls, rows := fakeTrainingWorker(t)
	reg.mlWorker = w

	dbPath := "file:mltrain_" + t.Name() + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE customers (age INTEGER, churned TEXT); INSERT INTO customers VALUES (30,'no'),(70,'yes')`); err != nil {
		t.Fatal(err)
	}
	if err := reg.store().(interface {
		CreateSource(context.Context, storage.Source) error
	}).CreateSource(t.Context(), storage.Source{
		ID: "crm", Name: "crm", Type: "sqlite", VHost: "default", Config: map[string]string{"path": dbPath},
	}); err != nil {
		t.Fatal(err)
	}

	got := transform(t, reg, map[string]any{"tick": 1.0}, map[string]any{
		"transType": "ml_train", "model": "churn", "dataset": "customers", "target": "churned",
		"goLive": "always", "sourceId": "crm", "query": "SELECT age, churned FROM customers",
	})
	res, _ := got["training"].(map[string]any)
	if res["version"] != "1" || res["live"] != true {
		t.Fatalf("training = %v", got["training"])
	}
	if len(*rows) != 2 {
		t.Errorf("the worker got %d dataset rows, want 2", len(*rows))
	}
	if (*calls)[0] != "POST /v1/datasets/default/customers/rows" || (*calls)[len(*calls)-1] != "POST /v1/models/default/churn/train" {
		t.Errorf("calls = %v, want the refill before the training", *calls)
	}
	m, err := reg.MLService().Model(t.Context(), "default", "churn")
	if err != nil || m.Backend != storage.MLBackendWorker || m.RemoteVersion != "1" {
		t.Errorf("registered = %+v, %v", m, err)
	}
}

func TestMLDatasetFromQueryRefusesAnotherVHostsSource(t *testing.T) {
	reg := newSimRegistry(t)
	w, calls, _ := fakeTrainingWorker(t)
	reg.mlWorker = w
	if err := reg.store().(interface {
		CreateSource(context.Context, storage.Source) error
	}).CreateSource(t.Context(), storage.Source{
		ID: "theirs", Name: "theirs", Type: "sqlite", VHost: "tenant-b", Config: map[string]string{"path": ":memory:"},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := reg.MLDatasetFromQuery(t.Context(), "tenant-a", "d", "theirs", "SELECT 1", 0)
	if err == nil || !strings.Contains(err.Error(), "tenant-a") {
		t.Errorf("err = %v, want a refusal naming the vhost", err)
	}
	if len(*calls) != 0 {
		t.Errorf("the worker was called: %v", *calls)
	}
}
