package postgres

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine/telemetry"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

// recordingExecutor remembers the statements it was asked to run.
type recordingExecutor struct {
	statements []string
	affected   int
}

// rowsAffected is what the fake reports back; pgconn.CommandTag carries it in
// its string form, which is how pgx parses a real server reply.
func (r *recordingExecutor) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	r.statements = append(r.statements, sql)
	return pgconn.NewCommandTag(fmt.Sprintf("DELETE %d", r.affected)), nil
}
func (r *recordingExecutor) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (r *recordingExecutor) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

// TestADeleteThatMatchedNothingIsReported pins that "no rows affected" is not
// read as success.
//
// With no column mappings the sink keys rows on the message's own id, and
// whether that identifies a row depends on the source: MySQL CDC derives it from
// the primary key, while PostgreSQL uses an LSN that differs for every event
// touching one row. In the latter case every delete matches nothing -- and it
// used to return no error, so the destination silently kept rows the source had
// removed.
//
// The delete is still issued, because the combinations where the id is stable
// work and must keep working. What changed is that a miss is counted.
func TestADeleteThatMatchedNothingIsReported(t *testing.T) {
	const table = "events_lsn_keyed"

	sink := NewPostgresSink("", table, nil, true, "", "", "", "", false, false)

	msg := message.AcquireMessage()
	msg.SetID("0/2604B18") // an LSN, which is what the PostgreSQL CDC source sets
	msg.SetOperation(hermod.OpDelete)
	msg.SetTable("orders")
	msg.SetBefore([]byte(`{"id":"M-1","customer_id":"C-1"}`))

	before := promtestutil.ToFloat64(telemetry.SinkDeleteMatchedNothing.WithLabelValues(table))

	exec := &recordingExecutor{affected: 0}
	if err := sink.applyOperation(t.Context(), exec, table, msg); err != nil {
		t.Fatalf("applyOperation: %v", err)
	}

	if len(exec.statements) != 1 {
		t.Fatalf("expected the DELETE still to be issued, got %q", exec.statements)
	}
	after := promtestutil.ToFloat64(telemetry.SinkDeleteMatchedNothing.WithLabelValues(table))
	if after != before+1 {
		t.Errorf("a delete that matched nothing was not counted: metric went %v -> %v", before, after)
	}
}

// TestADeleteThatMatchedIsNotReported is the control: a source whose id is stable
// per row -- MySQL CDC, for instance -- deletes successfully, and that must not
// be flagged.
func TestADeleteThatMatchedIsNotReported(t *testing.T) {
	const table = "events_pk_keyed"

	sink := NewPostgresSink("", table, nil, true, "", "", "", "", false, false)

	msg := message.AcquireMessage()
	msg.SetID("shop:orders:M-1") // what the MySQL CDC source sets
	msg.SetOperation(hermod.OpDelete)
	msg.SetTable("orders")
	msg.SetBefore([]byte(`{"id":"M-1"}`))

	before := promtestutil.ToFloat64(telemetry.SinkDeleteMatchedNothing.WithLabelValues(table))

	exec := &recordingExecutor{affected: 1}
	if err := sink.applyOperation(t.Context(), exec, table, msg); err != nil {
		t.Fatalf("applyOperation: %v", err)
	}
	if after := promtestutil.ToFloat64(telemetry.SinkDeleteMatchedNothing.WithLabelValues(table)); after != before {
		t.Errorf("a delete that removed a row was flagged as matching nothing")
	}
}

// TestAMappedSinkStillDeletes is the control: a mapping gives the sink a key, so
// the skip above must not apply to it.
func TestAMappedSinkStillDeletes(t *testing.T) {
	mappings := []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "order_id", IsPrimaryKey: true},
		{SourceField: "customer_id", TargetColumn: "customer_id"},
	}
	sink := NewPostgresSink("", "orders_mirror", mappings, true, "", "", "", "", false, false)

	msg := message.AcquireMessage()
	msg.SetID("0/2604B18")
	msg.SetOperation(hermod.OpDelete)
	msg.SetTable("orders")
	msg.SetBefore([]byte(`{"id":"M-1","customer_id":"C-1"}`))

	exec := &recordingExecutor{affected: 1}
	if err := sink.applyOperation(t.Context(), exec, "orders_mirror", msg); err != nil {
		t.Fatalf("applyOperation: %v", err)
	}
	if len(exec.statements) != 1 {
		t.Fatalf("expected one DELETE, got %q", exec.statements)
	}
}
