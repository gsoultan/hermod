package lookup

import "testing"

func TestBuildLookupQuery(t *testing.T) {
	tests := []struct {
		name        string
		driver      string
		selectList  string
		quotedTable string
		whereParts  []string
		batchMode   bool
		expected    string
	}{
		{
			name:        "PostgreSQL single",
			driver:      "pgx",
			selectList:  "*",
			quotedTable: `"users"`,
			whereParts:  []string{`"id" = $1`},
			batchMode:   false,
			expected:    `SELECT * FROM "users" WHERE "id" = $1 LIMIT 1`,
		},
		{
			name:        "MySQL single",
			driver:      "mysql",
			selectList:  "*",
			quotedTable: "`users`",
			whereParts:  []string{"`id` = ?"},
			batchMode:   false,
			expected:    "SELECT * FROM `users` WHERE `id` = ? LIMIT 1",
		},
		{
			name:        "MSSQL single",
			driver:      "mssql",
			selectList:  "*",
			quotedTable: "[users]",
			whereParts:  []string{"[id] = @p1"},
			batchMode:   false,
			expected:    "SELECT TOP 1 * FROM [users] WHERE [id] = @p1",
		},
		{
			name:        "Oracle single",
			driver:      "oracle",
			selectList:  "*",
			quotedTable: `"USERS"`,
			whereParts:  []string{`"ID" = :1`},
			batchMode:   false,
			expected:    `SELECT * FROM "USERS" WHERE "ID" = :1 FETCH FIRST 1 ROWS ONLY`,
		},
		{
			name:        "Batch mode",
			driver:      "pgx",
			selectList:  "*",
			quotedTable: `"users"`,
			whereParts:  []string{`"id" IN ($1, $2)`},
			batchMode:   true,
			expected:    `SELECT * FROM "users" WHERE "id" IN ($1, $2)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildLookupQuery(tc.driver, tc.selectList, tc.quotedTable, tc.whereParts, tc.batchMode)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}
