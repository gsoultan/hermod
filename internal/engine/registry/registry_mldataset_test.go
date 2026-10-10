package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// The Collect Dataset sink from where an operator configures it: a stored
// sink of a vhost, built the way a workflow builds it, writing a record
// marked with the workflow's vhost to that vhost's dataset on the worker.
func TestACollectDatasetSinkFromStorageAppendsToItsVHostsDataset(t *testing.T) {
	reg := newSimRegistry(t)
	var mu sync.Mutex
	var got []struct {
		Path    string
		Replace bool
		Rows    []map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no such dataset"}`))
			return
		}
		var body struct {
			Rows    []map[string]any `json:"rows"`
			Replace bool             `json:"replace"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, struct {
			Path    string
			Replace bool
			Rows    []map[string]any
		}{r.URL.Path, body.Replace, body.Rows})
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": len(body.Rows)})
	}))
	t.Cleanup(srv.Close)
	reg.mlWorker = worker.New(srv.URL, "", nil)

	if err := reg.store().(storage.Storage).CreateSink(t.Context(), storage.Sink{
		ID: "collect", Name: "collect customers", Type: "ml_dataset", VHost: "tenant-a",
		Config: map[string]string{"dataset": "customers", "mask_fields": "email", "mask_type": "email"},
	}); err != nil {
		t.Fatalf("CreateSink: %v", err)
	}
	snk, err := reg.resolveAndCreateSink(t.Context(), "collect")
	if err != nil {
		t.Fatalf("building the stored sink: %v", err)
	}
	t.Cleanup(func() { _ = snk.Close() })

	msg := message.AcquireMessage()
	msg.SetOperation(hermod.OpCreate)
	msg.SetData("age", 30)
	msg.SetData("email", "ada@example.com")
	msg.SetVHost("tenant-a")
	if err := snk.Write(t.Context(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Path != "/v1/datasets/tenant-a/customers/rows" || got[0].Replace {
		t.Fatalf("worker calls = %+v, want one append to tenant-a/customers", got)
	}
	if row := got[0].Rows[0]; row["age"] != 30.0 || row["email"] != "a****@example.com" {
		t.Errorf("row = %v", row)
	}
}

// One append is one part file on the worker, so a Collect Dataset sink
// batches unless told otherwise.
func TestACollectDatasetSinkBatchesByDefault(t *testing.T) {
	cfg := parseSinkEngineConfig(factory.SinkConfig{Type: "ml_dataset", Config: map[string]string{"dataset": "d"}})
	if cfg.BatchSize != mlDatasetBatchSize || cfg.BatchTimeout != mlDatasetBatchTimeout {
		t.Errorf("batch = %d / %v, want %d / %v", cfg.BatchSize, cfg.BatchTimeout, mlDatasetBatchSize, mlDatasetBatchTimeout)
	}
	cfg = parseSinkEngineConfig(factory.SinkConfig{Type: "ml_dataset", Config: map[string]string{"batch_size": "20", "batch_timeout": "1s"}})
	if cfg.BatchSize != 20 || cfg.BatchTimeout != time.Second {
		t.Errorf("the sink's own batch settings were overridden: %d / %v", cfg.BatchSize, cfg.BatchTimeout)
	}
	if cfg := parseSinkEngineConfig(factory.SinkConfig{Type: "stdout"}); cfg.BatchSize != 0 {
		t.Errorf("another sink type got the dataset batch: %d", cfg.BatchSize)
	}
}
