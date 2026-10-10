package sql

import (
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

func retrainStore(t *testing.T) (storage.MLModelStore, storage.MLRetrainStore) {
	t.Helper()
	st := mlModelStore(t)
	rs, ok := st.(storage.MLRetrainStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLRetrainStore")
	}
	return st, rs
}

func trainedModel(vhost, name string) storage.MLModel {
	return storage.MLModel{VHost: vhost, Name: name, Backend: storage.MLBackendWorker, RemoteVersion: "1", UpdatedBy: "ada"}
}

func TestARetrainPolicyRoundTripsAndSurvivesAnEdit(t *testing.T) {
	st, rs := retrainStore(t)
	ctx := t.Context()
	if err := st.PutMLModel(ctx, trainedModel("tenant-a", "churn")); err != nil {
		t.Fatal(err)
	}
	minScore := 0.8
	policy := &storage.MLRetrainPolicy{
		Schedule: "0 3 * * *", NewRows: 500,
		Spec:      worker.TrainSpec{Dataset: "customers", Target: "churned", Features: []string{"age"}, Algorithm: "xgboost"},
		GoLive:    storage.MLGoLive{Mode: "if", Metric: "f1", Min: &minScore},
		UpdatedBy: "ada",
	}
	if err := rs.SetMLModelRetrain(ctx, "tenant-a", "churn", policy); err != nil {
		t.Fatalf("SetMLModelRetrain: %v", err)
	}
	status := storage.MLRetrainStatus{Trigger: "schedule", Version: "2", Live: true, Reason: "f1 0.9 is within bounds", DatasetRows: 1200}
	if err := rs.SetMLModelRetrainStatus(ctx, "tenant-a", "churn", status); err != nil {
		t.Fatalf("SetMLModelRetrainStatus: %v", err)
	}

	// An edit of the definition, as training a new version makes, keeps both.
	m := trainedModel("tenant-a", "churn")
	m.RemoteVersion = "2"
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMLModel(ctx, "tenant-a", "churn")
	if err != nil {
		t.Fatal(err)
	}
	if got.Retrain == nil || got.Retrain.Schedule != "0 3 * * *" || got.Retrain.NewRows != 500 ||
		got.Retrain.Spec.Target != "churned" || got.Retrain.Spec.Algorithm != "xgboost" ||
		got.Retrain.GoLive.Min == nil || *got.Retrain.GoLive.Min != 0.8 || got.Retrain.GoLive.Metric != "f1" {
		t.Errorf("policy after an edit = %+v", got.Retrain)
	}
	if got.RetrainStatus == nil || got.RetrainStatus.Version != "2" || !got.RetrainStatus.Live || got.RetrainStatus.DatasetRows != 1200 {
		t.Errorf("status after an edit = %+v", got.RetrainStatus)
	}
	list, _ := st.ListMLModels(ctx, "tenant-a")
	if len(list) != 1 || list[0].Retrain == nil || list[0].RetrainStatus == nil {
		t.Errorf("the list does not show the policy and status: %+v", list)
	}

	if err := rs.SetMLModelRetrain(ctx, "tenant-a", "churn", nil); err != nil {
		t.Fatalf("clearing the policy: %v", err)
	}
	if got, _ := st.GetMLModel(ctx, "tenant-a", "churn"); got.Retrain != nil {
		t.Errorf("the policy stayed after it was cleared: %+v", got.Retrain)
	}
	if err := rs.SetMLModelRetrain(ctx, "tenant-a", "nope", policy); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a policy for a model that does not exist: err = %v", err)
	}
	if err := rs.SetMLModelRetrainStatus(ctx, "tenant-b", "churn", status); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a status for another vhost's model: err = %v", err)
	}
}

func TestRetrainingModelsAreListedAcrossVHosts(t *testing.T) {
	st, rs := retrainStore(t)
	ctx := t.Context()
	for _, m := range []storage.MLModel{trainedModel("a", "churn"), trainedModel("b", "fraud"), trainedModel("b", "idle")} {
		if err := st.PutMLModel(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	p := &storage.MLRetrainPolicy{Schedule: "@daily", Spec: worker.TrainSpec{Dataset: "d", Target: "y"}}
	for _, k := range [][2]string{{"a", "churn"}, {"b", "fraud"}} {
		if err := rs.SetMLModelRetrain(ctx, k[0], k[1], p); err != nil {
			t.Fatal(err)
		}
	}
	list, err := rs.ListRetrainingMLModels(ctx)
	if err != nil {
		t.Fatalf("ListRetrainingMLModels: %v", err)
	}
	got := map[string]bool{}
	for _, m := range list {
		got[m.VHost+"/"+m.Name] = m.Retrain != nil
	}
	if len(got) != 2 || !got["a/churn"] || !got["b/fraud"] {
		t.Errorf("listed %v, want a/churn and b/fraud with their policies", got)
	}
}

// One training of a model at a time, across every Hermod sharing the store.
func TestATrainingClaimIsHeldByOneOwnerUntilReleasedOrExpired(t *testing.T) {
	st, rs := retrainStore(t)
	ctx := t.Context()
	if err := st.PutMLModel(ctx, trainedModel("a", "churn")); err != nil {
		t.Fatal(err)
	}
	if ok, err := rs.ClaimMLModelTraining(ctx, "a", "churn", "replica-1", time.Hour); err != nil || !ok {
		t.Fatalf("the first claim: ok=%v err=%v", ok, err)
	}
	if ok, _ := rs.ClaimMLModelTraining(ctx, "a", "churn", "replica-2", time.Hour); ok {
		t.Fatal("a second replica claimed a model another one is training")
	}
	if ok, _ := rs.ClaimMLModelTraining(ctx, "a", "churn", "replica-1", time.Hour); !ok {
		t.Error("the owner could not renew its own claim")
	}
	if err := rs.ReleaseMLModelTraining(ctx, "a", "churn", "replica-2"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := rs.ClaimMLModelTraining(ctx, "a", "churn", "replica-2", time.Hour); ok {
		t.Fatal("a release by someone who does not hold the claim freed it")
	}
	if err := rs.ReleaseMLModelTraining(ctx, "a", "churn", "replica-1"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := rs.ClaimMLModelTraining(ctx, "a", "churn", "replica-2", -time.Second); !ok {
		t.Fatal("a released claim could not be taken")
	}
	// replica-2's claim above expired as it was made: a crashed replica does
	// not hold a model forever.
	if ok, _ := rs.ClaimMLModelTraining(ctx, "a", "churn", "replica-3", time.Hour); !ok {
		t.Error("an expired claim was not taken over")
	}
	if _, err := rs.ClaimMLModelTraining(ctx, "a", "nope", "replica-1", time.Hour); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("claiming a model that does not exist: err = %v", err)
	}
}

// A database the previous release made has ml_models without the retrain
// columns; start-up adds them and its models keep working.
func TestMLModelsOfThePreviousReleaseGainTheRetrainColumns(t *testing.T) {
	ctx := t.Context()
	db := newMigrationDB(t)
	if _, err := db.ExecContext(ctx, `CREATE TABLE ml_models (id TEXT PRIMARY KEY, vhost TEXT, name TEXT, spec TEXT,
		serving_key_hash TEXT, updated_by TEXT, created_at TIMESTAMP, updated_at TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO ml_models (id, vhost, name, spec, serving_key_hash, updated_by)
		VALUES ('a/churn', 'a', 'churn', '{"backend":"hermod-ml","url":"","remote_version":"3"}', 'h', 'ada')`); err != nil {
		t.Fatal(err)
	}
	s := NewSQLStorage(db, "sqlite").(*sqlStorage)
	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init over the previous release's table: %v", err)
	}
	m, err := s.GetMLModel(ctx, "a", "churn")
	if err != nil {
		t.Fatalf("reading a model of the previous release: %v", err)
	}
	if m.RemoteVersion != "3" || !m.Serving || m.Retrain != nil || m.RetrainStatus != nil {
		t.Errorf("model = %+v", m)
	}
	if err := s.SetMLModelRetrain(ctx, "a", "churn", &storage.MLRetrainPolicy{Schedule: "@daily"}); err != nil {
		t.Fatalf("SetMLModelRetrain on a migrated table: %v", err)
	}
	if ok, err := s.ClaimMLModelTraining(ctx, "a", "churn", "me", time.Minute); err != nil || !ok {
		t.Errorf("claim on a migrated table: ok=%v err=%v", ok, err)
	}
}
