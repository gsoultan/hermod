// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package sql

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

func mlJobStore(t *testing.T) (storage.MLJobStore, *sqlStorage) {
	t.Helper()
	withKey(t, "ml-job-test-key")
	s := newRotationStorage(t)
	var st storage.Storage = s
	js, ok := st.(storage.MLJobStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLJobStore")
	}
	return js, s
}

func newJob(id, vhost, model, kind string, created time.Time) storage.MLJob {
	return storage.MLJob{
		ID: id, VHost: vhost, Model: model, Kind: kind, Status: storage.MLJobQueued,
		Request: json.RawMessage(`{"instances":[{"x":1}]}`), CallerKind: storage.MLCallerREST,
		CreatedAt: created, ExpiresAt: created.Add(7 * 24 * time.Hour),
	}
}

func TestMLJobsAreCreatedReadAndListed(t *testing.T) {
	st, _ := mlJobStore(t)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	a := newJob("job_a", "tenant-a", "churn", storage.MLJobPredict, t0)
	a.CallbackURL, a.CreatedBy = "https://example.com/hook", "ada"
	for _, j := range []storage.MLJob{
		a,
		newJob("job_b", "tenant-a", "churn", storage.MLJobTrain, t0.Add(time.Minute)),
		newJob("job_c", "tenant-a", "recs", storage.MLJobRecommend, t0.Add(2*time.Minute)),
		newJob("job_d", "tenant-b", "churn", storage.MLJobPredict, t0.Add(3*time.Minute)),
	} {
		if err := st.CreateMLJob(ctx, j); err != nil {
			t.Fatalf("CreateMLJob %s: %v", j.ID, err)
		}
	}

	got, err := st.GetMLJob(ctx, "tenant-a", "job_a")
	if err != nil {
		t.Fatalf("GetMLJob: %v", err)
	}
	if got.Model != "churn" || got.Kind != storage.MLJobPredict || got.Status != storage.MLJobQueued ||
		string(got.Request) != `{"instances":[{"x":1}]}` || got.CallbackURL != a.CallbackURL || got.CreatedBy != "ada" ||
		got.CallerKind != storage.MLCallerREST || !got.CreatedAt.Equal(t0) || !got.ExpiresAt.Equal(a.ExpiresAt) {
		t.Errorf("GetMLJob = %+v", got)
	}
	if _, err := st.GetMLJob(ctx, "tenant-b", "job_a"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("another vhost read the job: %v", err)
	}

	list, total, err := st.ListMLJobs(ctx, storage.MLJobFilter{VHost: "tenant-a", Limit: 10})
	if err != nil {
		t.Fatalf("ListMLJobs: %v", err)
	}
	if total != 3 || len(list) != 3 || list[0].ID != "job_c" || list[2].ID != "job_a" {
		t.Fatalf("list = %d %+v, want job_c, job_b, job_a", total, list)
	}
	if list[2].Request != nil || list[2].Result != nil {
		t.Errorf("a listing carried the request or result: %+v", list[2])
	}

	byModel, total, _ := st.ListMLJobs(ctx, storage.MLJobFilter{VHost: "tenant-a", Model: "churn", Limit: 1})
	if total != 2 || len(byModel) != 1 || byModel[0].ID != "job_b" {
		t.Errorf("model filter, page 1 = %d %+v", total, byModel)
	}
	page2, _, _ := st.ListMLJobs(ctx, storage.MLJobFilter{VHost: "tenant-a", Model: "churn", Limit: 1, Offset: 1})
	if len(page2) != 1 || page2[0].ID != "job_a" {
		t.Errorf("model filter, page 2 = %+v", page2)
	}
	if none, total, _ := st.ListMLJobs(ctx, storage.MLJobFilter{VHost: "tenant-a", Status: storage.MLJobRunning, Limit: 10}); total != 0 || len(none) != 0 {
		t.Errorf("status filter = %d %+v", total, none)
	}
}

func TestMLJobClaimRunsOneJobOnceAndRecoversAnExpiredClaim(t *testing.T) {
	st, _ := mlJobStore(t)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	if err := st.CreateMLJob(ctx, newJob("job_1", "tenant-a", "churn", storage.MLJobPredict, t0)); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateMLJob(ctx, newJob("job_2", "tenant-a", "churn", storage.MLJobPredict, t0.Add(time.Second))); err != nil {
		t.Fatal(err)
	}

	now := t0.Add(time.Minute)
	first, ok, err := st.ClaimMLJob(ctx, "hermod-1", time.Minute, now)
	if err != nil || !ok || first.ID != "job_1" || first.Status != storage.MLJobRunning || first.Attempts != 1 ||
		first.StartedAt == nil || first.ClaimedBy != "hermod-1" {
		t.Fatalf("first claim = %+v, %v, %v; want job_1 running", first, ok, err)
	}
	second, ok, _ := st.ClaimMLJob(ctx, "hermod-2", time.Minute, now)
	if !ok || second.ID != "job_2" {
		t.Fatalf("second claim = %+v, %v; want job_2", second, ok)
	}
	if _, ok, _ := st.ClaimMLJob(ctx, "hermod-3", time.Minute, now); ok {
		t.Fatal("a third claim took a job two others hold")
	}

	// hermod-1 renews its claim and reports progress; hermod-2 goes quiet.
	cancel, err := st.UpdateMLJobProgress(ctx, "job_1", "hermod-1", 0.5, "500 of 1,000 rows", now.Add(3*time.Minute))
	if err != nil || cancel {
		t.Fatalf("UpdateMLJobProgress = %v, %v", cancel, err)
	}
	if _, err := st.UpdateMLJobProgress(ctx, "job_1", "hermod-2", 0.9, "", now.Add(3*time.Minute)); !errors.Is(err, storage.ErrMLJobClaimLost) {
		t.Errorf("another owner updated the job: %v", err)
	}

	// Two minutes on, hermod-2's claim has expired and job_2 is taken over;
	// job_1's renewed claim holds.
	later := now.Add(2 * time.Minute)
	taken, ok, err := st.ClaimMLJob(ctx, "hermod-3", time.Minute, later)
	if err != nil || !ok || taken.ID != "job_2" || taken.Attempts != 2 || taken.ClaimedBy != "hermod-3" {
		t.Fatalf("takeover = %+v, %v, %v; want job_2 on its second attempt", taken, ok, err)
	}
	if !taken.StartedAt.Equal(*second.StartedAt) {
		t.Errorf("a takeover moved started_at from %v to %v", second.StartedAt, taken.StartedAt)
	}
	if err := st.FinishMLJob(ctx, "job_2", "hermod-2", storage.MLJobFinish{Status: storage.MLJobSucceeded, At: later}); !errors.Is(err, storage.ErrMLJobClaimLost) {
		t.Errorf("the owner that lost the claim finished the job: %v", err)
	}

	got, _ := st.GetMLJob(ctx, "tenant-a", "job_1")
	if got.Progress != 0.5 || got.ProgressMessage != "500 of 1,000 rows" {
		t.Errorf("progress = %v %q", got.Progress, got.ProgressMessage)
	}
}

func TestMLJobFinishAndCallbackDelivery(t *testing.T) {
	st, _ := mlJobStore(t)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	j := newJob("job_cb", "tenant-a", "churn", storage.MLJobPredict, t0)
	j.CallbackURL = "https://example.com/hook"
	plain := newJob("job_plain", "tenant-a", "churn", storage.MLJobPredict, t0.Add(time.Second))
	for _, x := range []storage.MLJob{j, plain} {
		if err := st.CreateMLJob(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, ok, err := st.ClaimMLJob(ctx, "hermod-1", time.Minute, t0); !ok || err != nil {
			t.Fatalf("claim: %v %v", ok, err)
		}
	}
	if _, ok, _ := st.ClaimMLJobCallback(ctx, "hermod-1", time.Minute, t0); ok {
		t.Fatal("a running job's callback was claimed")
	}
	done := t0.Add(time.Minute)
	if err := st.FinishMLJob(ctx, "job_cb", "hermod-1", storage.MLJobFinish{
		Status: storage.MLJobSucceeded, Result: json.RawMessage(`{"predictions":[{"y":2}]}`), At: done}); err != nil {
		t.Fatalf("FinishMLJob: %v", err)
	}
	if err := st.FinishMLJob(ctx, "job_plain", "hermod-1", storage.MLJobFinish{Status: storage.MLJobFailed, Error: "boom", At: done}); err != nil {
		t.Fatalf("FinishMLJob: %v", err)
	}
	got, _ := st.GetMLJob(ctx, "tenant-a", "job_cb")
	if got.Status != storage.MLJobSucceeded || string(got.Result) != `{"predictions":[{"y":2}]}` || got.Progress != 1 ||
		got.FinishedAt == nil || !got.FinishedAt.Equal(done) || got.CallbackStatus != storage.MLCallbackPending || got.ClaimedBy != "" {
		t.Errorf("finished job = %+v", got)
	}
	if p, _ := st.GetMLJob(ctx, "tenant-a", "job_plain"); p.Status != storage.MLJobFailed || p.Error != "boom" || p.CallbackStatus != "" {
		t.Errorf("failed job without a callback = %+v", p)
	}

	cb, ok, err := st.ClaimMLJobCallback(ctx, "hermod-1", time.Minute, done)
	if err != nil || !ok || cb.ID != "job_cb" {
		t.Fatalf("ClaimMLJobCallback = %+v, %v, %v", cb, ok, err)
	}
	if _, ok, _ := st.ClaimMLJobCallback(ctx, "hermod-2", time.Minute, done); ok {
		t.Fatal("a callback another Hermod holds was claimed again")
	}
	retry := done.Add(30 * time.Second)
	if err := st.RecordMLJobCallback(ctx, "job_cb", "hermod-1", storage.MLCallbackPending, 1, retry, "status 503"); err != nil {
		t.Fatalf("RecordMLJobCallback: %v", err)
	}
	if _, ok, _ := st.ClaimMLJobCallback(ctx, "hermod-2", time.Minute, done.Add(time.Second)); ok {
		t.Fatal("a callback was retried before it was due")
	}
	cb, ok, _ = st.ClaimMLJobCallback(ctx, "hermod-2", time.Minute, retry)
	if !ok || cb.CallbackAttempts != 1 || cb.CallbackLastError != "status 503" {
		t.Fatalf("due retry = %+v, %v", cb, ok)
	}
	if err := st.RecordMLJobCallback(ctx, "job_cb", "hermod-2", storage.MLCallbackDelivered, 2, time.Time{}, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetMLJob(ctx, "tenant-a", "job_cb"); got.CallbackStatus != storage.MLCallbackDelivered || got.CallbackAttempts != 2 {
		t.Errorf("delivered = %+v", got)
	}
	if _, ok, _ := st.ClaimMLJobCallback(ctx, "hermod-2", time.Minute, retry.Add(time.Hour)); ok {
		t.Error("a delivered callback was claimed again")
	}
}

func TestMLJobCancel(t *testing.T) {
	st, _ := mlJobStore(t)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	running := newJob("job_run", "tenant-a", "churn", storage.MLJobTrain, t0)
	queued := newJob("job_q", "tenant-a", "churn", storage.MLJobPredict, t0.Add(time.Second))
	queued.CallbackURL = "https://example.com/hook"
	for _, x := range []storage.MLJob{running, queued} {
		if err := st.CreateMLJob(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, _ := st.ClaimMLJob(ctx, "hermod-1", time.Minute, t0); !ok {
		t.Fatal("claim")
	}

	q, err := st.CancelMLJob(ctx, "tenant-a", "job_q", t0.Add(time.Minute))
	if err != nil || q.Status != storage.MLJobCancelled || q.FinishedAt == nil || q.CallbackStatus != storage.MLCallbackPending {
		t.Fatalf("cancelling a queued job = %+v, %v", q, err)
	}
	if _, ok, _ := st.ClaimMLJob(ctx, "hermod-2", time.Minute, t0.Add(time.Minute)); ok {
		t.Error("a cancelled job was claimed")
	}

	r, err := st.CancelMLJob(ctx, "tenant-a", "job_run", t0.Add(time.Minute))
	if err != nil || r.Status != storage.MLJobRunning || !r.CancelRequested {
		t.Fatalf("cancelling a running job = %+v, %v; want it asked to stop", r, err)
	}
	cancel, err := st.UpdateMLJobProgress(ctx, "job_run", "hermod-1", 0.1, "", t0.Add(2*time.Minute))
	if err != nil || !cancel {
		t.Errorf("the runner was not told to cancel: %v, %v", cancel, err)
	}
	if err := st.FinishMLJob(ctx, "job_run", "hermod-1", storage.MLJobFinish{Status: storage.MLJobCancelled, At: t0.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	again, err := st.CancelMLJob(ctx, "tenant-a", "job_run", t0.Add(3*time.Minute))
	if err != nil || again.Status != storage.MLJobCancelled {
		t.Errorf("cancelling a finished job = %+v, %v", again, err)
	}
	if _, err := st.CancelMLJob(ctx, "tenant-b", "job_run", t0); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("another vhost cancelled the job: %v", err)
	}
}

func TestMLJobResultsAndRemoval(t *testing.T) {
	st, s := mlJobStore(t)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	old := newJob("job_old", "tenant-a", "churn", storage.MLJobPredict, t0)
	old.ExpiresAt = t0.Add(time.Hour)
	fresh := newJob("job_new", "tenant-a", "churn", storage.MLJobPredict, t0)
	other := newJob("job_other", "tenant-b", "churn", storage.MLJobPredict, t0)
	for _, x := range []storage.MLJob{old, fresh, other} {
		if err := st.CreateMLJob(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	big := json.RawMessage(`{"predictions":[` + `{"y":1}` + `]}`)
	if err := st.PutMLJobResult(ctx, "tenant-a", "job_old", big); err != nil {
		t.Fatalf("PutMLJobResult: %v", err)
	}
	if got, err := st.GetMLJobResult(ctx, "tenant-a", "job_old"); err != nil || string(got) != string(big) {
		t.Errorf("GetMLJobResult = %s, %v", got, err)
	}
	if _, err := st.GetMLJobResult(ctx, "tenant-b", "job_old"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("another vhost read the result: %v", err)
	}

	n, err := st.DeleteExpiredMLJobs(ctx, t0.Add(2*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpiredMLJobs = %d, %v; want 1", n, err)
	}
	if _, err := st.GetMLJob(ctx, "tenant-a", "job_old"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("the expired job is still there: %v", err)
	}
	if _, err := st.GetMLJobResult(ctx, "tenant-a", "job_old"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("the expired job's result is still there: %v", err)
	}

	// Deleting the vhost removes its jobs, and only its jobs.
	if err := s.CreateVHost(ctx, storage.VHost{ID: "vh-a", Name: "tenant-a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutMLJobResult(ctx, "tenant-a", "job_new", big); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVHost(ctx, "vh-a"); err != nil {
		t.Fatalf("DeleteVHost: %v", err)
	}
	if _, err := st.GetMLJob(ctx, "tenant-a", "job_new"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a deleted vhost's job survived: %v", err)
	}
	if _, err := st.GetMLJobResult(ctx, "tenant-a", "job_new"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a deleted vhost's result survived: %v", err)
	}
	if _, err := st.GetMLJob(ctx, "tenant-b", "job_other"); err != nil {
		t.Errorf("another vhost's job went with it: %v", err)
	}
}
