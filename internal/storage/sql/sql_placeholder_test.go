package sql

import "testing"

func TestPreparePlaceholders_PGX_Extra(t *testing.T) {
	s := &sqlStorage{driver: "pgx"}
	in := "SELECT * FROM t WHERE a = ? AND b = ? LIMIT ? OFFSET ?"
	want := "SELECT * FROM t WHERE a = $1 AND b = $2 LIMIT $3 OFFSET $4"
	got := s.preparePlaceholders(in)
	if got != want {
		t.Fatalf("pgx placeholders: want %q, got %q", want, got)
	}
}

func TestPreparePlaceholders_SQLServer_Extra(t *testing.T) {
	s := &sqlStorage{driver: "sqlserver"}
	in := "INSERT INTO t (a,b,c) VALUES (?, ?, ?)"
	want := "INSERT INTO t (a,b,c) VALUES (@p1, @p2, @p3)"
	got := s.preparePlaceholders(in)
	if got != want {
		t.Fatalf("sqlserver placeholders: want %q, got %q", want, got)
	}
}

func TestPreparePlaceholders_Default_Extra(t *testing.T) {
	s := &sqlStorage{driver: "sqlite"}
	in := "UPDATE t SET a = ? WHERE id = ?"
	want := in
	got := s.preparePlaceholders(in)
	if got != want {
		t.Fatalf("default placeholders should be unchanged: want %q, got %q", want, got)
	}
}
