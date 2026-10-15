// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package ml

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
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

func TestAReorderBuildWithinDaysWritesDueDates(t *testing.T) {
	svc := scenarioService(t, "reorder", storage.MLQuotas{})
	// Today is 2026-10-01: a 14-day window ends before 2026-10-15.
	b := ModelBuild{For: BuildAllSalesmen, WithinDays: 14, K: 500}
	rows, err := svc.BuildRecommendations(t.Context(), "v", "recs", b)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("nobody due in 14 days")
	}
	for _, r := range rows {
		if r.DueDate == "" || r.DueDate >= "2026-10-15" || r.PlanDate != "" {
			t.Errorf("%s/%s: due %q, plan %q", r.ForID, r.ItemID, r.DueDate, r.PlanDate)
		}
	}
	everyone, err := svc.BuildRecommendations(t.Context(), "v", "recs", ModelBuild{For: BuildAllCustomers, WithinDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	all, err := svc.BuildRecommendations(t.Context(), "v", "recs", ModelBuild{For: BuildAllCustomers})
	if err != nil {
		t.Fatal(err)
	}
	if len(everyone) == 0 || len(everyone) >= len(all) {
		t.Fatalf("all customers: %d due in 14 days of %d", len(everyone), len(all))
	}

	// A table with a due_date column gets it; a table without one, as built
	// before it existed, still takes the build.
	withDue := newTargetDB(t)
	if _, err := withDue.ExecContext(t.Context(), `ALTER TABLE recs_out ADD COLUMN due_date TEXT`); err != nil {
		t.Fatal(err)
	}
	target := BuildTarget{SourceID: "target", Table: "recs_out"}
	if err := WriteBuild(t.Context(), withDue, "sqlite", target, "recs", rows); err != nil {
		t.Fatal(err)
	}
	var n, due int
	var first string
	if err := withDue.QueryRowContext(t.Context(), `SELECT COUNT(*), COUNT(due_date), MIN(due_date) FROM recs_out`).Scan(&n, &due, &first); err != nil {
		t.Fatal(err)
	}
	if n != len(rows) || due != n || first != minDue(rows) {
		t.Fatalf("%d rows, %d with due_date, first %s", n, due, first)
	}
	t.Run("without due_date", func(t *testing.T) {
		without := newTargetDB(t)
		if err := WriteBuild(t.Context(), without, "sqlite", target, "recs", rows); err != nil {
			t.Fatalf("a table without due_date: %v", err)
		}
		if err := without.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM recs_out`).Scan(&n); err != nil || n != len(rows) {
			t.Fatalf("%d rows, %v", n, err)
		}
	})
	// Outside reorder the column is NULL.
	plan, err := recService(t, newRecStore(trainedRecs())).BuildRecommendations(t.Context(), "v", "recs", ModelBuild{For: BuildAllSalesmen})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteBuild(t.Context(), withDue, "sqlite", BuildTarget{SourceID: "target", Table: "recs_out", Mode: BuildAppend}, "plan", plan); err != nil {
		t.Fatal(err)
	}
	if err := withDue.QueryRowContext(t.Context(), `SELECT COUNT(due_date) FROM recs_out WHERE model = 'plan'`).Scan(&due); err != nil || due != 0 {
		t.Fatalf("a visit plan's due dates: %d, %v", due, err)
	}

	// A window is for reorder, and bounded.
	if _, err := recService(t, newRecStore(trainedRecs())).BuildRecommendations(t.Context(), "v", "recs",
		ModelBuild{For: BuildAllSalesmen, WithinDays: 14}); !errors.Is(err, ErrBadSchedule) || !strings.Contains(err.Error(), "within_days") {
		t.Errorf("a visit plan within 14 days: %v", err)
	}
	if err := (ModelBuild{For: BuildAllCustomers, WithinDays: 367}).Validate(false); !errors.Is(err, ErrBadSchedule) {
		t.Errorf("367 days: %v", err)
	}
}

func minDue(rows []BuildRow) string {
	first := rows[0].DueDate
	for _, r := range rows {
		first = min(first, r.DueDate)
	}
	return first
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

func TestABuildWritesCategoryAndSizeWhenTheTableHasThem(t *testing.T) {
	svc := scenarioService(t, "reorder_peers", storage.MLQuotas{})
	rows, err := svc.BuildRecommendations(t.Context(), "v", "recs", ModelBuild{For: BuildAllCustomers, WithinDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.Segment == "" || r.Size == "" {
			t.Fatalf("%s: segment %q, size %q", r.ItemID, r.Segment, r.Size)
		}
	}
	db := newTargetDB(t)
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE recs_out ADD COLUMN segment TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE recs_out ADD COLUMN SIZE TEXT`); err != nil {
		t.Fatal(err)
	}
	target := BuildTarget{SourceID: "target", Table: "recs_out"}
	if err := WriteBuild(t.Context(), db, "sqlite", target, "recs", rows); err != nil {
		t.Fatal(err)
	}
	var n, segs, sizes int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*), COUNT(segment), COUNT(size) FROM recs_out`).Scan(&n, &segs, &sizes); err != nil {
		t.Fatal(err)
	}
	if n != len(rows) || segs != n || sizes != n {
		t.Fatalf("%d rows, %d with segment, %d with size", n, segs, sizes)
	}
	var seg, size string
	if err := db.QueryRowContext(t.Context(), `SELECT segment, size FROM recs_out WHERE item_id = ?`, rows[0].ItemID).Scan(&seg, &size); err != nil ||
		seg != rows[0].Segment || size != rows[0].Size {
		t.Fatalf("%s: %q %q, want %q %q (%v)", rows[0].ItemID, seg, size, rows[0].Segment, rows[0].Size, err)
	}
	// A table with only one of them gets that one; one with neither, as built
	// before they existed, still takes the build.
	t.Run("size only", func(t *testing.T) {
		only := newTargetDB(t)
		if _, err := only.ExecContext(t.Context(), `ALTER TABLE recs_out ADD COLUMN size TEXT`); err != nil {
			t.Fatal(err)
		}
		if err := WriteBuild(t.Context(), only, "sqlite", target, "recs", rows); err != nil {
			t.Fatal(err)
		}
		if err := only.QueryRowContext(t.Context(), `SELECT COUNT(size) FROM recs_out`).Scan(&sizes); err != nil || sizes != len(rows) {
			t.Fatalf("%d sizes, %v", sizes, err)
		}
	})
	t.Run("neither", func(t *testing.T) {
		if err := WriteBuild(t.Context(), newTargetDB(t), "sqlite", target, "recs", rows); err != nil {
			t.Fatalf("a table without segment and size: %v", err)
		}
	})

	// For a product list, the row is of the customer it is for.
	offers := scenarioService(t, "products_peers", storage.MLQuotas{})
	rows, err = offers.BuildRecommendations(t.Context(), "v", "recs", ModelBuild{For: BuildAllCustomers, K: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || rows[0].Segment == "" || rows[0].Size == "" {
		t.Fatalf("products rows %+v", rows[:min(1, len(rows))])
	}
}

func TestABuildWritesBrandSubcategoryAndSuggestedQuantityWhenTheTableHasThem(t *testing.T) {
	svc := scenarioService(t, "similar_shop", storage.MLQuotas{})
	rows, err := svc.BuildRecommendations(t.Context(), "v", "recs", ModelBuild{For: BuildAllProducts, K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.Brand == "" || r.Subcategory == "" {
			t.Fatalf("%s: brand %q, subcategory %q", r.ItemID, r.Brand, r.Subcategory)
		}
	}
	// A product row carries the quantity its answer suggests.
	raw, err := os.ReadFile(filepath.Join(recFixtures, "scenario", "products_shop.rec"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := recommend.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	offer := appendBuildRows(nil, BuildRow{}, recommend.Result{For: "K02", Items: []recommend.Pick{
		{ID: "I1", Rank: 1, Fields: map[string]any{"suggested_qty": 3}}}}, rec)
	if len(offer) != 1 || offer[0].SuggestedQty != 3 || offer[0].Brand != "Indomie" {
		t.Fatalf("product row %+v", offer)
	}
	rows[0].SuggestedQty = offer[0].SuggestedQty
	db := newTargetDB(t)
	for _, col := range []string{"brand TEXT", "SUBCATEGORY TEXT", "suggested_qty INTEGER"} {
		if _, err := db.ExecContext(t.Context(), `ALTER TABLE recs_out ADD COLUMN `+col); err != nil {
			t.Fatal(err)
		}
	}
	target := BuildTarget{SourceID: "target", Table: "recs_out"}
	if err := WriteBuild(t.Context(), db, "sqlite", target, "recs", rows); err != nil {
		t.Fatal(err)
	}
	var n, brands, subs, qtys int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*), COUNT(brand), COUNT(subcategory), COUNT(suggested_qty) FROM recs_out`).
		Scan(&n, &brands, &subs, &qtys); err != nil {
		t.Fatal(err)
	}
	if n != len(rows) || brands != n || subs != n || qtys != 1 {
		t.Fatalf("%d rows, %d with brand, %d with subcategory, %d with suggested_qty", n, brands, subs, qtys)
	}
	var qty int
	if err := db.QueryRowContext(t.Context(), `SELECT suggested_qty FROM recs_out WHERE suggested_qty IS NOT NULL`).Scan(&qty); err != nil || qty != 3 {
		t.Fatalf("suggested_qty %d, %v", qty, err)
	}
	// A table without them, as built before they existed, still takes the build.
	t.Run("neither", func(t *testing.T) {
		if err := WriteBuild(t.Context(), newTargetDB(t), "sqlite", target, "recs", rows); err != nil {
			t.Fatalf("a table without brand, subcategory and suggested_qty: %v", err)
		}
	})

	// A customer list's rows have no product, so neither brand nor subcategory.
	aud := scenarioService(t, "audience_shop", storage.MLQuotas{})
	rows, err = aud.BuildRecommendations(t.Context(), "v", "recs", ModelBuild{For: BuildAllProducts, K: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Brand != "" || r.Subcategory != "" {
			t.Fatalf("audience row %+v", r)
		}
	}
}

func TestABuildWritesPriceTierChannelAndRegionWhenTheTableHasThem(t *testing.T) {
	svc := scenarioService(t, "audience_market", storage.MLQuotas{})
	rows, err := svc.BuildRecommendations(t.Context(), "v", "recs", ModelBuild{For: BuildAllProducts, K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.PriceTier == "" || r.Channel == "" || r.Region == "" {
			t.Fatalf("%s: tier %q, channel %q, region %q", r.ItemID, r.PriceTier, r.Channel, r.Region)
		}
	}
	db := newTargetDB(t)
	for _, col := range []string{"price_tier", "channel", "region"} {
		if _, err := db.ExecContext(t.Context(), `ALTER TABLE recs_out ADD COLUMN `+col+` TEXT`); err != nil {
			t.Fatal(err)
		}
	}
	target := BuildTarget{SourceID: "target", Table: "recs_out"}
	if err := WriteBuild(t.Context(), db, "sqlite", target, "recs", rows); err != nil {
		t.Fatal(err)
	}
	var n, tiers, channels, regions int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*), COUNT(price_tier), COUNT(channel), COUNT(region) FROM recs_out`).
		Scan(&n, &tiers, &channels, &regions); err != nil {
		t.Fatal(err)
	}
	if n != len(rows) || tiers != n || channels != n || regions != n {
		t.Fatalf("%d rows, %d tiers, %d channels, %d regions", n, tiers, channels, regions)
	}
	// A table without them still takes the build.
	t.Run("without them", func(t *testing.T) {
		if err := WriteBuild(t.Context(), newTargetDB(t), "sqlite", target, "recs", rows); err != nil {
			t.Fatalf("a table without them: %v", err)
		}
	})
}
