package advanced

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// execute_sql is the one transformer here that *writes*, and it had none of the
// guards the read-side lookups were given.
//
// It cannot serve a stale answer the way db_lookup could: it re-resolves its
// template against the current message on every call, holds no cache, and keeps
// no state between messages -- the struct has no fields. What was open is every
// way it could quietly do nothing.
// ---------------------------------------------------------------------------

type execSQLFakeRegistry struct {
	db     *sql.DB
	source storage.Source
}

func (f *execSQLFakeRegistry) GetOrOpenDBByID(ctx context.Context, id string) (*sql.DB, string, error) {
	return f.db, f.source.Type, nil
}

func (f *execSQLFakeRegistry) GetSourceConfig(ctx context.Context, id string) (storage.Source, error) {
	return f.source, nil
}

// newExecSQLFixture builds a writable table and an ordinary non-CDC source.
func newExecSQLFixture(t *testing.T) (*ExecuteSQLTransformer, *execSQLFakeRegistry) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(t.Context(),
		`CREATE TABLE audit (id TEXT, note TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	return &ExecuteSQLTransformer{}, &execSQLFakeRegistry{
		db: db,
		source: storage.Source{
			ID: "src1", Name: "audit-db", Type: "sqlite",
			Config: hermod.StringMap{"use_cdc": "false"},
		},
	}
}

func runExecSQL(t *testing.T, tr *ExecuteSQLTransformer, reg *execSQLFakeRegistry, cfg map[string]any, fields map[string]any) (hermod.Message, error) {
	t.Helper()

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	return tr.Transform(ctx, msg, cfg)
}

func countAudit(t *testing.T, reg *execSQLFakeRegistry) int {
	t.Helper()
	var n int
	if err := reg.db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// ---- 1. incomplete config -------------------------------------------------

// A node missing either field wrote nothing and reported success. On a
// transformer that exists to write rows, that is the worst available outcome:
// the pipeline is green and the table is empty.
func TestExecuteSQLReportsIncompleteConfig(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	for _, tc := range []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"no sourceId", map[string]any{"queryTemplate": "INSERT INTO audit VALUES ('a','b')"}, "sourceId"},
		{"no queryTemplate", map[string]any{"sourceId": "src1"}, "queryTemplate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runExecSQL(t, tr, reg, tc.cfg, nil)
			if err == nil {
				t.Fatalf("an execute_sql with %s returned success and wrote nothing", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not name the field at fault (%q): %v", tc.want, err)
			}
		})
	}
}

// ---- 2. unresolved template variables -------------------------------------

// The default must not change: ParameterizeTemplateEx binds an unresolved token
// as NULL on purpose, because an optional message field is a legitimate reason
// for a path to be empty.
func TestExecuteSQLBindsNullForAMissingFieldByDefault(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	_, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": "INSERT INTO audit (id, note) VALUES ({{.id}}, {{.note}})",
	}, map[string]any{"id": "r1"}) // note is absent
	if err != nil {
		t.Fatalf("an absent optional field is not an error by default: %v", err)
	}
	if countAudit(t, reg) != 1 {
		t.Errorf("the row was not written")
	}
}

// ...but a typo is indistinguishable from an optional field, and on a write a
// silently-NULL variable is a query that matches nothing or a column set to
// null. A node that wants that caught has to be able to say so.
func TestExecuteSQLCanFailOnAnUnresolvedVariable(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	_, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": "INSERT INTO audit (id, note) VALUES ({{.id}}, {{.nte}})",
		"onUnresolved":  "fail",
	}, map[string]any{"id": "r1", "note": "hello"})

	if err == nil {
		t.Fatalf("onUnresolved=fail accepted a template variable that resolved to nothing")
	}
	if !strings.Contains(err.Error(), "nte") {
		t.Errorf("the error does not name the unresolved path: %v", err)
	}
	if countAudit(t, reg) != 0 {
		t.Errorf("the statement ran anyway; a rejected template must not reach the database")
	}
}

// ---- 3. no CDC guard, deliberately ---------------------------------------

// db_lookup and batch_sql both refuse a CDC source. execute_sql does not, and
// this test exists so nobody "fixes" that without reading why.
//
// Their guard is about read load, which applies whatever table is read. A write
// is a different question: the danger is feeding the stream the source reads,
// and that depends on whether the *target table* is in the publication -- which
// nothing in the node config can tell us. Writing to an audit table the
// publication does not include is an ordinary thing to do against a CDC
// database. The workflow validator declined this for the same reason
// (TestValidateWorkflowLeavesExecuteSQLAlone); refusing at runtime but not at
// validation would also mean a workflow that validates clean and then fails on
// every message.
func TestExecuteSQLDoesNotRefuseACDCSource(t *testing.T) {
	tr, reg := newExecSQLFixture(t)
	reg.source.Config = hermod.StringMap{} // no use_cdc key: this is a CDC source

	if _, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": "INSERT INTO audit (id, note) VALUES ('a','b')",
	}, nil); err != nil {
		t.Fatalf("execute_sql refused a CDC source: %v -- writing to a table outside the "+
			"publication is legitimate, and the validator does not flag it either", err)
	}
	if countAudit(t, reg) != 1 {
		t.Errorf("the row was not written")
	}
}

// And the ordinary case still works.
func TestExecuteSQLWritesAgainstANonCDCSource(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	out, err := runExecSQL(t, tr, reg, map[string]any{
		"sourceId":          "src1",
		"queryTemplate":     "INSERT INTO audit (id, note) VALUES ({{.id}}, {{.note}})",
		"affectedRowsField": "written",
	}, map[string]any{"id": "r1", "note": "hello"})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if countAudit(t, reg) != 1 {
		t.Fatalf("the row was not written")
	}
	if got := out.Data()["written"]; got != int64(1) {
		t.Errorf("written = %#v, want int64(1)", got)
	}
}
