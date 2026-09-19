package mysql

// The bulk path trades one round trip per message for one per chunk. That is
// only sound where per-row ordering is not observable, so classifyBatch admits
// a batch only when every condition for safety is positively established and
// anything it cannot establish falls back to the ordered path.
//
// These are the conditions. A gap here is not a slow sink — it is rows written
// in the wrong order, or values written into the wrong columns, with no error.

import (
	"strconv"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
)

func bulkMappings() []sqlutil.ColumnMapping {
	return []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "id", IsPrimaryKey: true},
		{SourceField: "name", TargetColumn: "name"},
		{SourceField: "amount", TargetColumn: "amount"},
	}
}

func bulkSink(mut func(*MySQLSink)) *MySQLSink {
	s := &MySQLSink{
		tableName:     "orders",
		mappings:      bulkMappings(),
		operationMode: "auto",
	}
	if mut != nil {
		mut(s)
	}
	return s
}

// bulkBatch builds n insert messages for the orders table.
func bulkBatch(t *testing.T, n int) []hermod.Message {
	t.Helper()
	msgs := make([]hermod.Message, 0, n)
	for i := range n {
		m := message.AcquireMessage()
		t.Cleanup(func() { message.ReleaseMessage(m) })
		m.SetOperation(hermod.OpCreate)
		m.SetTable("orders")
		m.SetData("id", i)
		m.SetData("name", "row-"+strconv.Itoa(i))
		m.SetData("amount", float64(i)*1.5)
		msgs = append(msgs, m)
	}
	return msgs
}

func TestClassifyBatch(t *testing.T) {
	cases := []struct {
		name  string
		sink  func(*MySQLSink)
		batch func(*testing.T) []hermod.Message
		want  bulkMode
	}{
		{
			name:  "insert-only batch into one table",
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows) },
			want:  bulkModeMultiValues,
		},
		{
			name:  "below the minimum is not worth building",
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows-1) },
			want:  bulkModeNone,
		},
		{
			name:  "no mappings means no stable tuple shape",
			sink:  func(s *MySQLSink) { s.mappings = nil },
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows) },
			want:  bulkModeNone,
		},
		{
			name:  "soft delete rewrites rows rather than inserting",
			sink:  func(s *MySQLSink) { s.deleteStrategy = "soft_delete" },
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows) },
			want:  bulkModeNone,
		},
		{
			name:  "a soft-delete column implies the same",
			sink:  func(s *MySQLSink) { s.softDeleteColumn = "deleted_at" },
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows) },
			want:  bulkModeNone,
		},
		{
			name:  "explicit insert mode is still pure inserts",
			sink:  func(s *MySQLSink) { s.operationMode = "insert" },
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows) },
			want:  bulkModeMultiValues,
		},
		{
			name:  "update mode forces every message to an update",
			sink:  func(s *MySQLSink) { s.operationMode = "update" },
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows) },
			want:  bulkModeNone,
		},
		{
			name:  "upsert mode likewise",
			sink:  func(s *MySQLSink) { s.operationMode = "upsert" },
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows) },
			want:  bulkModeNone,
		},
		{
			name:  "delete mode likewise",
			sink:  func(s *MySQLSink) { s.operationMode = "delete" },
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows) },
			want:  bulkModeNone,
		},
		{
			name:  "per-message table routing fans across tables",
			sink:  func(s *MySQLSink) { s.tableName = "" },
			batch: func(t *testing.T) []hermod.Message { return bulkBatch(t, bulkMinRows) },
			want:  bulkModeNone,
		},
		{
			name: "one delete in the batch makes ordering observable",
			batch: func(t *testing.T) []hermod.Message {
				msgs := bulkBatch(t, bulkMinRows)
				msgs[bulkMinRows/2].(*message.DefaultMessage).SetOperation(hermod.OpDelete)
				return msgs
			},
			want: bulkModeNone,
		},
		{
			name: "one update in the batch likewise",
			batch: func(t *testing.T) []hermod.Message {
				msgs := bulkBatch(t, bulkMinRows)
				msgs[0].(*message.DefaultMessage).SetOperation(hermod.OpUpdate)
				return msgs
			},
			want: bulkModeNone,
		},
		{
			name: "a message routed to another table cannot join",
			batch: func(t *testing.T) []hermod.Message {
				msgs := bulkBatch(t, bulkMinRows)
				msgs[3].(*message.DefaultMessage).SetTable("other_table")
				return msgs
			},
			want: bulkModeNone,
		},
		{
			name: "a nil message",
			batch: func(t *testing.T) []hermod.Message {
				msgs := bulkBatch(t, bulkMinRows)
				msgs[7] = nil
				return msgs
			},
			want: bulkModeNone,
		},
		{
			name: "a snapshot message counts as an insert",
			batch: func(t *testing.T) []hermod.Message {
				msgs := bulkBatch(t, bulkMinRows)
				msgs[1].(*message.DefaultMessage).SetOperation(hermod.OpSnapshot)
				return msgs
			},
			want: bulkModeMultiValues,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bulkSink(tc.sink).classifyBatch(tc.batch(t)); got != tc.want {
				t.Errorf("classifyBatch = %v, want %v", got, tc.want)
			}
		})
	}
}

// An identity column is dropped per message when its value is empty, so two
// messages can contribute different column lists. Emitting those as one
// multi-row INSERT would shift values into the wrong columns — silently.
//
// The identity column here is deliberately not called "id". "id" is a virtual
// field: GetMsgValByPath falls back to the message's own ID when the data map
// has no usable value, and SetData("id", ...) populates that ID as a side
// effect — so an "id" column can never actually go missing, and a test built
// on one proves nothing. A plain column can.
func TestBuildBulkRowsRefusesAMixedTupleShape(t *testing.T) {
	s := bulkSink(func(s *MySQLSink) {
		s.mappings = []sqlutil.ColumnMapping{
			{SourceField: "seq", TargetColumn: "seq", IsPrimaryKey: true, IsIdentity: true},
			{SourceField: "name", TargetColumn: "name"},
		}
	})

	msgs := make([]hermod.Message, 0, 4)
	for i := range 4 {
		m := message.AcquireMessage()
		t.Cleanup(func() { message.ReleaseMessage(m) })
		m.SetOperation(hermod.OpCreate)
		m.SetTable("orders")
		m.SetData("name", "row-"+strconv.Itoa(i))
		// The third message carries no seq, so upsertMapped drops that column
		// for it and keeps it for the others.
		if i != 2 {
			m.SetData("seq", i+1)
		}
		msgs = append(msgs, m)
	}

	built, ok, err := s.buildBulkRows(msgs)
	if err != nil {
		t.Fatalf("buildBulkRows: %v", err)
	}
	if ok {
		t.Errorf("a batch with two different column lists was accepted for a single multi-row INSERT (columns %v)", built.columns)
	}
}

func TestBuildBulkRowsAcceptsAStableShape(t *testing.T) {
	built, ok, err := bulkSink(nil).buildBulkRows(bulkBatch(t, 5))
	if err != nil {
		t.Fatalf("buildBulkRows: %v", err)
	}
	if !ok {
		t.Fatal("a uniform batch was refused")
	}
	if len(built.columns) != 3 {
		t.Errorf("columns = %v, want 3", built.columns)
	}
	if len(built.rows) != 5 {
		t.Errorf("rows = %d, want 5", len(built.rows))
	}
	// The primary key must not be in the update list, or the statement would
	// rewrite the key it matched on.
	for _, u := range built.updates {
		if u == "`id` = VALUES(`id`)" {
			t.Error("the primary key is in the ON DUPLICATE KEY UPDATE list")
		}
	}
}

func TestDedupeByKeyLastWins(t *testing.T) {
	rows := [][]any{
		{1, "first"},
		{2, "other"},
		{1, "second"},
		{1, "third"},
	}
	got := dedupeByKeyLastWins(rows, []int{0})

	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %v", len(got), got)
	}
	// Last value, first position — the order the ordered path would leave.
	if got[0][1] != "third" {
		t.Errorf("row for key 1 = %v, want the last value \"third\"", got[0][1])
	}
	if got[1][1] != "other" {
		t.Errorf("row for key 2 = %v", got[1][1])
	}
}
