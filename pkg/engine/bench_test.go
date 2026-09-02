package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/buffer"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine/config"
)

// These benchmarks establish the end-to-end throughput baselines referenced in
// BENCHMARKS.md. Until they existed every msgs/s figure in README.md was an
// unverified target. Run with:
//
//	go test ./pkg/engine -bench=. -benchmem -run='^$'
//
// They deliberately use in-memory sources and sinks so the numbers isolate
// engine overhead (traversal, pooling, backpressure, buffer handoff) from
// network and disk cost. Sink-specific baselines live next to each sink.

// benchLogger discards everything. The engine logs per-message on some paths
// and writing to stderr would dominate the measurement.
type benchLogger struct{}

func (benchLogger) Debug(msg string, kv ...any) {}
func (benchLogger) Info(msg string, kv ...any)  {}
func (benchLogger) Warn(msg string, kv ...any)  {}
func (benchLogger) Error(msg string, kv ...any) {}

// benchSource emits pre-built messages with no artificial delay. Unlike
// mockSource in core_test.go it does not sleep, because a 10ms tick would
// measure time.After rather than the engine.
type benchSource struct {
	remaining atomic.Int64
	payload   []byte
}

func newBenchSource(count int64, payloadBytes int) *benchSource {
	s := &benchSource{payload: make([]byte, payloadBytes)}
	for i := range s.payload {
		s.payload[i] = byte('a' + i%26)
	}
	s.remaining.Store(count)
	return s
}

func (s *benchSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.remaining.Add(-1) < 0 {
		// Block until cancelled rather than spinning: the engine treats a nil
		// message as "nothing available" and would busy-loop otherwise.
		<-ctx.Done()
		return nil, ctx.Err()
	}
	m := message.AcquireMessage()
	m.SetPayload(s.payload)
	m.SetData("seq", s.remaining.Load())
	return m, nil
}

func (s *benchSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *benchSource) Ping(ctx context.Context) error                    { return nil }
func (s *benchSource) Close() error                                      { return nil }

// nullSink discards everything and counts. It is the ceiling: no engine
// configuration can be faster than writing to it.
type nullSink struct {
	written atomic.Int64
	done    chan struct{}
	target  int64
	fired   atomic.Bool
}

func newNullSink(target int64) *nullSink {
	return &nullSink{done: make(chan struct{}), target: target}
}

func (s *nullSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.written.Add(1) >= s.target && s.fired.CompareAndSwap(false, true) {
		close(s.done)
	}
	return nil
}

func (s *nullSink) Ping(ctx context.Context) error { return nil }
func (s *nullSink) Close() error                   { return nil }

// nullBatchSink additionally implements hermod.BatchSink so the engine takes
// its batched write path. Comparing it against nullSink isolates the value of
// batching independently of any real sink's network cost.
type nullBatchSink struct {
	nullSink
	batches atomic.Int64
}

func (s *nullBatchSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	s.batches.Add(1)
	if s.written.Add(int64(len(msgs))) >= s.target && s.fired.CompareAndSwap(false, true) {
		close(s.done)
	}
	return nil
}

// runThroughput drives count messages through an engine and reports msgs/s.
// It returns the observed wall time for the caller to convert into a rate.
func runThroughput(b *testing.B, count int64, payloadBytes int, cfg config.Config, sinkCfg config.SinkConfig, useBatch bool) time.Duration {
	b.Helper()

	src := newBenchSource(count, payloadBytes)

	var snk hermod.Sink
	var done chan struct{}
	if useBatch {
		bs := &nullBatchSink{done: make(chan struct{}), target: count}
		snk, done = bs, bs.done
	} else {
		ns := newNullSink(count)
		snk, done = ns, ns.done
	}

	eng := NewEngine(src, []hermod.Sink{snk}, buffer.NewRingBuffer(4096))
	eng.SetConfig(cfg)
	eng.SetSinkConfigs([]config.SinkConfig{sinkCfg})
	eng.SetLogger(benchLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	go func() { _ = eng.Start(ctx) }()

	select {
	case <-done:
	case <-ctx.Done():
		b.Fatalf("timed out before draining %d messages", count)
	}
	elapsed := time.Since(start)

	cancel()
	return elapsed
}

// BenchmarkEngineThroughput measures messages/second end-to-end through the
// engine with an in-memory sink, across payload sizes.
func BenchmarkEngineThroughput(b *testing.B) {
	sizes := []int{64, 1024, 16384}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("payload=%dB", size), func(b *testing.B) {
			const count = 50_000
			cfg := config.DefaultConfig()
			sinkCfg := config.SinkConfig{BackpressureBuffer: 4096}

			b.ResetTimer()
			var total time.Duration
			for i := 0; i < b.N; i++ {
				total += runThroughput(b, count, size, cfg, sinkCfg, false)
			}
			b.StopTimer()

			if b.N > 0 && total > 0 {
				perRun := total / time.Duration(b.N)
				b.ReportMetric(float64(count)/perRun.Seconds(), "msgs/s")
			}
		})
	}
}

// BenchmarkEngineThroughputBatched runs the same load against a BatchSink so
// the batched path can be compared directly with the per-message path.
func BenchmarkEngineThroughputBatched(b *testing.B) {
	batchSizes := []int{1, 100, 500}
	for _, bs := range batchSizes {
		b.Run(fmt.Sprintf("batch=%d", bs), func(b *testing.B) {
			const count = 50_000
			cfg := config.DefaultConfig()
			sinkCfg := config.SinkConfig{
				BatchSize:          bs,
				BatchTimeout:       50 * time.Millisecond,
				BackpressureBuffer: 4096,
			}

			b.ResetTimer()
			var total time.Duration
			for i := 0; i < b.N; i++ {
				total += runThroughput(b, count, 1024, cfg, sinkCfg, true)
			}
			b.StopTimer()

			if b.N > 0 && total > 0 {
				perRun := total / time.Duration(b.N)
				b.ReportMetric(float64(count)/perRun.Seconds(), "msgs/s")
			}
		})
	}
}

// BenchmarkMaxInflight shows how the in-flight cap trades memory for
// throughput, so the tuning advice in README.md can be grounded in numbers
// rather than intuition.
func BenchmarkMaxInflight(b *testing.B) {
	for _, inflight := range []int{16, 128, 512} {
		b.Run(fmt.Sprintf("max_inflight=%d", inflight), func(b *testing.B) {
			const count = 50_000
			cfg := config.DefaultConfig()
			cfg.MaxInflight = inflight
			sinkCfg := config.SinkConfig{BackpressureBuffer: 4096}

			b.ResetTimer()
			var total time.Duration
			for i := 0; i < b.N; i++ {
				total += runThroughput(b, count, 1024, cfg, sinkCfg, false)
			}
			b.StopTimer()

			if b.N > 0 && total > 0 {
				perRun := total / time.Duration(b.N)
				b.ReportMetric(float64(count)/perRun.Seconds(), "msgs/s")
			}
		})
	}
}

// BenchmarkBatchVsInflight is a regression guard for a throughput cliff found
// while establishing the first baselines: when BatchSize exceeds MaxInflight
// the batch can never fill from in-flight messages alone, so every flush waits
// out the full BatchTimeout. Throughput collapses by an order of magnitude.
//
// README.md's tuning advice recommends batch_size 200-500 while max_inflight
// defaults to 128, so the documented configuration lands squarely in the cliff.
func BenchmarkBatchVsInflight(b *testing.B) {
	cases := []struct {
		name      string
		batchSize int
		inflight  int
	}{
		{"batch500_inflight128_DEFAULT", 500, 128},
		{"batch500_inflight1024", 500, 1024},
		{"batch100_inflight128", 100, 128},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			const count = 50_000
			cfg := config.DefaultConfig()
			cfg.MaxInflight = tc.inflight
			sinkCfg := config.SinkConfig{
				BatchSize:          tc.batchSize,
				BatchTimeout:       50 * time.Millisecond,
				BackpressureBuffer: 4096,
			}

			b.ResetTimer()
			var total time.Duration
			for i := 0; i < b.N; i++ {
				total += runThroughput(b, count, 1024, cfg, sinkCfg, true)
			}
			b.StopTimer()

			if b.N > 0 && total > 0 {
				perRun := total / time.Duration(b.N)
				b.ReportMetric(float64(count)/perRun.Seconds(), "msgs/s")
			}
		})
	}
}
