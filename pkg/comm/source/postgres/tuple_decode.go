package postgres

import (
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgtype"
)

// decodeTuple turns one pgoutput tuple into the row image the rest of the
// pipeline works with, and reports any column whose value the stream did not
// carry.
//
// fallback is the tuple to borrow from when the primary one says "unchanged
// TOASTed value" -- in practice the before-image of an UPDATE. Pass nil when
// there is nothing to borrow from.
//
// Two things here are not obvious:
//
// Only json and jsonb are decoded past text. pgoutput sends every column as
// its text representation (START_REPLICATION asks for proto_version '1' with
// no binary option), and handing all of them to pgx's type map would change
// `id` from the string "7" to the number 7 in every message of every existing
// workflow. json/jsonb is the case where the text form is actively wrong: the
// snapshot, polling and Sample paths all run pgx's codec and produce
// map[string]any, the workflow editor builds its field list from the sample it
// gets that way, and CDC was the one path that handed the same column over as
// an opaque string -- so `meta.addr.city` existed in the editor and resolved to
// nil at runtime.
//
// 'u' is a value the stream deliberately omits, not a null. PostgreSQL sends it
// for a TOASTed column that this UPDATE did not touch, and jsonb goes out of
// line above about 2 KB, so it is the ordinary case for documents of any size.
// It was not a case in the switch at all, so the column was dropped from the
// row image and a sink writing that image lost the document. Under REPLICA
// IDENTITY FULL the before-image does carry the value, which is what fallback
// recovers; under REPLICA IDENTITY DEFAULT nothing carries it, and then the
// column stays out of the image rather than being invented -- but the caller is
// told, so "we did not receive this" can be distinguished from "this is not a
// column".
func decodeTuple(rel *pglogrepl.RelationMessage, t, fallback *pglogrepl.TupleData) (map[string]any, []string) {
	if rel == nil || t == nil {
		return nil, nil
	}

	data := make(map[string]any, len(t.Columns))
	var unavailable []string

	for i, c := range t.Columns {
		if i >= len(rel.Columns) || c == nil {
			continue
		}
		name := rel.Columns[i].Name
		oid := rel.Columns[i].DataType

		if v, ok := decodeTupleColumn(oid, c); ok {
			data[name] = v
			continue
		}
		// 'u': borrow the value from the tuple that does carry it.
		if v, ok := decodeTupleColumn(oid, columnAt(fallback, i)); ok {
			data[name] = v
			continue
		}
		unavailable = append(unavailable, name)
	}

	return data, unavailable
}

// decodeTupleColumn reports the column's value, or ok=false when the stream did
// not send one ('u', or no column at that position at all).
func decodeTupleColumn(oid uint32, c *pglogrepl.TupleDataColumn) (any, bool) {
	if c == nil {
		return nil, false
	}
	switch c.DataType {
	case pglogrepl.TupleDataTypeNull:
		return nil, true
	case pglogrepl.TupleDataTypeText:
		return decodeColumnText(oid, c.Data), true
	case pglogrepl.TupleDataTypeBinary:
		return c.Data, true
	default: // TupleDataTypeToast
		return nil, false
	}
}

// decodeColumnText gives a json/jsonb column the same shape pgx's codec gives
// it on every other path, and leaves every other type as the text PostgreSQL
// sent. Text that does not parse is kept verbatim: a column that cannot be
// decoded must still reach the sink, and losing it was the whole problem.
func decodeColumnText(oid uint32, raw []byte) any {
	switch oid {
	case pgtype.JSONOID, pgtype.JSONBOID:
		return sqlutil.DecodeJSONColumn(raw)
	}
	return string(raw)
}

func columnAt(t *pglogrepl.TupleData, i int) *pglogrepl.TupleDataColumn {
	if t == nil || i >= len(t.Columns) {
		return nil
	}
	return t.Columns[i]
}

// noteUnavailableColumns records, on the message, the columns whose value the
// replication stream did not carry.
//
// These are TOASTed values an UPDATE left alone, on a table whose REPLICA
// IDENTITY does not include them, so nothing in the WAL record holds the bytes.
// Leaving them out of the row image is the only honest option -- inventing a
// value would be worse -- but leaving them out *silently* is what made this
// cost data for years: a sink cannot tell "this column was not sent" from
// "this column does not exist", and writes the row without it.
//
// The fix on the source side is ALTER TABLE ... REPLICA IDENTITY FULL, which
// puts the old value in the before-image where decodeTuple can recover it.
const unavailableColumnsMetadataKey = "unchanged_toast_columns"

func noteUnavailableColumns(msg hermod.Message, cols []string) {
	if len(cols) == 0 {
		return
	}
	msg.SetMetadata(unavailableColumnsMetadataKey, strings.Join(cols, ","))
}

// relationOrderingKey builds the ordering key for a CDC row.
//
// A CDC message's ID is its LSN, which is unique per change, so anything that
// hashes on the ID scatters the changes to one row across every worker. The
// relation already carries the answer: pgoutput sets Flags == 1 on the columns
// that make up the replica identity, which is exactly the row's identity.
//
// A relation with no key columns (REPLICA IDENTITY NOTHING, or a table with no
// primary key) cannot identify a row at all. That returns no key rather than
// falling back to a table-wide one: a table-wide key would quietly serialise
// every change to the table, turning a missing replica identity into a
// throughput collapse with nothing to point at.
func relationOrderingKey(rel *pglogrepl.RelationMessage, t *pglogrepl.TupleData) string {
	if rel == nil || t == nil {
		return ""
	}

	var keyValues []string
	for i, c := range t.Columns {
		if i >= len(rel.Columns) || c == nil || rel.Columns[i] == nil {
			continue
		}
		if rel.Columns[i].Flags != 1 {
			continue
		}
		v, ok := decodeTupleColumn(rel.Columns[i].DataType, c)
		if !ok {
			// A key column the stream could not give us (TOASTed, unchanged)
			// means this row cannot be identified reliably. Better no key than
			// one that aliases two different rows onto the same worker.
			return ""
		}
		keyValues = append(keyValues, fmt.Sprintf("%v", v))
	}

	return hermod.BuildOrderingKey(rel.Namespace, rel.RelationName, keyValues)
}

// setOrderingKey stamps a non-empty ordering key onto a message. An empty key
// means the row could not be identified, and stamping "" would be
// indistinguishable from a message that never had one.
func setOrderingKey(msg hermod.Message, key string) {
	if msg == nil || key == "" {
		return
	}
	msg.SetMetadata(hermod.MetaOrderingKey, key)
}

// splitQualifiedTable splits "schema.table" into its parts. An unqualified name
// is reported as being in "public": that is what the search_path resolves it to,
// and therefore what the replication stream's relation message will call it. The
// two have to agree or the backfill and the CDC stream key the same row
// differently.
func splitQualifiedTable(qualified string) (schema, table string) {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[:i], qualified[i+1:]
	}
	return "public", qualified
}
