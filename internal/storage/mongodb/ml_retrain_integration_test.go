//go:build integration
// +build integration

package mongodb

import (
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// The same contract the SQL store keeps (internal/storage/sql/ml_retrain_test.go),
// against a live MongoDB.
func TestMongoMLRetrain(t *testing.T) {
	s, _ := newTraceMongo(t)
	st := s.(storage.MLModelStore)
	rs, ok := s.(storage.MLRetrainStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.MLRetrainStore")
	}
	ctx := t.Context()
	m := storage.MLModel{VHost: "tenant-a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"}
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	p := &storage.MLRetrainPolicy{Schedule: "@daily", NewRows: 10, Spec: worker.TrainSpec{Dataset: "d", Target: "y"}}
	if err := rs.SetMLModelRetrain(ctx, "tenant-a", "churn", p); err != nil {
		t.Fatalf("SetMLModelRetrain: %v", err)
	}
	if err := rs.SetMLModelRetrainStatus(ctx, "tenant-a", "churn", storage.MLRetrainStatus{Version: "2", DatasetRows: 7}); err != nil {
		t.Fatalf("SetMLModelRetrainStatus: %v", err)
	}
	m.RemoteVersion = "2"
	if err := st.PutMLModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetMLModel(ctx, "tenant-a", "churn")
	if got.Retrain == nil || got.Retrain.NewRows != 10 || got.RetrainStatus == nil || got.RetrainStatus.DatasetRows != 7 {
		t.Errorf("an edit lost the policy or status: %+v", got)
	}
	if list, _ := rs.ListRetrainingMLModels(ctx); len(list) != 1 {
		t.Errorf("retraining models = %+v", list)
	}
	if ok, _ := rs.ClaimMLModelTraining(ctx, "tenant-a", "churn", "one", time.Hour); !ok {
		t.Fatal("the first claim failed")
	}
	if ok, _ := rs.ClaimMLModelTraining(ctx, "tenant-a", "churn", "two", time.Hour); ok {
		t.Fatal("a second owner claimed it")
	}
	if err := rs.ReleaseMLModelTraining(ctx, "tenant-a", "churn", "one"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := rs.ClaimMLModelTraining(ctx, "tenant-a", "churn", "two", time.Hour); !ok {
		t.Error("a released claim could not be taken")
	}
	if _, err := rs.ClaimMLModelTraining(ctx, "tenant-a", "nope", "one", time.Hour); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("claiming a model that does not exist: %v", err)
	}
	if err := rs.SetMLModelRetrain(ctx, "tenant-a", "churn", nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := rs.ListRetrainingMLModels(ctx); len(list) != 0 {
		t.Errorf("a cleared policy is still listed: %+v", list)
	}
}
