package sql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/gsoultan/hermod/internal/storage"

	_ "modernc.org/sqlite"
)

// Sources and sinks list the same way workflows do, and had the same two
// defects: no ORDER BY under a LIMIT/OFFSET, and a LIKE that PostgreSQL does
// not fold case for. See workflow_list_test.go for the long version — these are
// the same assertions against the other two tables, which are the other two
// screens an operator pages and searches.
//
// They search four columns rather than one, so there are four ways for the case
// folding to be missed and only one of them is the name.

// seeded gives each column under test a token unique across the whole table, so
// a search for it can only match through that column.
type searchSeed struct{ id, name, typ, vhost string }

var sourceSinkSeeds = []searchSeed{
	{"src-QQAlpha", "one", "postgres", "va"},
	{"src-b", "QQBravo Ingest", "mysql", "vb"},
	{"src-c", "three", "QQCharlie", "vc"},
	{"src-d", "four", "kafka", "QQDelta"},
}

// The column each token has to be found through, searched in the case the
// stored value is not in.
var sourceSinkSearches = []struct{ search, want, column string }{
	{"qqalpha", "src-QQAlpha", "id"},
	{"QQBRAVO", "src-b", "name"},
	{"qqcharlie", "src-c", "type"},
	{"QQDELTA", "src-d", "vhost"},
}

func seedSources(t *testing.T, s storage.Storage) {
	t.Helper()
	for _, sd := range sourceSinkSeeds {
		if err := s.CreateSource(t.Context(), storage.Source{
			ID: sd.id, Name: sd.name, Type: sd.typ, VHost: sd.vhost,
		}); err != nil {
			t.Fatalf("seed source %s: %v", sd.id, err)
		}
	}
}

func seedSinks(t *testing.T, s storage.Storage) {
	t.Helper()
	for _, sd := range sourceSinkSeeds {
		if err := s.CreateSink(t.Context(), storage.Sink{
			ID: sd.id, Name: sd.name, Type: sd.typ, VHost: sd.vhost,
		}); err != nil {
			t.Fatalf("seed sink %s: %v", sd.id, err)
		}
	}
}

func sourceIDs(srcs []storage.Source) []string {
	out := make([]string, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, s.ID)
	}
	return out
}

func sinkIDs(snks []storage.Sink) []string {
	out := make([]string, 0, len(snks))
	for _, s := range snks {
		out = append(out, s.ID)
	}
	return out
}

func TestSourceSearchIgnoresCaseInEveryColumnItSearches(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	seedSources(t, s)

	for _, tc := range sourceSinkSearches {
		got, total, err := s.ListSources(t.Context(), storage.CommonFilter{Search: tc.search})
		if err != nil {
			t.Errorf("searching for %q failed: %v", tc.search, err)
			continue
		}
		if len(got) != 1 || got[0].ID != tc.want || total != 1 {
			t.Errorf("searching for %q returned %v (total %d), want only %s\n"+
				"the match has to come through the %s column, and PostgreSQL's LIKE "+
				"compares it as stored", tc.search, sourceIDs(got), total, tc.want, tc.column)
		}
	}
}

func TestSinkSearchIgnoresCaseInEveryColumnItSearches(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	seedSinks(t, s)

	for _, tc := range sourceSinkSearches {
		got, total, err := s.ListSinks(t.Context(), storage.CommonFilter{Search: tc.search})
		if err != nil {
			t.Errorf("searching for %q failed: %v", tc.search, err)
			continue
		}
		if len(got) != 1 || got[0].ID != tc.want || total != 1 {
			t.Errorf("searching for %q returned %v (total %d), want only %s\n"+
				"the match has to come through the %s column", tc.search, sinkIDs(got), total, tc.want, tc.column)
		}
	}
}

// The wildcards an operator types stay text, in all four columns.
func TestSourceSearchKeepsWildcardsLiteral(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	seedSources(t, s)
	if err := s.CreateSource(t.Context(), storage.Source{
		ID: "src-pct", Name: "50% off", Type: "http", VHost: "vx",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, total, err := s.ListSources(t.Context(), storage.CommonFilter{Search: "%"})
	if err != nil {
		t.Fatalf("searching for a per-cent sign failed: %v", err)
	}
	if len(got) != 1 || got[0].ID != "src-pct" || total != 1 {
		t.Errorf("searching for %q returned %v (total %d), want only src-pct\n"+
			"an unescaped per-cent sign in a LIKE pattern means \"match anything\"",
			"%", sourceIDs(got), total)
	}
}

func TestListSourcesReturnsNewestFirst(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	// Seeded out of creation order, so insertion order cannot pass for the
	// right answer.
	for _, seed := range []struct {
		id  string
		age time.Duration
	}{
		{"src-middle", 2 * time.Hour},
		{"src-oldest", 9 * time.Hour},
		{"src-newest", 0},
	} {
		if err := s.CreateSource(ctx, storage.Source{
			ID: seed.id, Name: seed.id, Type: "postgres", CreatedAt: base.Add(-seed.age),
		}); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}

	got, _, err := s.ListSources(ctx, storage.CommonFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"src-newest", "src-middle", "src-oldest"}
	if fmt.Sprint(sourceIDs(got)) != fmt.Sprint(want) {
		t.Errorf("ListSources returned %v, want %v\n"+
			"the list carries no ORDER BY", sourceIDs(got), want)
	}
}

func TestListSinksReturnsNewestFirst(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		id  string
		age time.Duration
	}{
		{"snk-middle", 2 * time.Hour},
		{"snk-oldest", 9 * time.Hour},
		{"snk-newest", 0},
	} {
		if err := s.CreateSink(ctx, storage.Sink{
			ID: seed.id, Name: seed.id, Type: "postgres", CreatedAt: base.Add(-seed.age),
		}); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}

	got, _, err := s.ListSinks(ctx, storage.CommonFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"snk-newest", "snk-middle", "snk-oldest"}
	if fmt.Sprint(sinkIDs(got)) != fmt.Sprint(want) {
		t.Errorf("ListSinks returned %v, want %v\n"+
			"the list carries no ORDER BY", sinkIDs(got), want)
	}
}

// Rows sharing a creation time still need a tie-break, or LIMIT/OFFSET slices
// an order the engine is free to change between pages.
func TestListSourcesPagesAWholeListExactlyOnce(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	all := []string{"src-a", "src-b", "src-c", "src-d", "src-e"}
	for _, id := range all {
		if err := s.CreateSource(ctx, storage.Source{
			ID: id, Name: id, Type: "postgres", CreatedAt: at,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	seen := map[string]int{}
	for page := 1; page <= 3; page++ {
		got, total, err := s.ListSources(ctx, storage.CommonFilter{Limit: 2, Page: page})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != 5 {
			t.Errorf("page %d reported total %d, want 5", page, total)
		}
		for _, src := range got {
			seen[src.ID]++
		}
	}
	for _, id := range all {
		if seen[id] != 1 {
			t.Errorf("paging returned %s %d times, want exactly 1 (saw %v)", id, seen[id], seen)
			break
		}
	}
}

func TestCreateSourceAndSinkStampTheCreationTime(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	before := time.Now().Add(-time.Second)
	if err := s.CreateSource(ctx, storage.Source{ID: "src-1", Name: "unstamped", Type: "postgres"}); err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := s.CreateSink(ctx, storage.Sink{ID: "snk-1", Name: "unstamped", Type: "postgres"}); err != nil {
		t.Fatalf("create sink: %v", err)
	}
	after := time.Now().Add(time.Second)

	src, err := s.GetSource(ctx, "src-1")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if src.CreatedAt.IsZero() || src.CreatedAt.Before(before) || src.CreatedAt.After(after) {
		t.Errorf("source CreatedAt is %v, want a time inside %v..%v", src.CreatedAt, before, after)
	}

	snk, err := s.GetSink(ctx, "snk-1")
	if err != nil {
		t.Fatalf("get sink: %v", err)
	}
	if snk.CreatedAt.IsZero() || snk.CreatedAt.Before(before) || snk.CreatedAt.After(after) {
		t.Errorf("sink CreatedAt is %v, want a time inside %v..%v", snk.CreatedAt, before, after)
	}
}

// Updating must not re-date the row: the sort key is the creation time, not the
// last-touched time.
func TestUpdatingASourceKeepsItsCreationTime(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	want := time.Date(2024, 7, 4, 9, 30, 0, 0, time.UTC)
	if err := s.CreateSource(ctx, storage.Source{
		ID: "src-1", Name: "before", Type: "postgres", CreatedAt: want,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpdateSource(ctx, storage.Source{
		ID: "src-1", Name: "after", Type: "postgres",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetSource(ctx, "src-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "after" {
		t.Fatalf("the update did not land: name is %q", got.Name)
	}
	if !got.CreatedAt.UTC().Equal(want) {
		t.Errorf("CreatedAt is %v after an update, want %v; saving a source moved it "+
			"to the top of a list sorted by creation time", got.CreatedAt.UTC(), want)
	}
}

// Rows that predate the column: NULL sorts to opposite ends on different
// engines, so Init fills them in.
func TestInitBackfillsSourcesAndSinksWithNoCreationTime(t *testing.T) {
	s, db := workflowStore(t)
	ctx := t.Context()

	if err := s.CreateSource(ctx, storage.Source{ID: "src-old", Name: "old", Type: "postgres"}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := s.CreateSink(ctx, storage.Sink{ID: "snk-old", Name: "old", Type: "postgres"}); err != nil {
		t.Fatalf("seed sink: %v", err)
	}
	for _, table := range []string{"sources", "sinks"} {
		if _, err := db.ExecContext(ctx, "UPDATE "+table+" SET created_at = NULL"); err != nil {
			t.Fatalf("blank %s.created_at: %v", table, err)
		}
	}

	if err := s.Init(ctx); err != nil {
		t.Fatalf("re-init: %v", err)
	}

	src, err := s.GetSource(ctx, "src-old")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if src.CreatedAt.IsZero() {
		t.Error("a source left with a NULL created_at by the migration still has one after Init")
	}
	snk, err := s.GetSink(ctx, "snk-old")
	if err != nil {
		t.Fatalf("get sink: %v", err)
	}
	if snk.CreatedAt.IsZero() {
		t.Error("a sink left with a NULL created_at by the migration still has one after Init")
	}
}

// The same assertions against real PostgreSQL, which folds no case in LIKE and
// owes an unordered query no particular order.
func TestSourceListAndSearchOnPostgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to enable")
	}

	ctx := t.Context()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be opened (%s): %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := NewSQLStorage(db, "pgx")
	if err := s.Init(ctx); err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be initialised (%s): %v", dsn, err)
	}

	// sources.name is UNIQUE, so the token has to make every seeded name
	// distinct as well as scope the search to this run.
	token := fmt.Sprintf("srclist%d", time.Now().UnixNano())
	base := time.Now().UTC().Truncate(time.Second)
	seeded := []struct {
		id, name string
		age      time.Duration
	}{
		{token + "-mid", "Middle " + token, 2 * time.Hour},
		{token + "-old", "Oldest " + token, 9 * time.Hour},
		{token + "-new", "Newest " + token, 0},
	}
	for _, sd := range seeded {
		if err := s.CreateSource(ctx, storage.Source{
			ID: sd.id, Name: sd.name, Type: "postgres", CreatedAt: base.Add(-sd.age),
		}); err != nil {
			t.Fatalf("seed %s: %v", sd.id, err)
		}
	}
	t.Cleanup(func() {
		for _, sd := range seeded {
			_, _ = db.ExecContext(context.Background(), `DELETE FROM sources WHERE id = $1`, sd.id)
		}
	})

	want := []string{token + "-new", token + "-mid", token + "-old"}
	for _, search := range []string{token, strings.ToUpper(token)} {
		got, total, err := s.ListSources(ctx, storage.CommonFilter{Search: search})
		if err != nil {
			t.Errorf("searching for %q failed: %v", search, err)
			continue
		}
		if total != 3 {
			t.Errorf("searching for %q reported total %d, want 3\n"+
				"PostgreSQL's LIKE folds no case", search, total)
			continue
		}
		if fmt.Sprint(sourceIDs(got)) != fmt.Sprint(want) {
			t.Errorf("searching for %q returned %v, want %v", search, sourceIDs(got), want)
		}
	}
}
