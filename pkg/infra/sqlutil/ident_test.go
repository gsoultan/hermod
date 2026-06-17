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
