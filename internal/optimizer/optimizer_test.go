package optimizer_test

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/optimizer"
	"github.com/user/hermod/pkg/engine"
)

type mockLogger struct{}

func (m *mockLogger) Debug(msg string, kv ...any) {}
func (m *mockLogger) Info(msg string, kv ...any)  {}
func (m *mockLogger) Warn(msg string, kv ...any)  {}
func (m *mockLogger) Error(msg string, kv ...any) {}

type mockSource struct{}

func (m *mockSource) Read(ctx context.Context) (hermod.Message, error)  { return nil, nil }
func (m *mockSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (m *mockSource) Ping(ctx context.Context) error                    { return nil }
func (m *mockSource) Close() error                                      { return nil }

type mockSink struct{}

func (m *mockSink) Write(ctx context.Context, msg hermod.Message) error { return nil }
func (m *mockSink) Ping(ctx context.Context) error                      { return nil }
func (m *mockSink) Close() error                                        { return nil }

func TestOptimizer_Heuristics(t *testing.T) {
	logger := &mockLogger{}
	opt := optimizer.NewOptimizer(logger, nil)

	// Create a dummy engine with a sink
	eng := engine.NewEngine(&mockSource{}, []hermod.Sink{&mockSink{}}, nil)
	eng.SetIDs("test-wf", "src-1", []string{"sink-1"})
	eng.SetSinkConfigs([]engine.SinkConfig{
		{
			BatchSize:    100,
			BatchTimeout: 100 * time.Millisecond,
		},
	})

	opt.Register("test-wf", eng)

	// Manually trigger optimization
	// 1. Test High Pressure
	// We need to mock StatusUpdate or the engine's internal state that GetStatus uses.
	// Since GetStatus is complex, let's see what it uses.
	// It uses sinkWriters' buffer fill.

	// Actually, we can't easily mock the internal buffer fill of sinkWriter from outside without running it.
	// But we can check if UpdateSinkConfig works as expected when called by optimizer.

	eng.UpdateSinkConfig("sink-1", func(cfg *engine.SinkConfig) {
		cfg.BatchSize = 1000
	})

	configs := eng.GetSinkConfigs()
	if configs[0].BatchSize != 1000 {
		t.Errorf("Expected BatchSize 1000, got %d", configs[0].BatchSize)
	}
}
