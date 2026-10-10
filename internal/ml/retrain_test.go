package ml

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// memClaim is a training claim in memStore.
type memClaim struct {
	owner string
	until time.Time
}

// memStore keeps retrain policies the way the SQL and MongoDB stores do: apart
// from the definition, with one claim per model.
func (s *memStore) ListRetrainingMLModels(_ context.Context) ([]storage.MLModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.MLModel
	for _, m := range s.models {
		if m.Retrain != nil {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *memStore) SetMLModelRetrain(_ context.Context, vhost, name string, p *storage.MLRetrainPolicy) error {
	return s.edit(vhost, name, func(m *storage.MLModel) { m.Retrain = p })
}

func (s *memStore) SetMLModelRetrainStatus(_ context.Context, vhost, name string, st storage.MLRetrainStatus) error {
	return s.edit(vhost, name, func(m *storage.MLModel) { m.RetrainStatus = &st })
}

func (s *memStore) edit(vhost, name string, change func(*storage.MLModel)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.ErrNotFound
	}
	change(&m)
	s.models[vhost+"/"+name] = m
	return nil
}

func (s *memStore) ClaimMLModelTraining(_ context.Context, vhost, name, owner string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := vhost + "/" + name
	if _, ok := s.models[key]; !ok {
		return false, storage.ErrNotFound
	}
	if c, ok := s.claims[key]; ok && c.owner != owner && time.Now().Before(c.until) {
		return false, nil
	}
	s.claims[key] = memClaim{owner: owner, until: time.Now().Add(ttl)}
	return true, nil
}

func (s *memStore) ReleaseMLModelTraining(_ context.Context, vhost, name, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.claims[vhost+"/"+name]; ok && c.owner == owner {
		delete(s.claims, vhost+"/"+name)
	}
	return nil
}

// churn is the model these tests retrain, as the store holds it now.
func (s *memStore) churn(t *testing.T) storage.MLModel {
	t.Helper()
	m, err := s.GetMLModel(t.Context(), "a", "churn")
	if err != nil {
		t.Fatalf("model a/churn: %v", err)
	}
	return m
}

func f64(v float64) *float64 { return &v }

var churnSpec = worker.TrainSpec{Dataset: "customers", Target: "churned"}

func TestValidateRetrainPolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  storage.MLRetrainPolicy
		wantErr string
	}{
		{"a cron schedule", storage.MLRetrainPolicy{Schedule: "0 3 * * 1", Spec: churnSpec}, ""},
		{"a descriptor", storage.MLRetrainPolicy{Schedule: "@daily", Spec: churnSpec}, ""},
		{"new rows only", storage.MLRetrainPolicy{NewRows: 1000, Spec: churnSpec}, ""},
		{"both, with a rule", storage.MLRetrainPolicy{Schedule: "@weekly", NewRows: 10, Spec: churnSpec,
			GoLive: storage.MLGoLive{Mode: GoLiveIf, Min: f64(0.8)}}, ""},
		{"no trigger", storage.MLRetrainPolicy{Spec: churnSpec}, "schedule"},
		{"a bad schedule", storage.MLRetrainPolicy{Schedule: "every day", Spec: churnSpec}, "schedule"},
		{"seconds are not offered", storage.MLRetrainPolicy{Schedule: "0 0 3 * * *", Spec: churnSpec}, "schedule"},
		{"negative rows", storage.MLRetrainPolicy{NewRows: -1, Schedule: "@daily", Spec: churnSpec}, "rows"},
		{"no dataset", storage.MLRetrainPolicy{Schedule: "@daily", Spec: worker.TrainSpec{Target: "y"}}, "dataset"},
		{"no target", storage.MLRetrainPolicy{Schedule: "@daily", Spec: worker.TrainSpec{Dataset: "d"}}, "target"},
		{"a bad rule", storage.MLRetrainPolicy{Schedule: "@daily", Spec: churnSpec, GoLive: storage.MLGoLive{Mode: GoLiveIf}}, "minimum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRetrainPolicy(tt.policy)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want one about %q", err, tt.wantErr)
			}
		})
	}
}

func TestRetrainDue(t *testing.T) {
	set := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	daily3am := storage.MLRetrainPolicy{Schedule: "0 3 * * *", Spec: churnSpec, UpdatedAt: set}
	rows100 := storage.MLRetrainPolicy{NewRows: 100, Spec: churnSpec, UpdatedAt: set}
	tests := []struct {
		name   string
		policy storage.MLRetrainPolicy
		status *storage.MLRetrainStatus
		rows   int
		now    time.Time
		want   string
	}{
		{"before the first run", daily3am, nil, -1, set.Add(2 * time.Hour), ""},
		{"at the first run", daily3am, nil, -1, set.Add(3 * time.Hour), TriggerSchedule},
		{"a run that was missed runs once", daily3am, nil, -1, set.Add(50 * time.Hour), TriggerSchedule},
		{"after it ran", daily3am, &storage.MLRetrainStatus{At: set.Add(3*time.Hour + time.Second)}, -1, set.Add(4 * time.Hour), ""},
		{"the next day", daily3am, &storage.MLRetrainStatus{At: set.Add(3 * time.Hour)}, -1, set.Add(27 * time.Hour), TriggerSchedule},
		{"too few new rows", rows100, &storage.MLRetrainStatus{DatasetRows: 500}, 599, set.Add(time.Hour), ""},
		{"enough new rows", rows100, &storage.MLRetrainStatus{DatasetRows: 500}, 600, set.Add(time.Hour), TriggerNewRows},
		{"a refilled, smaller dataset counts as new", rows100, &storage.MLRetrainStatus{DatasetRows: 500}, 150, set.Add(time.Hour), TriggerNewRows},
		{"rows unknown", rows100, &storage.MLRetrainStatus{DatasetRows: 0}, -1, set.Add(time.Hour), ""},
		{"no baseline yet", rows100, nil, 900, set.Add(time.Hour), ""},
		{"a failure waits before rows try again", rows100,
			&storage.MLRetrainStatus{DatasetRows: 0, At: set, Error: "boom"}, 900, set.Add(time.Minute), ""},
		{"and then tries again", rows100,
			&storage.MLRetrainStatus{DatasetRows: 0, At: set, Error: "boom"}, 900, set.Add(time.Hour), TriggerNewRows},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retrainDue(tt.policy, tt.status, tt.rows, tt.now); got != tt.want {
				t.Errorf("retrainDue = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestARetrainPolicyIsOnlyForAModelTrainedHere(t *testing.T) {
	store := newMemStore(
		storage.MLModel{VHost: "a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"},
		storage.MLModel{VHost: "a", Name: "fraud", Backend: inference.BackendOIP, URL: "http://ml:8080", RemoteModel: "fraud"},
	)
	svc, _ := workerService(t, store, 0.9)
	ctx := t.Context()
	p := storage.MLRetrainPolicy{Schedule: "@daily", Spec: churnSpec}

	m, err := svc.SetRetrainPolicy(ctx, "a", "churn", p, "ada")
	if err != nil {
		t.Fatalf("SetRetrainPolicy: %v", err)
	}
	if m.Retrain == nil || m.Retrain.UpdatedBy != "ada" || m.Retrain.UpdatedAt.IsZero() {
		t.Errorf("stored policy = %+v", m.Retrain)
	}
	if _, err := svc.SetRetrainPolicy(ctx, "a", "fraud", p, "ada"); err == nil {
		t.Error("a model served elsewhere took a retrain policy")
	}
	if _, err := svc.SetRetrainPolicy(ctx, "b", "churn", p, "ada"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("another vhost's model: err = %v", err)
	}
	if _, err := svc.SetRetrainPolicy(ctx, "a", "churn", storage.MLRetrainPolicy{Spec: churnSpec}, "ada"); err == nil {
		t.Error("a policy with no trigger was stored")
	}
	if err := svc.ClearRetrainPolicy(ctx, "a", "churn"); err != nil {
		t.Fatalf("ClearRetrainPolicy: %v", err)
	}
	if got := store.churn(t); got.Retrain != nil {
		t.Errorf("the policy stayed: %+v", got.Retrain)
	}
}

func TestTrainingRefusesAModelAnotherTrainingHolds(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"})
	svc, f := workerService(t, store, 0.9)
	if ok, _ := store.ClaimMLModelTraining(t.Context(), "a", "churn", "another-replica", time.Hour); !ok {
		t.Fatal("setup: could not claim")
	}
	_, err := svc.Train(t.Context(), "a", "churn", churnSpec, GoLive{Mode: GoLiveAlways}, "ada")
	if !errors.Is(err, ErrTrainingRunning) {
		t.Fatalf("err = %v, want ErrTrainingRunning", err)
	}
	if len(f.versions["a/churn"]) != 0 {
		t.Error("the worker trained a model another training holds")
	}
	if err := store.ReleaseMLModelTraining(t.Context(), "a", "churn", "another-replica"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Train(t.Context(), "a", "churn", churnSpec, GoLive{Mode: GoLiveAlways}, "ada"); err != nil {
		t.Fatalf("once released: %v", err)
	}
	if len(store.claims) != 0 {
		t.Errorf("a finished training left its claim: %+v", store.claims)
	}
}

func TestTrainingRecordsTheRowsItTrainedOn(t *testing.T) {
	store := newMemStore(storage.MLModel{VHost: "a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"})
	svc, f := workerService(t, store, 0.9)
	f.rows["a/customers"] = 420
	if _, err := svc.Train(t.Context(), "a", "churn", churnSpec, GoLive{}, "ada"); err != nil {
		t.Fatal(err)
	}
	st := store.churn(t).RetrainStatus
	if st == nil || st.DatasetRows != 420 || st.TrainedAt.IsZero() {
		t.Errorf("status = %+v, want 420 rows and when", st)
	}
	if st.Version != "" || !st.At.IsZero() {
		t.Errorf("a training by hand was recorded as a retraining: %+v", st)
	}
}

// clock is a time a test moves by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func retrainer(svc *Service, c *clock, owner string) *Retrainer {
	r := NewRetrainer(func() *Service { return svc }, nil)
	r.owner, r.now = owner, c.now
	return r
}

func withPolicy(t *testing.T, store *memStore, p storage.MLRetrainPolicy) {
	t.Helper()
	if err := store.SetMLModelRetrain(t.Context(), "a", "churn", &p); err != nil {
		t.Fatal(err)
	}
}

func TestTheScheduleRetrainsOnceAndRecordsTheResult(t *testing.T) {
	set := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	c := &clock{t: set}
	store := newMemStore(storage.MLModel{VHost: "a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"})
	svc, f := workerService(t, store, 0.91)
	f.rows["a/customers"] = 300
	withPolicy(t, store, storage.MLRetrainPolicy{Schedule: "0 3 * * *", Spec: churnSpec,
		GoLive: storage.MLGoLive{Mode: GoLiveIf, Min: f64(0.8)}, UpdatedAt: set})
	r := retrainer(svc, c, "replica-1")

	c.t = set.Add(2 * time.Hour)
	r.Tick(t.Context())
	if n := len(f.versions["a/churn"]); n != 0 {
		t.Fatalf("trained %d times before 03:00", n)
	}

	c.t = set.Add(3*time.Hour + 30*time.Second)
	r.Tick(t.Context())
	m := store.churn(t)
	if len(f.versions["a/churn"]) != 1 || m.RemoteVersion != "1" && m.RemoteVersion != f.versions["a/churn"][0].Version {
		t.Fatalf("versions = %+v, live = %q", f.versions["a/churn"], m.RemoteVersion)
	}
	st := m.RetrainStatus
	if st == nil || st.Trigger != TriggerSchedule || !st.Live || st.Version != "1" || st.Error != "" ||
		!st.At.Equal(c.t) || st.DatasetRows != 300 || !strings.Contains(st.Reason, "within bounds") {
		t.Errorf("status = %+v", st)
	}
	if m.RemoteVersion != "1" || m.UpdatedBy != retrainedBy {
		t.Errorf("model = %+v, want version 1 live by %q", m, retrainedBy)
	}

	c.t = set.Add(3*time.Hour + 90*time.Second)
	r.Tick(t.Context())
	if n := len(f.versions["a/churn"]); n != 1 {
		t.Errorf("trained %d times for one scheduled run", n)
	}
	if len(store.claims) != 0 {
		t.Errorf("the retrainer left its claim: %+v", store.claims)
	}
}

// Every replica runs a retrainer. The one that is second to a run must see
// that the run happened, even when it decided to train before it did.
func TestTwoReplicasRunAScheduledRetrainingOnce(t *testing.T) {
	set := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	c := &clock{t: set.Add(3 * time.Hour)}
	store := newMemStore(storage.MLModel{VHost: "a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"})
	svc, f := workerService(t, store, 0.5)
	withPolicy(t, store, storage.MLRetrainPolicy{Schedule: "0 3 * * *", Spec: churnSpec, UpdatedAt: set})
	stale := store.churn(t) // what replica 2 listed before replica 1 trained

	retrainer(svc, c, "replica-1").Tick(t.Context())
	r2 := retrainer(svc, c, "replica-2")
	r2.consider(t.Context(), svc, store, stale)
	r2.Tick(t.Context())
	if n := len(f.versions["a/churn"]); n != 1 {
		t.Errorf("two replicas trained %d versions for one scheduled run", n)
	}
}

func TestARetrainerLeavesAModelAnotherTrainingHolds(t *testing.T) {
	set := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	c := &clock{t: set.Add(3 * time.Hour)}
	store := newMemStore(storage.MLModel{VHost: "a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"})
	svc, f := workerService(t, store, 0.5)
	withPolicy(t, store, storage.MLRetrainPolicy{Schedule: "0 3 * * *", Spec: churnSpec, UpdatedAt: set})
	if ok, _ := store.ClaimMLModelTraining(t.Context(), "a", "churn", "a-person-training-by-hand", time.Hour); !ok {
		t.Fatal("setup: could not claim")
	}
	retrainer(svc, c, "replica-1").Tick(t.Context())
	if n := len(f.versions["a/churn"]); n != 0 {
		t.Errorf("trained over a running training: %d versions", n)
	}
}

func TestNewRowsRetrainFromTheFirstCountSeen(t *testing.T) {
	set := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	c := &clock{t: set}
	store := newMemStore(storage.MLModel{VHost: "a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"})
	svc, f := workerService(t, store, 0.5)
	withPolicy(t, store, storage.MLRetrainPolicy{NewRows: 100, Spec: churnSpec, UpdatedAt: set})
	r := retrainer(svc, c, "replica-1")

	f.rows["a/customers"] = 1000
	r.Tick(t.Context()) // no baseline yet: the rows already there are not new
	if n := len(f.versions["a/churn"]); n != 0 {
		t.Fatalf("trained on rows that were there before the policy: %d", n)
	}
	if st := store.churn(t).RetrainStatus; st == nil || st.DatasetRows != 1000 {
		t.Fatalf("baseline = %+v, want 1000", st)
	}

	f.rows["a/customers"] = 1099
	r.Tick(t.Context())
	if n := len(f.versions["a/churn"]); n != 0 {
		t.Fatalf("trained after 99 new rows of 100")
	}

	f.rows["a/customers"] = 1100
	r.Tick(t.Context())
	st := store.churn(t).RetrainStatus
	if n := len(f.versions["a/churn"]); n != 1 || st.Trigger != TriggerNewRows || st.DatasetRows != 1100 {
		t.Fatalf("after 100 new rows: %d versions, status %+v", n, st)
	}
	if st.Live || !strings.Contains(st.Reason, "by hand") {
		t.Errorf("the default rule put the version live: %+v", st)
	}
	r.Tick(t.Context())
	if n := len(f.versions["a/churn"]); n != 1 {
		t.Errorf("the same rows trained again: %d versions", n)
	}
}

func TestABusyWorkerIsTriedAgainAndAFailureIsRecorded(t *testing.T) {
	set := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	c := &clock{t: set.Add(3 * time.Hour)}
	store := newMemStore(storage.MLModel{VHost: "a", Name: "churn", Backend: storage.MLBackendWorker, RemoteVersion: "1"})
	svc, f := workerService(t, store, 0.5)
	withPolicy(t, store, storage.MLRetrainPolicy{Schedule: "0 3 * * *", Spec: churnSpec, UpdatedAt: set})
	r := retrainer(svc, c, "replica-1")

	f.busy = true
	r.Tick(t.Context())
	st := store.churn(t).RetrainStatus
	if st == nil || !strings.Contains(st.Error, "busy") || !st.At.IsZero() {
		t.Fatalf("status after a busy worker = %+v, want the error and the run still due", st)
	}

	f.busy = false
	c.t = c.t.Add(time.Minute)
	r.Tick(t.Context())
	st = store.churn(t).RetrainStatus
	if n := len(f.versions["a/churn"]); n != 1 || st.Error != "" || st.Version != "1" {
		t.Fatalf("after the worker was free: %d versions, status %+v", n, st)
	}

	// A failure that another attempt will not fix is recorded, and the
	// schedule moves on to its next run rather than trying every minute.
	withPolicy(t, store, storage.MLRetrainPolicy{Schedule: "0 3 * * *", Spec: worker.TrainSpec{Dataset: "customers", Target: "churned"},
		UpdatedAt: set})
	svc.WithWorker(worker.New("http://127.0.0.1:1", "", nil))
	c.t = set.Add(27 * time.Hour)
	r.Tick(t.Context())
	st = store.churn(t).RetrainStatus
	if st.Error == "" || !st.At.Equal(c.t) || st.Version != "" {
		t.Errorf("status after a failure = %+v", st)
	}
}
