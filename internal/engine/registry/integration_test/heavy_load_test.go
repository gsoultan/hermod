package registry_test

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/testutil"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/engine/config"
)

type mockSource struct {
	msgChan chan hermod.Message
}

func (m *mockSource) Read(ctx context.Context) (hermod.Message, error) {
	select {
	case msg := <-m.msgChan:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (m *mockSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (m *mockSource) Close() error                                      { return nil }
func (m *mockSource) Ping(ctx context.Context) error                    { return nil }

type mockSink struct {
	count atomic.Int64
}

func (m *mockSink) Write(ctx context.Context, msg hermod.Message) error {
	m.count.Add(1)
	return nil
}
func (m *mockSink) Close() error                   { return nil }
func (m *mockSink) Ping(ctx context.Context) error { return nil }

type mockStorage struct {
	testutil.BaseMockStorage
}

func (m *mockStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id, Name: "mock"}, nil
}
func (m *mockStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id, Name: "mock"}, nil
}
func (m *mockStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return storage.Workflow{ID: id}, nil
}
func (m *mockStorage) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	return nil
}
func (m *mockStorage) UpdateSourceStatus(ctx context.Context, id string, status string) error {
	return nil
}
func (m *mockStorage) UpdateSinkStatus(ctx context.Context, id string, status string) error {
	return nil
}

func TestHeavyLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy load test in short mode")
	}

	ms := &mockStorage{}
	reg := registry.NewRegistry(ms)

	// Create a complex workflow
	wf := storage.Workflow{
		ID: "heavy_load_wf",
		Nodes: []storage.WorkflowNode{
			{ID: "src1", Type: "source", RefID: "src_ref"},
			{ID: "trans1", Type: "transformation", Config: map[string]any{"transType": "set", "field": "a", "value": 1}},
			{ID: "trans2", Type: "transformation", Config: map[string]any{"transType": "set", "field": "b", "value": 2}},
			{ID: "snk1", Type: "sink", RefID: "snk_ref"},
		},
		Edges: []storage.WorkflowEdge{
			{ID: "e1", SourceID: "src1", TargetID: "trans1"},
			{ID: "e2", SourceID: "trans1", TargetID: "trans2"},
			{ID: "e3", SourceID: "trans2", TargetID: "snk1"},
		},
		ThroughputRequest: 1024,
		MaxRetries:        3,
		RetryInterval:     "100ms",
	}

	// Increase engine limits for stress test
	os.Setenv("HERMOD_RINGBUFFER_CAP", "10000")
	defer os.Unsetenv("HERMOD_RINGBUFFER_CAP")

	src := &mockSource{msgChan: make(chan hermod.Message, 1000)}
	snk := &mockSink{}

	// We need to inject these into registry factories
	reg.SetSourceFactory(func(cfg factory.SourceConfig) (hermod.Source, error) {
		return src, nil
	})
	reg.SetSinkFactory(func(cfg factory.SinkConfig) (hermod.Sink, error) {
		return snk, nil
	})

	// Mock storage
	// We'll skip actual storage for this test by not providing it to registry
	// But StartWorkflow needs to fetch it. Let's mock a simple storage.
	// Actually, let's just use reg.StartWorkflowWithConfig if available? No.

	// I'll manually start the engine for simplicity if Registry doesn't allow direct injection.
	// But I want to test the WHOLE Registry path including traversal.

	err := reg.StartWorkflow(wf.ID, wf)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}

	// Dynamically update sink config (this works because it's checked per batch)
	if eng, found := reg.GetEngine(wf.ID); found {
		eng.UpdateSinkConfig("snk_ref", func(cfg *config.SinkConfig) {
			cfg.BatchSize = 1000
			cfg.BatchTimeout = 10 * time.Millisecond
		})
	}

	const numMessages = 100000

	var msStart runtime.MemStats
	runtime.ReadMemStats(&msStart)

	start := time.Now()

	go func() {
		for i := 0; i < numMessages; i++ {
			msg := message.AcquireMessage()
			msg.SetData("i", i)
			src.msgChan <- msg
		}
	}()

	// Wait for processing
	for snk.count.Load() < numMessages {
		if time.Since(start) > 60*time.Second {
			t.Fatalf("Timeout waiting for messages, got %d", snk.count.Load())
		}
		time.Sleep(100 * time.Millisecond)
	}

	duration := time.Since(start)
	t.Logf("Processed %d messages in %v (%.2f msg/s)", numMessages, duration, float64(numMessages)/duration.Seconds())

	reg.StopEngine(t.Context(), wf.ID)

	// Check memory
	runtime.GC()
	var msEnd runtime.MemStats
	runtime.ReadMemStats(&msEnd)

	t.Logf("HeapAlloc: Start=%d, End=%d, Diff=%d", msStart.HeapAlloc, msEnd.HeapAlloc, int64(msEnd.HeapAlloc)-int64(msStart.HeapAlloc))

	// If the difference is huge (e.g. > 10MB) considering we only processed 100k small messages and released them,
	// it might indicate a leak in Message pool or traversal state.
	// Note: sync.Pool might not reclaim everything immediately.
}
