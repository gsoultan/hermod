package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/testutil"
)

// panicStorage panics on GetWorker, which is invoked unguarded from the worker
// loop via pollShutdownRequest. It is used to verify that Worker.Start never
// lets a panic escape and crash the host process.
type panicStorage struct {
	testutil.BaseMockStorage
}

func (s *panicStorage) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	panic("boom: simulated storage failure")
}

func (s *panicStorage) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, nil
}

// TestWorkerStartRecoversFromPanic ensures that an unexpected panic raised deep
// inside a worker step is recovered by Start and surfaced as an error instead
// of propagating and crashing the process.
func TestWorkerStartRecoversFromPanic(t *testing.T) {
	w := NewWorker(&panicStorage{}, nil)
	w.SetWorkerConfig(0, 1, "worker-guid", "")
	w.SetSyncInterval(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	err := w.Start(ctx)
	if err == nil {
		t.Fatal("expected Start to return an error after recovering from panic")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("expected panic-recovery error, got: %v", err)
	}
}
