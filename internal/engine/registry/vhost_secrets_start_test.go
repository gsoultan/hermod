package registry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	"github.com/gsoultan/hermod/pkg/infra/state"
)

// configuredStorage is pipeStorage whose connectors carry a config, the way
// every stored source and sink does. A connector with no config at all skips
// secret resolution, which is how the other fixtures here never reached it.
type configuredStorage struct {
	*pipeStorage
}

func (s configuredStorage) GetSource(_ context.Context, id string) (storage.Source, error) {
	return storage.Source{ID: id, Name: id, Type: "test", VHost: "tenant-a", Config: map[string]string{
		"host": "db", "password": "secret:DB_PASSWORD",
	}}, nil
}

func (s configuredStorage) GetSink(_ context.Context, id string) (storage.Sink, error) {
	return storage.Sink{ID: id, Name: id, Type: "test", VHost: "tenant-a", Config: map[string]string{
		"host": "db", "password": "secret:DB_PASSWORD",
	}}, nil
}

// StartWorkflow builds its connectors while it holds the registry's lock, so
// resolving their secrets must not take that lock again. It did: every
// workflow whose source had a config hung on start, and stayed hung.
func TestStartingAWorkflowResolvesItsConnectorsSecrets(t *testing.T) {
	t.Setenv("HERMOD_SECRET_DB_PASSWORD", "from-env")
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })

	reg := NewRegistry(configuredStorage{newPipeStorage()})
	reg.SetStateStore(state.NewMemoryStore())

	var mu sync.Mutex
	built := map[string]string{}
	record := func(id string, cfg map[string]string) {
		mu.Lock()
		defer mu.Unlock()
		built[id] = cfg["password"]
	}
	sinks := map[string]*pipeSink{"snk-out": {name: "out"}}
	reg.SetFactories(
		func(cfg factory.SourceConfig) (hermod.Source, error) {
			record(cfg.ID, cfg.Config)
			return &arraySource{count: 1}, nil
		},
		func(cfg factory.SinkConfig) (hermod.Sink, error) {
			record(cfg.ID, cfg.Config)
			return sinks[cfg.ID], nil
		},
	)

	wf := storage.Workflow{
		ID: "wf-start-secrets", Name: "wf-start-secrets", VHost: "tenant-a",
		Nodes: []storage.WorkflowNode{
			{ID: "src", Type: "source", RefID: "s-orders"},
			{ID: "out", Type: "sink", RefID: "snk-out"},
		},
		Edges: []storage.WorkflowEdge{{ID: "e1", SourceID: "src", TargetID: "out"}},
	}

	started := make(chan error, 1)
	go func() { started <- reg.StartWorkflow(wf.ID, wf) }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("StartWorkflow: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("StartWorkflow did not return: resolving a connector's secrets waits on the lock StartWorkflow holds")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = reg.StopEngine(ctx, wf.ID)
	})

	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"s-orders", "snk-out"} {
		if built[id] != "from-env" {
			t.Errorf("%s was built with password %q, want the resolved secret", id, built[id])
		}
	}
}
