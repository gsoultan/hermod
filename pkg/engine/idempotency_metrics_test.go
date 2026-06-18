package engine

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/engine/telemetry"
)

// idempNoopLogger is a no-op logger used to satisfy hermod.Logger in tests.
type idempNoopLogger struct{}

func (idempNoopLogger) Debug(string, ...any) {}
func (idempNoopLogger) Info(string, ...any)  {}
func (idempNoopLogger) Warn(string, ...any)  {}
func (idempNoopLogger) Error(string, ...any) {}

// reporterSink is a fake sink that reports idempotency results.
type reporterSink struct {
	dedup    bool
	conflict bool
}

func (r *reporterSink) Write(ctx context.Context, msg hermod.Message) error { return nil }
func (r *reporterSink) Ping(ctx context.Context) error                      { return nil }
func (r *reporterSink) Close() error                                        { return nil }
func (r *reporterSink) LastWriteIdempotent() (bool, bool)                   { return r.dedup, r.conflict }

func TestWriteToSink_EmitsIdempotencyMetrics(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	e.logger = idempNoopLogger{}
	e.workflowID = "wf1"
	e.sinkConfigs = []SinkConfig{{}}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("m1")

	sinkID := "s1"
	rs := &reporterSink{dedup: true}

	before := testutil.ToFloat64(telemetry.IdempotencyDedupTotal.WithLabelValues(e.workflowID, sinkID))
	if err := e.writeToSink(t.Context(), rs, msg, sinkID, 0); err != nil {
		t.Fatalf("writeToSink error: %v", err)
	}
	after := testutil.ToFloat64(telemetry.IdempotencyDedupTotal.WithLabelValues(e.workflowID, sinkID))
	if after != before+1 {
		t.Fatalf("expected dedup metric to increment by 1, got before=%v after=%v", before, after)
	}
}
