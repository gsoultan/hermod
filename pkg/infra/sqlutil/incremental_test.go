package sqlutil

import (
	"regexp"
	"strings"
	"testing"
)

// TestBuildIncrementalQuery_PerDialect pins the exact SQL for every supported
// dialect. These strings are the contract: a connector that stops matching one
// has drifted, which is how the Oracle row-skipping bug got in.
func TestBuildIncrementalQuery_PerDialect(t *testing.T) {
	cases := []struct {
		driver string
		want   string
	}{
		{
			driver: "postgres",
			want:   `SELECT * FROM "t" WHERE "id" > $1 ORDER BY "id" ASC LIMIT 1`,
		},
		{
			driver: "yugabyte",
			want:   `SELECT * FROM "t" WHERE "id" > $1 ORDER BY "id" ASC LIMIT 1`,
		},
		{
			driver: "mysql",
			want:   "SELECT * FROM `t` WHERE `id` > ? ORDER BY `id` ASC LIMIT 1",
		},
		{
			driver: "mariadb",
			want:   "SELECT * FROM `t` WHERE `id` > ? ORDER BY `id` ASC LIMIT 1",
		},
		{
			driver: "sqlite",
			want:   "SELECT * FROM `t` WHERE `id` > ? ORDER BY `id` ASC LIMIT 1",
		},
		{
			driver: "clickhouse",
			want:   `SELECT * FROM "t" WHERE "id" > ? ORDER BY "id" ASC LIMIT 1`,
		},
		{
			// Upper case, because Snowflake folds unquoted identifiers that
			// way: quoting the name as typed would address a lower-case
			// column ordinary DDL never created. See
			// TestQuotingFollowsTheDialectsOwnCaseFolding.
			driver: "snowflake",
			want:   `SELECT * FROM "T" WHERE "ID" > ? ORDER BY "ID" ASC LIMIT 1`,
		},
		{
			// SQL Server has no LIMIT; TOP binds after ORDER BY is applied.
			driver: "mssql",
			want:   `SELECT TOP 1 * FROM [t] WHERE [id] > @p1 ORDER BY [id] ASC`,
		},
		{
			// DB2 uses the SQL-standard row-limiting clause, which is applied
			// after ORDER BY.
			driver: "db2",
			want:   `SELECT * FROM "t" WHERE "id" > ? ORDER BY "id" ASC FETCH FIRST 1 ROWS ONLY`,
		},
		{
			// Oracle: ROWNUM is assigned during predicate evaluation, BEFORE
			// ORDER BY. It must therefore be applied to an already-ordered
			// subquery, never in the same WHERE clause as the watermark.
			// Identifiers are upper-cased for the same reason as Snowflake
			// above; verified against a real Oracle server, where the
			// lower-case form failed every statement with ORA-00904.
			driver: "oracle",
			want:   `SELECT * FROM (SELECT * FROM "T" WHERE "ID" > :1 ORDER BY "ID" ASC) WHERE ROWNUM <= 1`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.driver, func(t *testing.T) {
			got, err := BuildIncrementalQuery(tc.driver, "t", "id")
			if err != nil {
				t.Fatalf("BuildIncrementalQuery(%q) returned error: %v", tc.driver, err)
			}
			if got != tc.want {
				t.Errorf("BuildIncrementalQuery(%q)\n got: %s\nwant: %s", tc.driver, got, tc.want)
			}
		})
	}
}

// TestBuildIncrementalQuery_RowLimitNeverPrecedesOrdering is the regression test
// for the class of bug this helper exists to prevent.
//
// A watermark poller advances its cursor to the id of whatever row came back. If
// the engine is allowed to pick the row BEFORE sorting, it returns an arbitrary
// row matching id > watermark, the cursor jumps to it, and every row with a
// smaller id is skipped forever — silent, non-deterministic data loss.
//
// The original Oracle query was:
//
//	SELECT * FROM t WHERE id > ? AND ROWNUM <= 1 ORDER BY id ASC
//
// which is exactly that mistake. This test asserts the shape is impossible for
// every dialect, so a future connector cannot reintroduce it.
func TestBuildIncrementalQuery_RowLimitNeverPrecedesOrdering(t *testing.T) {
	// Row-limiting constructs that Oracle/SQL Server evaluate against the
	// unordered row set when they appear in a query's own WHERE clause.
	preOrderLimiters := regexp.MustCompile(`(?i)\bROWNUM\b`)

	for _, driver := range []string{
		"postgres", "yugabyte", "mysql", "mariadb", "sqlite",
		"clickhouse", "snowflake", "mssql", "db2", "oracle",
	} {
		t.Run(driver, func(t *testing.T) {
			q, err := BuildIncrementalQuery(driver, "t", "id")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !strings.Contains(strings.ToUpper(q), "ORDER BY") {
				t.Fatalf("query has no ORDER BY, cursor advance is non-deterministic: %s", q)
			}

			// If ROWNUM is used at all, it must sit outside the ordered
			// subquery — i.e. after the closing paren, never in the same
			// clause as the watermark predicate.
			if loc := preOrderLimiters.FindStringIndex(q); loc != nil {
				closeParen := strings.LastIndex(q, ")")
				if closeParen == -1 || loc[0] < closeParen {
					t.Errorf("ROWNUM is evaluated before ORDER BY, rows will be skipped: %s", q)
				}
				// The watermark predicate must be inside the subquery.
				if idx := strings.Index(q, "> :1"); idx == -1 || idx > closeParen {
					t.Errorf("watermark predicate is outside the ordered subquery: %s", q)
				}
			}
		})
	}
}

// TestBuildIncrementalQuery_RejectsUnknownDriver: a query builder that guesses
// is how the wrong dialect ships. Fail closed instead.
func TestBuildIncrementalQuery_RejectsUnknownDriver(t *testing.T) {
	if _, err := BuildIncrementalQuery("nosuchdb", "t", "id"); err == nil {
		t.Fatal("expected an error for an unknown driver, got nil")
	}
}

// TestBuildIncrementalQuery_RejectsBadIdentifiers keeps the builder injection
// safe: identifiers are validated and quoted, never interpolated raw.
func TestBuildIncrementalQuery_RejectsBadIdentifiers(t *testing.T) {
	cases := []struct{ name, table, id string }{
		{"table injection", `users"; DROP TABLE users--`, "id"},
		{"id injection", "users", `id" OR "1"="1`},
		{"empty table", "", "id"},
		{"empty id", "users", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildIncrementalQuery("postgres", tc.table, tc.id); err == nil {
				t.Fatalf("expected an error for %s, got nil", tc.name)
			}
		})
	}
}

func TestBuildFirstRowQuery_PerDialect(t *testing.T) {
	cases := []struct{ driver, want string }{
		{"postgres", `SELECT * FROM "t" LIMIT 1`},
		{"mysql", "SELECT * FROM `t` LIMIT 1"},
		{"sqlite", "SELECT * FROM `t` LIMIT 1"},
		{"clickhouse", `SELECT * FROM "t" LIMIT 1`},
		{"mssql", `SELECT TOP 1 * FROM [t]`},
		{"db2", `SELECT * FROM "t" FETCH FIRST 1 ROWS ONLY`},
		{"oracle", `SELECT * FROM "T" WHERE ROWNUM <= 1`},
	}
	for _, tc := range cases {
		t.Run(tc.driver, func(t *testing.T) {
			got, err := BuildFirstRowQuery(tc.driver, "t")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("BuildFirstRowQuery(%q)\n got: %s\nwant: %s", tc.driver, got, tc.want)
			}
		})
	}
}

// TestBuildFirstRowQuery_OracleRownumIsSafeWithoutOrdering documents why ROWNUM
// alone is acceptable here but not in BuildIncrementalQuery: this query has no
// watermark and no cursor to advance, so "any one row" is the intended result.
func TestBuildFirstRowQuery_OracleRownumIsSafeWithoutOrdering(t *testing.T) {
	q, err := BuildFirstRowQuery("oracle", "t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.ToUpper(q), "ORDER BY") {
		t.Errorf("first-row probe should not pay for a sort: %s", q)
	}
}
