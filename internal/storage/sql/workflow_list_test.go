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

// Listing and searching workflows.
//
// Two things an operator assumes about a list screen were not true here.
//
// The list had no ORDER BY at all, so the rows arrived in whatever order the
// engine happened to produce them — and LIMIT/OFFSET then sliced that unordered
// set, which is free to show the same workflow on two pages and none of the
// others. There was also nothing to order by: the workflows table had no
// created_at column, so "when was this made" was not a question the database
// could answer.
//
// The search matched with LIKE against the raw column. sqlite and MySQL fold
// case for ASCII on their own, so it looked case-insensitive in development;
// PostgreSQL's LIKE does not, so on the driver Hermod actually deploys with,
// typing "Order" found nothing named "order ingest".

// workflowStore returns storage over a private in-memory sqlite database, plus
// the handle, so a test can write the shapes an older schema left behind.
//
// pragmas run on the same single connection the storage uses. The one that
// matters is case_sensitive_like: sqlite folds ASCII case in LIKE by default and
// PostgreSQL does not, so without it a case-insensitivity test passes here and
// still fails in production.
func workflowStore(t *testing.T, pragmas ...string) (storage.Storage, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	for _, p := range pragmas {
		if _, err := db.ExecContext(t.Context(), p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}

	s := NewSQLStorage(db, "sqlite")
	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("init: %v", err)
	}
	return s, db
}

func workflowIDs(wfs []storage.Workflow) []string {
	out := make([]string, 0, len(wfs))
	for _, wf := range wfs {
		out = append(out, wf.ID)
	}
	return out
}

func sameIDs(got []storage.Workflow, want []string) bool {
	ids := workflowIDs(got)
	if len(ids) != len(want) {
		return false
	}
	for i := range ids {
		if ids[i] != want[i] {
			return false
		}
	}
	return true
}

// The order the list comes back in.
//
// Seeded deliberately out of creation order, so a pass cannot be insertion
// order wearing the right answer's clothes.
func TestListWorkflowsReturnsNewestFirst(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		id  string
		age time.Duration
	}{
		{"wf-middle", 2 * time.Hour},
		{"wf-oldest", 9 * time.Hour},
		{"wf-newest", 0},
	} {
		if err := s.CreateWorkflow(ctx, storage.Workflow{
			ID: seed.id, Name: seed.id, CreatedAt: base.Add(-seed.age),
		}); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}

	got, _, err := s.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	want := []string{"wf-newest", "wf-middle", "wf-oldest"}
	if !sameIDs(got, want) {
		t.Errorf("ListWorkflows returned %v, want %v\n"+
			"the list carries no ORDER BY, so the rows arrive in whatever order the "+
			"engine produced them", workflowIDs(got), want)
	}
}

// Paging over an order that is not total is the failure that actually reaches
// an operator: two workflows created in the same instant have nothing left to
// separate them, so page 1 and page 2 are free to disagree about which is which.
func TestListWorkflowsPagesAWholeListExactlyOnce(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	// One shared timestamp, so created_at alone cannot decide the order.
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"wf-a", "wf-b", "wf-c", "wf-d", "wf-e"} {
		if err := s.CreateWorkflow(ctx, storage.Workflow{ID: id, Name: id, CreatedAt: at}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	seen := map[string]int{}
	for page := 1; page <= 3; page++ {
		got, total, err := s.ListWorkflows(ctx, storage.CommonFilter{Limit: 2, Page: page})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != 5 {
			t.Errorf("page %d reported total %d, want 5", page, total)
		}
		for _, wf := range got {
			seen[wf.ID]++
		}
	}

	for _, id := range []string{"wf-a", "wf-b", "wf-c", "wf-d", "wf-e"} {
		if seen[id] != 1 {
			t.Errorf("paging the whole list returned %s %d times, want exactly 1 (saw %v)\n"+
				"workflows sharing a creation time need a tie-break, or LIMIT/OFFSET "+
				"slices an order the engine is free to change between pages",
				id, seen[id], seen)
			break
		}
	}
}

// Nothing can be ordered by a creation time nobody recorded, and four call
// sites create workflows. Stamping it in storage is the only place that cannot
// be forgotten by a fifth.
func TestCreateWorkflowStampsTheCreationTime(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	before := time.Now().Add(-time.Second)
	if err := s.CreateWorkflow(ctx, storage.Workflow{ID: "wf-1", Name: "unstamped"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	after := time.Now().Add(time.Second)

	got, err := s.GetWorkflow(ctx, "wf-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("a workflow created without an explicit CreatedAt has none stored; " +
			"it will sort against every other unstamped row arbitrarily")
	}
	if got.CreatedAt.Before(before) || got.CreatedAt.After(after) {
		t.Errorf("stored CreatedAt %v is outside the window the row was written in (%v..%v)",
			got.CreatedAt, before, after)
	}
}

// A caller that supplies a creation time keeps it — restoring a backup must not
// re-date every workflow in it to the moment of the restore.
func TestCreateWorkflowKeepsACreationTimeItWasGiven(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	want := time.Date(2024, 7, 4, 9, 30, 0, 0, time.UTC)
	if err := s.CreateWorkflow(ctx, storage.Workflow{ID: "wf-1", Name: "restored", CreatedAt: want}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetWorkflow(ctx, "wf-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.CreatedAt.UTC().Equal(want) {
		t.Errorf("stored CreatedAt is %v, want %v; a restore re-dated the workflow",
			got.CreatedAt.UTC(), want)
	}
}

// Every workflow that predates the column has a NULL in it, and NULL sorts to
// opposite ends on different engines — first in a PostgreSQL DESC, last in
// sqlite's. Init backfills them so the existing estate has one order, not one
// per driver.
func TestInitBackfillsWorkflowsWithNoCreationTime(t *testing.T) {
	s, db := workflowStore(t)
	ctx := t.Context()

	if err := s.CreateWorkflow(ctx, storage.Workflow{ID: "wf-old", Name: "predates the column"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The shape autoMigrate leaves behind: the column is there, the value is not.
	if _, err := db.ExecContext(ctx, "UPDATE workflows SET created_at = NULL"); err != nil {
		t.Fatalf("blank the creation time: %v", err)
	}

	// The upgrade: the binary restarts against the migrated database.
	if err := s.Init(ctx); err != nil {
		t.Fatalf("re-init: %v", err)
	}

	got, err := s.GetWorkflow(ctx, "wf-old")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CreatedAt.IsZero() {
		t.Error("a workflow left with a NULL created_at by the migration still has one " +
			"after Init; on PostgreSQL those rows sort ahead of every dated workflow")
	}
}

// Case.
//
// case_sensitive_like makes sqlite's LIKE behave the way PostgreSQL's always
// has, which is the only way this assertion can fail here for the reason it
// fails in production.
func TestWorkflowSearchIgnoresCase(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	ctx := t.Context()

	if err := s.CreateWorkflow(ctx, storage.Workflow{ID: "wf-1", Name: "Order Ingest"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, search := range []string{"order ingest", "ORDER INGEST", "Order Ingest", "oRdEr"} {
		got, total, err := s.ListWorkflows(ctx, storage.CommonFilter{Search: search})
		if err != nil {
			t.Errorf("searching for %q failed: %v", search, err)
			continue
		}
		if len(got) != 1 || got[0].ID != "wf-1" {
			t.Errorf("searching for %q returned %v, want only wf-1 (%q)\n"+
				"LIKE compares the column as stored, and PostgreSQL does not fold case",
				search, workflowIDs(got), "Order Ingest")
		}
		if total != 1 {
			t.Errorf("searching for %q reported total %d, want 1; the count query "+
				"matches differently from the page query", search, total)
		}
	}
}

// Contains, not prefix and not equality — and still only the rows that contain
// it, with the LIKE wildcards an operator types treated as text.
func TestWorkflowSearchMatchesAnywhereInTheName(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	ctx := t.Context()

	seeded := map[string]string{
		"wf-1": "Nightly Order Ingest",
		"wf-2": "customer-sync",
		"wf-3": "50% Sampled Trace",
		"wf-4": "a_c reconciliation",
	}
	for id, name := range seeded {
		if err := s.CreateWorkflow(ctx, storage.Workflow{ID: id, Name: name}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	for _, tc := range []struct{ search, want string }{
		{"ORDER", "wf-1"},   // in the middle, and the wrong case
		{"nightly", "wf-1"}, // at the start
		{"INGEST", "wf-1"},  // at the end
		{"-SYNC", "wf-2"},   // spanning a separator
		{"50% ", "wf-3"},    // a per-cent sign is text, not "match anything"
		{"a_c", "wf-4"},     // an underscore is text, not "any character"
	} {
		got, total, err := s.ListWorkflows(ctx, storage.CommonFilter{Search: tc.search})
		if err != nil {
			t.Errorf("searching for %q failed: %v", tc.search, err)
			continue
		}
		if len(got) != 1 || got[0].ID != tc.want || total != 1 {
			t.Errorf("searching for %q returned %v (total %d), want only %s (%q)",
				tc.search, workflowIDs(got), total, tc.want, seeded[tc.want])
		}
	}

	got, total, err := s.ListWorkflows(ctx, storage.CommonFilter{Search: "zzz"})
	if err != nil {
		t.Fatalf("searching for %q failed: %v", "zzz", err)
	}
	if len(got) != 0 || total != 0 {
		t.Errorf("searching for %q returned %v (total %d), want nothing",
			"zzz", workflowIDs(got), total)
	}
}

// The two backends have to search the same fields, or which one is deployed
// changes what the search box finds. Mongo has always matched _id, name and
// vhost for workflows (searchAcross in mongodb.go) and SQL matched name alone,
// so an operator pasting a workflow id into the box got a hit on one backend
// and nothing on the other. Sources and sinks already agree across both.
func TestWorkflowSearchMatchesIDAndVHostAsWellAsName(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	ctx := t.Context()

	// One token per column, unique across the table, so a match can only have
	// come through the column it is testing — and in the wrong case, so the
	// folding is tested in each of the three.
	for _, seed := range []struct{ id, name, vhost string }{
		{"wf-QQAlpha", "one", "va"},
		{"wf-b", "QQBravo Ingest", "vb"},
		{"wf-c", "three", "QQCharlie"},
	} {
		if err := s.CreateWorkflow(ctx, storage.Workflow{
			ID: seed.id, Name: seed.name, VHost: seed.vhost,
		}); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}

	for _, tc := range []struct{ search, want, column string }{
		{"qqalpha", "wf-QQAlpha", "id"},
		{"QQBRAVO", "wf-b", "name"},
		{"qqcharlie", "wf-c", "vhost"},
	} {
		got, total, err := s.ListWorkflows(ctx, storage.CommonFilter{Search: tc.search})
		if err != nil {
			t.Errorf("searching for %q failed: %v", tc.search, err)
			continue
		}
		if len(got) != 1 || got[0].ID != tc.want || total != 1 {
			t.Errorf("searching for %q returned %v (total %d), want only %s\n"+
				"the match has to come through the %s column, which the mongo backend "+
				"already searches", tc.search, workflowIDs(got), total, tc.want, tc.column)
		}
	}
}

// The same two assertions against real PostgreSQL.
//
// This is the one that matters. Both behaviours were already correct on sqlite
// by accident — its LIKE folds ASCII case on its own, and an in-memory table it
// just wrote comes back in insertion order whether or not anything asked it to.
// PostgreSQL does neither, and PostgreSQL is what Hermod deploys on, so a green
// sqlite suite was never evidence about the driver with the bug.
func TestWorkflowListAndSearchOnPostgres(t *testing.T) {
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

	// A unique token in every name, so a shared database cannot change the
	// answer and a search for it scopes to exactly these three rows.
	token := fmt.Sprintf("wflist%d", time.Now().UnixNano())
	base := time.Now().UTC().Truncate(time.Second)
	seeded := []struct {
		id, name string
		age      time.Duration
	}{
		{token + "-mid", "Middle " + token + " Sync", 2 * time.Hour},
		{token + "-old", "Oldest " + token + " Sync", 9 * time.Hour},
		{token + "-new", "Newest " + token + " Sync", 0},
	}
	for _, seed := range seeded {
		if err := s.CreateWorkflow(ctx, storage.Workflow{
			ID: seed.id, Name: seed.name, CreatedAt: base.Add(-seed.age),
		}); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}
	t.Cleanup(func() {
		for _, seed := range seeded {
			_, _ = db.ExecContext(context.Background(), `DELETE FROM workflows WHERE id = $1`, seed.id)
		}
	})

	// Case: the token is lower-case in the stored names, so an upper-case search
	// for it is the exact shape that returned nothing before.
	for _, search := range []string{token, strings.ToUpper(token), " " + token + " "} {
		got, total, err := s.ListWorkflows(ctx, storage.CommonFilter{Search: search})
		if err != nil {
			t.Errorf("searching for %q failed: %v", search, err)
			continue
		}
		if len(got) != 3 || total != 3 {
			t.Errorf("searching for %q returned %v (total %d), want the 3 seeded workflows\n"+
				"PostgreSQL's LIKE compares the column as stored and folds no case",
				search, workflowIDs(got), total)
			continue
		}
		// Order: newest first, on the engine that has no reason to return
		// insertion order.
		want := []string{token + "-new", token + "-mid", token + "-old"}
		if !sameIDs(got, want) {
			t.Errorf("searching for %q returned %v, want %v", search, workflowIDs(got), want)
		}
	}

	// Paging that order must hand back each row exactly once.
	seen := map[string]int{}
	for page := 1; page <= 3; page++ {
		got, _, err := s.ListWorkflows(ctx, storage.CommonFilter{Search: token, Limit: 2, Page: page})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, wf := range got {
			seen[wf.ID]++
		}
	}
	for _, seed := range seeded {
		if seen[seed.id] != 1 {
			t.Errorf("paging returned %s %d times, want exactly 1 (saw %v)", seed.id, seen[seed.id], seen)
		}
	}
}
