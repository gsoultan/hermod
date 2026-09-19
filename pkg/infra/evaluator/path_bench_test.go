package evaluator

// Benchmarks for the field-access paths every transformation, router condition
// and sink mapping goes through. They exist because these were once O(row) per
// field read — see path_fastpath_test.go — and nothing else would show that
// coming back.

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
)

// benchRow builds a row of the shape a CDC source actually emits: a few dozen
// flat columns of mixed scalar types.
func benchRow(cols int) map[string]any {
	m := make(map[string]any, cols)
	for i := range cols {
		switch i % 4 {
		case 0:
			m[fmt.Sprintf("col_%d", i)] = fmt.Sprintf("value-%d-abcdefghijklmnop", i)
		case 1:
			m[fmt.Sprintf("col_%d", i)] = i
		case 2:
			m[fmt.Sprintf("col_%d", i)] = float64(i) * 1.5
		case 3:
			m[fmt.Sprintf("col_%d", i)] = i%2 == 0
		}
	}
	return m
}

// BenchmarkGetValByPath measures one field read out of a row.
func BenchmarkGetValByPath(b *testing.B) {
	for _, cols := range []int{8, 32, 128} {
		b.Run(fmt.Sprintf("cols=%d", cols), func(b *testing.B) {
			row := benchRow(cols)
			path := fmt.Sprintf("col_%d", cols/2)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if v := GetValByPath(row, path); v == nil {
					b.Fatal("nil")
				}
			}
		})
	}
}

// BenchmarkSetValByPath measures one field write into a row.
//
// The row is normalised up front, deliberately. SetValByPath's fast path only
// applies to an already-JSON-native map, and its slow path *makes* the map
// native as a side effect — so a benchmark starting from a raw row measures
// the round trip once and the fast path for every iteration after, and
// reports the average of two different things. Normalising here is what a
// message hydrated from a payload looks like anyway.
func BenchmarkSetValByPath(b *testing.B) {
	for _, cols := range []int{8, 32, 128} {
		b.Run(fmt.Sprintf("cols=%d", cols), func(b *testing.B) {
			row := jsonNativeRow(b, cols)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				SetValByPath(row, "col_3", "updated")
			}
		})
	}
}

// BenchmarkSetValByPathRoundTrip measures the path a non-native map still
// takes. The row is rebuilt per iteration because the write normalises it,
// which would otherwise hand the next iteration to the fast path; the rebuild
// is in the measurement, so read this as an upper bound.
func BenchmarkSetValByPathRoundTrip(b *testing.B) {
	for _, cols := range []int{8, 32, 128} {
		b.Run(fmt.Sprintf("cols=%d", cols), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				row := benchRow(cols) // holds ints, so never native
				SetValByPath(row, "col_3", "updated")
			}
		})
	}
}

// jsonNativeRow is benchRow put through a JSON round trip, which is the shape
// a message decoded from a payload arrives in.
func jsonNativeRow(b *testing.B, cols int) map[string]any {
	b.Helper()
	raw, err := json.Marshal(benchRow(cols))
	if err != nil {
		b.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		b.Fatalf("unmarshal: %v", err)
	}
	return out
}

// BenchmarkResolveTemplate is the sink-mapping path: one template string with
// several placeholders resolved against a row.
func BenchmarkResolveTemplate(b *testing.B) {
	for _, cols := range []int{8, 32, 128} {
		b.Run(fmt.Sprintf("cols=%d", cols), func(b *testing.B) {
			row := benchRow(cols)
			tmpl := "INSERT INTO t VALUES ('{{.col_0}}', {{.col_1}}, {{.col_2}}, '{{.col_4}}', {{.col_5}}, {{.col_6}})"
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = ResolveTemplate(tmpl, row)
			}
		})
	}
}

// BenchmarkEvaluateConditions is the router path: a two-condition filter.
func BenchmarkEvaluateConditions(b *testing.B) {
	row := benchRow(32)
	msg := message.AcquireMessage()
	for k, v := range row {
		msg.SetData(k, v)
	}
	conds := []map[string]any{
		{"field": "col_1", "operator": "gt", "value": 0},
		{"field": "col_0", "operator": "contains", "value": "value"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if !EvaluateConditions(msg, conds) {
			b.Fatal("no match")
		}
	}
}

// BenchmarkGetMsgValByPath measures the message-level field read that
// transformations use.
func BenchmarkGetMsgValByPath(b *testing.B) {
	row := benchRow(32)
	msg := message.AcquireMessage()
	for k, v := range row {
		msg.SetData(k, v)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if v := GetMsgValByPath(msg, "col_16"); v == nil {
			b.Fatal("nil")
		}
	}
}
