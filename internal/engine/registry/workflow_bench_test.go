package registry

// End-to-end workflow throughput.
//
// pkg/engine's BenchmarkEngineThroughput measures the engine with no workflow
// at all — in-memory source straight to in-memory sink. That is the right
// baseline for engine overhead and the wrong one for anything else: it never
// touches the evaluator, so it cannot see the cost of a condition, a mapping,
// or a sink resolving its column mappings, which is where a real pipeline
// spends its time.
//
// This drives a source -> condition -> mapping -> sink graph over rows of a
// realistic width, so the per-field-access cost is actually in the measurement.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/engine/config"
	"github.com/gsoultan/hermod/pkg/infra/state"
)

// wideSource emits `count` rows of `cols` columns, the shape a CDC source
// hands over for a table of that width.
//
// Column names and string values are built once, in newWideSource. Formatting
// them per message made the fixture itself the largest single allocation site
// in the profile — 64 Sprintf calls a message at 32 columns — which both
// inflated the per-message budget and buried the pipeline's own costs
// underneath the harness's.
type wideSource struct {
	count   int64
	cols    int
	names   []string
	strVals []string
	emitted atomic.Int64
}

func newWideSource(count int64, cols int) *wideSource {
	s := &wideSource{count: count, cols: cols,
		names:   make([]string, cols),
		strVals: make([]string, cols),
	}
	for i := range cols {
		s.names[i] = fmt.Sprintf("col_%d", i)
		s.strVals[i] = fmt.Sprintf("value-%d-abcdefghijklmnop", i)
	}
	return s
}

func (s *wideSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.emitted.Add(1) > s.count {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	m := message.AcquireMessage()
	for i := range s.cols {
		switch i % 4 {
		case 0:
			m.SetData(s.names[i], s.strVals[i])
		case 1:
			m.SetData(s.names[i], i)
		case 2:
			m.SetData(s.names[i], float64(i)*1.5)
		case 3:
			m.SetData(s.names[i], i%2 == 0)
		}
	}
	m.SetData("status", "active")
	return m, nil
}

func (s *wideSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *wideSource) Close() error                                      { return nil }
func (s *wideSource) Ping(ctx context.Context) error                    { return nil }

// benchWorkflow is source -> condition -> mapping -> sink. The condition and
// the mapping are the two node types a pipeline almost always has, and between
// them they exercise EvaluateConditions and EvaluateField.
func benchWorkflow(id string) storage.Workflow {
	return storage.Workflow{
		ID:   id,
		Name: id,
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-wide"},
			{ID: "cond", Type: "condition", Config: map[string]any{
				"field":    "status",
				"operator": "regex",
				"value":    "^(active|pending)$",
			}},
			{ID: "map", Type: "transformation", Config: map[string]any{
				"transType":   "mapping",
				"field":       "col_1",
				"targetField": "band",
				"mappingType": "range",
				"mapping":     `{"0-10":"low","11-1000":"high"}`,
			}},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src", TargetID: "cond"},
			{ID: "e2", SourceID: "cond", TargetID: "map", SourceHandle: "true"},
			{ID: "e3", SourceID: "map", TargetID: "out"},
		},
	}
}

func startBenchPipeline(b testing.TB, reg *Registry, wf storage.Workflow, src hermod.Source, snk *pipeSink) func() {
	b.Helper()
	reg.SetFactories(
		func(cfg factory.SourceConfig) (hermod.Source, error) { return src, nil },
		func(cfg factory.SinkConfig) (hermod.Sink, error) { return snk, nil },
	)
	if err := reg.StartWorkflow(wf.ID, wf); err != nil {
		b.Fatalf("StartWorkflow(%s): %v", wf.ID, err)
	}
	if eng, ok := reg.GetEngine(wf.ID); ok {
		eng.UpdateSinkConfig("snk-out", func(cfg *config.SinkConfig) {
			cfg.BatchSize = 1
			cfg.BatchTimeout = 5 * time.Millisecond
		})
	}
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = reg.StopEngine(stopCtx, wf.ID)
	}
}

// BenchmarkWorkflowThroughput reports messages/second through a real workflow,
// across row widths. Width is the axis that matters: field access used to cost
// O(row), so a wider table got quadratically more expensive to process.
func BenchmarkWorkflowThroughput(b *testing.B) {
	for _, cols := range []int{8, 32, 128} {
		b.Run(fmt.Sprintf("cols=%d", cols), func(b *testing.B) {
			const messages = 20_000

			var total time.Duration
			for i := 0; i < b.N; i++ {
				total += runWorkflowOnce(b, messages, cols, i)
			}
			b.StopTimer()

			if b.N > 0 && total > 0 {
				perRun := total / time.Duration(b.N)
				b.ReportMetric(float64(messages)/perRun.Seconds(), "msgs/s")
			}
		})
	}
}

func runWorkflowOnce(b testing.TB, messages, cols, run int) time.Duration {
	b.Helper()

	reg := NewRegistry(newPipeStorage())
	reg.SetStateStore(state.NewMemoryStore())

	snk := &pipeSink{name: "out", countOnly: true}
	src := newWideSource(int64(messages), cols)
	wf := benchWorkflow(fmt.Sprintf("wf-bench-%d-%d", cols, run))

	start := time.Now()
	stop := startBenchPipeline(b, reg, wf, src, snk)

	deadline := time.After(2 * time.Minute)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for snk.count() < messages {
		select {
		case <-deadline:
			stop()
			b.Fatalf("only %d of %d messages reached the sink before the deadline", snk.count(), messages)
		case <-tick.C:
		}
	}
	elapsed := time.Since(start)
	stop()
	return elapsed
}
