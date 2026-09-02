//go:build integration

package registry

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/factory"
	"github.com/gsoultan/Hermod/internal/storage"
	sqlstorage "github.com/gsoultan/Hermod/internal/storage/sql"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Sequential and parallel sinks both deliver, with the field values to prove it.
//
// These are genuinely different execution models, not a flag on one path. A
// sequential sink is written inline and exposes success and error branches to
// the nodes downstream of it; a parallel sink is handed to the async writers and
// has no branches at all. Go covered only the flag that chooses between them
// (TestResolveSinkNodeSequential) and nothing about what either one then does.
//
// The behaviour was covered instead by ui/__tests__/production_grade.spec.ts,
// which drove a browser, needed Postgres and RabbitMQ, took minutes, and ran in
// a nightly job marked continue-on-error — so a regression in either model
// could merge without anything going red. None of what it was really testing
// lives in the DOM. This drives the registry directly and writes to real
// tables, which is where the assertion belongs.
//
// What this does not distinguish: if the sequential flag stopped resolving and
// both sinks became parallel, both would still deliver and this would still
// pass. That half is TestResolveSinkNodeSequential's job. Together they cover
// the flag resolving and both paths delivering; neither covers the
// success/error branches a sequential sink exposes downstream, which is still
// uncovered.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:5432/hermod_it?sslmode=disable' \
//	go test -tags=integration -run TestSequentialAndParallelSinksBothDeliver ./internal/engine/registry/

// rowID is the single row the fixture emits and both sinks must land.
const rowID = "row-1"

func newSinkDeliveryFixture(t *testing.T) (*Registry, *sql.DB, string, string) {
	t.Helper()

	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q POSTGRES_DSN=%q in CI, where PostgreSQL is started "+
				"for exactly this; both sink models would go unexercised and the run would "+
				"still be green",
				os.Getenv("HERMOD_INTEGRATION"), dsn)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		// A failure, not a skip: the DSN naming this server is the statement
		// that it should be there.
		t.Fatalf("the configured PostgreSQL is not reachable: %v", err)
	}

	suffix := strings.ToLower(t.Name())
	seqTable := "sink_seq_" + suffix
	parTable := "sink_par_" + suffix

	for _, tbl := range []string{seqTable, parTable} {
		if _, err := db.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+tbl); err != nil {
			t.Fatalf("dropping %s: %v", tbl, err)
		}
		// The shape the PostgreSQL sink writes by default: an id and a JSONB
		// document (see pkg/comm/sink/postgres/queries.go). Mapping to real
		// columns is a separate feature with its own coverage; what is under
		// test here is which sink path runs, not how it lays out a row.
		if _, err := db.ExecContext(t.Context(), fmt.Sprintf(
			`CREATE TABLE %s (id TEXT PRIMARY KEY, data JSONB)`, tbl)); err != nil {
			t.Fatalf("creating %s: %v", tbl, err)
		}
		t.Cleanup(func() {
			_, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+tbl)
		})
	}

	meta, err := sql.Open("sqlite", "file:sinkdelivery_"+suffix+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })

	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init metadata store: %v", err)
	}

	// The sequential flag lives on the sink entity as a string, which is the
	// shape the API writes; resolveSinkNodeSequential turns it into the node's
	// bool. Setting it here rather than on the node is deliberate — it is the
	// path a sink created through the UI actually takes.
	if err := store.CreateSink(t.Context(), storage.Sink{
		ID: "sink-seq", Name: "sequential sink", Type: "postgres",
		Config: map[string]string{
			"connection_string":  dsn,
			"table":              seqTable,
			"use_existing_table": "true",
			"sequential":         "true",
		},
	}); err != nil {
		t.Fatalf("create sequential sink: %v", err)
	}
	if err := store.CreateSink(t.Context(), storage.Sink{
		ID: "sink-par", Name: "parallel sink", Type: "postgres",
		Config: map[string]string{
			"connection_string":  dsn,
			"table":              parTable,
			"use_existing_table": "true",
		},
	}); err != nil {
		t.Fatalf("create parallel sink: %v", err)
	}

	if err := store.CreateSource(t.Context(), storage.Source{
		ID: "src-1", Name: "fixture source", Type: "postgres",
		Config: map[string]string{"connection_string": dsn},
	}); err != nil {
		t.Fatalf("create source: %v", err)
	}

	reg := NewRegistry(store)

	// The source is stubbed and the sinks are real. What is under test is the
	// two sink execution models, so the source only has to be deterministic —
	// driving CDC here would add a replication slot to leak and prove nothing
	// that pkg/comm/source/postgres does not already cover.
	msg := message.AcquireMessage()
	msg.SetID(rowID)
	msg.SetData("id", rowID)
	msg.SetData("name", "ada")
	msg.SetData("city", "London")
	reg.sourceFactory = func(factory.SourceConfig) (hermod.Source, error) {
		return &onceSource{msg: msg}, nil
	}

	return reg, db, seqTable, parTable
}

// onceSource hands over a single message and then reports nothing, so the
// assertions are about one row rather than a race with a repeating poller.
type onceSource struct{ msg hermod.Message }

func (s *onceSource) Read(context.Context) (hermod.Message, error) {
	if s.msg == nil {
		return nil, nil
	}
	m := s.msg
	s.msg = nil
	return m, nil
}
func (s *onceSource) Ack(context.Context, hermod.Message) error { return nil }
func (s *onceSource) Ping(context.Context) error                { return nil }
func (s *onceSource) Close() error                              { return nil }

// rowIn returns the single row's name and city, or ok=false if the table is
// still empty.
func rowIn(t *testing.T, db *sql.DB, table, id string) (name, city string, ok bool) {
	t.Helper()
	err := db.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT data->>'name', data->>'city' FROM %s WHERE id = $1", table),
		id).Scan(&name, &city)
	switch {
	case err == sql.ErrNoRows:
		return "", "", false
	case err != nil:
		t.Fatalf("reading %s: %v", table, err)
	}
	return name, city, true
}

func TestSequentialAndParallelSinksBothDeliver(t *testing.T) {
	reg, db, seqTable, parTable := newSinkDeliveryFixture(t)

	// One source feeding a transformation that fans out to both sinks, which is
	// the shape the retired browser spec built. The transformation is here so
	// the assertion can be about a *derived* value: a row that arrived without
	// having been through the pipeline would still have a name, but it would
	// not have an upper-cased one.
	wf := storage.Workflow{
		ID:     "sink-delivery",
		Name:   "sequential and parallel delivery",
		Active: true,
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "src-1", Config: map[string]any{"label": "source"}},
			{
				ID:   "shout",
				Type: "transformation",
				Config: map[string]any{
					"transType":   "set",
					"column.name": `upper(source.name)`,
					"column.city": `concat(source.city, "!")`,
					"label":       "derive",
				},
			},
			{ID: "snk_seq", Type: "sink", RefID: "sink-seq"},
			{ID: "snk_par", Type: "sink", RefID: "sink-par"},
		},
		Edges: []storage.WorkflowEdge{
			{SourceID: "src", TargetID: "shout"},
			{SourceID: "shout", TargetID: "snk_seq"},
			{SourceID: "shout", TargetID: "snk_par"},
		},
	}

	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		t.Fatalf("starting the workflow: %v", err)
	}
	t.Cleanup(func() { _ = reg.StopEngine(context.Background(), wf.ID) })

	deadline := time.Now().Add(30 * time.Second)
	var seqName, seqCity, parName, parCity string
	var seqOK, parOK bool
	for time.Now().Before(deadline) {
		seqName, seqCity, seqOK = rowIn(t, db, seqTable, rowID)
		parName, parCity, parOK = rowIn(t, db, parTable, rowID)
		if seqOK && parOK {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !seqOK {
		t.Errorf("the sequential sink wrote nothing to %s\n"+
			"a sequential sink is written inline and exposes success and error branches; "+
			"nothing arriving means that path is not delivering at all", seqTable)
	}
	if !parOK {
		t.Errorf("the parallel sink wrote nothing to %s\n"+
			"a parallel sink is handed to the async writers, which is a different path from "+
			"the sequential one; nothing arriving means that path is not delivering at all",
			parTable)
	}
	if !seqOK || !parOK {
		return
	}

	// The field values, which are what prove the row went through the pipeline
	// rather than merely appearing.
	for _, c := range []struct {
		model, table, name, city string
	}{
		{"sequential", seqTable, seqName, seqCity},
		{"parallel", parTable, parName, parCity},
	} {
		if c.name != "ADA" {
			t.Errorf("%s sink: %s.name is %q, want %q — the transformation did not run on "+
				"the way to this sink", c.model, c.table, c.name, "ADA")
		}
		if c.city != "London!" {
			t.Errorf("%s sink: %s.city is %q, want %q — the transformation did not run on "+
				"the way to this sink", c.model, c.table, c.city, "London!")
		}
	}
}
