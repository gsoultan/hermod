package autoscaler

import (
	"context"

	"github.com/user/hermod/internal/storage"
)

// WorkerManager defines the interface for managing workers (scaling, listing).
type WorkerManager interface {
	// ListWorkers returns the list of current workers and their count.
	ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error)
	// Scale updates the number of worker replicas.
	Scale(ctx context.Context, replicas int) error
}
