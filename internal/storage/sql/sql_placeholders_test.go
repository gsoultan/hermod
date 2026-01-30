package sql

import "testing"

func TestPreparePlaceholders_PGX(t *testing.T) {
	s := &sqlStorage{driver: "pgx"}
	in := "SELECT * FROM t WHERE a = ? AND b = ? LIMIT ? OFFSET ?"
	got := s.preparePlaceholders(in)
	want := "SELECT * FROM t WHERE a = $1 AND b = $2 LIMIT $3 OFFSET $4"
	if got != want {
		t.Fatalf("pgx rewrite mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestPreparePlaceholders_SQLServer(t *testing.T) {
	s := &sqlStorage{driver: "sqlserver"}
	in := "UPDATE t SET a = ? WHERE id = ?"
	got := s.preparePlaceholders(in)
	want := "UPDATE t SET a = @p1 WHERE id = @p2"
	if got != want {
		t.Fatalf("sqlserver rewrite mismatch:\n got:  %q\n want: %q", got, want)
	}
}

func TestPreparePlaceholders_SQLite_NoChange(t *testing.T) {
	s := &sqlStorage{driver: "sqlite"}
	in := "INSERT INTO t (a, b) VALUES (?, ?)"
	got := s.preparePlaceholders(in)
	if got != in {
		t.Fatalf("sqlite should not rewrite placeholders: got %q want %q", got, in)
	}
}
