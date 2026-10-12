package ml

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod/internal/storage"
)

// recStore is memStore with feedback and schedules, in memory.
type recStore struct {
	*memStore
	mu        sync.Mutex
	feedback  []storage.MLFeedbackEvent
	schedules map[string]storage.MLSchedule
	owners    map[string]string
}

func newRecStore(models ...storage.MLModel) *recStore {
	return &recStore{memStore: newMemStore(models...), schedules: map[string]storage.MLSchedule{}, owners: map[string]string{}}
}

func (s *recStore) AddMLFeedback(_ context.Context, events []storage.MLFeedbackEvent) error {
	for _, e := range events {
		if err := storage.ValidateMLFeedbackEvent(e); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feedback = append(s.feedback, events...)
	return nil
}
func (s *recStore) CountMLFeedback(_ context.Context, vhost, model string, since time.Time) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for _, e := range s.feedback {
		if e.VHost == vhost && e.Model == model && !e.At.Before(since) {
			out[e.Outcome]++
		}
	}
	return out, nil
}
func (s *recStore) DeleteMLFeedback(context.Context, string, string) error { return nil }

func schedKey(vhost, kind, name string) string { return vhost + "/" + kind + "/" + name }

func (s *recStore) ListMLSchedules(context.Context) ([]storage.MLSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.MLSchedule
	for _, sc := range s.schedules {
		out = append(out, sc)
	}
	return out, nil
}
func (s *recStore) GetMLSchedule(_ context.Context, vhost, kind, name string) (storage.MLSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.schedules[schedKey(vhost, kind, name)]
	if !ok {
		return sc, storage.ErrNotFound
	}
	return sc, nil
}
func (s *recStore) PutMLSchedule(_ context.Context, sc storage.MLSchedule) error {
	if err := storage.ValidateMLSchedule(sc); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := schedKey(sc.VHost, sc.Kind, sc.Name)
	old := s.schedules[k]
	sc.LastRun, sc.LastRows, sc.LastError, sc.UpdatedAt = old.LastRun, old.LastRows, old.LastError, time.Now().UTC()
	s.schedules[k] = sc
	return nil
}
func (s *recStore) DeleteMLSchedule(_ context.Context, vhost, kind, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := schedKey(vhost, kind, name)
	if _, ok := s.schedules[k]; !ok {
		return storage.ErrNotFound
	}
	delete(s.schedules, k)
	return nil
}
func (s *recStore) SetMLScheduleRun(_ context.Context, vhost, kind, name string, at time.Time, rows int, runErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := schedKey(vhost, kind, name)
	sc := s.schedules[k]
	sc.LastRun, sc.LastRows, sc.LastError = at, rows, runErr
	s.schedules[k] = sc
	return nil
}
func (s *recStore) ClaimMLSchedule(_ context.Context, vhost, kind, name, owner string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := schedKey(vhost, kind, name)
	if o := s.owners[k]; o != "" && o != owner {
		return false, nil
	}
	s.owners[k] = owner
	return true, nil
}
func (s *recStore) ReleaseMLSchedule(_ context.Context, vhost, kind, name, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[schedKey(vhost, kind, name)] == owner {
		delete(s.owners, schedKey(vhost, kind, name))
	}
	return nil
}
func (s *recStore) DeleteMLSchedules(context.Context, string) error { return nil }

// recService is a service over st whose worker serves the visits fixture.
func recService(t *testing.T, st *recStore) *Service {
	t.Helper()
	resetRecommenders()
	svc := NewService(func() any { return st }, nil, nil).WithWorker(scenarioWorker(t, "visits")).WithQuotaDefaults(storage.MLQuotas{})
	svc.now = func() time.Time { return time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC) }
	return svc
}

func TestFeedbackIsCountedAndSummarised(t *testing.T) {
	st := newRecStore(trainedRecs())
	svc := recService(t, st)
	ctx := t.Context()
	ev := func(id, outcome string) FeedbackEvent { return FeedbackEvent{ID: id, Outcome: outcome} }
	if err := svc.RecordFeedback(ctx, "v", "recs", FeedbackRequest{RecommendationID: "rec_1",
		Events: []FeedbackEvent{ev("C1", "shown"), ev("C2", "shown"), ev("C3", "shown"), ev("C4", "shown"), ev("C1", "accepted"), ev("C2", "dismissed")}}); err != nil {
		t.Fatal(err)
	}
	sum, err := svc.FeedbackSummary(ctx, "v", "recs", 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Days != 30 || sum.Shown != 4 || sum.Accepted != 1 || sum.Dismissed != 1 || sum.AcceptanceRate != 0.25 {
		t.Fatalf("summary %+v", sum)
	}
	bad := []FeedbackRequest{
		{Events: []FeedbackEvent{ev("C1", "shown")}},
		{RecommendationID: "rec_1"},
		{RecommendationID: "rec_1", Events: []FeedbackEvent{ev("C1", "liked")}},
		{RecommendationID: "rec_1", Events: []FeedbackEvent{ev("", "shown")}},
		{RecommendationID: "rec_1", Events: make([]FeedbackEvent, MaxFeedbackEvents+1)},
	}
	for _, req := range bad {
		if err := svc.RecordFeedback(ctx, "v", "recs", req); !errors.Is(err, ErrBadFeedback) {
			t.Errorf("%+v: %v, want ErrBadFeedback", req.Events, err)
		}
	}
	if _, err := svc.FeedbackSummary(ctx, "v", "recs", 400); !errors.Is(err, ErrBadFeedback) {
		t.Errorf("400 days: %v", err)
	}
	if err := svc.RecordFeedback(ctx, "v", "nothing", FeedbackRequest{RecommendationID: "r", Events: []FeedbackEvent{ev("x", "shown")}}); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("unknown model: %v", err)
	}
	// With nothing reported shown, the rate is accepted / (accepted + dismissed).
	st2 := newRecStore(trainedRecs())
	svc2 := recService(t, st2)
	_ = svc2.RecordFeedback(ctx, "v", "recs", FeedbackRequest{RecommendationID: "r", Events: []FeedbackEvent{ev("a", "accepted"), ev("b", "dismissed"), ev("c", "dismissed"), ev("d", "accepted")}})
	if sum, _ := svc2.FeedbackSummary(ctx, "v", "recs", 7); sum.AcceptanceRate != 0.5 {
		t.Fatalf("without shown: %+v", sum)
	}
}

// fakeRunner is the engine: a dataset refill counter and one SQLite target.
type fakeRunner struct {
	db       *sql.DB
	refilled []string
	vhost    string
}

func (f *fakeRunner) MLDatasetFromQuery(_ context.Context, vhost, dataset, sourceID, query string, _ int) (int, error) {
	f.refilled = append(f.refilled, vhost+"/"+dataset+"/"+sourceID+"/"+query)
	return 12, nil
}
func (f *fakeRunner) OpenMLTarget(_ context.Context, vhost, sourceID string) (*sql.DB, string, error) {
	if vhost != f.vhost || sourceID != "target" {
		return nil, "", errors.New("source does not belong to the vhost")
	}
	return f.db, "sqlite", nil
}

func newTargetDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE recs_out (model TEXT, version TEXT, recommendation_id TEXT, for_id TEXT, item_id TEXT,
		rank INTEGER, score REAL, plan_date TEXT, reasons_text TEXT, built_at TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestABuildWritesEverySalesmansPlan(t *testing.T) {
	st := newRecStore(trainedRecs())
	svc := recService(t, st)
	runner := &fakeRunner{db: newTargetDB(t), vhost: "v"}
	b := ModelBuild{For: BuildAllSalesmen, PeriodDays: 7, Target: BuildTarget{SourceID: "target", Table: "recs_out"}}
	n, err := svc.RunBuild(t.Context(), runner, "v", "recs", b)
	if err != nil {
		t.Fatal(err)
	}
	// Four salesmen of 20 customers: two plan 4 a day and two 2 a day, over
	// five working days; one of the first two has a blocked customer, so 19.
	if n != 19+4*5+2*5+2*5 {
		t.Fatalf("%d rows", n)
	}
	var rows, planned, ids int
	if err := runner.db.QueryRowContext(t.Context(), `SELECT COUNT(*), COUNT(plan_date), COUNT(DISTINCT recommendation_id) FROM recs_out WHERE model = 'recs'`).
		Scan(&rows, &planned, &ids); err != nil {
		t.Fatal(err)
	}
	if rows != n || planned != n || ids != 1 {
		t.Fatalf("%d rows, %d planned, %d ids", rows, planned, ids)
	}
	var reasons string
	if err := runner.db.QueryRowContext(t.Context(), `SELECT reasons_text FROM recs_out WHERE for_id = 'R01' AND rank = 1`).Scan(&reasons); err != nil || reasons == "" {
		t.Fatalf("reasons %q, %v", reasons, err)
	}
	// Replace leaves one build's rows; append adds another's.
	if _, err := svc.RunBuild(t.Context(), runner, "v", "recs", b); err != nil {
		t.Fatal(err)
	}
	b.Target.Mode = BuildAppend
	if _, err := svc.RunBuild(t.Context(), runner, "v", "recs", b); err != nil {
		t.Fatal(err)
	}
	if err := runner.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM recs_out`).Scan(&rows); err != nil || rows != 2*n {
		t.Fatalf("after replace and append: %d rows, %v", rows, err)
	}

	// A build for the wrong subjects, or to another vhost's source, is refused.
	if _, err := svc.RunBuild(t.Context(), runner, "v", "recs", ModelBuild{For: BuildAllProducts, Target: b.Target}); !errors.Is(err, ErrBadSchedule) {
		t.Errorf("visits for all products: %v", err)
	}
	if _, err := svc.RunBuild(t.Context(), runner, "v", "recs", ModelBuild{For: BuildAllSalesmen, Target: BuildTarget{SourceID: "elsewhere", Table: "recs_out"}}); err == nil {
		t.Error("wrote to a source the vhost does not have")
	}
	if err := (ModelBuild{For: BuildAllSalesmen, Target: BuildTarget{SourceID: "target", Table: "recs; DROP TABLE x"}}).Validate(false); !errors.Is(err, ErrBadSchedule) {
		t.Errorf("an unsafe table name: %v", err)
	}
}

func TestTheSchedulerRunsDueSchedulesOnce(t *testing.T) {
	st := newRecStore(trainedRecs())
	svc := recService(t, st)
	ctx := t.Context()
	runner := &fakeRunner{db: newTargetDB(t), vhost: "v"}
	if _, err := svc.SetDatasetRefresh(ctx, "v", "orders", DatasetRefresh{Schedule: "@every 1m", SourceID: "src", Query: "SELECT 1"}, "ann"); err == nil {
		// @every is a descriptor robfig accepts; either way the refresh below is hourly.
		_ = svc.ClearDatasetRefresh(ctx, "v", "orders")
	}
	status, err := svc.SetDatasetRefresh(ctx, "v", "orders", DatasetRefresh{Schedule: "@hourly", SourceID: "src", Query: "SELECT * FROM orders"}, "ann")
	if err != nil {
		t.Fatal(err)
	}
	if status.NextRun == nil || status.LastRun != nil {
		t.Fatalf("status %+v", status)
	}
	if _, err := svc.SetModelBuild(ctx, "v", "recs", ModelBuild{Schedule: "@daily", For: BuildAllSalesmen, Target: BuildTarget{SourceID: "target", Table: "recs_out"}}, "ann"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []DatasetRefresh{{Schedule: "every day", SourceID: "s", Query: "q"}, {Schedule: "@daily", Query: "q"}, {Schedule: "@daily", SourceID: "s", Query: " "}} {
		if _, err := svc.SetDatasetRefresh(ctx, "v", "orders", bad, "ann"); !errors.Is(err, ErrBadSchedule) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if _, err := svc.SetModelBuild(ctx, "v", "recs", ModelBuild{Schedule: "@daily", For: BuildAllSalesmen}, "ann"); !errors.Is(err, ErrBadSchedule) {
		t.Errorf("a scheduled build with no target: %v", err)
	}

	sched := NewScheduler(func() *Service { return svc }, runner, nil)
	// Not due yet.
	sched.now = func() time.Time { return time.Now().Add(30 * time.Second) }
	sched.Tick(ctx)
	if len(runner.refilled) != 0 {
		t.Fatalf("ran early: %v", runner.refilled)
	}
	// Two days later both are due; each runs once, however often it ticks.
	sched.now = func() time.Time { return time.Now().Add(49 * time.Hour) }
	sched.Tick(ctx)
	sched.Tick(ctx)
	if len(runner.refilled) != 1 || runner.refilled[0] != "v/orders/src/SELECT * FROM orders" {
		t.Fatalf("refills %v", runner.refilled)
	}
	refresh, rs, _ := svc.Schedule(ctx, "v", storage.MLScheduleDatasetRefresh, "orders")
	if refresh == nil || rs.LastRun == nil || rs.LastRows != 12 || rs.LastError != "" {
		t.Fatalf("refresh status %+v", rs)
	}
	_, bs, _ := svc.Schedule(ctx, "v", storage.MLScheduleModelBuild, "recs")
	if bs == nil || bs.LastRows != 59 || bs.LastError != "" {
		t.Fatalf("build status %+v", bs)
	}
	var spec ModelBuild
	if err := json.Unmarshal(st.schedules[schedKey("v", storage.MLScheduleModelBuild, "recs")].Spec, &spec); err != nil || spec.For != BuildAllSalesmen {
		t.Fatalf("stored spec %+v, %v", spec, err)
	}
	// A schedule another replica holds is left alone.
	st.owners[schedKey("v", storage.MLScheduleDatasetRefresh, "orders")] = "someone-else"
	sched.now = func() time.Time { return time.Now().Add(100 * time.Hour) }
	sched.Tick(ctx)
	if len(runner.refilled) != 1 {
		t.Fatalf("ran a claimed schedule: %v", runner.refilled)
	}
	if err := svc.ClearModelBuild(ctx, "v", "recs"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearModelBuild(ctx, "v", "recs"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("second clear: %v", err)
	}
	if !strings.HasPrefix(newRecommendationID(), "rec_") {
		t.Fatal("id prefix")
	}
}
