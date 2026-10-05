package sqlutil

import (
	"database/sql"
)

// DefaultMaxRows is the maximum number of rows ScanRows will fetch to prevent OOM.
const DefaultMaxRows = 1000

// ScanRows scans sql.Rows into a slice of maps. It is hard-limited to DefaultMaxRows.
//
// Values go through RecordFromValues, so a column the driver names JSON or
// JSONB arrives as the value its document describes and every other column
// keeps the shape it has always had -- driver []byte becomes a string.
//
// This is the generic database/sql read path: db_lookup uses it in both
// key-column and query-template mode, and the editor's SQL builder falls back
// to it whenever a query binds arguments. Without the JSON half, a `jsonb`
// column reached transformations, sinks and the message trace as a string
// holding JSON, while the very same column fetched through pgx natively
// (PostgresSource.ExecuteSQL, and the source's snapshot and polling paths)
// arrived as a map. One document, two shapes, decided by which path fetched it.
func ScanRows(rows *sql.Rows) ([]map[string]any, error) {
	results, _, err := scanRows(rows, false)
	return results, err
}

// ScanRowsCounted is ScanRows for a statement that writes and returns what it
// wrote -- INSERT ... RETURNING, UPDATE ... OUTPUT. It keeps the same bounded
// set of rows, then reads the rest of the result without keeping it and reports
// how many rows there were in all.
//
// Stopping at the cap is right for a SELECT. For a write it means closing a
// cursor the statement is still feeding, which a driver may answer by
// cancelling the statement, and it leaves the number of rows written unknown.
func ScanRowsCounted(rows *sql.Rows) ([]map[string]any, int64, error) {
	return scanRows(rows, true)
}

func scanRows(rows *sql.Rows, drain bool) ([]map[string]any, int64, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}
	// Read once, outside the loop: the description is the same for every row,
	// and ColumnTypes allocates a ColumnType per column each time it is called.
	typeNames := ColumnTypeNames(rows)

	var results []map[string]any
	var total int64
	for rows.Next() {
		if total >= DefaultMaxRows {
			if !drain {
				break
			}
			total++
			continue
		}
		total++
		columns := make([]any, len(cols))
		columnPointers := make([]any, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			return nil, 0, err
		}

		results = append(results, RecordFromValues(cols, typeNames, columns))
	}
	// A mid-stream failure must surface as an error rather than returning a
	// silently truncated result set -- the same guard PostgresSource.ExecuteSQL
	// carries on its own scan loop.
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return results, total, nil
}
