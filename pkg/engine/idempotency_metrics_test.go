package engine

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// noop logger to satisfy hermod.Logger
type noopLogger struct{}

func (noopLogger) Debug(string, ...interface{}) {}
func (noopLogger) Info(string, ...interface{})  {}
func (noopLogger) Warn(string, ...interface{})  {}
func (noopLogger) Error(string, ...interface{}) {}

// fake sink that reports idempotency results
type reporterSink struct {
	dedup    bool
	conflict bool
}

func (r *reporterSink) Write(ctx context.Context, msg hermod.Message) error { return nil }
func (r *reporterSink) Ping(ctx context.Context) error                      { return nil }
func (r *reporterSink) Close() error                                        { return nil }
func (r *reporterSink) LastWriteIdempotent() (bool, bool)                   { return r.dedup, r.conflict }

func TestWriteToSink_EmitsIdempotencyMetrics(t *testing.T) {
	e := &Engine{}
	e.logger = noopLogger{}
	e.workflowID = "wf1"
	e.sinkConfigs = []SinkConfig{{}}

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)
	msg.SetID("m1")

	sinkID := "s1"
	rs := &reporterSink{dedup: true}

	before := testutil.ToFloat64(IdempotencyDedupTotal.WithLabelValues(e.workflowID, sinkID))
	if err := e.writeToSink(context.Background(), rs, msg, sinkID, 0); err != nil {
		t.Fatalf("writeToSink error: %v", err)
	}
	after := testutil.ToFloat64(IdempotencyDedupTotal.WithLabelValues(e.workflowID, sinkID))
	if after != before+1 {
		t.Fatalf("expected dedup metric to increment by 1, got before=%v after=%v", before, after)
	}
}
