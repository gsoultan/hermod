package engine

import (
	"context"
	"hash/fnv"
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	pkgengine "github.com/user/hermod/pkg/engine"
)

// Storage interface subset needed by worker.
type WorkerStorage interface {
	GetWorker(ctx context.Context, id string) (storage.Worker, error)
	CreateWorker(ctx context.Context, worker storage.Worker) error
	ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error)
	GetWorkflow(ctx context.Context, id string) (storage.Workflow, error)
	UpdateWorkflow(ctx context.Context, wf storage.Workflow) error
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetSink(ctx context.Context, id string) (storage.Sink, error)
	ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error)
	ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error)
	UpdateSource(ctx context.Context, src storage.Source) error
	UpdateSink(ctx context.Context, snk storage.Sink) error
	UpdateWorkerHeartbeat(ctx context.Context, id string) error
	CreateLog(ctx context.Context, log storage.Log) error
}

// Worker syncs the state of workflows from storage to the registry.
type Worker struct {
	storage           WorkerStorage
	registry          *Registry
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
}

// NewWorker creates a new worker.
func NewWorker(storage WorkerStorage, registry *Registry) *Worker {
	return &Worker{
		storage:      storage,
		registry:     registry,
		interval:     10 * time.Second,
		workerID:     0,
		totalWorkers: 1,
		workerGUID:   "",
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

// SetRegistrationInfo sets the information used for self-registration.
func (w *Worker) SetRegistrationInfo(name, host string, port int, description string) {
	w.workerName = name
	w.workerHost = host
	w.workerPort = port
	w.workerDescription = description
}

// Start starts the worker loop.
func (w *Worker) Start(ctx context.Context) error {
	// Self-register if GUID is provided
	if w.workerGUID != "" {
		if err := w.SelfRegister(ctx); err != nil {
			log.Printf("Worker: self-registration failed: %v", err)
		}
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	log.Println("Engine worker started. Checking for active workflows to resume...")

	// Initial sync
	w.sync(ctx, true)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.sync(ctx, false)
			w.checkHealth(ctx)
		}
	}
}

// SelfRegister registers the worker in the storage if it doesn't already exist.
func (w *Worker) SelfRegister(ctx context.Context) error {
	if w.workerGUID == "" {
		return nil
	}

	_, err := w.storage.GetWorker(ctx, w.workerGUID)
	if err == nil {
		// Already registered
		return nil
	}

	name := w.workerName
	if name == "" {
		name = w.workerGUID
	}

	// Not registered, create it
	log.Printf("Worker: self-registering as %s...", w.workerGUID)
	return w.storage.CreateWorker(ctx, storage.Worker{
		ID:          w.workerGUID,
		Name:        name,
		Host:        w.workerHost,
		Port:        w.workerPort,
		Description: w.workerDescription,
		Token:       w.workerToken,
	})
}

func (w *Worker) sync(ctx context.Context, initial bool) {
	start := time.Now()
	workerID := w.workerGUID
	if workerID == "" {
		workerID = "default"
	}

	defer func() {
		pkgengine.WorkerSyncDuration.WithLabelValues(workerID).Observe(time.Since(start).Seconds())
		if r := recover(); r != nil {
			log.Printf("Worker: sync panicked: %v", r)
			pkgengine.WorkerSyncErrors.WithLabelValues(workerID).Inc()
		}
	}()

	workflows, _, err := w.storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		log.Printf("Worker: failed to list workflows: %v", err)
		pkgengine.WorkerSyncErrors.WithLabelValues(workerID).Inc()
		return
	}

	// Pre-fetch all sources and sinks to avoid redundant storage calls in the loop
	sources, _, err := w.storage.ListSources(ctx, storage.CommonFilter{})
	if err != nil {
		log.Printf("Worker: failed to list sources: %v", err)
		return
	}
	sourceMap := make(map[string]storage.Source)
	for _, s := range sources {
		sourceMap[s.ID] = s
	}

	sinks, _, err := w.storage.ListSinks(ctx, storage.CommonFilter{})
	if err != nil {
		log.Printf("Worker: failed to list sinks: %v", err)
		return
	}
	sinkMap := make(map[string]storage.Sink)
	for _, s := range sinks {
		sinkMap[s.ID] = s
	}

	assignedActiveCount := 0
	for _, wf := range workflows {
		if wf.Active {
			assigned := false
			if wf.WorkerID != "" {
				if w.workerGUID != "" && wf.WorkerID == w.workerGUID {
					assigned = true
				}
			} else if w.isAssigned(wf.ID) {
				assigned = true
			}
			if assigned {
				assignedActiveCount++
			}
		}
	}
	pkgengine.WorkerActiveWorkflows.WithLabelValues(workerID).Set(float64(assignedActiveCount))

	if initial {
		log.Printf("Worker: found %d active workflows assigned to this worker", assignedActiveCount)
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit to 10 concurrent sync operations

	for i := range workflows {
		wf := workflows[i]
		wg.Add(1)
		go func(wf storage.Workflow) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			w.syncWorkflow(ctx, wf, workerID, sourceMap, sinkMap)
		}(wf)
	}
	wg.Wait()

	if initial {
		log.Printf("Worker: initial sync complete")
	}
}

func (w *Worker) syncWorkflow(ctx context.Context, wf storage.Workflow, workerID string, sourceMap map[string]storage.Source, sinkMap map[string]storage.Sink) {
	// Assignment logic:
	// 1. If workerID (GUID) is set in workflow, only that worker processes it.
	// 2. If no workerID is set, use hash-based sharding for unassigned workflows.
	assigned := false
	if wf.WorkerID != "" {
		if w.workerGUID != "" && wf.WorkerID == w.workerGUID {
			assigned = true
		}
	} else {
		if w.isAssigned(wf.ID) {
			assigned = true
		}
	}

	if !assigned {
		// If it's running but no longer assigned, stop it
		if w.registry.IsEngineRunning(wf.ID) {
			log.Printf("Worker: stopping workflow %s (no longer assigned to this worker)", wf.ID)
			_ = w.registry.StopEngineWithoutUpdate(wf.ID)
		}
		return
	}

	isRunning := w.registry.IsEngineRunning(wf.ID)
	if isRunning {
		if !wf.Active {
			log.Printf("Worker: stopping workflow %s (marked inactive in storage)", wf.ID)
			_ = w.registry.StopEngine(wf.ID)
		} else {
			// Check if configuration has changed
			curWf, ok := w.registry.GetWorkflowConfig(wf.ID)
			if ok {
				configChanged := !reflect.DeepEqual(curWf.Nodes, wf.Nodes) || !reflect.DeepEqual(curWf.Edges, wf.Edges) || curWf.Name != wf.Name
				if !configChanged {
					// Check if underlying sources or sinks have changed
					if w.hasResourceConfigChanged(wf.ID, sourceMap, sinkMap) {
						configChanged = true
					}
				}

				if configChanged {
					log.Printf("Worker: configuration changed for workflow %s, restarting...", wf.ID)
					_ = w.registry.StopEngineWithoutUpdate(wf.ID)
					isRunning = false
				}
			}
		}
	}

	if wf.Active && !isRunning {
		log.Printf("Worker: starting workflow %s...", wf.ID)
		err := w.registry.StartWorkflow(wf.ID, wf)
		if err != nil {
			log.Printf("Worker: failed to start workflow %s: %v", wf.ID, err)
			pkgengine.WorkerSyncErrors.WithLabelValues(workerID).Inc()
		}
	}
}

func (w *Worker) isAssigned(workflowID string) bool {
	if w.totalWorkers <= 1 {
		return true
	}

	// Use FNV-1a for better distribution
	h := fnv.New32a()
	h.Write([]byte(workflowID))
	hash := h.Sum32()

	return int(hash)%w.totalWorkers == w.workerID
}

func (w *Worker) hasResourceConfigChanged(workflowID string, sourceMap map[string]storage.Source, sinkMap map[string]storage.Sink) bool {
	srcConfigs, ok := w.registry.GetSourceConfigs(workflowID)
	if ok {
		for _, sc := range srcConfigs {
			if dbSrc, exists := sourceMap[sc.ID]; exists {
				if !reflect.DeepEqual(dbSrc.Config, sc.Config) || dbSrc.Type != sc.Type {
					return true
				}
			}
		}
	}

	snkConfigs, ok := w.registry.GetSinkConfigs(workflowID)
	if ok {
		for _, sc := range snkConfigs {
			if dbSnk, exists := sinkMap[sc.ID]; exists {
				if !reflect.DeepEqual(dbSnk.Config, sc.Config) || dbSnk.Type != sc.Type {
					return true
				}
			}
		}
	}
	return false
}

func (w *Worker) checkHealth(ctx context.Context) {
	if time.Since(w.lastHealthCheck) < 30*time.Second {
		return
	}
	w.lastHealthCheck = time.Now()

	// Update worker heartbeat
	if w.workerGUID != "" {
		if err := w.storage.UpdateWorkerHeartbeat(ctx, w.workerGUID); err != nil {
			log.Printf("Worker: failed to update heartbeat: %v", err)
		}
	}

	// Sources
	sources, _, err := w.storage.ListSources(ctx, storage.CommonFilter{})
	if err == nil {
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, 5) // Limit to 5 concurrent health checks
		for i := range sources {
			src := sources[i]
			wg.Add(1)
			go func(s storage.Source) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()
				w.checkSourceHealth(ctx, s)
			}(src)
		}
		wg.Wait()
	}

	// Sinks
	sinks, _, err := w.storage.ListSinks(ctx, storage.CommonFilter{})
	if err == nil {
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, 5) // Limit to 5 concurrent health checks
		for i := range sinks {
			snk := sinks[i]
			wg.Add(1)
			go func(s storage.Sink) {
				defer wg.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()
				w.checkSinkHealth(ctx, s)
			}(snk)
		}
		wg.Wait()
	}
}

func (w *Worker) checkSourceHealth(ctx context.Context, src storage.Source) {
	// Sharding check
	assigned := false
	if src.WorkerID != "" {
		if w.workerGUID != "" && src.WorkerID == w.workerGUID {
			assigned = true
		}
	} else if w.isAssigned(src.ID) {
		assigned = true
	}

	if !assigned {
		return
	}

	// If in use, we skip pinging as engine handles it
	if w.registry.IsResourceInUse(ctx, src.ID, "", true) {
		return
	}

	status := "running"
	s, err := CreateSource(SourceConfig{Type: src.Type, Config: src.Config})
	if err != nil {
		status = "error"
	} else {
		var pingErr error
		if readyChecker, ok := s.(hermod.ReadyChecker); ok {
			pingErr = readyChecker.IsReady(ctx)
		} else {
			pingErr = s.Ping(ctx)
		}

		if pingErr != nil {
			status = "error"
		}
		s.Close()
	}

	if src.Status != status {
		src.Status = status
		_ = w.storage.UpdateSource(ctx, src)
		w.registry.broadcastStatus(pkgengine.StatusUpdate{
			SourceID:     src.ID,
			SourceStatus: status,
		})
	}
}

func (w *Worker) checkSinkHealth(ctx context.Context, snk storage.Sink) {
	// Sharding check
	assigned := false
	if snk.WorkerID != "" {
		if w.workerGUID != "" && snk.WorkerID == w.workerGUID {
			assigned = true
		}
	} else if w.isAssigned(snk.ID) {
		assigned = true
	}

	if !assigned {
		return
	}

	// If in use, we skip pinging as engine handles it
	if w.registry.IsResourceInUse(ctx, snk.ID, "", false) {
		return
	}

	status := "running"
	s, err := CreateSink(SinkConfig{Type: snk.Type, Config: snk.Config})
	if err != nil {
		status = "error"
	} else {
		if err := s.Ping(ctx); err != nil {
			status = "error"
		}
		s.Close()
	}

	if snk.Status != status {
		snk.Status = status
		_ = w.storage.UpdateSink(ctx, snk)
		w.registry.broadcastStatus(pkgengine.StatusUpdate{
			SinkID:     snk.ID,
			SinkStatus: status,
		})
	}
}
