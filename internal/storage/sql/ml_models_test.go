package sql

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
)

func mlModelStore(t *testing.T) storage.MLModelStore {
	t.Helper()
	withKey(t, "ml-model-test-key")
	var st storage.Storage = newRotationStorage(t)
	ms, ok := st.(storage.MLModelStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLModelStore")
	}
	return ms
}

func churnModel(vhost string) storage.MLModel {
	return storage.MLModel{
		VHost: vhost, Name: "churn", Description: "who leaves",
		Backend: inference.BackendOIP, URL: "http://ml:8080", RemoteModel: "churn", RemoteVersion: "2",
		TokenSecret: "ML_TOKEN", InputName: "input", Features: []string{"age", "tenure"}, TimeoutMs: 1500,
		UpdatedBy: "ada",
	}
}

func TestMLModelRoundTripsAndStaysInItsVHost(t *testing.T) {
	st := mlModelStore(t)
	ctx := t.Context()
	if err := st.PutMLModel(ctx, churnModel("tenant-a")); err != nil {
		t.Fatalf("PutMLModel: %v", err)
	}

	got, err := st.GetMLModel(ctx, "tenant-a", "churn")
	if err != nil {
		t.Fatalf("GetMLModel: %v", err)
	}
	want := churnModel("tenant-a")
	if got.URL != want.URL || got.RemoteVersion != "2" || got.TokenSecret != "ML_TOKEN" ||
		got.InputName != "input" || len(got.Features) != 2 || got.Features[1] != "tenure" ||
		got.TimeoutMs != 1500 || got.Description != "who leaves" || got.UpdatedBy != "ada" {
		t.Errorf("round trip lost fields: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("no timestamps: %+v", got)
	}
	if got.Serving || got.ServingKeyHash != "" {
		t.Errorf("a new model is served before a key was made: %+v", got)
	}

	if _, err := st.GetMLModel(ctx, "tenant-b", "churn"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("another vhost read tenant-a's model: err = %v", err)
	}
}

func TestPutMLModelReplacesTheDefinitionButKeepsTheServingKey(t *testing.T) {
	st := mlModelStore(t)
	ctx := t.Context()
	m := churnModel("v")
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMLModelServingKey(ctx, "v", "churn", "hash-1"); err != nil {
		t.Fatalf("SetMLModelServingKey: %v", err)
	}
	first, _ := st.GetMLModel(ctx, "v", "churn")

	m.URL = "http://ml-2:8080"
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetMLModel(ctx, "v", "churn")
	if got.URL != "http://ml-2:8080" {
		t.Errorf("URL = %q, the update was lost", got.URL)
	}
	if got.ServingKeyHash != "hash-1" || !got.Serving {
		t.Errorf("an edit dropped the serving key: %+v", got)
	}
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("an edit moved created_at from %v to %v", first.CreatedAt, got.CreatedAt)
	}

	if err := st.SetMLModelServingKey(ctx, "v", "churn", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetMLModel(ctx, "v", "churn"); got.Serving {
		t.Error("serving stayed on after the key was cleared")
	}
	if err := st.SetMLModelServingKey(ctx, "v", "nope", "h"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a key for a model that does not exist: err = %v", err)
	}
}

func TestMLModelListIsOrderedAndScopedAndDeletesWork(t *testing.T) {
	st := mlModelStore(t)
	ctx := t.Context()
	for _, name := range []string{"zeta", "alpha"} {
		m := churnModel("a")
		m.Name = name
		if err := st.PutMLModel(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutMLModel(ctx, churnModel("b")); err != nil {
		t.Fatal(err)
	}

	list, err := st.ListMLModels(ctx, "a")
	if err != nil {
		t.Fatalf("ListMLModels: %v", err)
	}
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zeta" {
		t.Fatalf("list = %+v, want alpha then zeta and nothing of vhost b", list)
	}

	if err := st.DeleteMLModel(ctx, "a", "alpha"); err != nil {
		t.Fatalf("DeleteMLModel: %v", err)
	}
	if err := st.DeleteMLModel(ctx, "a", "alpha"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("deleting twice: err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteMLModels(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListMLModels(ctx, "a"); len(list) != 0 {
		t.Errorf("vhost a still has %d models", len(list))
	}
	if list, _ := st.ListMLModels(ctx, "b"); len(list) != 1 {
		t.Errorf("deleting vhost a's models touched vhost b: %+v", list)
	}
}

func TestPutMLModelRefusesAnInvalidModel(t *testing.T) {
	st := mlModelStore(t)
	m := churnModel("v")
	m.Name = "../etc"
	if err := st.PutMLModel(t.Context(), m); err == nil {
		t.Fatal("a model named ../etc was saved")
	}
}

func TestMLModelMonitoringRoundTrips(t *testing.T) {
	st := mlModelStore(t)
	ctx := t.Context()
	m := churnModel("v")
	m.Monitoring = storage.MLMonitoring{LogSampleRate: 0.25, LogMaskFields: []string{"email"}, LogMaskType: "email",
		LogRetention: "3d", DriftWarn: 0.15, DriftAlert: 0.3}
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMLModel(ctx, "v", "churn")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Monitoring, m.Monitoring) {
		t.Errorf("monitoring = %+v, want %+v", got.Monitoring, m.Monitoring)
	}
	list, _ := st.ListMLModels(ctx, "v")
	if len(list) != 1 || !reflect.DeepEqual(list[0].Monitoring, m.Monitoring) {
		t.Errorf("listed monitoring = %+v", list)
	}
}

func predictionLogStore(t *testing.T) storage.MLPredictionLogStore {
	t.Helper()
	withKey(t, "ml-model-test-key")
	var st storage.Storage = newRotationStorage(t)
	ls, ok := st.(storage.MLPredictionLogStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLPredictionLogStore")
	}
	return ls
}

func predictionAt(vhost, model string, at time.Time, n float64) storage.MLPredictionLog {
	return storage.MLPredictionLog{
		VHost: vhost, Model: model, Version: "3", Timestamp: at,
		Inputs: map[string]any{"amount": n, "email": "a****@x.io"}, Outputs: map[string]any{"score": n / 100},
		LatencyMs: 12.5, CallerKind: storage.MLCallerWorkflow, CallerID: "wf-1",
	}
}

func TestPredictionLogsListNewestFirstWithinTheirModel(t *testing.T) {
	st := predictionLogStore(t)
	ctx := t.Context()
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := st.InsertMLPredictionLogs(ctx, []storage.MLPredictionLog{
		predictionAt("a", "fraud", base, 1),
		predictionAt("a", "fraud", base.Add(2*time.Minute), 3),
		predictionAt("a", "fraud", base.Add(time.Minute), 2),
		predictionAt("a", "churn", base, 9),
		predictionAt("b", "fraud", base, 8),
	}); err != nil {
		t.Fatalf("InsertMLPredictionLogs: %v", err)
	}

	got, err := st.ListMLPredictionLogs(ctx, "a", "fraud", 2)
	if err != nil {
		t.Fatalf("ListMLPredictionLogs: %v", err)
	}
	if len(got) != 2 || got[0].Inputs["amount"] != 3.0 || got[1].Inputs["amount"] != 2.0 {
		t.Fatalf("got %+v, want the two newest of a/fraud", got)
	}
	first := got[0]
	if first.VHost != "a" || first.Model != "fraud" || first.Version != "3" || !first.Timestamp.Equal(base.Add(2*time.Minute)) ||
		first.Outputs["score"] != 0.03 || first.Inputs["email"] != "a****@x.io" || first.LatencyMs != 12.5 ||
		first.CallerKind != storage.MLCallerWorkflow || first.CallerID != "wf-1" {
		t.Errorf("row lost fields: %+v", first)
	}
	if all, _ := st.ListMLPredictionLogs(ctx, "a", "fraud", 0); len(all) != 3 {
		t.Errorf("an unbounded read returned %d rows, want 3", len(all))
	}
	if other, _ := st.ListMLPredictionLogs(ctx, "b", "fraud", 10); len(other) != 1 || other[0].Inputs["amount"] != 8.0 {
		t.Errorf("vhost b read %+v", other)
	}
}

func TestPredictionLogsArePurgedByAgeAndDeletedWithTheirModel(t *testing.T) {
	st := predictionLogStore(t)
	ctx := t.Context()
	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC().Add(-time.Hour)
	if err := st.InsertMLPredictionLogs(ctx, []storage.MLPredictionLog{
		predictionAt("a", "fraud", old, 1), predictionAt("a", "fraud", recent, 2),
		predictionAt("a", "churn", old, 3), predictionAt("b", "fraud", old, 4),
	}); err != nil {
		t.Fatal(err)
	}

	if err := st.PurgeMLPredictionLogs(ctx, "a", "fraud", time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("PurgeMLPredictionLogs: %v", err)
	}
	if got, _ := st.ListMLPredictionLogs(ctx, "a", "fraud", 10); len(got) != 1 || got[0].Inputs["amount"] != 2.0 {
		t.Errorf("after the purge a/fraud holds %+v, want only the recent row", got)
	}
	if got, _ := st.ListMLPredictionLogs(ctx, "a", "churn", 10); len(got) != 1 {
		t.Errorf("purging a/fraud touched a/churn: %+v", got)
	}

	if err := st.DeleteMLPredictionLogs(ctx, "a", "churn"); err != nil {
		t.Fatalf("DeleteMLPredictionLogs: %v", err)
	}
	if got, _ := st.ListMLPredictionLogs(ctx, "a", "churn", 10); len(got) != 0 {
		t.Errorf("a/churn still holds %d rows", len(got))
	}
	if err := st.DeleteMLPredictionLogs(ctx, "a", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ListMLPredictionLogs(ctx, "a", "fraud", 10); len(got) != 0 {
		t.Errorf("deleting vhost a's logs left %d rows", len(got))
	}
	if got, _ := st.ListMLPredictionLogs(ctx, "b", "fraud", 10); len(got) != 1 {
		t.Errorf("deleting vhost a's logs touched vhost b: %+v", got)
	}

	if err := st.PurgeMLPredictionLogs(ctx, "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ListMLPredictionLogs(ctx, "b", "fraud", 10); len(got) != 0 {
		t.Errorf("a purge of every vhost left %+v", got)
	}
}

// A deleted vhost must not leave its models for a later vhost of the same name.
func TestDeletingAVHostDeletesItsModels(t *testing.T) {
	withKey(t, "ml-model-test-key")
	s := newRotationStorage(t)
	if err := s.CreateVHost(t.Context(), storage.VHost{ID: "vh-1", Name: "tenant-a"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	for _, vhost := range []string{"tenant-a", "tenant-b"} {
		if err := s.PutMLModel(t.Context(), churnModel(vhost)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteVHost(t.Context(), "vh-1"); err != nil {
		t.Fatalf("DeleteVHost: %v", err)
	}
	if list, _ := s.ListMLModels(t.Context(), "tenant-a"); len(list) != 0 {
		t.Errorf("tenant-a was deleted and still holds %d models", len(list))
	}
	if list, _ := s.ListMLModels(t.Context(), "tenant-b"); len(list) != 1 {
		t.Errorf("deleting tenant-a left tenant-b with %d models, want 1", len(list))
	}
}

// The MCP flag and the feature types are part of the definition: they round
// trip, and a new model is not exposed until someone says so.
func TestMLModelKeepsItsMCPFlagAndFeatureTypes(t *testing.T) {
	st := mlModelStore(t)
	ctx := t.Context()
	m := churnModel("v")
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetMLModel(ctx, "v", "churn"); got.MCPExposed {
		t.Fatal("a new model is exposed to MCP before anyone said so")
	}

	m.MCPExposed = true
	m.FeatureTypes = map[string]string{"age": "number", "tenure": "number"}
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMLModel(ctx, "v", "churn")
	if err != nil {
		t.Fatal(err)
	}
	if !got.MCPExposed || got.FeatureTypes["age"] != "number" || len(got.FeatureTypes) != 2 {
		t.Errorf("round trip lost the MCP flag or the feature types: %+v", got)
	}
	list, _ := st.ListMLModels(ctx, "v")
	if len(list) != 1 || !list[0].MCPExposed {
		t.Errorf("the list does not carry the MCP flag: %+v", list)
	}
}

// Nor its logged predictions, which hold what its models were sent.
func TestDeletingAVHostDeletesItsPredictionLogs(t *testing.T) {
	withKey(t, "ml-model-test-key")
	s := newRotationStorage(t)
	if err := s.CreateVHost(t.Context(), storage.VHost{ID: "vh-1", Name: "tenant-a"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	now := time.Now().UTC()
	if err := s.InsertMLPredictionLogs(t.Context(), []storage.MLPredictionLog{
		predictionAt("tenant-a", "churn", now, 1), predictionAt("tenant-b", "churn", now, 2),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVHost(t.Context(), "vh-1"); err != nil {
		t.Fatalf("DeleteVHost: %v", err)
	}
	if got, _ := s.ListMLPredictionLogs(t.Context(), "tenant-a", "churn", 10); len(got) != 0 {
		t.Errorf("tenant-a was deleted and still has %d logged predictions", len(got))
	}
	if got, _ := s.ListMLPredictionLogs(t.Context(), "tenant-b", "churn", 10); len(got) != 1 {
		t.Errorf("deleting tenant-a left tenant-b with %d logged predictions, want 1", len(got))
	}
}
