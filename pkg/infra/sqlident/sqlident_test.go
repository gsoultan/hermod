package sqlident

import (
	"strings"
	"testing"
)

// TestValidateRejectsInjection is the reason this package exists. Each payload
// is something that could reach a sink as a table name — from sink config set
// by an Editor, or from the wire, since the WebSocket source takes a message's
// table straight from the inbound envelope. All of them end up interpolated
// into a statement, because no driver accepts a placeholder for an identifier.
func TestValidateRejectsInjection(t *testing.T) {
	hostile := []struct {
		name    string
		payload string
	}{
		{"statement terminator", "users; DROP TABLE users--"},
		{"stacked query", "t; DELETE FROM audit_logs"},
		{"comment truncation", "users--"},
		{"block comment", "users/*x*/"},
		{"double quote escape", `users" ; DROP TABLE x; --`},
		{"backtick escape", "users` ; DROP TABLE x; --"},
		{"bracket escape", "users] ; DROP TABLE x; --"},
		{"single quote", "users'"},
		{"union select", "users UNION SELECT * FROM secrets"},
		{"whitespace", "my table"},
		{"newline", "users\nDROP TABLE x"},
		{"tab", "users\tx"},
		{"null byte", "users\x00"},
		{"parenthesis", "users()"},
		{"star", "users*"},
		{"leading digit", "1users"},
		{"empty", ""},
		{"only a dot", "."},
		{"empty schema", ".users"},
		{"empty table", "users."},
		{"three parts", "db.schema.table"},
		{"non-ascii homoglyph", "usеrs"}, // Cyrillic 'е'
		{"too long", strings.Repeat("a", MaxLength+1)},
		{"path traversal", "../etc/passwd"},
		{"backslash", `users\x`},
		{"semicolon only", ";"},
	}

	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.payload); err == nil {
				t.Errorf("Validate(%q) accepted a hostile identifier; it would be pasted "+
					"straight into a SQL statement", tc.payload)
			}
			// Quote must refuse it too — quoting is not a substitute for
			// validation, and a caller that only uses Quote must be safe.
			if _, err := Quote(Postgres, tc.payload); err == nil {
				t.Errorf("Quote(%q) accepted a hostile identifier", tc.payload)
			}
		})
	}
}

// TestValidateAcceptsRealTableNames keeps the allowlist from being so strict it
// breaks legitimate use. Over-rejecting here means a user cannot name their
// table what it is actually called.
func TestValidateAcceptsRealTableNames(t *testing.T) {
	legitimate := []string{
		"users",
		"Users",
		"user_accounts",
		"_internal",
		"t1",
		"orders2024",
		"public.users",
		"my_schema.my_table",
		"A",
		strings.Repeat("a", MaxLength),
	}

	for _, name := range legitimate {
		t.Run(name, func(t *testing.T) {
			if err := Validate(name); err != nil {
				t.Errorf("Validate(%q) rejected a legitimate table name: %v", name, err)
			}
		})
	}
}

// TestQuotePerDialect pins the quoting so a name that collides with a reserved
// word still works once it has been validated.
func TestQuotePerDialect(t *testing.T) {
	cases := []struct {
		dialect Dialect
		in      string
		want    string
	}{
		{Postgres, "users", `"users"`},
		{Postgres, "public.users", `"public"."users"`},
		{SQLite, "order", `"order"`},
		{MySQL, "users", "`users`"},
		{MySQL, "db.users", "`db`.`users`"},
		{ClickHouse, "events", "`events`"},
		{MSSQL, "users", "[users]"},
		{MSSQL, "dbo.users", "[dbo].[users]"},
		{Dialect("unknown"), "users", `"users"`},
	}

	for _, tc := range cases {
		got, err := Quote(tc.dialect, tc.in)
		if err != nil {
			t.Errorf("Quote(%s, %q): unexpected error %v", tc.dialect, tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Quote(%s, %q) = %q, want %q", tc.dialect, tc.in, got, tc.want)
		}
	}
}

// TestQuotedOutputCannotEscapeItsQuotes is the property that makes quoting
// meaningful: because validation already excluded every quote character, no
// validated name can terminate its own quoting.
func TestQuotedOutputCannotEscapeItsQuotes(t *testing.T) {
	for _, d := range []Dialect{Postgres, MySQL, MSSQL, SQLite, ClickHouse} {
		for _, name := range []string{"users", "public.users", "_x1"} {
			got, err := Quote(d, name)
			if err != nil {
				t.Fatalf("Quote(%s, %q): %v", d, name, err)
			}
			inner := strings.NewReplacer(`"`, "", "`", "", "[", "", "]", "", ".", "").Replace(got)
			if strings.ContainsAny(inner, "\"`[];-/*' \t\n") {
				t.Errorf("Quote(%s, %q) = %q contains a character that could break out", d, name, got)
			}
		}
	}
}
