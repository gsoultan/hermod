package sqlutil

import (
	"fmt"
)

// rowLimitStyle describes how a dialect restricts a result set to a single row,
// and — critically — whether that restriction is applied before or after ORDER BY.
type rowLimitStyle int

const (
	// limitSuffix appends "LIMIT 1" after ORDER BY. Applied post-sort.
	limitSuffix rowLimitStyle = iota
	// fetchFirst appends the SQL-standard "FETCH FIRST 1 ROWS ONLY". Post-sort.
	fetchFirst
	// topPrefix uses "SELECT TOP 1". SQL Server applies TOP after ORDER BY.
	topPrefix
	// rownumSubquery wraps an ordered subquery in "WHERE ROWNUM <= 1".
	//
	// Oracle assigns ROWNUM as rows are produced by the WHERE clause, BEFORE
	// ORDER BY runs. "WHERE id > :1 AND ROWNUM <= 1 ORDER BY id ASC" therefore
	// returns an ARBITRARY qualifying row, not the next one — so a watermark
	// cursor built on it skips rows permanently. The ordering has to be fully
	// materialised in a subquery before ROWNUM is allowed to see it.
	rownumSubquery
)

// limitStyleFor maps a user-facing source type to its row-limiting style.
//
// This deliberately does not route through CanonicalDriver: that function maps
// to database/sql driver names and does not cover engines with their own drivers
// (DB2). Row-limiting syntax is a property of the SQL dialect, not the driver.
func limitStyleFor(sourceType string) (rowLimitStyle, bool) {
	switch sourceType {
	case "postgres", "pgx", "yugabyte", "mysql", "mariadb", "sqlite", "clickhouse", "snowflake":
		return limitSuffix, true
	case "mssql", "sqlserver":
		return topPrefix, true
	case "db2":
		return fetchFirst, true
	case "oracle":
		return rownumSubquery, true
	default:
		return 0, false
	}
}

// BuildIncrementalQuery returns a dialect-correct query that selects the single
// next row after a watermark, ordered ascending by idField. The watermark value
// is bound as parameter 1; callers must pass it as the only query argument.
//
// Use this instead of hand-writing the query in a connector. Watermark polling
// looks trivial and is not: the row-limiting clause must be applied after the
// sort, and three of the four dialect families here spell that differently. Every
// connector that wrote its own got at least the Oracle case wrong.
//
// Both identifiers are validated and quoted, so the result is injection-safe.
// An unknown sourceType is an error rather than a guess — emitting plausible SQL
// for the wrong dialect is the failure mode this helper exists to prevent.
func BuildIncrementalQuery(sourceType, table, idField string) (string, error) {
	style, ok := limitStyleFor(sourceType)
	if !ok {
		return "", fmt.Errorf("no row-limiting syntax known for source type %q", sourceType)
	}

	quotedTable, err := QuoteIdent(sourceType, table)
	if err != nil {
		return "", fmt.Errorf("table: %w", err)
	}
	quotedID, err := QuoteIdent(sourceType, idField)
	if err != nil {
		return "", fmt.Errorf("id field: %w", err)
	}

	ph := Placeholder(sourceType, 1)

	switch style {
	case limitSuffix:
		return fmt.Sprintf(
			"SELECT * FROM %s WHERE %s > %s ORDER BY %s ASC LIMIT 1",
			quotedTable, quotedID, ph, quotedID,
		), nil
	case fetchFirst:
		return fmt.Sprintf(
			"SELECT * FROM %s WHERE %s > %s ORDER BY %s ASC FETCH FIRST 1 ROWS ONLY",
			quotedTable, quotedID, ph, quotedID,
		), nil
	case topPrefix:
		return fmt.Sprintf(
			"SELECT TOP 1 * FROM %s WHERE %s > %s ORDER BY %s ASC",
			quotedTable, quotedID, ph, quotedID,
		), nil
	case rownumSubquery:
		return fmt.Sprintf(
			"SELECT * FROM (SELECT * FROM %s WHERE %s > %s ORDER BY %s ASC) WHERE ROWNUM <= 1",
			quotedTable, quotedID, ph, quotedID,
		), nil
	default:
		return "", fmt.Errorf("unhandled row-limiting style for source type %q", sourceType)
	}
}

// BuildFirstRowQuery returns a dialect-correct query selecting any single row
// from a table, with no watermark and no ordering.
//
// It is used for schema probes and for the first read of a table that has no
// cursor yet. Ordering is intentionally omitted: there is no cursor to advance,
// so "any row" is the intended semantics and a sort would be wasted work on a
// large table.
func BuildFirstRowQuery(sourceType, table string) (string, error) {
	style, ok := limitStyleFor(sourceType)
	if !ok {
		return "", fmt.Errorf("no row-limiting syntax known for source type %q", sourceType)
	}

	quotedTable, err := QuoteIdent(sourceType, table)
	if err != nil {
		return "", fmt.Errorf("table: %w", err)
	}

	switch style {
	case limitSuffix:
		return "SELECT * FROM " + quotedTable + " LIMIT 1", nil
	case fetchFirst:
		return "SELECT * FROM " + quotedTable + " FETCH FIRST 1 ROWS ONLY", nil
	case topPrefix:
		return "SELECT TOP 1 * FROM " + quotedTable, nil
	case rownumSubquery:
		return "SELECT * FROM " + quotedTable + " WHERE ROWNUM <= 1", nil
	default:
		return "", fmt.Errorf("unhandled row-limiting style for source type %q", sourceType)
	}
}
