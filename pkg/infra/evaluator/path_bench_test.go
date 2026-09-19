package evaluator

// Benchmarks for the field-access paths every transformation, router condition
// and sink mapping goes through. They exist because these were once O(row) per
// field read — see path_fastpath_test.go — and nothing else would show that
// coming back.

import (
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
func BenchmarkSetValByPath(b *testing.B) {
	for _, cols := range []int{8, 32, 128} {
		b.Run(fmt.Sprintf("cols=%d", cols), func(b *testing.B) {
			row := benchRow(cols)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				SetValByPath(row, "col_3", "updated")
			}
		})
	}
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
