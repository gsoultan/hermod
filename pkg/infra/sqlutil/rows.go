package sqlutil

import (
	"database/sql"
)

// DefaultMaxRows is the maximum number of rows ScanRows will fetch to prevent OOM.
const DefaultMaxRows = 1000

// ScanRows scans sql.Rows into a slice of maps. It is hard-limited to DefaultMaxRows.
func ScanRows(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]any
	rowCount := 0
	for rows.Next() {
		if rowCount >= DefaultMaxRows {
			break
		}
		rowCount++
		columns := make([]any, len(cols))
		columnPointers := make([]any, len(cols))
		for i := range columns {
			columnPointers[i] = &columns[i]
		}

		if err := rows.Scan(columnPointers...); err != nil {
			return nil, err
		}

		m := make(map[string]any)
		for i, colName := range cols {
			val := columns[i]
			if b, ok := val.([]byte); ok {
				m[colName] = string(b)
			} else {
				m[colName] = val
			}
		}
		results = append(results, m)
	}
	return results, nil
}
