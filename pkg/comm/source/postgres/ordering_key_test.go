package postgres

import (
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// A CDC message's ordering key must identify the row, not the change.
//
// Every CDC message's ID is its LSN, which is unique per change. Anything that
// hashes on the ID — the engine's worker fan-out, the sink writer's shards —
// therefore scatters the changes to one row across every worker, which is the
// exact opposite of what per-key ordering needs. The relation message already
// says which columns are the key (Flags == 1); nothing read it.
// ---------------------------------------------------------------------------

func keyCol(name string, oid uint32) pglogrepl.RelationMessageColumn {
	return pglogrepl.RelationMessageColumn{Name: name, DataType: oid, Flags: 1}
}

func TestOrderingKeyUsesThePrimaryKey(t *testing.T) {
	r := rel(keyCol("id", pgtype.Int8OID), col("status", pgtype.TextOID))
	tup := tuple(text("42"), text("shipped"))

	got := relationOrderingKey(r, tup)
	want := hermod.BuildOrderingKey("public", "orders", []string{"42"})

	if got != want {
		t.Errorf("ordering key = %q, want %q", got, want)
	}
	if got == "" {
		t.Fatal("no ordering key, so every change to this row scatters across workers")
	}
}

// Two changes to the same row must produce the same key even when the non-key
// columns differ — that is the whole point.
func TestTwoChangesToOneRowShareAKey(t *testing.T) {
	r := rel(keyCol("id", pgtype.Int8OID), col("status", pgtype.TextOID))

	first := relationOrderingKey(r, tuple(text("42"), text("pending")))
	second := relationOrderingKey(r, tuple(text("42"), text("shipped")))

	if first != second {
		t.Errorf("two changes to row 42 produced different keys: %q vs %q", first, second)
	}
}

func TestDifferentRowsGetDifferentKeys(t *testing.T) {
	r := rel(keyCol("id", pgtype.Int8OID), col("status", pgtype.TextOID))

	a := relationOrderingKey(r, tuple(text("42"), text("shipped")))
	b := relationOrderingKey(r, tuple(text("43"), text("shipped")))

	if a == b {
		t.Errorf("two different rows share key %q, so they serialise behind each other", a)
	}
}

// A composite key has to use every key column, or two distinct rows collide.
func TestCompositeKeyUsesEveryKeyColumn(t *testing.T) {
	r := rel(keyCol("tenant", pgtype.TextOID), keyCol("id", pgtype.Int8OID), col("v", pgtype.TextOID))

	a := relationOrderingKey(r, tuple(text("acme"), text("1"), text("x")))
	b := relationOrderingKey(r, tuple(other, text("1"), text("x")))

	if a == b {
		t.Errorf("two tenants' row 1 share key %q", a)
	}
}

var other = text("globex")

// A table with no replica identity has no key columns, so no row can be
// identified. Returning a table-wide key is wrong (it would serialise the whole
// table silently); returning nothing is honest and keeps today's parallelism.
func TestNoKeyColumnsYieldsNoKey(t *testing.T) {
	r := rel(col("a", pgtype.TextOID), col("b", pgtype.TextOID))

	if got := relationOrderingKey(r, tuple(text("1"), text("2"))); got != "" {
		t.Errorf("a keyless relation produced ordering key %q", got)
	}
}

func TestNilRelationOrTupleIsSafe(t *testing.T) {
	if got := relationOrderingKey(nil, tuple(text("1"))); got != "" {
		t.Errorf("nil relation produced %q", got)
	}
	r := rel(keyCol("id", pgtype.Int8OID))
	if got := relationOrderingKey(r, nil); got != "" {
		t.Errorf("nil tuple produced %q", got)
	}
}

// The key is only useful if the handlers actually put it on the message. Each
// CDC operation carries the row it changed, so each must stamp it.
func TestCDCHandlersStampTheOrderingKey(t *testing.T) {
	r := rel(keyCol("id", pgtype.Int8OID), col("status", pgtype.TextOID))
	r.RelationID = 7

	newSource := func() *PostgresSource {
		p := &PostgresSource{relations: map[uint32]*pglogrepl.RelationMessage{7: r}}
		return p
	}
	want := hermod.BuildOrderingKey("public", "orders", []string{"42"})

	t.Run("insert", func(t *testing.T) {
		m := newSource().handleInsert(100, &pglogrepl.InsertMessage{
			RelationID: 7,
			Tuple:      tuple(text("42"), text("new")),
		})
		if got := hermod.OrderingKey(m); got != want {
			t.Errorf("insert ordering key = %q, want %q", got, want)
		}
	})

	t.Run("update", func(t *testing.T) {
		m := newSource().handleUpdate(101, &pglogrepl.UpdateMessage{
			RelationID: 7,
			NewTuple:   tuple(text("42"), text("shipped")),
		})
		if got := hermod.OrderingKey(m); got != want {
			t.Errorf("update ordering key = %q, want %q", got, want)
		}
	})

	// A delete only has the old tuple, and it is the change most in need of
	// ordering: a delete overtaking the update before it resurrects the row.
	t.Run("delete", func(t *testing.T) {
		m := newSource().handleDelete(102, &pglogrepl.DeleteMessage{
			RelationID: 7,
			OldTuple:   tuple(text("42"), text("shipped")),
		})
		if got := hermod.OrderingKey(m); got != want {
			t.Errorf("delete ordering key = %q, want %q", got, want)
		}
	})

	// All three changes to one row must agree, or they do not serialise.
	t.Run("all three agree", func(t *testing.T) {
		ins := hermod.OrderingKey(newSource().handleInsert(100, &pglogrepl.InsertMessage{
			RelationID: 7, Tuple: tuple(text("42"), text("new"))}))
		upd := hermod.OrderingKey(newSource().handleUpdate(101, &pglogrepl.UpdateMessage{
			RelationID: 7, NewTuple: tuple(text("42"), text("shipped"))}))
		del := hermod.OrderingKey(newSource().handleDelete(102, &pglogrepl.DeleteMessage{
			RelationID: 7, OldTuple: tuple(text("42"), text("shipped"))}))

		if ins != upd || upd != del {
			t.Errorf("insert/update/delete of one row disagree: %q / %q / %q", ins, upd, del)
		}
	})
}

// The initial load and the CDC stream must agree on a row's key.
//
// The backfill finishes before streaming starts, but both are in flight inside
// the engine at the handover: the snapshot row is still queued on one worker
// while the first UPDATE to that row arrives. If they hash differently the
// update can be written first and the snapshot row then overwrites it with the
// older value — silently, and only for rows changed during the handover.
func TestSnapshotAndCDCAgreeOnTheKey(t *testing.T) {
	p := &PostgresSource{msgChan: make(chan hermod.Message, 1)}

	if err := p.emitSnapshotRecord(t.Context(), "public.orders",
		map[string]any{"id": 42, "status": "new"}, []string{"id"}); err != nil {
		t.Fatalf("emitSnapshotRecord: %v", err)
	}
	snap := <-p.msgChan
	defer snap.Release()

	r := rel(keyCol("id", pgtype.Int8OID), col("status", pgtype.TextOID))
	r.RelationID = 7
	cdcSrc := &PostgresSource{relations: map[uint32]*pglogrepl.RelationMessage{7: r}}
	cdc := cdcSrc.handleUpdate(101, &pglogrepl.UpdateMessage{
		RelationID: 7, NewTuple: tuple(text("42"), text("shipped"))})

	if got, want := hermod.OrderingKey(snap), hermod.OrderingKey(cdc); got != want {
		t.Errorf("snapshot key %q != CDC key %q; at handover the snapshot row and the "+
			"update to it land on different workers and the older value can win", got, want)
	}
}

// An unqualified table name has to resolve to the same schema the replication
// stream reports, which is the search_path default.
func TestUnqualifiedSnapshotTableAssumesPublic(t *testing.T) {
	p := &PostgresSource{msgChan: make(chan hermod.Message, 1)}
	if err := p.emitSnapshotRecord(t.Context(), "orders",
		map[string]any{"id": 42}, []string{"id"}); err != nil {
		t.Fatalf("emitSnapshotRecord: %v", err)
	}
	m := <-p.msgChan
	defer m.Release()

	if got, want := hermod.OrderingKey(m), "public.orders:42"; got != want {
		t.Errorf("ordering key = %q, want %q", got, want)
	}
}
