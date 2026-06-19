package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/engine/telemetry"
)

// Worker syncs the state of workflows from storage to the registry.
type Worker struct {
	storage           WorkerStorage
	registry          *registry.Registry
	logger            hermod.Logger
	interval          time.Duration
	workerID          int
	totalWorkers      int
	workerGUID        string
	workerToken       string
	workerName        string
	workerHost        string
	workerPort        int
	workerDescription string
	lastHealthCheck   time.Time
	leaseTTLSeconds   int
	renewMu           sync.Mutex
	renewCancel       map[string]context.CancelFunc
	cacheMu           sync.RWMutex
	workerCache       []storage.Worker
	workerCacheTime   time.Time
	workerCacheTTL    time.Duration
	currentCPU        float64
	currentMem        float64
	draining          atomic.Bool
	shutdownFunc      context.CancelFunc
}

// NewWorker creates a new worker.
func NewWorker(storage WorkerStorage, registry *registry.Registry) *Worker {
	var logger hermod.Logger = telemetry.NewDefaultLogger()
	if registry != nil {
		if rl := registry.GetLogger(); rl != nil {
			logger = rl
		}
	}
	return &Worker{
		storage:         storage,
		registry:        registry,
		logger:          logger,
		interval:        10 * time.Second,
		workerID:        0,
		totalWorkers:    1,
		workerGUID:      "",
		leaseTTLSeconds: 30,
		renewCancel:     make(map[string]context.CancelFunc),
	}
}

// SetWorkerConfig sets the worker sharding configuration and optional GUID and Token.
func (w *Worker) SetWorkerConfig(workerID, totalWorkers int, workerGUID string, workerToken string) {
	if totalWorkers < 1 {
		totalWorkers = 1
	}
	w.workerID = workerID
	w.totalWorkers = totalWorkers
	w.workerGUID = workerGUID
	w.workerToken = workerToken
}

// SetStorage updates the worker's storage backend.
func (w *Worker) SetStorage(s storage.Storage) {
	w.storage = s
}

// SetLeaseTTL allows configuring the lease TTL in seconds (default 30).
func (w *Worker) SetLeaseTTL(ttlSeconds int) {
	if ttlSeconds <= 0 {
		ttlSeconds = 30
	}
	w.leaseTTLSeconds = ttlSeconds
}

// SetSyncInterval sets how often the worker reconciles workflows from storage.
func (w *Worker) SetSyncInterval(d time.Duration) {
	if d < 200*time.Millisecond {
		d = 200 * time.Millisecond
	}
	w.interval = d
}

// SetWorkerCacheTTL sets the TTL for the worker sharding cache.
func (w *Worker) SetWorkerCacheTTL(d time.Duration) {
	w.workerCacheTTL = d
}

// SetRegistrationInfo sets the information used for self-registration.
func (w *Worker) SetRegistrationInfo(name, host string, port int, description string) {
	w.workerName = name
	w.workerHost = host
	w.workerPort = port
	w.workerDescription = description
}

// SetShutdownFunc registers a callback used to stop the host process after the
// worker has gracefully drained. It is typically the application's context
// cancel function so a dedicated worker process exits cleanly.
func (w *Worker) SetShutdownFunc(fn context.CancelFunc) {
	w.shutdownFunc = fn
}

// RequestShutdown asks this worker to begin a graceful shutdown if the given id
// matches its own GUID. It is safe to call concurrently and is a no-op for a
// different identity.
func (w *Worker) RequestShutdown(id string) {
	if id != "" && id == w.workerGUID {
		w.draining.Store(true)
	}
}

// IsDraining reports whether a graceful shutdown has been requested.
func (w *Worker) IsDraining() bool {
	return w.draining.Load()
}

// TriggerShutdown invokes the registered shutdown callback, if any, to stop the
// host process. It is called after the worker has drained.
func (w *Worker) TriggerShutdown() {
	if w.shutdownFunc != nil {
		w.shutdownFunc()
	}
}

// pollShutdownRequest checks whether the platform has flagged this worker for a
// graceful shutdown by reading its own record. It returns true once draining
// should begin.
func (w *Worker) pollShutdownRequest(ctx context.Context) bool {
	if w.draining.Load() {
		return true
	}
	if w.workerGUID == "" {
		return false
	}
	self, err := w.storage.GetWorker(ctx, w.workerGUID)
	if err != nil {
		return false
	}
	if self.Draining {
		w.draining.Store(true)
		return true
	}
	return false
}

// Start starts the worker loop.
func (w *Worker) Start(ctx context.Context) error {
	if w.workerGUID != "" {
		_ = w.SelfRegister(ctx)
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.logger.Info("Engine worker started.")
	w.checkHealth(ctx)
	w.sync(ctx, true)

	defer w.cleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if w.pollShutdownRequest(ctx) {
				w.logger.Info("Worker: graceful shutdown requested by platform; draining and handing off workflows")
				return nil
			}
			w.sync(ctx, false)
			w.checkHealth(ctx)
		}
	}
}

func (w *Worker) cleanup(ctx context.Context) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w.ReleaseAllLeases(cleanupCtx)
	if w.registry != nil {
		w.registry.StopAll()
	}
	w.Deregister(cleanupCtx)
}

// ReleaseAllLeases releases all workflow leases held by this worker.
func (w *Worker) ReleaseAllLeases(ctx context.Context) {
	if w.workerGUID == "" {
		return
	}
	workflows, _, _ := w.storage.ListWorkflows(ctx, storage.CommonFilter{})
	for _, wf := range workflows {
		if wf.OwnerID == w.workerGUID {
			_ = w.storage.ReleaseWorkflowLease(ctx, wf.ID, w.workerGUID)
		}
	}
	w.stopAllLeaseRenewals()
}

func (w *Worker) stopAllLeaseRenewals() {
	w.renewMu.Lock()
	defer w.renewMu.Unlock()
	for id, cancel := range w.renewCancel {
		cancel()
		delete(w.renewCancel, id)
	}
}

// Deregister removes the worker entry from storage.
func (w *Worker) Deregister(ctx context.Context) {
	if w.workerGUID != "" {
		_ = w.storage.DeleteWorker(ctx, w.workerGUID)
	}
}

// SelfRegister registers the worker in the storage if it doesn't already exist.
func (w *Worker) SelfRegister(ctx context.Context) error {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("Worker: self-registration panicked", "panic", r)
		}
	}()
	if w.workerGUID == "" {
		return nil
	}
	w.cleanupStaleWorkerEntries(ctx)
	_, err := w.storage.GetWorker(ctx, w.workerGUID)
	if err == nil {
		return nil
	}
	name := w.workerName
	if name == "" {
		name = w.workerGUID
	}
	return w.storage.CreateWorker(ctx, storage.Worker{
		ID:          w.workerGUID,
		Name:        name,
		Host:        w.workerHost,
		Port:        w.workerPort,
		Description: w.workerDescription,
		Token:       w.workerToken,
		LastSeen:    new(time.Now()),
	})
}

func (w *Worker) cleanupStaleWorkerEntries(ctx context.Context) {
	workers, _, err := w.storage.ListWorkers(ctx, storage.CommonFilter{})
	if err != nil {
		return
	}
	// Only remove entries that share this worker's identity (a previous
	// registration of the same logical worker) AND are no longer alive. This
	// avoids deleting a distinct, healthy peer that happens to share a name or
	// host:port (e.g. behind NAT or with duplicate configuration).
	staleAfter := w.onlineThreshold()
	for _, wrk := range workers {
		if wrk.ID == w.workerGUID {
			continue
		}
		sameIdentity := (w.workerHost != "" && wrk.Host == w.workerHost && wrk.Port == w.workerPort) ||
			(w.workerName != "" && wrk.Name == w.workerName)
		if sameIdentity && isWorkerStale(wrk, staleAfter) {
			_ = w.storage.DeleteWorker(ctx, wrk.ID)
		}
	}
}

// isWorkerStale reports whether a worker registration has not been seen within
// the given window and is therefore safe to reclaim.
func isWorkerStale(wrk storage.Worker, staleAfter time.Duration) bool {
	if wrk.LastSeen == nil {
		return true
	}
	return time.Since(*wrk.LastSeen) > staleAfter
}

func (w *Worker) startLeaseRenewal(workflowID string) {
	w.renewMu.Lock()
	if _, ok := w.renewCancel[workflowID]; ok {
		w.renewMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.renewCancel[workflowID] = cancel
	interval := time.Duration(max(5, w.leaseTTLSeconds/2)) * time.Second
	w.renewMu.Unlock()

	go w.leaseRenewalLoop(ctx, workflowID, interval)
}

func (w *Worker) leaseRenewalLoop(ctx context.Context, workflowID string, interval time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("Worker: lease renewal panicked", "workflow_id", workflowID, "panic", r)
		}
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := w.storage.RenewWorkflowLease(ctx, workflowID, w.workerGUID, w.leaseTTLSeconds)
			if err != nil || !ok {
				w.logger.Warn("Worker: lease renewal failed, stopping workflow", "workflow_id", workflowID, "renewed", ok, "error", err)
				if w.registry != nil {
					_ = w.registry.StopEngineWithoutUpdate(workflowID)
				}
				w.stopLeaseRenewal(workflowID)
				return
			}
		}
	}
}

func (w *Worker) stopLeaseRenewal(workflowID string) {
	w.renewMu.Lock()
	defer w.renewMu.Unlock()
	if cancel, ok := w.renewCancel[workflowID]; ok {
		cancel()
		delete(w.renewCancel, workflowID)
	}
}
