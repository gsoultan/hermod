package postgres

import (
	"testing"

	"github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	"github.com/gsoultan/Hermod/pkg/infra/sqlutil"
)

func bulkTestMappings() []sqlutil.ColumnMapping {
	return []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "id", DataType: "TEXT", IsPrimaryKey: true},
		{SourceField: "name", TargetColumn: "name", DataType: "TEXT", IsNullable: true},
	}
}

func msgWithOp(id string, op hermod.Operation) hermod.Message {
	m := message.AcquireMessage()
	m.SetID(id)
	m.SetOperation(op)
	m.SetAfter([]byte(`{"id":"` + id + `","name":"n"}`))
	return m
}

func batchOf(n int, op hermod.Operation) []hermod.Message {
	msgs := make([]hermod.Message, 0, n)
	for i := range n {
		msgs = append(msgs, msgWithOp(string(rune('a'+i%26))+string(rune('0'+i/26)), op))
	}
	return msgs
}

// The bulk path trades per-row ordering for throughput, so it may only be
// chosen when that trade is invisible: an insert-only batch, large enough for
// COPY to pay for itself, with no per-message table routing or soft-delete
// rewriting. Anything else — and anything uncertain — must fall back to the
// ordered per-message path that preserves CDC semantics.
func TestClassifyBatch(t *testing.T) {
	tests := []struct {
		name  string
		build func() (*PostgresSink, []hermod.Message)
		want  bulkMode
	}{
		{
			name: "large insert-only batch is eligible",
			build: func() (*PostgresSink, []hermod.Message) {
				s := NewPostgresSink("", "t", bulkTestMappings(), false, "hard_delete", "", "", "", false, false)
				return s, batchOf(bulkMinRows, hermod.OpCreate)
			},
			want: bulkModeCopy,
		},
		{
			name: "batch containing a delete is not eligible",
			build: func() (*PostgresSink, []hermod.Message) {
				s := NewPostgresSink("", "t", bulkTestMappings(), false, "hard_delete", "", "", "", false, false)
				msgs := batchOf(bulkMinRows, hermod.OpCreate)
				msgs[len(msgs)-1] = msgWithOp("zz", hermod.OpDelete)
				return s, msgs
			},
			want: bulkModeNone,
		},
		{
			name: "batch containing an update is not eligible",
			build: func() (*PostgresSink, []hermod.Message) {
				s := NewPostgresSink("", "t", bulkTestMappings(), false, "hard_delete", "", "", "", false, false)
				msgs := batchOf(bulkMinRows, hermod.OpCreate)
				msgs[0] = msgWithOp("zz", hermod.OpUpdate)
				return s, msgs
			},
			want: bulkModeNone,
		},
		{
			name: "small batch is not worth COPY overhead",
			build: func() (*PostgresSink, []hermod.Message) {
				s := NewPostgresSink("", "t", bulkTestMappings(), false, "hard_delete", "", "", "", false, false)
				return s, batchOf(bulkMinRows-1, hermod.OpCreate)
			},
			want: bulkModeNone,
		},
		{
			name: "no mappings means no known column list",
			build: func() (*PostgresSink, []hermod.Message) {
				s := NewPostgresSink("", "t", nil, false, "hard_delete", "", "", "", false, false)
				return s, batchOf(bulkMinRows, hermod.OpCreate)
			},
			want: bulkModeNone,
		},
		{
			name: "soft delete rewriting is not eligible",
			build: func() (*PostgresSink, []hermod.Message) {
				s := NewPostgresSink("", "t", bulkTestMappings(), false, "soft_delete", "deleted_at", "now()", "", false, false)
				return s, batchOf(bulkMinRows, hermod.OpCreate)
			},
			want: bulkModeNone,
		},
		{
			name: "per-message table routing is not eligible",
			build: func() (*PostgresSink, []hermod.Message) {
				s := NewPostgresSink("", "", bulkTestMappings(), false, "hard_delete", "", "", "", false, false)
				msgs := batchOf(bulkMinRows, hermod.OpCreate)
				msgs[0].SetMetadata("table", "other_table")
				return s, msgs
			},
			want: bulkModeNone,
		},
		{
			name: "empty batch is not eligible",
			build: func() (*PostgresSink, []hermod.Message) {
				s := NewPostgresSink("", "t", bulkTestMappings(), false, "hard_delete", "", "", "", false, false)
				return s, nil
			},
			want: bulkModeNone,
		},
		{
			name: "explicit upsert operation mode is not eligible",
			build: func() (*PostgresSink, []hermod.Message) {
				s := NewPostgresSink("", "t", bulkTestMappings(), false, "hard_delete", "", "", "update", false, false)
				return s, batchOf(bulkMinRows, hermod.OpCreate)
			},
			want: bulkModeNone,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, msgs := tc.build()
			defer func() {
				for _, m := range msgs {
					m.Release()
				}
			}()

			if got := s.classifyBatch(msgs); got != tc.want {
				t.Errorf("classifyBatch = %v; want %v", got, tc.want)
			}
		})
	}
}

// Within one COPY batch the rows land in a staging table and are merged in a
// single statement, so a duplicate key would make the merge ambiguous
// ("ON CONFLICT DO UPDATE command cannot affect row a second time"). Collapsing
// to last-wins matches what the sequential path produces, where a later message
// overwrites an earlier one with the same key.
func TestDedupeByKeyLastWins(t *testing.T) {
	rows := [][]any{
		{"a", "first"},
		{"b", "only"},
		{"a", "second"},
		{"c", "only"},
		{"a", "third"},
	}

	got := dedupeByKeyLastWins(rows, []int{0})

	if len(got) != 3 {
		t.Fatalf("got %d rows; want 3 (%v)", len(got), got)
	}
	// Order of first appearance is preserved so the merge stays deterministic.
	if got[0][0] != "a" || got[0][1] != "third" {
		t.Errorf("row 0 = %v; want [a third] (last write must win)", got[0])
	}
	if got[1][0] != "b" || got[2][0] != "c" {
		t.Errorf("surviving order = %v, %v; want b then c", got[1][0], got[2][0])
	}
}
