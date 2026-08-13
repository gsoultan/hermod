package sql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/user/hermod/internal/storage"
	_ "modernc.org/sqlite"
)

// What an operator types into a search box is text. The three storage backends
// have to agree on that, because which one is running is a deployment choice
// and the search box is the same screen either way.
//
// This backend builds "%" + search + "%" and hands it to LIKE, where "%" and
// "_" are wildcards. Searching for a per-cent sign therefore matched every row
// rather than the rows containing one.

func searchStore(t *testing.T) (storage.Storage, []string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	s := NewSQLStorage(db, "sqlite")
	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("init: %v", err)
	}

	names := []string{"alpha", "bravo", "50% off", "a_c"}
	for i, name := range names {
		if err := s.CreateSource(t.Context(), storage.Source{
			ID: string(rune('a'+i)) + "-src", Name: name, Type: "postgres",
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	return s, names
}

func namesOf(got []storage.Source) []string {
	out := make([]string, 0, len(got))
	for _, s := range got {
		out = append(out, s.Name)
	}
	return out
}

// A per-cent sign is the character this gets wrong most visibly: unescaped it
// is "match anything", so the search returns the whole table.
func TestSearchingForAPerCentSignFindsOnlyPerCentSigns(t *testing.T) {
	s, all := searchStore(t)

	got, total, err := s.ListSources(t.Context(), storage.CommonFilter{Search: "%"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].Name != "50% off" {
		t.Errorf("searching for %q returned %d of %d sources (%v), want only %q\n"+
			"the search text is concatenated into a LIKE pattern, so a per-cent sign "+
			"typed by an operator is read as \"match anything\"",
			"%", total, len(all), namesOf(got), "50% off")
	}
}

// The underscore is the quieter half of the same bug: it matches any single
// character, so a search silently returns rows that do not contain it.
func TestSearchingForAnUnderscoreFindsOnlyUnderscores(t *testing.T) {
	s, _ := searchStore(t)

	got, _, err := s.ListSources(t.Context(), storage.CommonFilter{Search: "_"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].Name != "a_c" {
		t.Errorf("searching for %q returned %v, want only %q\n"+
			"an underscore in a LIKE pattern matches any single character",
			"_", namesOf(got), "a_c")
	}
}

// Ordinary searches must keep working, including one that spans the escape
// character itself, so the fix cannot be "escape everything and match nothing".
func TestOrdinarySearchesStillMatch(t *testing.T) {
	s, _ := searchStore(t)

	for _, tc := range []struct{ search, want string }{
		{"alpha", "alpha"},
		{"BRAVO", "bravo"},     // LIKE is case-insensitive for ASCII in sqlite
		{"50% off", "50% off"}, // literal text containing a wildcard
	} {
		got, _, err := s.ListSources(t.Context(), storage.CommonFilter{Search: tc.search})
		if err != nil {
			t.Errorf("searching for %q failed: %v", tc.search, err)
			continue
		}
		if len(got) != 1 || got[0].Name != tc.want {
			t.Errorf("searching for %q returned %v, want only %q", tc.search, namesOf(got), tc.want)
		}
	}
}

// Nothing may match when nothing contains the text, including when the text is
// only escape characters.
func TestASearchThatMatchesNothingReturnsNothing(t *testing.T) {
	s, _ := searchStore(t)

	for _, search := range []string{"zzz", "!", "!!", "%%"} {
		got, total, err := s.ListSources(t.Context(), storage.CommonFilter{Search: search})
		if err != nil {
			t.Errorf("searching for %q failed: %v", search, err)
			continue
		}
		if len(got) != 0 || total != 0 {
			t.Errorf("searching for %q returned %v, want nothing", search, namesOf(got))
		}
	}
}

// The same assertions against real PostgreSQL.
//
// Postgres is the dialect where the ESCAPE clause could be lost: queries are
// written with `?` placeholders and rewritten to `$1` before they are sent, so
// a clause added to the SQL text has to survive that rewriting. sqlite runs the
// text as written and would never show the difference.
func TestSearchEscapingSurvivesPlaceholderRewritingOnPostgres(t *testing.T) {
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

	st := NewSQLStorage(db, "pgx")
	if err := st.Init(ctx); err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be initialised (%s): %v", dsn, err)
	}

	prefix := fmt.Sprintf("srch-%d", time.Now().UnixNano())
	seeded := map[string]string{
		prefix + "-a": "alpha",
		prefix + "-b": "50% off",
		prefix + "-c": "a_c",
	}
	for id, name := range seeded {
		if err := st.CreateSource(ctx, storage.Source{ID: id, Name: name, Type: "postgres"}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		for id := range seeded {
			_, _ = db.ExecContext(context.Background(), `DELETE FROM sources WHERE id = $1`, id)
		}
	})

	// Scoped by the unique prefix so other rows in a shared database cannot
	// change the answer.
	for _, tc := range []struct{ search, want string }{
		{prefix + "-b", "50% off"},
		{"50% off", "50% off"},
		{"a_c", "a_c"},
	} {
		got, _, err := st.ListSources(ctx, storage.CommonFilter{Search: tc.search})
		if err != nil {
			t.Errorf("searching for %q failed: %v\n"+
				"if this is a syntax error the ESCAPE clause did not survive placeholder rewriting",
				tc.search, err)
			continue
		}
		var matched []string
		for _, s := range got {
			if _, ours := seeded[s.ID]; ours {
				matched = append(matched, s.Name)
			}
		}
		if len(matched) != 1 || matched[0] != tc.want {
			t.Errorf("searching for %q matched %v of the seeded rows, want only %q",
				tc.search, matched, tc.want)
		}
	}

	// The wildcard itself must no longer act as one.
	got, _, err := st.ListSources(ctx, storage.CommonFilter{Search: "%"})
	if err != nil {
		t.Fatalf("searching for a per-cent sign failed: %v", err)
	}
	var wild []string
	for _, s := range got {
		if _, ours := seeded[s.ID]; ours {
			wild = append(wild, s.Name)
		}
	}
	if len(wild) != 1 || wild[0] != "50% off" {
		t.Errorf("searching for %q matched %v of the seeded rows, want only %q", "%", wild, "50% off")
	}
}
