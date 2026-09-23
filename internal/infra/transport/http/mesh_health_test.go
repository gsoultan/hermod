package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// ---------------------------------------------------------------------------
// Mesh health is the third screen that describes a worker, and it described one
// less accurately than the other two.
//
// It carried a `memory` field holding storage.Worker.MemoryUsage — a fraction
// in 0..1 — which the page rendered as `{node.memory.toFixed(1)} MB`. A worker
// using 78% of its memory therefore appeared as "0.8 MB". There was no disk
// reading and no core count, so the same two numbers that could not answer
// "how much room is left" on the workers page could not answer it here either.
//
// These pin the shape the page now reads: capacity in bytes and cores,
// utilisation as a fraction, under the same names the workers list and the
// dashboard use.
// ---------------------------------------------------------------------------

type meshHealthStorage struct {
	testutil.BaseMockStorage
	workers []storage.Worker
}

func (m *meshHealthStorage) ListWorkers(context.Context, storage.CommonFilter) ([]storage.Worker, int, error) {
	return m.workers, len(m.workers), nil
}

func (m *meshHealthStorage) ListWorkflows(context.Context, storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}

func meshHealth(t *testing.T, workers []storage.Worker) []map[string]any {
	t.Helper()

	h := NewInfraHandler(&handlers.Handler{Storage: &meshHealthStorage{workers: workers}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/infra/mesh-health", nil)
	h.GetMeshHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var nodes []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("decoding the response: %v\n%s", err, rec.Body.String())
	}
	return nodes
}

func TestMeshHealth_ReportsCapacityAndUtilisation(t *testing.T) {
	seen := time.Now()
	nodes := meshHealth(t, []storage.Worker{{
		ID:       "w1",
		Name:     "worker-1",
		LastSeen: &seen,
		WorkerResources: storage.WorkerResources{
			CPUUsage:          0.25,
			MemoryUsage:       0.5,
			CPUCores:          8,
			MemoryTotalBytes:  32 << 30,
			MemoryUsedBytes:   16 << 30,
			StorageTotalBytes: 500 << 30,
			StorageUsedBytes:  125 << 30,
		},
	}})

	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	node := nodes[0]

	// A bare "memory" that is really a fraction is what produced "0.8 MB". The
	// page reads bytes now, and the fraction travels under a name that says so.
	if _, stale := node["memory"]; stale {
		t.Error(`"memory" is still on the wire; it held a fraction and the page rendered it as megabytes`)
	}

	for field, want := range map[string]float64{
		"cpu_usage":           0.25,
		"memory_usage":        0.5,
		"cpu_cores":           8,
		"memory_total_bytes":  32 << 30,
		"memory_used_bytes":   16 << 30,
		"storage_total_bytes": 500 << 30,
		"storage_used_bytes":  125 << 30,
	} {
		got, ok := node[field].(float64)
		if !ok {
			t.Errorf("%s missing from the response: %v", field, node[field])
			continue
		}
		if got != want {
			t.Errorf("%s = %v, want %v", field, got, want)
		}
	}
}

// A worker on a release from before capacity reporting says nothing, and the
// response has to carry that as absent rather than as a machine with no CPU —
// the same rule the dashboard totals and the workers list follow.
func TestMeshHealth_OmitsCapacityAWorkerDidNotReport(t *testing.T) {
	seen := time.Now()
	nodes := meshHealth(t, []storage.Worker{{ID: "old", Name: "old", LastSeen: &seen}})

	for _, field := range []string{"cpu_cores", "memory_total_bytes", "storage_total_bytes"} {
		if v, present := nodes[0][field]; present {
			t.Errorf("%s = %v, want it absent so the page can tell silence from zero", field, v)
		}
	}
}

// Status is what the badge reads, and it must not change shape underneath the
// capacity work.
func TestMeshHealth_StatusFollowsTheHeartbeat(t *testing.T) {
	now := time.Now()
	old := now.Add(-5 * time.Minute)
	degraded := now.Add(-45 * time.Second)

	nodes := meshHealth(t, []storage.Worker{
		{ID: "fresh", LastSeen: &now},
		{ID: "degraded", LastSeen: &degraded},
		{ID: "gone", LastSeen: &old},
		{ID: "never"},
	})

	want := map[string]string{"fresh": "online", "degraded": "degraded", "gone": "offline", "never": "offline"}
	for _, node := range nodes {
		id, _ := node["id"].(string)
		if got := node["status"]; got != want[id] {
			t.Errorf("%s status = %v, want %s", id, got, want[id])
		}
	}
}
