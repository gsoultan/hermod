package worker

import (
	"context"

	"github.com/user/hermod/internal/storage"
)

// WorkerStorage interface subset needed by worker.
type WorkerStorage interface {
	GetWorker(ctx context.Context, id string) (storage.Worker, error)
	CreateWorker(ctx context.Context, worker storage.Worker) error
	ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error)
	GetWorkflow(ctx context.Context, id string) (storage.Workflow, error)
	UpdateWorkflow(ctx context.Context, wf storage.Workflow) error
	UpdateWorkflowStatus(ctx context.Context, id string, status string) error
	UpdateWorkflowStats(ctx context.Context, id string, processed, errors, lag uint64) error
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetSink(ctx context.Context, id string) (storage.Sink, error)
	ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error)
	ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error)
	ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error)
	UpdateSource(ctx context.Context, src storage.Source) error
	UpdateSink(ctx context.Context, snk storage.Sink) error
	UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error
	DeleteWorker(ctx context.Context, id string) error
	CreateLog(ctx context.Context, log storage.Log) error
	AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error)
	RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error)
	ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error
}

type SyncContext struct {
	SourceMap map[string]storage.Source
	SinkMap   map[string]storage.Sink
	WorkerID  string
}
