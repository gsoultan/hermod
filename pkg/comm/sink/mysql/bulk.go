package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// bulkMode selects how WriteBatch applies a batch.
type bulkMode int

const (
	// bulkModeNone is the ordered, one-statement-per-message path. It is the
	// default and the only path that preserves CDC semantics (a delete followed
	// by an insert on the same key must be applied in that order).
	bulkModeNone bulkMode = iota
	// bulkModeMultiValues collapses the batch into multi-row INSERT statements,
	// turning one round trip per message into one per chunk.
	bulkModeMultiValues
)

func (m bulkMode) String() string {
	if m == bulkModeMultiValues {
		return "multi-values"
	}
	return "none"
}

// bulkMinRows is the batch size below which a multi-row INSERT is not worth
// building. Below it the per-statement path is competitive and simpler.
//
// A var rather than a const so a benchmark can pin the sink to the ordered
// path and measure the two against each other on the same server.
var bulkMinRows = 50

// maxBulkPlaceholders bounds one statement's parameter count.
//
// The MySQL protocol caps a prepared statement at 65535 placeholders, and
// exceeding it fails the whole statement rather than degrading. Chunking well
// under the cap also keeps each statement comfortably inside
// max_allowed_packet, which is the other limit a wide batch can hit.
//
// A var rather than a const so a test can lower it and actually cross a chunk
// boundary; forcing one at the real value would need 20,000 rows.
var maxBulkPlaceholders = 60000

// classifyBatch decides whether a batch may take the bulk path.
//
// Conservative by construction, the same way the Postgres sink's is:
// bulkModeMultiValues is returned only when every condition for safety is
// positively established, and anything unknown falls through to bulkModeNone.
// Losing per-row ordering to gain throughput would trade away the guarantee
// that makes Hermod useful for CDC, so the fast path is restricted to batches
// where ordering is not observable — inserts only, one target table, no
// soft-delete rewriting.
func (s *MySQLSink) classifyBatch(msgs []hermod.Message) bulkMode {
	if len(msgs) < bulkMinRows {
		return bulkModeNone
	}
	// Without mappings the column list is derived per message, so there is no
	// stable tuple shape to batch.
	if len(s.mappings) == 0 {
		return bulkModeNone
	}
	// Soft delete rewrites rows rather than inserting them.
	if strings.EqualFold(s.deleteStrategy, "soft_delete") || s.softDeleteColumn != "" {
		return bulkModeNone
	}
	// An explicit operation mode forces every message down a specific path
	// (e.g. always-update); only "auto" and "insert" are pure inserts.
	if s.operationMode != "" && !strings.EqualFold(s.operationMode, "auto") &&
		!strings.EqualFold(s.operationMode, "insert") {
		return bulkModeNone
	}
	// Per-message table routing would fan one batch across several tables.
	if s.tableName == "" {
		return bulkModeNone
	}

	for _, m := range msgs {
		if m == nil {
			return bulkModeNone
		}
		if s.resolveOperation(m) != hermod.OpCreate {
			return bulkModeNone
		}
		// A message routed at runtime to a different table cannot join the
		// single-table batch.
		if t := m.Table(); t != "" && t != s.tableName {
			return bulkModeNone
		}
	}
	return bulkModeMultiValues
}

// resolveOperation reports the operation a message will be applied as, after
// the sink's operation mode has had its say. It mirrors the switch in
// WriteBatch so classifyBatch cannot disagree with what actually runs.
func (s *MySQLSink) resolveOperation(msg hermod.Message) hermod.Operation {
	op := msg.Operation()
	switch strings.ToLower(s.operationMode) {
	case "insert":
		return hermod.OpCreate
	case "upsert", "update":
		return hermod.OpUpdate
	case "delete":
		return hermod.OpDelete
	}
	if op == "" {
		return hermod.OpCreate
	}
	if op == hermod.OpSnapshot {
		return hermod.OpCreate
	}
	return op
}

// bulkRows is a batch flattened into one column list and one row of values per
// message.
type bulkRows struct {
	columns []string // quoted, in statement order
	updates []string // "col = VALUES(col)" for every non-key column
	rows    [][]any
}

// buildBulkRows flattens msgs into a single tuple shape, or reports that it
// cannot.
//
// The column list has to be identical for every row, and upsertMapped drops an
// identity column whose value is empty — per message. So a batch where one
// message carries an id and the next does not has two different shapes, and
// emitting them as one multi-row INSERT would shift values into the wrong
// columns: a silent corruption, not an error. Establishing the shape from the
// first row and requiring every other row to match is what rules that out.
func (s *MySQLSink) buildBulkRows(msgs []hermod.Message) (*bulkRows, bool, error) {
	out := &bulkRows{rows: make([][]any, 0, len(msgs))}

	for i, msg := range msgs {
		cols, updates, args, err := s.buildBulkRow(msg)
		if err != nil {
			return nil, false, err
		}
		if len(cols) == 0 {
			return nil, false, nil
		}

		if i == 0 {
			out.columns = cols
			out.updates = updates
		} else if !sameColumns(out.columns, cols) {
			// Shapes differ across the batch; the ordered path handles it.
			return nil, false, nil
		}

		out.rows = append(out.rows, args)
	}

	return out, true, nil
}

// buildBulkRow returns one message's contribution: the quoted columns it fills,
// the ON DUPLICATE KEY UPDATE clauses for the non-key ones, and the values.
//
// The skip rule is identical to upsertMapped's, kept in step with it on
// purpose: the two must agree about which columns a message contributes, or
// the fast path writes a different row from the slow one.
func (s *MySQLSink) buildBulkRow(msg hermod.Message) (cols, updates []string, args []any, err error) {
	args = make([]any, 0, len(s.mappings))

	for _, m := range s.mappings {
		if m.SourceField == "" {
			continue
		}
		val := evaluator.GetMsgValByPath(msg, m.SourceField)
		if m.IsIdentity && (val == nil || val == "" || val == 0) {
			continue
		}

		q, qerr := qcol(m.TargetColumn)
		if qerr != nil {
			return nil, nil, nil, qerr
		}
		cols = append(cols, q)
		args = append(args, val)
		if !m.IsPrimaryKey {
			updates = append(updates, q+" = VALUES("+q+")")
		}
	}
	return cols, updates, args, nil
}

func sameColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// writeBatchMultiValues applies an insert-only batch as multi-row INSERTs.
//
// One statement per chunk rather than one per message: for a remote database
// that is the difference between N network round trips and a handful, which
// dominates everything else a sink does.
func (s *MySQLSink) writeBatchMultiValues(ctx context.Context, tx *sql.Tx, table string, msgs []hermod.Message) (bool, error) {
	built, ok, err := s.buildBulkRows(msgs)
	if err != nil || !ok {
		return false, err
	}

	rows := built.rows
	if keyIdx := bulkKeyIndexes(s.mappings, built.columns); len(keyIdx) > 0 {
		rows = dedupeByKeyLastWins(rows, keyIdx)
	}

	perRow := len(built.columns)
	chunk := maxBulkPlaceholders / perRow
	if chunk < 1 {
		// A single row already exceeds the placeholder cap; nothing here can
		// help, and the ordered path will report the real error.
		return false, nil
	}

	for start := 0; start < len(rows); start += chunk {
		part := rows[start:min(start+chunk, len(rows))]
		stmt, args := multiValuesStatement(table, built, part)
		if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
			return false, fmt.Errorf("mysql bulk insert of %d rows into %s: %w", len(part), table, err)
		}
	}
	return true, nil
}

// multiValuesStatement renders one chunk as a single INSERT ... VALUES (...),
// (...) statement and its flattened arguments.
func multiValuesStatement(table string, built *bulkRows, part [][]any) (string, []any) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "INSERT INTO %s (%s) VALUES ", table, strings.Join(built.columns, ", "))

	args := make([]any, 0, len(part)*len(built.columns))
	for i, row := range part {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteByte('(')
		for j := range row {
			if j > 0 {
				sb.WriteString(", ")
			}
			sb.WriteByte('?')
		}
		sb.WriteByte(')')
		args = append(args, row...)
	}

	if len(built.updates) > 0 {
		sb.WriteString(" ON DUPLICATE KEY UPDATE ")
		sb.WriteString(strings.Join(built.updates, ", "))
	}
	return sb.String(), args
}

// bulkKeyIndexes returns the positions of primary-key columns within the
// statement's column order.
//
// It matches on the quoted name rather than the mapping order, because a
// dropped identity column shifts every position after it.
func bulkKeyIndexes(mappings []sqlutil.ColumnMapping, columns []string) []int {
	var idx []int
	for i, c := range columns {
		for _, m := range mappings {
			if m.SourceField == "" || !m.IsPrimaryKey {
				continue
			}
			q, err := qcol(m.TargetColumn)
			if err == nil && q == c {
				idx = append(idx, i)
				break
			}
		}
	}
	return idx
}

// dedupeByKeyLastWins collapses rows sharing the same key, keeping the last
// occurrence's values at the first occurrence's position.
//
// MySQL applies a multi-row INSERT ... ON DUPLICATE KEY UPDATE row by row, so
// duplicates within one statement do not error the way Postgres's merge does —
// but they cost a second write of the same target row, and relying on that
// ordering is a weaker guarantee than simply not emitting the duplicate.
// Collapsing to last-wins reproduces exactly what the ordered path yields,
// where a later message overwrites an earlier one with the same key.
func dedupeByKeyLastWins(rows [][]any, keyIdx []int) [][]any {
	if len(keyIdx) == 0 || len(rows) < 2 {
		return rows
	}
	pos := make(map[string]int, len(rows))
	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		var sb strings.Builder
		for _, i := range keyIdx {
			if i < len(row) {
				fmt.Fprintf(&sb, "%v\x00", row[i])
			}
		}
		k := sb.String()
		if at, seen := pos[k]; seen {
			out[at] = row
			continue
		}
		pos[k] = len(out)
		out = append(out, row)
	}
	return out
}
