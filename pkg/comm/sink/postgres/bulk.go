package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5"
)

// bulkMode selects how WriteBatch applies a batch.
type bulkMode int

const (
	// bulkModeNone is the ordered, one-statement-per-message path. It is the
	// default and the only path that preserves CDC semantics (a delete followed
	// by an insert on the same key must be applied in that order).
	bulkModeNone bulkMode = iota
	// bulkModeCopy streams the batch into a staging table with COPY and merges
	// it into the target in a single statement.
	bulkModeCopy
)

func (m bulkMode) String() string {
	if m == bulkModeCopy {
		return "copy"
	}
	return "none"
}

// bulkMinRows is the batch size below which COPY is not worth its setup cost.
// A staging table plus a merge statement is roughly two extra round-trips, so
// for small batches the ordered path is competitive and simpler.
const bulkMinRows = 50

// classifyBatch decides whether a batch may take the bulk path.
//
// The rule is conservative by construction: bulkModeCopy is returned only when
// every condition for safety is positively established, and anything unknown
// falls through to bulkModeNone. Losing per-row ordering to gain throughput
// would trade away the guarantee that makes Hermod useful for CDC, so the fast
// path is restricted to batches where ordering is not observable — inserts
// only, one target table, no soft-delete rewriting.
func (s *PostgresSink) classifyBatch(msgs []hermod.Message) bulkMode {
	if len(msgs) < bulkMinRows {
		return bulkModeNone
	}
	// Without mappings the column list is derived per message, so there is no
	// stable tuple shape to COPY.
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
		// single-table COPY.
		if t := m.Table(); t != "" && t != s.tableName {
			return bulkModeNone
		}
	}
	return bulkModeCopy
}

// dedupeByKeyLastWins collapses rows sharing the same key, keeping the last
// occurrence's values at the first occurrence's position.
//
// All rows in a COPY batch are merged into the target by one statement, and
// Postgres rejects a merge that would touch the same target row twice
// ("ON CONFLICT DO UPDATE command cannot affect row a second time"). Collapsing
// to last-wins reproduces what the ordered path yields, where a later message
// simply overwrites an earlier one with the same key.
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

// primaryKeyIndexes returns the positions of primary-key columns in the
// mapping order used to build COPY tuples.
func primaryKeyIndexes(mappings []sqlutil.ColumnMapping) []int {
	var idx []int
	for i, m := range mappings {
		if m.IsPrimaryKey {
			idx = append(idx, i)
		}
	}
	return idx
}

// writeBatchCopy applies an insert-only batch using COPY into a staging table
// followed by a single merge into the target.
//
// The staging table is TEMP and ON COMMIT DROP, so it lives and dies with the
// transaction and cannot leak if the merge fails partway.
func (s *PostgresSink) writeBatchCopy(ctx context.Context, tx pgx.Tx, table string, msgs []hermod.Message) error {
	// Mirror the ordered path: mappings with no source field are skipped there
	// (see buildUpsertArgs), so they must be skipped here too or the tuple shape
	// would not match the column list.
	active := make([]sqlutil.ColumnMapping, 0, len(s.mappings))
	for _, m := range s.mappings {
		if m.SourceField == "" {
			continue
		}
		active = append(active, m)
	}
	if len(active) == 0 {
		return fmt.Errorf("no mapped columns available for bulk copy")
	}

	cols := make([]string, 0, len(active))
	for _, m := range active {
		cols = append(cols, m.TargetColumn)
	}

	rows := make([][]any, 0, len(msgs))
	for _, msg := range msgs {
		vals := make([]any, 0, len(active))
		for _, m := range active {
			vals = append(vals, s.convertValue(evaluator.GetMsgValByPath(msg, m.SourceField), m.DataType))
		}
		rows = append(rows, vals)
	}

	pkIdx := primaryKeyIndexes(active)
	rows = dedupeByKeyLastWins(rows, pkIdx)

	staging := "hermod_bulk_stage"
	quotedCols := make([]string, 0, len(cols))
	for _, c := range cols {
		quotedCols = append(quotedCols, pgx.Identifier{c}.Sanitize())
	}

	// LIKE ... copies column types but no constraints, so COPY cannot fail on a
	// conflict that the merge is about to resolve.
	createStaging := fmt.Sprintf(
		"CREATE TEMP TABLE %s (LIKE %s INCLUDING DEFAULTS) ON COMMIT DROP",
		pgx.Identifier{staging}.Sanitize(), table)
	if _, err := tx.Exec(ctx, createStaging); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}

	copied, err := tx.CopyFrom(ctx, pgx.Identifier{staging}, cols, pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("copy into staging: %w", err)
	}
	if int(copied) != len(rows) {
		return fmt.Errorf("copy wrote %d of %d rows", copied, len(rows))
	}

	colList := strings.Join(quotedCols, ", ")
	merge := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s",
		table, colList, colList, pgx.Identifier{staging}.Sanitize())

	if len(pkIdx) > 0 {
		pkCols := make([]string, 0, len(pkIdx))
		for _, i := range pkIdx {
			pkCols = append(pkCols, pgx.Identifier{active[i].TargetColumn}.Sanitize())
		}
		updates := make([]string, 0, len(quotedCols))
		for i, qc := range quotedCols {
			if active[i].IsPrimaryKey {
				continue
			}
			updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", qc, qc))
		}
		if len(updates) > 0 {
			merge += fmt.Sprintf(" ON CONFLICT (%s) DO UPDATE SET %s",
				strings.Join(pkCols, ", "), strings.Join(updates, ", "))
		} else {
			merge += fmt.Sprintf(" ON CONFLICT (%s) DO NOTHING", strings.Join(pkCols, ", "))
		}
	}

	if _, err := tx.Exec(ctx, merge); err != nil {
		return fmt.Errorf("merge staging into %s: %w", table, err)
	}
	return nil
}
