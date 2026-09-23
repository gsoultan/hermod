package advanced

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

// execute_sql binds its {{ }} tokens the same way db_lookup does, and had the
// same gap: tokens resolved with a literal walk of the data map, which for a
// CDC message is the after-image itself, so `after.x` bound NULL while every
// other template in Hermod resolved it. A write that binds NULL silently
// changes nothing, which is the one outcome nothing downstream can detect.
func runExecSQLCDC(t *testing.T, tr *ExecuteSQLTransformer, reg *execSQLFakeRegistry, cfg map[string]any, fields map[string]any) error {
	t.Helper()

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetOperation(hermod.Operation("insert"))
	msg.SetTable("registrants")
	for k, v := range fields {
		msg.SetData(k, v)
	}

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	_, err := tr.Transform(ctx, msg, cfg)
	return err
}

func TestExecuteSQLResolvesTheAfterPrefix(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	if err := runExecSQLCDC(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": `INSERT INTO audit (id, note) VALUES ({{.after.row_id}}, 'x')`,
	}, map[string]any{"row_id": "r-1"}); err != nil {
		t.Fatalf("Transform: %v", err)
	}

	var id string
	if err := reg.db.QueryRowContext(t.Context(), `SELECT id FROM audit`).Scan(&id); err != nil {
		t.Fatalf("select: %v", err)
	}
	if id != "r-1" {
		t.Errorf("id = %q, want r-1 -- {{.after.x}} must resolve the way every other template does", id)
	}
}

func TestExecuteSQLResolvesTheVirtualFields(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	if err := runExecSQLCDC(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": `INSERT INTO audit (id, note) VALUES ({{.row_id}}, {{.operation}})`,
	}, map[string]any{"row_id": "r-1"}); err != nil {
		t.Fatalf("Transform: %v", err)
	}

	var note string
	if err := reg.db.QueryRowContext(t.Context(), `SELECT note FROM audit`).Scan(&note); err != nil {
		t.Fatalf("select: %v", err)
	}
	if note != "insert" {
		t.Errorf("note = %q, want insert", note)
	}
}

// onUnresolved: fail must still see a genuinely unresolvable path as
// unresolved -- widening the resolver must not make every typo "resolve".
func TestExecuteSQLStillFailsOnARealTypo(t *testing.T) {
	tr, reg := newExecSQLFixture(t)

	err := runExecSQLCDC(t, tr, reg, map[string]any{
		"sourceId":      "src1",
		"queryTemplate": `INSERT INTO audit (id, note) VALUES ({{.row_id}}, {{.typoo}})`,
		"onUnresolved":  "fail",
	}, map[string]any{"row_id": "r-1"})

	if err == nil {
		t.Fatal("want an error naming the unresolved variable")
	}
	if countAudit(t, reg) != 0 {
		t.Error("the statement must not have run")
	}
}
