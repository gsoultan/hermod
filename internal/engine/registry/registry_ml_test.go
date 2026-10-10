package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/ml/monitor"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
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

// predictNode is a Predict node of workflow wf-orders, as the editor saves it.
func predictNode(model string) *storage.WorkflowNode {
	return &storage.WorkflowNode{ID: "score", Type: "transformation", Config: map[string]any{
		"transType": "ml_predict", "model": model, "outputField": "score",
	}}
}

func runPredictNode(t *testing.T, reg *Registry, model string, record map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(record)
	msg := message.AcquireMessage()
	msg.SetAfter(raw)
	msg.SetPayload(raw)
	out, _, err := reg.RunWorkflowNode("wf-orders", predictNode(model), msg)
	if err != nil {
		t.Fatalf("Predict node: %v", err)
	}
	for _, m := range out {
		m.Release()
	}
}

// mustWait is waitUntil that stops the test when the wait runs out.
func mustWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	if !waitUntil(t, 10*time.Second, what, cond) {
		t.FailNow()
	}
}

// Prediction logging, from where the operator turns it on: the model's stored
// monitoring setting. A Predict node in a workflow calls the model; the
// prediction lands in the log store, masked, with the workflow as its caller.
func TestAStoredLoggingSettingLogsAPredictNodesCalls(t *testing.T) {
	reg := newSimRegistry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"predictions": []float64{0.7}})
	}))
	t.Cleanup(srv.Close)
	models := reg.store().(storage.MLModelStore)
	if err := models.PutMLModel(t.Context(), storage.MLModel{
		VHost: "default", Name: "fraud", Backend: inference.BackendMLflow, URL: srv.URL,
		Monitoring: storage.MLMonitoring{LogSampleRate: 1, LogMaskFields: []string{"email"}},
	}); err != nil {
		t.Fatal(err)
	}

	runPredictNode(t, reg, "fraud", map[string]any{"amount": 250.0, "email": "jane@example.com"})

	logs := reg.GetLogStorage().(storage.MLPredictionLogStore)
	var got []storage.MLPredictionLog
	mustWait(t, "the prediction to be logged", func() bool {
		got, _ = logs.ListMLPredictionLogs(t.Context(), "default", "fraud", 10)
		return len(got) == 1
	})
	l := got[0]
	if l.CallerKind != storage.MLCallerWorkflow || l.CallerID != "wf-orders" {
		t.Errorf("caller = %s %q, want the workflow", l.CallerKind, l.CallerID)
	}
	if l.Inputs["email"] != "****" || l.Inputs["amount"] != 250.0 || l.Outputs["prediction"] != 0.7 {
		t.Errorf("logged inputs %v outputs %v", l.Inputs, l.Outputs)
	}
}

// fakeServingWorker serves version 1 of tenant model "churn", trained on ages
// between 20 and 60, and answers every prediction "yes".
func fakeServingWorker(t *testing.T) *worker.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions"):
			_ = json.NewEncoder(w).Encode(map[string]any{"versions": []worker.Version{{
				Model: "churn", Version: "1", Features: []string{"age"},
				FeatureStats: map[string]worker.FeatureStats{"age": {Kind: worker.StatsNumeric, Count: 100,
					Edges: []float64{30, 40, 50}, Fractions: []float64{0.25, 0.25, 0.25, 0.25}}},
			}}})
		case strings.HasSuffix(r.URL.Path, "/infer"):
			_ = json.NewEncoder(w).Encode(map[string]any{"outputs": []map[string]any{
				{"name": "label", "shape": []int{1}, "datatype": "BYTES", "data": []any{"yes"}},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return worker.New(srv.URL, "", nil)
}

// Drift, end to end: a trained model's live inputs move away from its
// training split, the window is judged, and an alert goes out through the
// notification channels.
func TestDriftingInputsRaiseAnAlert(t *testing.T) {
	t.Setenv("HERMOD_ML_DRIFT_WINDOW", "50ms")
	t.Setenv("HERMOD_ML_DRIFT_MIN_ROWS", "3")
	reg := newSimRegistry(t)
	reg.mlWorker = fakeServingWorker(t)
	models := reg.store().(storage.MLModelStore)
	if err := models.PutMLModel(t.Context(), storage.MLModel{
		VHost: "default", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1", Features: []string{"age"},
	}); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		runPredictNode(t, reg, "churn", map[string]any{"age": 95.0})
	}

	var report ml.DriftStatus
	mustWait(t, "a drift report", func() bool {
		report, _ = reg.MLService().Drift(t.Context(), "default", "churn")
		return report.Report != nil
	})
	if report.Report.Status != monitor.StatusAlert {
		t.Fatalf("report = %+v, want an alert", report.Report)
	}
	store := reg.GetStorage()
	mustWait(t, "the drift alert", func() bool {
		reg.notificationService.WaitFor(time.Second)
		logs, _, _ := store.ListLogs(t.Context(), storage.LogFilter{
			CommonFilter: storage.CommonFilter{Limit: 10}, Action: "NOTIFICATION",
		})
		for _, l := range logs {
			if strings.Contains(l.Data, "default/churn") && strings.Contains(l.Message, "age") {
				return true
			}
		}
		return false
	})
}

// Each model keeps its prediction log for its own retention.
func TestRetentionPurgesEachModelsPredictionLog(t *testing.T) {
	reg := newSimRegistry(t)
	store := reg.GetStorage()
	if err := store.CreateVHost(t.Context(), storage.VHost{ID: "vh-a", Name: "tenant-a"}); err != nil {
		t.Fatal(err)
	}
	models := reg.store().(storage.MLModelStore)
	for _, m := range []storage.MLModel{
		{VHost: "default", Name: "short", Backend: inference.BackendMLflow, URL: "http://x",
			Monitoring: storage.MLMonitoring{LogSampleRate: 1, LogRetention: "1h"}},
		{VHost: "tenant-a", Name: "long", Backend: inference.BackendMLflow, URL: "http://x",
			Monitoring: storage.MLMonitoring{LogSampleRate: 1}},
	} {
		if err := models.PutMLModel(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	logs := reg.GetLogStorage().(storage.MLPredictionLogStore)
	at := time.Now().Add(-3 * time.Hour)
	ancient := time.Now().Add(-400 * 24 * time.Hour)
	row := func(vhost, model string, ts time.Time) storage.MLPredictionLog {
		return storage.MLPredictionLog{VHost: vhost, Model: model, Timestamp: ts, Inputs: map[string]any{}, Outputs: map[string]any{}}
	}
	if err := logs.InsertMLPredictionLogs(t.Context(), []storage.MLPredictionLog{
		row("default", "short", at), row("tenant-a", "long", at), row("tenant-b", "deleted", ancient),
	}); err != nil {
		t.Fatal(err)
	}

	reg.purgeRetention()

	if got, _ := logs.ListMLPredictionLogs(t.Context(), "default", "short", 10); len(got) != 0 {
		t.Errorf("a 1h log kept a 3h-old prediction")
	}
	if got, _ := logs.ListMLPredictionLogs(t.Context(), "tenant-a", "long", 10); len(got) != 1 {
		t.Errorf("a 7d log lost a 3h-old prediction")
	}
	if got, _ := logs.ListMLPredictionLogs(t.Context(), "tenant-b", "deleted", 10); len(got) != 0 {
		t.Errorf("a prediction older than any retention survived")
	}
}
