package sql

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

// newDashboardHistoryStorage returns an initialised in-memory SQLite storage.
func newDashboardHistoryStorage(t *testing.T) storage.Storage {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	s := NewSQLStorage(db, "sqlite")
	initer, ok := s.(interface{ Init(context.Context) error })
	if !ok {
		t.Fatal("storage does not implement Init")
	}
	if err := initer.Init(t.Context()); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}
	return s
}

// The dashboard chart is rebuilt from scratch on every page load, so a reload
// throws away every point the browser had accumulated. Persisting the samples
// is what lets the chart survive a reload and show a trend rather than a
// thirty-second sliver, so the round-trip is the thing to pin down first.
func TestRecordDashboardSample_RoundTrip(t *testing.T) {
	s := newDashboardHistoryStorage(t)
	ctx := t.Context()

	now := time.Now().UTC().Truncate(time.Second)
	want := storage.DashboardSample{
		Timestamp:       now,
		VHost:           "",
		Throughput:      12.5,
		TotalProcessed:  400,
		TotalErrors:     3,
		TotalLag:        17,
		ErrorRate:       0.75,
		AvgLatencyMs:    4.25,
		ActiveWorkflows: 2,
		ActiveWorkers:   1,
	}

	if err := s.RecordDashboardSample(ctx, want); err != nil {
		t.Fatalf("RecordDashboardSample: %v", err)
	}

	got, err := s.GetDashboardHistory(ctx, "", time.Time{}, 100)
	if err != nil {
		t.Fatalf("GetDashboardHistory: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(got))
	}

	g := got[0]
	if !g.Timestamp.UTC().Equal(want.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", g.Timestamp.UTC(), want.Timestamp)
	}
	if g.Throughput != want.Throughput {
		t.Errorf("Throughput = %v, want %v", g.Throughput, want.Throughput)
	}
	if g.TotalProcessed != want.TotalProcessed {
		t.Errorf("TotalProcessed = %v, want %v", g.TotalProcessed, want.TotalProcessed)
	}
	if g.TotalErrors != want.TotalErrors {
		t.Errorf("TotalErrors = %v, want %v", g.TotalErrors, want.TotalErrors)
	}
	if g.TotalLag != want.TotalLag {
		t.Errorf("TotalLag = %v, want %v", g.TotalLag, want.TotalLag)
	}
	if g.ErrorRate != want.ErrorRate {
		t.Errorf("ErrorRate = %v, want %v", g.ErrorRate, want.ErrorRate)
	}
	if g.AvgLatencyMs != want.AvgLatencyMs {
		t.Errorf("AvgLatencyMs = %v, want %v", g.AvgLatencyMs, want.AvgLatencyMs)
	}
	if g.ActiveWorkflows != want.ActiveWorkflows {
		t.Errorf("ActiveWorkflows = %v, want %v", g.ActiveWorkflows, want.ActiveWorkflows)
	}
	if g.ActiveWorkers != want.ActiveWorkers {
		t.Errorf("ActiveWorkers = %v, want %v", g.ActiveWorkers, want.ActiveWorkers)
	}
}

// A vhost is a tenant boundary. Showing one tenant's throughput on another
// tenant's dashboard is a data leak, not a cosmetic bug, so the filter is
// asserted rather than assumed.
func TestGetDashboardHistory_FiltersByVHost(t *testing.T) {
	s := newDashboardHistoryStorage(t)
	ctx := t.Context()

	now := time.Now().UTC()
	samples := []storage.DashboardSample{
		{Timestamp: now.Add(-3 * time.Minute), VHost: "tenant-a", Throughput: 1},
		{Timestamp: now.Add(-2 * time.Minute), VHost: "tenant-b", Throughput: 2},
		{Timestamp: now.Add(-1 * time.Minute), VHost: "tenant-a", Throughput: 3},
	}
	for _, smp := range samples {
		if err := s.RecordDashboardSample(ctx, smp); err != nil {
			t.Fatalf("RecordDashboardSample: %v", err)
		}
	}

	got, err := s.GetDashboardHistory(ctx, "tenant-a", time.Time{}, 100)
	if err != nil {
		t.Fatalf("GetDashboardHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 samples for tenant-a, got %d", len(got))
	}
	for _, g := range got {
		if g.VHost != "tenant-a" {
			t.Errorf("leaked a sample from vhost %q into tenant-a's history", g.VHost)
		}
	}
}

// "all" is what the UI sends for the unfiltered view, and GetDashboardStats
// already treats it and "" as the same global aggregate. History has to agree,
// or the chart silently reads a different series than the cards above it.
func TestGetDashboardHistory_AllIsTheGlobalAggregate(t *testing.T) {
	s := newDashboardHistoryStorage(t)
	ctx := t.Context()

	now := time.Now().UTC()
	if err := s.RecordDashboardSample(ctx, storage.DashboardSample{
		Timestamp: now, VHost: "all", Throughput: 9,
	}); err != nil {
		t.Fatalf("RecordDashboardSample: %v", err)
	}
	if err := s.RecordDashboardSample(ctx, storage.DashboardSample{
		Timestamp: now.Add(time.Second), VHost: "", Throughput: 11,
	}); err != nil {
		t.Fatalf("RecordDashboardSample: %v", err)
	}

	for _, query := range []string{"", "all"} {
		got, err := s.GetDashboardHistory(ctx, query, time.Time{}, 100)
		if err != nil {
			t.Fatalf("GetDashboardHistory(%q): %v", query, err)
		}
		if len(got) != 2 {
			t.Fatalf("GetDashboardHistory(%q) = %d samples, want 2 — "+
				"%q and \"all\" must name the same series", query, len(got), query)
		}
	}
}

// The chart asks for a window ("the last hour"), so the cutoff has to be
// applied in the database rather than by handing the caller everything ever
// recorded and letting it filter.
func TestGetDashboardHistory_RespectsSince(t *testing.T) {
	s := newDashboardHistoryStorage(t)
	ctx := t.Context()

	now := time.Now().UTC()
	for _, age := range []time.Duration{90 * time.Minute, 30 * time.Minute, time.Minute} {
		if err := s.RecordDashboardSample(ctx, storage.DashboardSample{
			Timestamp: now.Add(-age), Throughput: 1,
		}); err != nil {
			t.Fatalf("RecordDashboardSample: %v", err)
		}
	}

	got, err := s.GetDashboardHistory(ctx, "", now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("GetDashboardHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 samples within the last hour, got %d", len(got))
	}
}

// Oldest-to-newest, because the chart plots them left to right. A descending
// result would render the trend backwards, which is worse than no trend.
func TestGetDashboardHistory_ReturnsOldestFirst(t *testing.T) {
	s := newDashboardHistoryStorage(t)
	ctx := t.Context()

	now := time.Now().UTC()
	for _, offset := range []time.Duration{-time.Minute, -3 * time.Minute, -2 * time.Minute} {
		if err := s.RecordDashboardSample(ctx, storage.DashboardSample{
			Timestamp: now.Add(offset), Throughput: 1,
		}); err != nil {
			t.Fatalf("RecordDashboardSample: %v", err)
		}
	}

	got, err := s.GetDashboardHistory(ctx, "", time.Time{}, 100)
	if err != nil {
		t.Fatalf("GetDashboardHistory: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Timestamp.Before(got[i-1].Timestamp) {
			t.Fatalf("samples are not oldest-first: index %d (%v) precedes %d (%v)",
				i, got[i].Timestamp, i-1, got[i-1].Timestamp)
		}
	}
}

// The limit exists to bound the response, so it has to keep the *newest*
// samples. Dropping the recent end would make a busy dashboard show only
// ancient history.
func TestGetDashboardHistory_LimitKeepsNewest(t *testing.T) {
	s := newDashboardHistoryStorage(t)
	ctx := t.Context()

	now := time.Now().UTC()
	for i := range 10 {
		if err := s.RecordDashboardSample(ctx, storage.DashboardSample{
			Timestamp:  now.Add(-time.Duration(10-i) * time.Minute),
			Throughput: float64(i),
		}); err != nil {
			t.Fatalf("RecordDashboardSample: %v", err)
		}
	}

	got, err := s.GetDashboardHistory(ctx, "", time.Time{}, 3)
	if err != nil {
		t.Fatalf("GetDashboardHistory: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(got))
	}
	// Newest three are throughput 7, 8, 9 — in oldest-first order.
	for i, want := range []float64{7, 8, 9} {
		if got[i].Throughput != want {
			t.Errorf("sample %d throughput = %v, want %v (limit must keep the newest)",
				i, got[i].Throughput, want)
		}
	}
}

// A row per sample per vhost, forever, is an unbounded table on the platform's
// own metadata database. Retention has to be enforceable.
func TestPurgeDashboardHistory_DropsOnlyOlderSamples(t *testing.T) {
	s := newDashboardHistoryStorage(t)
	ctx := t.Context()

	now := time.Now().UTC()
	for _, age := range []time.Duration{48 * time.Hour, 2 * time.Hour} {
		if err := s.RecordDashboardSample(ctx, storage.DashboardSample{
			Timestamp: now.Add(-age), Throughput: 1,
		}); err != nil {
			t.Fatalf("RecordDashboardSample: %v", err)
		}
	}

	if err := s.PurgeDashboardHistory(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("PurgeDashboardHistory: %v", err)
	}

	got, err := s.GetDashboardHistory(ctx, "", time.Time{}, 100)
	if err != nil {
		t.Fatalf("GetDashboardHistory: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 surviving sample, got %d", len(got))
	}
}

// --- What the table costs ---------------------------------------------------
//
// dashboard_history is the only append-only table in the metadata database: a
// row every five seconds, per watched vhost, whether or not anybody is looking.
// That makes a write-only column here different in kind from a write-only
// column anywhere else — it is not wasted bytes once, it is wasted bytes
// forever. Measured on one week of samples for one vhost in SQLite, the UUID
// primary key this test was written against was 9.6 MB of a 21 MB table: 46%
// of the disk this feature asks a self-hosted user to give up, to store an
// identifier no query selects and nothing looks up by.
//
// The assertion is deliberately not "there is no id column". It compares the
// columns Init creates against the columns the read query selects, so the next
// write-only column fails here too, for the same stated reason.
func TestDashboardHistoryStoresNoColumnItNeverReadsBack(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	s := NewSQLStorage(db, "sqlite")
	if err := s.(interface{ Init(context.Context) error }).Init(t.Context()); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	stored := storedColumns(t, db)
	if len(stored) == 0 {
		t.Fatal("dashboard_history was not created by Init")
	}

	for _, c := range readBackColumns(t, db) {
		delete(stored, c)
	}
	for c := range stored {
		t.Errorf("dashboard_history stores column %q that GetDashboardHistory never "+
			"reads back; on an append-only table sampled every five seconds that is "+
			"disk a self-hosted user pays for forever and can never see", c)
	}
}

// A sample taken every five seconds does not have microsecond precision, and
// storing it is not free. The SQLite driver writes a time.Time as text, so
// those six false digits are six real bytes in the row and six more in the
// (vhost, timestamp) index that covers every read. Measured over a week of one
// series: 15.71 MB with the sub-second noise, 14.08 MB without it.
func TestRecordDashboardSample_StoresNoFalsePrecision(t *testing.T) {
	s := newDashboardHistoryStorage(t)
	ctx := t.Context()

	noisy := time.Date(2026, 3, 4, 5, 6, 7, 891234567, time.UTC)
	if err := s.RecordDashboardSample(ctx, storage.DashboardSample{
		Timestamp: noisy, Throughput: 1,
	}); err != nil {
		t.Fatalf("RecordDashboardSample: %v", err)
	}

	got, err := s.GetDashboardHistory(ctx, "", time.Time{}, 10)
	if err != nil {
		t.Fatalf("GetDashboardHistory: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(got))
	}
	if n := got[0].Timestamp.Nanosecond(); n != 0 {
		t.Errorf("stored timestamp kept %d ns of precision the sample never had; on "+
			"a table written every five seconds those digits cost bytes in the row "+
			"and again in the index", n)
	}
	if want := noisy.Truncate(time.Second); !got[0].Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", got[0].Timestamp, want)
	}
}

// storedColumns is every column Init actually creates on dashboard_history.
func storedColumns(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), "PRAGMA table_info(dashboard_history)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scanning table_info: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading table_info: %v", err)
	}
	return cols
}

// readBackColumns is every column the history read query selects.
func readBackColumns(t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), commonQueries[QueryGetDashboardHistory],
		"", beginningOfTime, 1)
	if err != nil {
		t.Fatalf("running the history read query: %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("reading result columns: %v", err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the history read query: %v", err)
	}
	return cols
}
