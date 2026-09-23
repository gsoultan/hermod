package service

import (
	"reflect"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// The editor's SQL query builder and the node that runs the query it produces
// were handed the same sample and disagreed about it. The builder resolved
// {{ }} tokens against the sample map, which still carries the CDC envelope
// ToMap() put there; the engine resolves against the message, whose data map
// *is* the after-image. So `{{.after.payload}}` returned rows in the builder and
// bound NULL in the pipeline -- reported as "the builder shows results but the
// preview does not show the target field".
//
// This is the round trip that has to hold: engine message -> ToMap() -> the
// editor -> back here -> the same bound arguments.
func TestTheBuilderBindsWhatTheEngineBinds(t *testing.T) {
	// A CDC message exactly as a source hands it to the engine: row columns
	// flat in the data map, envelope on the message.
	engineMsg := message.AcquireMessage()
	defer message.ReleaseMessage(engineMsg)
	engineMsg.SetOperation(hermod.Operation("insert"))
	engineMsg.SetTable("reminders")
	engineMsg.SetData("payload", map[string]any{"registrantId": "r-1"})
	engineMsg.SetData("row_id", "u1")

	// What the editor holds and posts back is ToMap(), which re-nests the
	// after-image under "after".
	editorSample := engineMsg.ToMap()
	if _, ok := editorSample["after"]; !ok {
		t.Fatal("precondition: ToMap of a CDC message should carry an \"after\" envelope")
	}

	queries := []string{
		`SELECT * FROM t WHERE id = {{.row_id}}`,
		`SELECT {{.after.payload}}::JSON`,
		`SELECT {{.after.payload.registrantId}}`,
		`SELECT {{.operation}}, {{.table}}`,
		`SELECT {{.nope}}`,
	}

	for _, q := range queries {
		_, builderArgs := bindSample("pgx", q, editorSample)
		engineArgs := sqlutil.TemplateArgsWith(q, sqlutil.Resolver(evaluator.MessageResolver(engineMsg)))

		if !reflect.DeepEqual(builderArgs, engineArgs) {
			t.Errorf("%s\n  builder binds %#v\n  engine  binds %#v", q, builderArgs, engineArgs)
		}
	}
}

// The reported query, end to end through the binder the builder now uses.
func TestBindSampleResolvesTheAfterPrefix(t *testing.T) {
	sample := map[string]any{
		"operation": "insert",
		"after":     map[string]any{"payload": map[string]any{"registrantId": "r-1"}},
	}

	_, args := bindSample("pgx", `SELECT {{.after.payload}}::JSON AS payload`, sample)

	if len(args) != 1 {
		t.Fatalf("args = %#v, want one", args)
	}
	got, ok := args[0].(map[string]any)
	if !ok || got["registrantId"] != "r-1" {
		t.Errorf("args[0] = %#v, want the payload object", args[0])
	}
}

// With no sample at all the binder still has to produce a usable statement --
// the seeded id is what lets an operator run a query before a sample exists.
func TestBindSampleWithNoSampleStillBinds(t *testing.T) {
	sql, args := bindSample("pgx", `SELECT * FROM t WHERE id = {{.id}}`, defaultSample())

	if sql != "SELECT * FROM t WHERE id = $1" {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 1 || args[0] == nil {
		t.Errorf("args = %#v, want a seeded id rather than NULL", args)
	}
}
