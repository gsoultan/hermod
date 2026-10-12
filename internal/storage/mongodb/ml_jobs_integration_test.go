//go:build integration
// +build integration

package mongodb

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// The same contract the SQL store keeps (internal/storage/sql/ml_jobs_test.go),
// against a live MongoDB.
func TestMongoMLJobs(t *testing.T) {
	s, _ := newTraceMongo(t)
	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	st, ok := s.(storage.MLJobStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.MLJobStore")
	}
	ctx := t.Context()
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	job := func(id string, at time.Time) storage.MLJob {
		return storage.MLJob{ID: id, VHost: "tenant-a", Model: "churn", Kind: storage.MLJobPredict, Status: storage.MLJobQueued,
			Request: json.RawMessage(`{"instances":[{"x":1}]}`), CallerKind: storage.MLCallerREST,
			CallbackURL: "https://example.com/hook", CreatedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour)}
	}
	for _, j := range []storage.MLJob{job("job_1", t0), job("job_2", t0.Add(time.Second))} {
		if err := st.CreateMLJob(ctx, j); err != nil {
			t.Fatalf("CreateMLJob: %v", err)
		}
	}
	if list, total, err := st.ListMLJobs(ctx, storage.MLJobFilter{VHost: "tenant-a", Limit: 10}); err != nil ||
		total != 2 || list[0].ID != "job_2" || list[0].Request != nil {
		t.Fatalf("ListMLJobs = %d %+v, %v", total, list, err)
	}

	first, ok, err := st.ClaimMLJob(ctx, "hermod-1", time.Minute, t0)
	if err != nil || !ok || first.ID != "job_1" || first.Attempts != 1 || first.StartedAt == nil {
		t.Fatalf("claim = %+v, %v, %v", first, ok, err)
	}
	// hermod-1 stops; its claim expires and another Hermod takes the job over.
	taken, ok, _ := st.ClaimMLJob(ctx, "hermod-2", time.Minute, t0.Add(2*time.Minute))
	if !ok || taken.ID != "job_2" {
		t.Fatalf("second claim = %+v, %v", taken, ok)
	}
	taken, ok, _ = st.ClaimMLJob(ctx, "hermod-2", time.Minute, t0.Add(2*time.Minute))
	if !ok || taken.ID != "job_1" || taken.Attempts != 2 || !taken.StartedAt.Equal(*first.StartedAt) {
		t.Fatalf("takeover = %+v, %v", taken, ok)
	}
	if _, err := st.UpdateMLJobProgress(ctx, "job_1", "hermod-1", 0.5, "", t0); !errors.Is(err, storage.ErrMLJobClaimLost) {
		t.Errorf("the owner that lost the claim updated the job: %v", err)
	}
	if r, err := st.CancelMLJob(ctx, "tenant-a", "job_1", t0); err != nil || !r.CancelRequested {
		t.Errorf("CancelMLJob = %+v, %v", r, err)
	}
	if cancel, err := st.UpdateMLJobProgress(ctx, "job_1", "hermod-2", 0.5, "half", t0.Add(3*time.Minute)); err != nil || !cancel {
		t.Errorf("UpdateMLJobProgress = %v, %v", cancel, err)
	}
	done := t0.Add(3 * time.Minute)
	if err := st.FinishMLJob(ctx, "job_1", "hermod-2", storage.MLJobFinish{Status: storage.MLJobCancelled, At: done}); err != nil {
		t.Fatalf("FinishMLJob: %v", err)
	}
	cb, ok, err := st.ClaimMLJobCallback(ctx, "hermod-2", time.Minute, done)
	if err != nil || !ok || cb.ID != "job_1" {
		t.Fatalf("ClaimMLJobCallback = %+v, %v, %v", cb, ok, err)
	}
	if err := st.RecordMLJobCallback(ctx, "job_1", "hermod-2", storage.MLCallbackDelivered, 1, time.Time{}, ""); err != nil {
		t.Fatal(err)
	}

	if err := st.PutMLJobResult(ctx, "tenant-a", "job_2", json.RawMessage(`{"predictions":[]}`)); err != nil {
		t.Fatal(err)
	}
	if got, err := st.GetMLJobResult(ctx, "tenant-a", "job_2"); err != nil || string(got) != `{"predictions":[]}` {
		t.Errorf("GetMLJobResult = %s, %v", got, err)
	}
	if _, err := st.GetMLJobResult(ctx, "tenant-b", "job_2"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("another vhost read the result: %v", err)
	}
	if n, err := st.DeleteExpiredMLJobs(ctx, t0.Add(8*24*time.Hour)); err != nil || n != 2 {
		t.Errorf("DeleteExpiredMLJobs = %d, %v", n, err)
	}
	if _, err := st.GetMLJobResult(ctx, "tenant-a", "job_2"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("an expired result survived: %v", err)
	}
}
