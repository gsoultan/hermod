package registry

import (
	"context"
	"fmt"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/internal/factory"
	"github.com/gsoultan/Hermod/internal/storage"
	"github.com/gsoultan/Hermod/internal/testutil"
	"github.com/gsoultan/Hermod/pkg/comm/message"
)

func TestE2EScenario_FullWorkflow(t *testing.T) {
	requireIntegrationInfra(t)
	// 1. Setup Registry with Mock Storage
	ms := &mockE2EStorage{
		sources:   make(map[string]storage.Source),
		sinks:     make(map[string]storage.Sink),
		workflows: make(map[string]storage.Workflow),
	}

	// Pre-populate storage for Registry to find configs
	ms.sources["source-1"] = storage.Source{ID: "source-1", Type: "json"}
	ms.sinks["sink-1"] = storage.Sink{ID: "sink-1", Type: "postgres"}

	reg := NewRegistry(ms)

	// 2. Define mock components
	received := make(chan hermod.Message, 10)
	mockPgSink := &mockSink{received: received}

	msg := message.AcquireMessage()
	msg.SetData("name", "hermod")
	mockSrc := &mockE2ESource{msg: msg}

	reg.sourceFactory = func(cfg factory.SourceConfig) (hermod.Source, error) {
		return mockSrc, nil
	}
	reg.sinkFactory = func(cfg factory.SinkConfig) (hermod.Sink, error) {
		if cfg.Type == "postgres" {
			return mockPgSink, nil
		}
		return factory.CreateSink(cfg)
	}

	// 3. Define Workflow
	wf := storage.Workflow{
		ID:     "e2e-workflow",
		Name:   "E2E Scenario",
		Active: true,
		Nodes: []storage.WorkflowNode{
			{
				ID:     "src-1",
				Type:   "source",
				RefID:  "source-1",
				Config: map[string]any{"label": "JSON Source"},
			},
			{
				ID:   "trans-1",
				Type: "transformation",
				Config: map[string]any{
					"transType":        "set",
					"label":            "Add Prefix",
					"column.new_field": `concat("prefix_", source.name)`,
				},
			},
			{
				ID:   "trans-2",
				Type: "transformation",
				Config: map[string]any{
					"transType":         "set",
					"label":             "Uppercase",
					"column.upper_name": `upper(source.new_field)`,
				},
			},
			{
				ID:    "snk-1",
				Type:  "sink",
				RefID: "sink-1",
				Config: map[string]any{
					"label":      "Postgres Sink",
					"sequential": true,
				},
			},
		},
		Edges: []storage.WorkflowEdge{
			{SourceID: "src-1", TargetID: "trans-1"},
			{SourceID: "trans-1", TargetID: "trans-2"},
			{SourceID: "trans-2", TargetID: "snk-1"},
		},
	}

	// 4. Start Workflow
	err := reg.StartWorkflow(wf.ID, wf)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	defer reg.StopEngine(context.Background(), wf.ID)

	// 5. Verify data flow
	select {
	case receivedMsg := <-received:
		if receivedMsg.Data()["upper_name"] != "PREFIX_HERMOD" {
			t.Errorf("Sink received wrong data: %v", receivedMsg.Data())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for message in sink")
	}
}

func TestE2EPostgresConnectionTest_NoBlock(t *testing.T) {
	requireIntegrationInfra(t)
	// This test verifies that a hanging Postgres connection test does not block the environment.
	// Since we fixed Pooler.Get with singleflight and checkResourcesHealth to be async.

	reg := NewRegistry(nil)

	// Config with a DSN that will timeout (simulated by a long-running factory)
	cfg := factory.SinkConfig{
		Type: "postgres",
		Config: hermod.StringMap{
			"dsn": "postgres://postgres:postgres@10.255.255.1:5432/postgres?sslmode=disable",
		},
	}

	// We'll use a real-ish Registry.TestSink but with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := reg.TestSink(ctx, cfg)
	elapsed := time.Since(start)

	// It should return context deadline exceeded FAST (due to our ctx)
	// and NOT hang the entire process.
	if err == nil {
		t.Log("Warning: TestSink succeeded, was Postgres running?")
	} else if err != context.DeadlineExceeded && err != context.Canceled {
		t.Logf("TestSink returned error: %v (expected timeout/cancel)", err)
	}

	if elapsed > 200*time.Millisecond {
		t.Errorf("TestSink took too long: %v", elapsed)
	}
}

type mockE2EStorage struct {
	testutil.BaseMockStorage
	sources   map[string]storage.Source
	sinks     map[string]storage.Sink
	workflows map[string]storage.Workflow
}

func (m *mockE2EStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	if s, ok := m.sources[id]; ok {
		return s, nil
	}
	return storage.Source{}, storage.ErrNotFound
}

func (m *mockE2EStorage) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	if s, ok := m.sinks[id]; ok {
		return s, nil
	}
	return storage.Sink{}, storage.ErrNotFound
}

func (m *mockE2EStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	if w, ok := m.workflows[id]; ok {
		return w, nil
	}
	return storage.Workflow{}, storage.ErrNotFound
}

func (m *mockE2EStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, nil
}

func (m *mockE2EStorage) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	return nil, 0, nil
}

type mockSink struct {
	received chan hermod.Message
	fail     int
}

func (s *mockSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.fail > 0 {
		s.fail--
		return fmt.Errorf("mock sink error")
	}
	if s.received != nil {
		s.received <- msg.Clone()
	}
	return nil
}

func (s *mockSink) Ping(ctx context.Context) error { return nil }
func (s *mockSink) Close() error                   { return nil }

type mockE2ESource struct {
	msg hermod.Message
}

func (s *mockE2ESource) Read(ctx context.Context) (hermod.Message, error) {
	if s.msg == nil {
		return nil, nil
	}
	m := s.msg
	s.msg = nil // Return only once
	return m, nil
}

func (s *mockE2ESource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (s *mockE2ESource) Ping(ctx context.Context) error                    { return nil }
func (s *mockE2ESource) Close() error                                      { return nil }
