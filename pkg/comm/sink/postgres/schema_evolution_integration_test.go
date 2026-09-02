//go:build integration

package postgres

import (
	"fmt"
	"os"
	"strings"
	"testing"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// What happens when a source grows a column.
//
// A CDC source picks up whatever the upstream table has. Add a column there and
// it starts appearing in every message, without anything in Hermod being
// reconfigured. What the sink then does with it was never defined or tested,
// and the answer turns out to depend entirely on how the sink is configured:
//
//   - with no column mappings the sink writes (id, data JSONB), so the new
//     field lands inside the document and the pipeline evolves on its own;
//   - with column mappings the sink writes exactly the columns it was told
//     about, so the new field is read by nothing and silently dropped.
//
// The second is the one worth pinning. It is not wrong — a mapping is a
// statement about which fields matter — but losing data with no error, no log
// and no metric is precisely the failure mode this project treats as
// page-worthy elsewhere. These tests fix both behaviours in place so neither
// changes by accident, and so the lossy one is visible.

func evolutionPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q POSTGRES_DSN=%q in CI, where PostgreSQL is "+
				"started for exactly this", os.Getenv("HERMOD_INTEGRATION"), dsn)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, dsn
}

// Unmapped mode carries a new field across on its own, because the field lands
// inside the JSONB document rather than in a column somebody had to declare.
func TestANewSourceFieldSurvivesWithoutColumnMappings(t *testing.T) {
	pool, dsn := evolutionPool(t)
	ctx := t.Context()

	const table = "hermod_evolution_jsonb"
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"CREATE TABLE "+table+" (id text PRIMARY KEY, data jsonb NOT NULL)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+table) })

	sink := NewPostgresSink(dsn, table, nil, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("row-1")
	msg.SetData("name", "ada")
	// The column the upstream table just grew.
	msg.SetData("nickname", "the countess")

	if err := sink.Write(ctx, msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	var nickname *string
	if err := pool.QueryRow(ctx,
		"SELECT data->>'nickname' FROM "+table+" WHERE id = $1", "row-1").Scan(&nickname); err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if nickname == nil || *nickname != "the countess" {
		t.Errorf("the new field did not survive the write: got %v\n"+
			"without column mappings the whole message is written as a document, so a field "+
			"the source grew should arrive without anyone reconfiguring anything", nickname)
	}
}

// Mapped mode drops it, and must say so. The drop itself is a legitimate
// design — a mapping states which fields matter — but it has to be reported,
// or a source growing a column loses data with every status green.
func TestANewSourceFieldIsReportedWhenColumnMappingsDropIt(t *testing.T) {
	pool, dsn := evolutionPool(t)
	ctx := t.Context()

	const table = "hermod_evolution_mapped"
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"CREATE TABLE "+table+" (id text PRIMARY KEY, name text)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+table) })

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", DataType: "text", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: "name", DataType: "text"},
	}
	sink := NewPostgresSink(dsn, table, mappings, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	rec := &recordingLogger{}
	sink.SetLogger(rec)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("row-1")
	msg.SetData("id", "row-1")
	msg.SetData("name", "ada")
	msg.SetData("nickname", "the countess")

	if err := sink.Write(ctx, msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The mapped columns still land. Dropping the unmapped field must not have
	// disturbed the write itself.
	var name string
	if err := pool.QueryRow(ctx,
		"SELECT name FROM "+table+" WHERE id = $1", "row-1").Scan(&name); err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if name != "ada" {
		t.Errorf("mapped column did not land: got %q", name)
	}

	if !rec.mentions("nickname") {
		t.Errorf("the sink dropped the unmapped field %q and said nothing.\n"+
			"logged: %v\n"+
			"a source that grows a column then loses it at the sink with no error, no log "+
			"and no metric — the pipeline reports healthy while the destination quietly "+
			"stops matching it",
			"nickname", rec.messages)
	}
}

// Reporting it once per sink, not once per row. A message-rate log is a log
// nobody can read and a cost nobody wants.
func TestTheUnmappedFieldReportIsNotPerMessage(t *testing.T) {
	pool, dsn := evolutionPool(t)
	ctx := t.Context()

	const table = "hermod_evolution_rate"
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"CREATE TABLE "+table+" (id text PRIMARY KEY, name text)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+table) })

	mappings := []sqlutil.ColumnMapping{
		{SourceField: "$.id", TargetColumn: "id", DataType: "text", IsPrimaryKey: true},
		{SourceField: "$.name", TargetColumn: "name", DataType: "text"},
	}
	sink := NewPostgresSink(dsn, table, mappings, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	rec := &recordingLogger{}
	sink.SetLogger(rec)

	for i := range 5 {
		msg := message.AcquireMessage()
		msg.SetID("row-" + string(rune('a'+i)))
		msg.SetData("id", "row-"+string(rune('a'+i)))
		msg.SetData("name", "ada")
		msg.SetData("nickname", "the countess")
		if err := sink.Write(ctx, msg); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		msg.Release()
	}

	if n := rec.count("nickname"); n != 1 {
		t.Errorf("the unmapped field was reported %d times across 5 messages, want 1\n"+
			"reporting per message turns a schema change into a log flood", n)
	}
}

type recordingLogger struct {
	messages []string
}

func (l *recordingLogger) record(msg string, kv ...any) {
	parts := make([]string, 0, len(kv)+1)
	parts = append(parts, msg)
	for _, v := range kv {
		parts = append(parts, fmt.Sprint(v))
	}
	l.messages = append(l.messages, strings.Join(parts, " "))
}

func (l *recordingLogger) Debug(msg string, kv ...any) { l.record(msg, kv...) }
func (l *recordingLogger) Info(msg string, kv ...any)  { l.record(msg, kv...) }
func (l *recordingLogger) Warn(msg string, kv ...any)  { l.record(msg, kv...) }
func (l *recordingLogger) Error(msg string, kv ...any) { l.record(msg, kv...) }

func (l *recordingLogger) mentions(s string) bool { return l.count(s) > 0 }

func (l *recordingLogger) count(s string) int {
	n := 0
	for _, m := range l.messages {
		if strings.Contains(m, s) {
			n++
		}
	}
	return n
}

var _ hermod.Logger = (*recordingLogger)(nil)
