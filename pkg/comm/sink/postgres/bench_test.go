package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/infra/sqlutil"
)

// These benchmarks establish the SQL-sink rows/s baseline referenced in
// BENCHMARKS.md. Until they existed, the "1-5k rows/s" figure in README.md was
// an estimate with nothing behind it.
//
// Run with a live Postgres:
//
//	HERMOD_INTEGRATION=1 \
//	POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/hermod_test_sink?sslmode=disable' \
//	go test ./pkg/comm/sink/postgres -bench=. -run='^$' -benchtime=1x
//
// WriteBatch applies messages one statement at a time inside a single
// transaction (see the ordering comment in WriteBatch), so throughput is bound
// by round-trips rather than by batch size. That is the ceiling these numbers
// measure, and the reason the bulk-load fast path exists.

func benchDSN(b *testing.B) string {
	b.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		b.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	return dsn
}

func benchMappings() []sqlutil.ColumnMapping {
	return []sqlutil.ColumnMapping{
		{SourceField: "id", TargetColumn: "id", DataType: "TEXT", IsPrimaryKey: true},
		{SourceField: "name", TargetColumn: "name", DataType: "TEXT", IsNullable: true},
		{SourceField: "amount", TargetColumn: "amount", DataType: "INTEGER", IsNullable: true},
		{SourceField: "note", TargetColumn: "note", DataType: "TEXT", IsNullable: true},
	}
}

// buildBatch produces insert-only messages, the shape a backfill or batch_sql
// source emits. CDC batches with mixed operations are deliberately excluded:
// they must keep the ordered per-message path.
func buildBatch(n int, idPrefix string) []hermod.Message {
	msgs := make([]hermod.Message, 0, n)
	for i := 0; i < n; i++ {
		m := message.AcquireMessage()
		id := fmt.Sprintf("%s-%d", idPrefix, i)
		m.SetID(id)
		m.SetOperation(hermod.OpCreate)
		payload, _ := json.Marshal(map[string]any{
			"id":     id,
			"name":   fmt.Sprintf("row %d", i),
			"amount": i,
			"note":   "benchmark row used to measure sink throughput",
		})
		m.SetAfter(payload)
		msgs = append(msgs, m)
	}
	return msgs
}

func resetBenchTable(b *testing.B, dsn, table string) {
	b.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		b.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		b.Fatalf("drop table: %v", err)
	}
}

// BenchmarkPostgresWriteBatch measures rows/second through WriteBatch across
// batch sizes. It reports rows/s so the result can be compared directly with
// SSIS fast-load and raw COPY figures.
func BenchmarkPostgresWriteBatch(b *testing.B) {
	dsn := benchDSN(b)

	for _, size := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("rows=%d", size), func(b *testing.B) {
			table := fmt.Sprintf("hermod_bench_%d", size)
			resetBenchTable(b, dsn, table)

			snk := NewPostgresSink(dsn, table, benchMappings(), false, "hard_delete", "", "", "", false, false)
			defer snk.Close()

			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				msgs := buildBatch(size, fmt.Sprintf("run%d", i))
				if err := snk.WriteBatch(ctx, msgs); err != nil {
					b.Fatalf("WriteBatch: %v", err)
				}
				for _, m := range msgs {
					m.Release()
				}
			}
			b.StopTimer()

			elapsed := b.Elapsed().Seconds()
			if elapsed > 0 {
				b.ReportMetric(float64(size*b.N)/elapsed, "rows/s")
			}
		})
	}
}
