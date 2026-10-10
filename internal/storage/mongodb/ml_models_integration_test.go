//go:build integration
// +build integration

package mongodb

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
)

// The same contract the SQL store keeps (internal/storage/sql/ml_models_test.go),
// against a live MongoDB.
func TestMongoMLModels(t *testing.T) {
	s, _ := newTraceMongo(t)
	st, ok := s.(storage.MLModelStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.MLModelStore")
	}
	ctx := t.Context()
	m := storage.MLModel{
		VHost: "tenant-a", Name: "churn", Backend: inference.BackendOIP, URL: "http://ml:8080",
		RemoteModel: "churn", InputName: "input", Features: []string{"age"}, UpdatedBy: "ada",
	}
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatalf("PutMLModel: %v", err)
	}
	if err := st.SetMLModelServingKey(ctx, "tenant-a", "churn", "hash"); err != nil {
		t.Fatalf("SetMLModelServingKey: %v", err)
	}
	m.URL = "http://ml-2:8080"
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMLModel(ctx, "tenant-a", "churn")
	if err != nil {
		t.Fatalf("GetMLModel: %v", err)
	}
	if got.URL != "http://ml-2:8080" || got.ServingKeyHash != "hash" || !got.Serving || len(got.Features) != 1 {
		t.Errorf("round trip: %+v", got)
	}
	if _, err := st.GetMLModel(ctx, "tenant-b", "churn"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("another vhost read it: %v", err)
	}
	if list, _ := st.ListMLModels(ctx, "tenant-a"); len(list) != 1 {
		t.Errorf("list = %+v", list)
	}
	if err := st.DeleteMLModel(ctx, "tenant-a", "churn"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteMLModel(ctx, "tenant-a", "churn"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestMongoMLModelMonitoringRoundTrips(t *testing.T) {
	s, _ := newTraceMongo(t)
	st := s.(storage.MLModelStore)
	ctx := t.Context()
	m := storage.MLModel{VHost: "tenant-a", Name: "churn", Backend: storage.MLBackendWorker,
		Monitoring: storage.MLMonitoring{LogSampleRate: 0.5, LogMaskFields: []string{"email"}, LogMaskType: "partial",
			LogRetention: "2d", DriftWarn: 0.2, DriftAlert: 0.3}}
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMLModel(ctx, "tenant-a", "churn")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Monitoring, m.Monitoring) {
		t.Errorf("monitoring = %+v, want %+v", got.Monitoring, m.Monitoring)
	}
}

// The prediction log contract of internal/storage/sql/ml_models_test.go.
func TestMongoMLPredictionLogs(t *testing.T) {
	s, _ := newTraceMongo(t)
	st, ok := s.(storage.MLPredictionLogStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.MLPredictionLogStore")
	}
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := func(vhost, model string, at time.Time, n float64) storage.MLPredictionLog {
		return storage.MLPredictionLog{VHost: vhost, Model: model, Version: "1", Timestamp: at,
			Inputs: map[string]any{"n": n}, Outputs: map[string]any{"score": n / 10}, LatencyMs: 3,
			CallerKind: storage.MLCallerREST}
	}
	if err := st.InsertMLPredictionLogs(ctx, []storage.MLPredictionLog{
		row("a", "fraud", now.Add(-48*time.Hour), 1), row("a", "fraud", now, 2), row("a", "fraud", now.Add(-time.Minute), 3),
		row("b", "fraud", now, 4),
	}); err != nil {
		t.Fatalf("InsertMLPredictionLogs: %v", err)
	}
	got, err := st.ListMLPredictionLogs(ctx, "a", "fraud", 2)
	if err != nil {
		t.Fatalf("ListMLPredictionLogs: %v", err)
	}
	if len(got) != 2 || got[0].Inputs["n"] != 2.0 || got[1].Inputs["n"] != 3.0 || got[0].Outputs["score"] != 0.2 ||
		got[0].CallerKind != storage.MLCallerREST || !got[0].Timestamp.Equal(now) {
		t.Fatalf("got %+v", got)
	}
	if err := st.PurgeMLPredictionLogs(ctx, "a", "fraud", now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ListMLPredictionLogs(ctx, "a", "fraud", 0); len(got) != 2 {
		t.Errorf("after the purge a/fraud holds %d rows, want 2", len(got))
	}
	if err := st.DeleteMLPredictionLogs(ctx, "a", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ListMLPredictionLogs(ctx, "a", "fraud", 0); len(got) != 0 {
		t.Errorf("vhost a still holds %d rows", len(got))
	}
	if got, _ := st.ListMLPredictionLogs(ctx, "b", "fraud", 0); len(got) != 1 {
		t.Errorf("deleting vhost a touched vhost b: %+v", got)
	}
}
