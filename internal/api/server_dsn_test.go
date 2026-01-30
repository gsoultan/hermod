package api

import "testing"

func TestMaskDSN_SQLite(t *testing.T) {
	in := "hermod.db"
	out := maskDSN("sqlite", in)
	if out != in {
		t.Fatalf("expected unchanged for sqlite, got %q", out)
	}
}

func TestMaskDSN_PostgresURL(t *testing.T) {
	in := "postgres://user:pass@localhost:5432/db?sslmode=disable"
	out := maskDSN("postgres", in)
	if out == in {
		t.Fatalf("expected masked url, got unchanged")
	}
	if !containsAll(out, []string{"postgres://", "user:", "@localhost:5432", "/db"}) {
		t.Fatalf("masked url unexpected: %s", out)
	}
	if !contains(out, "****") && !contains(out, "%2A%2A%2A%2A") {
		t.Fatalf("password should be masked (either **** or encoded): %s", out)
	}
}

func TestMaskDSN_MySQLDSN(t *testing.T) {
	in := "user:secret@tcp(localhost:3306)/db"
	out := maskDSN("mysql", in)
	if out == in {
		t.Fatalf("expected masked DSN, got unchanged")
	}
	if !containsAll(out, []string{"user:****@tcp(localhost:3306)/db"}) {
		t.Fatalf("unexpected masked DSN: %s", out)
	}
	if contains(out, "secret") {
		t.Fatalf("password should be masked: %s", out)
	}
}

// tiny helpers to avoid importing strings in test file
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}
