package sqlutil

import (
	"strings"
	"testing"
)

func TestValidateIdent(t *testing.T) {
	tests := []struct {
		name    string
		ident   string
		wantErr bool
	}{
		{"Simple", "users", false},
		{"Qualified", "public.users", false},
		{"Underscore", "user_accounts", false},
		{"Numeric", "table123", false},
		{"Empty", "", true},
		{"Space", "user accounts", true},
		{"SingleQuoteInjection", "users; DROP TABLE users--", true},
		{"DoubleQuote", `users"`, true},
		{"Backtick", "users`", true},
		{"Parenthesis", "users()", true},
		{"Semicolon", "users;", true},
		{"DotOnly", ".", true},
		{"DoubleDot", "..", true},
		{"TrailingDot", "users.", true},
		{"LeadingDot", ".users", true},
		{"ConsecutiveDots", "public..users", true},
		{"TooLong", strings.Repeat("a", maxIdentLen+1), true},
		{"MaxLength", strings.Repeat("a", maxIdentLen), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateIdent(tc.ident)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateIdent(%q) error = %v, wantErr %v", tc.ident, err, tc.wantErr)
			}
		})
	}
}

func TestQuoteIdentRejectsInjection(t *testing.T) {
	if _, err := QuoteIdent("postgres", `users"; DROP TABLE users--`); err == nil {
		t.Fatal("expected QuoteIdent to reject an identifier containing a quote")
	}

	quoted, err := QuoteIdent("postgres", "public.users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(quoted, `"public"."users"`) {
		t.Errorf("unexpected quoting result: %s", quoted)
	}
}

func TestCanonicalDriver(t *testing.T) {
	tests := []struct {
		name       string
		sourceType string
		want       string
		wantOK     bool
	}{
		{"Postgres", "postgres", "pgx", true},
		{"Yugabyte", "yugabyte", "pgx", true},
		{"PgxPassthrough", "pgx", "pgx", true},
		{"MySQL", "mysql", "mysql", true},
		{"MariaDB", "mariadb", "mysql", true},
		{"SQLite", "sqlite", "sqlite", true},
		{"MSSQL", "mssql", "sqlserver", true},
		{"SQLServerPassthrough", "sqlserver", "sqlserver", true},
		{"Oracle", "oracle", "oracle", true},
		{"ClickHouse", "clickhouse", "clickhouse", true},
		{"Snowflake", "snowflake", "snowflake", true},
		{"Unknown", "mongodb", "", false},
		{"Empty", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := CanonicalDriver(tc.sourceType)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("CanonicalDriver(%q) = (%q, %v); want (%q, %v)", tc.sourceType, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestPlaceholder locks in the driver<->placeholder contract. The MSSQL cases
// guard against a regression where the microsoft/go-mssqldb "sqlserver" driver
// was handed '?' placeholders ("Incorrect syntax near '?'.").
func TestPlaceholder(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		index  int
		want   string
	}{
		{"Postgres", "postgres", 1, "$1"},
		{"Yugabyte", "yugabyte", 5, "$5"},
		{"Oracle", "oracle", 3, ":3"},
		{"MSSQLType", "mssql", 1, "@p1"},
		{"SQLServerDriver", "sqlserver", 4, "@p4"},
		{"MySQL", "mysql", 1, "?"},
		{"MariaDB", "mariadb", 2, "?"},
		{"SQLite", "sqlite", 9, "?"},
		{"ClickHouse", "clickhouse", 1, "?"},
		{"Snowflake", "snowflake", 2, "?"},
		{"UnknownFallback", "cassandra", 1, "?"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Placeholder(tc.driver, tc.index); got != tc.want {
				t.Errorf("Placeholder(%q, %d) = %q; want %q", tc.driver, tc.index, got, tc.want)
			}
		})
	}
}

// TestQuoteIdentRejectsMalformedDots guards against the latent builder bug where a
// permissive regex accepted empty dot segments, producing structurally invalid SQL
// like "schema"."" instead of failing closed at validation time.
func TestQuoteIdentRejectsMalformedDots(t *testing.T) {
	bad := []string{".", "..", "users.", ".users", "a..b", "public..t"}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			if got, err := QuoteIdent("postgres", name); err == nil {
				t.Errorf("QuoteIdent(%q) = (%q, nil); want rejection", name, got)
			}
		})
	}
}

// TestQuoteIdentEscapesCloseQuote verifies the defense-in-depth doubling of the
// closing quote per driver, so the quoter stays injection-safe even if the
// validation allow-list is ever loosened.
func TestQuoteIdentEscapesCloseQuote(t *testing.T) {
	tests := []struct {
		name    string
		driver  string
		segment string
		want    string
	}{
		{"MSSQL", "mssql", "a]b", "[a]]b]"},
		{"SQLServer", "sqlserver", "a]b", "[a]]b]"},
		{"MySQL", "mysql", "a`b", "`a``b`"},
		{"Postgres", "postgres", `a"b`, `"a""b"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteSegment(tc.driver, tc.segment); got != tc.want {
				t.Errorf("quoteSegment(%q, %q) = %q; want %q", tc.driver, tc.segment, got, tc.want)
			}
		})
	}
}

// TestQuoteIdentPostgresPath locks in the Postgres quoting + placeholder contract:
// identifiers are double-quoted (case-sensitive) and placeholders are positional $N.
func TestQuoteIdentPostgresPath(t *testing.T) {
	got, err := QuoteIdent("postgres", "public.users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `"public"."users"` {
		t.Errorf("QuoteIdent(postgres, public.users) = %q; want %q", got, `"public"."users"`)
	}
	if ph := Placeholder("postgres", 3); ph != "$3" {
		t.Errorf("Placeholder(postgres, 3) = %q; want %q", ph, "$3")
	}
}

// TestQuoteIdentLengthLimit ensures over-long identifiers fail closed at the common
// 63-byte limit instead of deferring to an opaque server error.
func TestQuoteIdentLengthLimit(t *testing.T) {
	ok := strings.Repeat("c", maxIdentLen)
	if _, err := QuoteIdent("postgres", ok); err != nil {
		t.Errorf("QuoteIdent(%d chars) unexpected error: %v", maxIdentLen, err)
	}
	tooLong := strings.Repeat("c", maxIdentLen+1)
	if _, err := QuoteIdent("postgres", tooLong); err == nil {
		t.Errorf("QuoteIdent(%d chars) = nil error; want rejection", maxIdentLen+1)
	}
}

// TestPlaceholderMatchesCanonicalDriver asserts the invariant that the placeholder
// style is determined solely by the canonical driver, so any user-facing alias of
// the same database produces an identical placeholder.
func TestPlaceholderMatchesCanonicalDriver(t *testing.T) {
	aliases := map[string][]string{
		"pgx":       {"postgres", "yugabyte", "pgx"},
		"mysql":     {"mysql", "mariadb"},
		"sqlserver": {"mssql", "sqlserver"},
	}
	for canonical, group := range aliases {
		want := Placeholder(canonical, 1)
		for _, alias := range group {
			if got := Placeholder(alias, 1); got != want {
				t.Errorf("alias %q placeholder = %q; want %q (canonical %q)", alias, got, want, canonical)
			}
		}
	}
}
