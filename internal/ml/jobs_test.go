package ml

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod/internal/storage"
	sqlstore "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// jobsStore is a real SQLite store: jobs are claimed with the same SQL a
// deployment runs, so a takeover in these tests is one the database decided.
func jobsStore(t *testing.T) storage.Storage {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "jobs.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return st
}

// echoModel is an MLflow server that doubles x and counts its calls. hold,
// when set, keeps every call waiting until it is closed or the call ends.
type echoModel struct {
	calls atomic.Int32
	rows  atomic.Int32
	hold  chan struct{}
}

func (e *echoModel) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The body is read before holding: the server notices a client that
		// went away only once it has.
		var body struct {
			Records []map[string]float64 `json:"dataframe_records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.calls.Add(1)
		if e.hold != nil {
			select {
			case <-e.hold:
			case <-r.Context().Done():
				return
			}
		}
		e.rows.Add(int32(len(body.Records)))
		preds := make([]float64, len(body.Records))
		for i, rec := range body.Records {
			preds[i] = rec["x"] * 2
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"predictions": preds})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// jobService is a service over a SQLite store holding model "double" of
// vhost "a", served by m, with callbacks signed with "cb-secret".
func jobService(t *testing.T, m *echoModel) (*Service, storage.Storage) {
	t.Helper()
	st := jobsStore(t)
	srv := m.server(t)
	if err := st.(storage.MLModelStore).PutMLModel(t.Context(), storage.MLModel{
		VHost: "a", Name: "double", Backend: inference.BackendMLflow, URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(func() any { return st }, secretsOf{"a/" + CallbackSecretName: "cb-secret"}, nil).WithWorker(nil)
	svc.checkCallback = func(string) error { return nil }
	return svc, st
}

// testRunner is a runner over svc whose clock is now, with short timings.
func testRunner(svc *Service, owner string, now func() time.Time) *JobRunner {
	r := NewJobRunner(func() *Service { return svc }, nil)
	r.owner, r.now = owner, now
	r.heartbeat, r.poll, r.claimTTL = 10*time.Millisecond, 10*time.Millisecond, time.Minute
	r.client = http.DefaultClient
	return r
}

func rowsOf(n int) []inference.Row {
	rows := make([]inference.Row, n)
	for i := range rows {
		rows[i] = inference.Row{"x": float64(i)}
	}
	return rows
}

func TestSubmitJobRefusesWhatCouldNeverRun(t *testing.T) {
	svc, _ := jobService(t, &echoModel{})
	svc.checkCallback = CheckCallbackURL
	ctx := t.Context()
	cases := []struct {
		name string
		sub  JobSubmission
		want error
	}{
		{"unknown kind", JobSubmission{VHost: "a", Model: "double", Kind: "dance", Rows: rowsOf(1)}, ErrBadJob},
		{"unknown model", JobSubmission{VHost: "a", Model: "nope", Kind: storage.MLJobPredict, Rows: rowsOf(1)}, ErrModelNotFound},
		{"another vhost's model", JobSubmission{VHost: "b", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1)}, ErrModelNotFound},
		{"no rows", JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict}, ErrBadJob},
		{"too many rows", JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(MaxRowsPerJob + 1)}, ErrTooManyRows},
		{"recommend without a request", JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobRecommend}, ErrBadJob},
		{"train without a worker", JobSubmission{VHost: "a", Model: "churn", Kind: storage.MLJobTrain,
			Train: &TrainJob{Spec: worker.TrainSpec{Dataset: "d", Target: "y"}}}, ErrNoWorker},
		{"callback to a private address", JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1),
			CallbackURL: "http://10.0.0.5/hook"}, ErrBadJob},
		{"callback to localhost", JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1),
			CallbackURL: "https://localhost/hook"}, ErrBadJob},
		{"callback that is not http", JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1),
			CallbackURL: "file:///etc/passwd"}, ErrBadJob},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.SubmitJob(ctx, c.sub); !errors.Is(err, c.want) {
				t.Errorf("SubmitJob = %v, want %v", err, c.want)
			}
		})
	}

	t.Run("callback without the vhost's signing secret", func(t *testing.T) {
		bare := NewService(svc.store, secretsOf{}, nil)
		_, err := bare.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1),
			CallbackURL: "https://hooks.example.com/done"})
		if !errors.Is(err, ErrBadJob) || !strings.Contains(err.Error(), CallbackSecretName) {
			t.Errorf("SubmitJob = %v, want a refusal naming %s", err, CallbackSecretName)
		}
	})
}

func TestAPredictJobRunsInChunksAndKeepsItsAnswer(t *testing.T) {
	m := &echoModel{}
	svc, _ := jobService(t, m)
	ctx := t.Context()
	j, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(5000),
		CallerKind: storage.MLCallerREST})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if j.Status != storage.MLJobQueued || !strings.HasPrefix(j.ID, "job_") || j.ExpiresAt.Sub(j.CreatedAt) != JobRetention {
		t.Fatalf("submitted job = %+v", j)
	}

	testRunner(svc, "hermod-1", time.Now).Tick(ctx)

	got, err := svc.Job(ctx, "a", j.ID)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	if got.Status != storage.MLJobSucceeded || got.Progress != 1 || got.Attempts != 1 || got.FinishedAt == nil {
		t.Fatalf("job = %+v", got)
	}
	if n := m.calls.Load(); n != 5 {
		t.Errorf("the model was called %d times, want 5 calls of at most %d rows", n, MaxRowsPerCall)
	}
	raw, err := svc.JobResult(ctx, "a", j.ID)
	if err != nil {
		t.Fatalf("JobResult: %v", err)
	}
	var res struct {
		Model       string          `json:"model"`
		Predictions []inference.Row `json:"predictions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Model != "double" || len(res.Predictions) != 5000 || res.Predictions[4999]["prediction"] != 9998.0 {
		t.Errorf("result = %s… (%d predictions)", raw[:min(len(raw), 80)], len(res.Predictions))
	}
	if got.ResultRef != storage.MLJobResultStored || got.Result != nil {
		t.Errorf("a %d-byte result was kept on the job, not apart: ref %q", len(raw), got.ResultRef)
	}
}

func TestAPredictJobPacesItselfWithinTheRateQuota(t *testing.T) {
	m := &echoModel{}
	svc, st := jobService(t, m)
	ctx := t.Context()
	rate := 40.0
	if err := st.(storage.MLQuotaStore).PutMLQuotas(ctx, storage.MLQuotas{VHost: "a-paced", MaxPredictionsPerSecond: &rate}); err != nil {
		t.Fatal(err)
	}
	if err := st.(storage.MLModelStore).PutMLModel(ctx, storage.MLModel{VHost: "a-paced", Name: "double",
		Backend: inference.BackendMLflow, URL: mustModelURL(t, svc)}); err != nil {
		t.Fatal(err)
	}
	j, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a-paced", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(100)})
	if err != nil {
		t.Fatal(err)
	}
	testRunner(svc, "hermod-1", time.Now).Tick(ctx)
	got, _ := svc.Job(ctx, "a-paced", j.ID)
	if got.Status != storage.MLJobSucceeded {
		t.Fatalf("a job over one second's quota failed instead of waiting its turn: %+v", got)
	}
	if n := m.calls.Load(); n < 3 {
		t.Errorf("the model was called %d times; at 40 rows a second 100 rows take at least 3 calls", n)
	}
}

// mustModelURL is the URL of model "double" of vhost "a".
func mustModelURL(t *testing.T, svc *Service) string {
	t.Helper()
	m, err := svc.Model(t.Context(), "a", "double")
	if err != nil {
		t.Fatal(err)
	}
	return m.URL
}

func TestATrainingJobTrainsAndRegistersTheModel(t *testing.T) {
	st := jobsStore(t)
	f := &fakeWorker{score: 0.9, versions: map[string][]worker.Version{}, rows: map[string]int{}}
	srv := f.server(t)
	svc := NewService(func() any { return st }, nil, nil).WithWorker(worker.New(srv.URL, "t", nil))
	ctx := t.Context()

	j, err := svc.SubmitJob(ctx, JobSubmission{VHost: "tenant-a", Model: "churn", Kind: storage.MLJobTrain, CreatedBy: "ada",
		Train: &TrainJob{Spec: worker.TrainSpec{Dataset: "customers", Target: "churned"}, GoLive: GoLive{Mode: GoLiveAlways}}})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	testRunner(svc, "hermod-1", time.Now).Tick(ctx)

	got, _ := svc.Job(ctx, "tenant-a", j.ID)
	if got.Status != storage.MLJobSucceeded {
		t.Fatalf("job = %+v", got)
	}
	var res TrainResult
	if err := json.Unmarshal(got.Result, &res); err != nil || res.Version.Version != "1" || !res.Live {
		t.Errorf("result = %s, %v", got.Result, err)
	}
	m, err := svc.Model(ctx, "tenant-a", "churn")
	if err != nil || m.RemoteVersion != "1" || m.UpdatedBy != "ada" {
		t.Errorf("model = %+v, %v; want version 1 live, trained by ada", m, err)
	}
}

func TestAFailedJobSaysWhyInWords(t *testing.T) {
	st := jobsStore(t)
	f := &fakeWorker{score: 0.9, versions: map[string][]worker.Version{}, rows: map[string]int{}}
	srv := f.server(t)
	svc := NewService(func() any { return st }, nil, nil).WithWorker(worker.New(srv.URL, "t", nil))
	ctx := t.Context()
	// The model is served elsewhere, so training under its name is refused.
	if err := st.(storage.MLModelStore).PutMLModel(ctx, storage.MLModel{VHost: "tenant-a", Name: "churn",
		Backend: inference.BackendMLflow, URL: "http://example.com"}); err != nil {
		t.Fatal(err)
	}
	j, err := svc.SubmitJob(ctx, JobSubmission{VHost: "tenant-a", Model: "churn", Kind: storage.MLJobTrain,
		Train: &TrainJob{Spec: worker.TrainSpec{Dataset: "customers", Target: "churned"}}})
	if err != nil {
		t.Fatal(err)
	}
	testRunner(svc, "hermod-1", time.Now).Tick(ctx)
	got, _ := svc.Job(ctx, "tenant-a", j.ID)
	if got.Status != storage.MLJobFailed || !strings.Contains(got.Error, "served elsewhere") {
		t.Errorf("job = %+v, want failed with the reason", got)
	}
}

func TestCancellingAJob(t *testing.T) {
	m := &echoModel{hold: make(chan struct{})}
	svc, _ := jobService(t, m)
	ctx := t.Context()

	queued, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1)})
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CancelJob(ctx, "a", queued.ID)
	if err != nil || c.Status != storage.MLJobCancelled {
		t.Fatalf("cancelling a queued job = %+v, %v", c, err)
	}

	running, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1)})
	if err != nil {
		t.Fatal(err)
	}
	r := testRunner(svc, "hermod-1", time.Now)
	done := make(chan struct{})
	go func() { r.Tick(ctx); close(done) }()
	waitFor(t, "the model to be called", func() bool { return m.calls.Load() == 1 })

	if c, err := svc.CancelJob(ctx, "a", running.ID); err != nil || !c.CancelRequested {
		t.Fatalf("cancelling a running job = %+v, %v", c, err)
	}
	<-done
	got, _ := svc.Job(ctx, "a", running.ID)
	if got.Status != storage.MLJobCancelled {
		t.Errorf("job = %+v, want cancelled", got)
	}
	if _, err := svc.CancelJob(ctx, "b", running.ID); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("another vhost cancelled the job: %v", err)
	}
}

// TestAJobSurvivesARunnerRestart is the crash a claim exists for: the Hermod
// running a job stops without finishing it, its claim expires, and another
// runner takes the job over from the store and finishes it.
func TestAJobSurvivesARunnerRestart(t *testing.T) {
	m := &echoModel{hold: make(chan struct{})}
	svc, _ := jobService(t, m)
	ctx := t.Context()
	j, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(3)})
	if err != nil {
		t.Fatal(err)
	}

	// The first runner claims the job and is stopped mid-call.
	first := testRunner(svc, "hermod-1", time.Now)
	firstCtx, crash := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { first.Run(firstCtx); close(stopped) }()
	waitFor(t, "the first runner to call the model", func() bool { return m.calls.Load() == 1 })
	crash()
	<-stopped

	held, _ := svc.Job(ctx, "a", j.ID)
	if held.Status != storage.MLJobRunning || held.Attempts != 1 {
		t.Fatalf("after the crash the job is %+v; want it still running under the lost claim", held)
	}

	// A runner whose clock is before the claim expires leaves it alone.
	close(m.hold)
	testRunner(svc, "hermod-2", time.Now).Tick(ctx)
	if got, _ := svc.Job(ctx, "a", j.ID); got.Status != storage.MLJobRunning {
		t.Fatalf("a live claim was taken over: %+v", got)
	}

	// Once it has expired, the next runner takes the job over and finishes it.
	later := func() time.Time { return time.Now().Add(2 * time.Minute) }
	testRunner(svc, "hermod-2", later).Tick(ctx)
	got, _ := svc.Job(ctx, "a", j.ID)
	if got.Status != storage.MLJobSucceeded || got.Attempts != 2 {
		t.Fatalf("after the takeover the job is %+v; want succeeded on its second attempt", got)
	}
	var res struct {
		Predictions []inference.Row `json:"predictions"`
	}
	if err := json.Unmarshal(got.Result, &res); err != nil || len(res.Predictions) != 3 {
		t.Errorf("result = %s, %v", got.Result, err)
	}
}

func TestAJobInterruptedTooOftenIsGivenUp(t *testing.T) {
	m := &echoModel{}
	svc, st := jobService(t, m)
	ctx := t.Context()
	j, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1)})
	if err != nil {
		t.Fatal(err)
	}
	js := st.(storage.MLJobStore)
	at := time.Now()
	for range maxJobAttempts {
		if _, ok, err := js.ClaimMLJob(ctx, "gone", time.Minute, at); !ok || err != nil {
			t.Fatalf("claim: %v %v", ok, err)
		}
		at = at.Add(2 * time.Minute)
	}
	testRunner(svc, "hermod-2", func() time.Time { return at }).Tick(ctx)
	got, _ := svc.Job(ctx, "a", j.ID)
	if got.Status != storage.MLJobFailed || !strings.Contains(got.Error, "stopped") || m.calls.Load() != 0 {
		t.Errorf("job = %+v after %d model calls; want it failed without running again", got, m.calls.Load())
	}
}

func TestACallbackIsSignedAndRetried(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var headers []http.Header
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies, headers = append(bodies, b), append(headers, r.Header.Clone())
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(hook.Close)

	svc, _ := jobService(t, &echoModel{})
	ctx := t.Context()
	j, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(2),
		CallbackURL: hook.URL + "/done", CallerKind: storage.MLCallerREST})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	clock := func() time.Time { return now }
	r := testRunner(svc, "hermod-1", clock)
	r.Tick(ctx) // runs the job, then tries the callback once

	got, _ := svc.Job(ctx, "a", j.ID)
	if got.CallbackStatus != storage.MLCallbackPending || got.CallbackAttempts != 1 || !strings.Contains(got.CallbackLastError, "503") {
		t.Fatalf("after a refused delivery the job is %+v", got)
	}
	r.Tick(ctx)
	mu.Lock()
	if len(bodies) != 1 {
		t.Fatalf("the callback was retried before its backoff: %d deliveries", len(bodies))
	}
	mu.Unlock()

	now = now.Add(time.Hour)
	r.Tick(ctx)
	got, _ = svc.Job(ctx, "a", j.ID)
	if got.CallbackStatus != storage.MLCallbackDelivered || got.CallbackAttempts != 2 {
		t.Fatalf("after a delivery the job is %+v", got)
	}

	mu.Lock()
	defer mu.Unlock()
	body, h := bodies[1], headers[1]
	mac := hmac.New(sha256.New, []byte("cb-secret"))
	mac.Write([]byte(h.Get("X-Hermod-Timestamp") + "."))
	mac.Write(body)
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); h.Get("X-Hermod-Signature-256") != want {
		t.Errorf("signature = %q, want %q", h.Get("X-Hermod-Signature-256"), want)
	}
	if h.Get("X-Hermod-Job-Id") != j.ID || h.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", h)
	}
	var payload struct {
		JobID     string          `json:"job_id"`
		Status    string          `json:"status"`
		StatusURL string          `json:"status_url"`
		Result    json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.JobID != j.ID || payload.Status != storage.MLJobSucceeded ||
		payload.StatusURL != "/api/ml/serve/a/jobs/"+j.ID || !strings.Contains(string(payload.Result), "predictions") {
		t.Errorf("payload = %s", body)
	}
}

func TestACallbackThatNeverLandsGivesUp(t *testing.T) {
	var calls atomic.Int32
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(hook.Close)
	svc, _ := jobService(t, &echoModel{})
	ctx := t.Context()
	j, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1),
		CallbackURL: hook.URL})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r := testRunner(svc, "hermod-1", func() time.Time { return now })
	for range maxCallbackAttempts + 2 {
		r.Tick(ctx)
		now = now.Add(2 * time.Hour)
	}
	got, _ := svc.Job(ctx, "a", j.ID)
	if got.CallbackStatus != storage.MLCallbackFailed || int(calls.Load()) != maxCallbackAttempts {
		t.Errorf("job = %+v after %d deliveries; want failed after %d", got, calls.Load(), maxCallbackAttempts)
	}
}

func TestQueuedJobsAreBoundedPerVHost(t *testing.T) {
	svc, _ := jobService(t, &echoModel{})
	svc.maxQueuedJobs = 2
	ctx := t.Context()
	for range 2 {
		if _, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := svc.SubmitJob(ctx, JobSubmission{VHost: "a", Model: "double", Kind: storage.MLJobPredict, Rows: rowsOf(1)})
	var quota *QuotaError
	if !errors.As(err, &quota) || !quota.Retryable {
		t.Errorf("the third job = %v, want a retryable quota refusal", err)
	}
}

func TestSyncCallsHaveATimeLimitThatPointsAtAsync(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"predictions": []float64{1}})
	}))
	t.Cleanup(slow.Close)
	store := newMemStore(
		storage.MLModel{VHost: "a", Name: "slow", Backend: inference.BackendMLflow, URL: slow.URL},
		storage.MLModel{VHost: "a", Name: "patient", Backend: inference.BackendMLflow, URL: slow.URL, TimeoutMs: 5000},
	)
	svc := NewService(func() any { return store }, nil, nil).WithSyncTimeout(50 * time.Millisecond)
	ctx := t.Context()

	err := svc.Sync(ctx, "a", "slow", func(ctx context.Context) error {
		_, err := svc.Predict(ctx, "a", "slow", rowsOf(1))
		return err
	})
	if !errors.Is(err, ErrSyncTimeout) || !strings.Contains(err.Error(), "async") {
		t.Errorf("a slow sync call = %v, want ErrSyncTimeout pointing at async", err)
	}

	// A model whose own timeout is longer keeps it.
	if err := svc.Sync(ctx, "a", "patient", func(ctx context.Context) error {
		_, err := svc.Predict(ctx, "a", "patient", rowsOf(1))
		return err
	}); err != nil {
		t.Errorf("a model with a longer timeout of its own = %v", err)
	}

	// A caller that went away is not told to use async.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if err := svc.Sync(gone, "a", "slow", func(ctx context.Context) error { return ctx.Err() }); errors.Is(err, ErrSyncTimeout) {
		t.Errorf("a cancelled caller was told it timed out: %v", err)
	}
}

func TestCheckCallbackURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://hooks.example.com/ml":                   true,
		"http://hooks.example.com:8080/":                 true,
		"ftp://hooks.example.com/":                       false,
		"https://localhost/":                             false,
		"https://127.0.0.1/":                             false,
		"https://[::1]/":                                 false,
		"https://169.254.169.254/":                       false,
		"https://192.168.1.2/":                           false,
		"https://0.0.0.0/":                               false,
		"https://user:pw@example.com/":                   false,
		"not a url":                                      false,
		"https://" + strings.Repeat("a", 2100) + ".com/": false,
	} {
		if err := CheckCallbackURL(raw); (err == nil) != ok {
			t.Errorf("CheckCallbackURL(%.40q) = %v, want ok=%v", raw, err, ok)
		}
	}
}
