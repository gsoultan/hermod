package core

import (
	"testing"
)

// TestParameterizeTemplate verifies that {{ ... }} tokens are rewritten into the
// correct driver-specific bound placeholders and that the returned args slice is
// in ordinal order. The MSSQL case is the regression guard: the sqlserver driver
// requires @p1..@pN and rejects '?'.
func TestParameterizeTemplate(t *testing.T) {
	data := map[string]any{
		"after": map[string]any{
			"id":   "abc-123",
			"name": "widget",
		},
	}

	tests := []struct {
		name     string
		driver   string
		tpl      string
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "Postgres",
			driver:   "postgres",
			tpl:      "SELECT * FROM t WHERE id = {{ after.id }}",
			wantSQL:  "SELECT * FROM t WHERE id = $1",
			wantArgs: []any{"abc-123"},
		},
		{
			name:     "MSSQLTypeAlias",
			driver:   "mssql",
			tpl:      "SELECT * FROM t WHERE id = {{ after.id }} AND name = {{ after.name }}",
			wantSQL:  "SELECT * FROM t WHERE id = @p1 AND name = @p2",
			wantArgs: []any{"abc-123", "widget"},
		},
		{
			name:     "SQLServerDriverName",
			driver:   "sqlserver",
			tpl:      "INSERT INTO t (id) VALUES ({{ after.id }})",
			wantSQL:  "INSERT INTO t (id) VALUES (@p1)",
			wantArgs: []any{"abc-123"},
		},
		{
			name:     "MySQL",
			driver:   "mysql",
			tpl:      "SELECT * FROM t WHERE id = {{ after.id }}",
			wantSQL:  "SELECT * FROM t WHERE id = ?",
			wantArgs: []any{"abc-123"},
		},
		{
			name:     "NoTokens",
			driver:   "mssql",
			tpl:      "SELECT 1",
			wantSQL:  "SELECT 1",
			wantArgs: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSQL, gotArgs := ParameterizeTemplate(tc.driver, tc.tpl, data)
			if gotSQL != tc.wantSQL {
				t.Errorf("ParameterizeTemplate(%q) sql = %q; want %q", tc.driver, gotSQL, tc.wantSQL)
			}
			if len(gotArgs) != len(tc.wantArgs) {
				t.Fatalf("ParameterizeTemplate(%q) args len = %d; want %d", tc.driver, len(gotArgs), len(tc.wantArgs))
			}
			for i := range tc.wantArgs {
				if gotArgs[i] != tc.wantArgs[i] {
					t.Errorf("arg[%d] = %v; want %v", i, gotArgs[i], tc.wantArgs[i])
				}
			}
		})
	}
}
