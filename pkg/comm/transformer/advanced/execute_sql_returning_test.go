package advanced

import (
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// ---------------------------------------------------------------------------
// execute_sql ran every statement through ExecContext, which has nowhere to put
// a result set. `INSERT ... RETURNING id` therefore wrote the row and threw the
// id away: the message left the node exactly as it arrived, and the editor's
// Run Preview -- which shows that message -- showed nothing new.
//
// resultField is where the returned rows go. It is opt-in: a statement that has
// carried a RETURNING clause all along must not start adding a field to
// messages a sink is already mapping.
// ---------------------------------------------------------------------------

func TestExecuteSQLKeepsTheRowAnInsertReturns(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	out, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": "INSERT INTO audit (id, note) VALUES ({{.id}}, {{.note}}) RETURNING id, note",
		"resultField":   "inserted",
	}, map[string]any{"id": "r1", "note": "hello"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	row, ok := out.Data()["inserted"].(map[string]any)
	if !ok {
		t.Fatalf("inserted = %#v, want the returned row as an object -- the RETURNING "+
			"clause ran and its result was discarded", out.Data()["inserted"])
	}
	if row["id"] != "r1" || row["note"] != "hello" {
		t.Errorf("inserted = %#v, want id=r1 note=hello", row)
	}
	// Once. Reading the rows must not be a second execution of the statement.
	if n := countAudit(t, reg); n != 1 {
		t.Errorf("the statement wrote %d rows, want exactly 1", n)
	}
}

// One row is the common case and gets the convenient shape. A statement that
// returns several needs all of them, in a shape that does not change with the
// count -- db_lookup's "an object for one row, a list for two" is a template
// that works in the preview and breaks on the first busy message.
func TestExecuteSQLKeepsEveryReturnedRowWhenAsked(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	out, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": "INSERT INTO audit (id, note) VALUES ('a','x'), ('b','y') RETURNING id",
		"resultField":   "inserted",
		"resultRows":    "all",
	}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	rows, ok := out.Data()["inserted"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("inserted = %#v, want a list of the two returned rows", out.Data()["inserted"])
	}
	first, _ := rows[0].(map[string]any)
	if first["id"] != "a" {
		t.Errorf("first returned row = %#v, want id=a", rows[0])
	}

	// And a single row is still a list in this mode.
	out, err = runExecSQL(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": "INSERT INTO audit (id, note) VALUES ('c','z') RETURNING id",
		"resultField":   "inserted",
		"resultRows":    "all",
	}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if rows, ok := out.Data()["inserted"].([]any); !ok || len(rows) != 1 {
		t.Errorf("inserted = %#v, want a one-element list", out.Data()["inserted"])
	}
}

// A statement that matched nothing must say so. Leaving the field untouched
// would hand downstream whatever an earlier node -- or the source row -- had
// already put under that name.
func TestExecuteSQLOverwritesTheFieldWhenNothingIsReturned(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		check func(t *testing.T, got any, present bool)
	}{
		{"first", func(t *testing.T, got any, present bool) {
			if !present || got != nil {
				t.Errorf("inserted = %#v (present=%v), want an explicit null", got, present)
			}
		}},
		{"all", func(t *testing.T, got any, present bool) {
			if rows, ok := got.([]any); !ok || len(rows) != 0 {
				t.Errorf("inserted = %#v, want an empty list", got)
			}
		}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			tr, reg := newExecSQLFixture(t)

			out, err := runExecSQL(t, tr, reg, map[string]any{
				"sourceId":      "src1",
				"queryTemplate": "UPDATE audit SET note = 'x' WHERE id = 'nobody' RETURNING id",
				"resultField":   "inserted",
				"resultRows":    tc.mode,
			}, map[string]any{"inserted": "stale"})
			if err != nil {
				t.Fatalf("Transform: %v", err)
			}
			got, present := out.Data()["inserted"]
			tc.check(t, got, present)
		})
	}
}

// With the rows read, the affected-row count is how many came back -- every row
// a RETURNING statement touches is a row it returns.
func TestExecuteSQLCountsReturnedRowsAsAffected(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	out, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":          "src1",
		"queryTemplate":     "INSERT INTO audit (id, note) VALUES ('a','x'), ('b','y') RETURNING id",
		"resultField":       "inserted",
		"affectedRowsField": "written",
	}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got := out.Data()["written"]; got != int64(2) {
		t.Errorf("written = %#v, want int64(2)", got)
	}
}

// ...but a statement with no result set at all has no count to read on this
// path, and 0 would be a lie about a row that was written. Absent is honest.
func TestExecuteSQLDoesNotInventACountWithoutAResultSet(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	out, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":          "src1",
		"queryTemplate":     "INSERT INTO audit (id, note) VALUES ('a','x')",
		"resultField":       "inserted",
		"affectedRowsField": "written",
	}, nil)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if countAudit(t, reg) != 1 {
		t.Fatalf("the row was not written")
	}
	if got, present := out.Data()["written"]; present {
		t.Errorf("written = %#v; the statement returned no result set, so there is no "+
			"count to report and a 0 here contradicts the row it just wrote", got)
	}
}

// The returned rows are bounded, because one message must not be able to hold
// an unbounded result -- but the statement still has to run to its end and the
// count still has to be the real one. Stopping at the cap and closing the
// cursor is how a driver gets to cancel a write that is halfway through.
func TestExecuteSQLReadsPastTheRowCapWithoutKeepingTheRows(t *testing.T) {
	tr, reg := newExecSQLFixture(t)
	const total = sqlutil.DefaultMaxRows + 5

	out, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId": "src1",
		"queryTemplate": `INSERT INTO audit (id, note)
			SELECT 'r' || x, 'n' FROM (
				WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < {{.n}})
				SELECT x FROM c
			) RETURNING id`,
		"resultField":       "inserted",
		"resultRows":        "all",
		"affectedRowsField": "written",
	}, map[string]any{"n": total})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	if n := countAudit(t, reg); n != total {
		t.Errorf("the statement wrote %d rows, want all %d", n, total)
	}
	if rows, _ := out.Data()["inserted"].([]any); len(rows) != sqlutil.DefaultMaxRows {
		t.Errorf("kept %d returned rows, want the cap of %d", len(rows), sqlutil.DefaultMaxRows)
	}
	if got := out.Data()["written"]; got != int64(total) {
		t.Errorf("written = %#v, want int64(%d) -- the count is of rows written, not rows kept", got, total)
	}
}

// A refused write must still be an error. Some drivers only report it once the
// result is read, so a query path that ignores the cursor's error turns a
// constraint violation into a success with no rows.
func TestExecuteSQLStillFailsARefusedWriteWhenReadingRows(t *testing.T) {
	tr, reg := newExecSQLFixture(t)
	if _, err := reg.db.ExecContext(t.Context(),
		`CREATE TABLE once (id TEXT PRIMARY KEY); INSERT INTO once VALUES ('taken')`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": "INSERT INTO once (id) VALUES ({{.id}}) RETURNING id",
		"resultField":   "inserted",
	}, map[string]any{"id": "taken"})
	if err == nil {
		t.Fatalf("a duplicate key was reported as success")
	}
	if !strings.Contains(err.Error(), "failed to execute SQL") {
		t.Errorf("the error lost the execute_sql wrapper: %v", err)
	}
}

// The default does not move. A node that never named a field keeps the message
// it was given, whatever its statement returns.
func TestExecuteSQLDiscardsReturnedRowsUnlessAskedToKeepThem(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	out, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": "INSERT INTO audit (id, note) VALUES ({{.id}}, 'n') RETURNING id",
	}, map[string]any{"id": "r1"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if countAudit(t, reg) != 1 {
		t.Fatalf("the row was not written")
	}
	if len(out.Data()) != 1 {
		t.Errorf("message = %#v, want only the field it arrived with", out.Data())
	}
}
