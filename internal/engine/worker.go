package engine

import (
	"context"
	"hash/fnv"
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/user/hermod/internal/storage"
	pkgengine "github.com/user/hermod/pkg/engine"
)

// Storage interface subset needed by worker.
type WorkerStorage interface {
	GetWorker(ctx context.Context, id string) (storage.Worker, error)
	CreateWorker(ctx context.Context, worker storage.Worker) error
	ListConnections(ctx context.Context, filter storage.CommonFilter) ([]storage.Connection, int, error)
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetSink(ctx context.Context, id string) (storage.Sink, error)
	ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error)
	ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error)
	UpdateSource(ctx context.Context, src storage.Source) error
	UpdateSink(ctx context.Context, snk storage.Sink) error
	UpdateWorkerHeartbeat(ctx context.Context, id string) error
}

// Worker syncs the state of connections from storage to the registry.
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

	log.Println("Engine worker started. Checking for active connections to resume...")

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

	connections, _, err := w.storage.ListConnections(ctx, storage.CommonFilter{})
	if err != nil {
		log.Printf("Worker: failed to list connections: %v", err)
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
	for _, c := range connections {
		if c.Active {
			// Check if assigned to this worker
			assigned := false
			if c.WorkerID != "" {
				if w.workerGUID != "" && c.WorkerID == w.workerGUID {
					assigned = true
				}
			} else if w.isAssigned(c.ID) {
				assigned = true
			}
			if assigned {
				assignedActiveCount++
			}
		}
	}
	pkgengine.WorkerActiveConnections.WithLabelValues(workerID).Set(float64(assignedActiveCount))

	if initial {
		log.Printf("Worker: found %d connections in storage assigned to this worker", assignedActiveCount)
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit to 10 concurrent sync operations
	for i := range connections {
		conn := connections[i]
		wg.Add(1)
		go func(c storage.Connection) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			w.syncConnection(ctx, c, sourceMap, sinkMap, workerID)
		}(conn)
	}
	wg.Wait()

	if initial {
		log.Printf("Worker: initial sync complete")
	}
}

func (w *Worker) syncConnection(ctx context.Context, conn storage.Connection, sourceMap map[string]storage.Source, sinkMap map[string]storage.Sink, workerID string) {
	// Assignment logic:
	// 1. If workerID (GUID) is set in connection, only that worker processes it.
	// 2. If no workerID is set, use hash-based sharding for unassigned connections.
	assigned := false
	if conn.WorkerID != "" {
		if w.workerGUID != "" && conn.WorkerID == w.workerGUID {
			assigned = true
		}
	} else {
		if w.isAssigned(conn.ID) {
			assigned = true
		}
	}

	if !assigned {
		// If it's running but no longer assigned, stop it
		if w.registry.IsEngineRunning(conn.ID) {
			log.Printf("Worker: stopping connection %s (no longer assigned to this worker)", conn.ID)
			_ = w.registry.StopEngine(conn.ID)
		}
		return
	}

	isRunning := w.registry.IsEngineRunning(conn.ID)
	if isRunning {
		if !conn.Active {
			log.Printf("Worker: stopping connection %s (%s)", conn.ID, conn.Name)
			err := w.registry.StopEngine(conn.ID)
			if err != nil {
				log.Printf("Worker: failed to stop engine for connection %s: %v", conn.ID, err)
			}
			return
		}

		// Check if configuration has changed
		curSrcCfg, curSnkConfigs, curTransformations, curTransformationIDs, ok := w.registry.GetEngineConfigs(conn.ID)
		if ok {
			// Fetch latest source and sink configs from map
			src, ok := sourceMap[conn.SourceID]
			if !ok {
				log.Printf("Worker: source %s not found for connection %s", conn.SourceID, conn.ID)
				return
			}

			newSrcCfg := SourceConfig{
				ID:     src.ID,
				Type:   src.Type,
				Config: src.Config,
			}

			newSnkConfigs := make([]SinkConfig, 0, len(conn.SinkIDs))
			skipConfigCheck := false
			for _, sinkID := range conn.SinkIDs {
				snk, ok := sinkMap[sinkID]
				if !ok {
					log.Printf("Worker: sink %s not found for connection %s", sinkID, conn.ID)
					skipConfigCheck = true
					break
				}
				newSnkConfigs = append(newSnkConfigs, SinkConfig{
					ID:     snk.ID,
					Type:   snk.Type,
					Config: snk.Config,
				})
			}

			if !skipConfigCheck && (!reflect.DeepEqual(curSrcCfg, newSrcCfg) || !reflect.DeepEqual(curSnkConfigs, newSnkConfigs) || !reflect.DeepEqual(curTransformations, conn.Transformations) || !reflect.DeepEqual(curTransformationIDs, conn.TransformationIDs)) {
				log.Printf("Worker: configuration changed for connection %s (%s), restarting...", conn.ID, conn.Name)
				_ = w.registry.StopEngine(conn.ID)
				// Fall through to start logic below by setting isRunning to false
				isRunning = false
			} else if skipConfigCheck {
				return
			} else {
				// Configuration is the same, no action needed
				return
			}
		}
	}

	if conn.Active && !isRunning {
		log.Printf("Worker: starting connection %s (%s)", conn.ID, conn.Name)

		src, ok := sourceMap[conn.SourceID]
		if !ok {
			log.Printf("Worker: source %s not found for connection %s", conn.SourceID, conn.ID)
			return
		}

		// If source is assigned to another worker, this connection shouldn't run here
		if src.WorkerID != "" && w.workerGUID != "" && src.WorkerID != w.workerGUID {
			log.Printf("Worker: skipping connection %s because source %s is assigned to worker %s", conn.ID, src.ID, src.WorkerID)
			return
		}

		snkConfigs := make([]SinkConfig, 0, len(conn.SinkIDs))
		skipConnection := false
		for _, sinkID := range conn.SinkIDs {
			snk, ok := sinkMap[sinkID]
			if !ok {
				log.Printf("Worker: sink %s not found for connection %s", sinkID, conn.ID)
				continue
			}

			// If any sink is assigned to another worker, skip this connection for now
			if snk.WorkerID != "" && w.workerGUID != "" && snk.WorkerID != w.workerGUID {
				log.Printf("Worker: skipping connection %s because sink %s is assigned to worker %s", conn.ID, snk.ID, snk.WorkerID)
				skipConnection = true
				break
			}

			sc := SinkConfig{
				ID:     snk.ID,
				Type:   snk.Type,
				Config: snk.Config,
			}

			snkConfigs = append(snkConfigs, sc)
		}

		if skipConnection {
			return
		}

		err := w.registry.StartEngine(conn.ID, SourceConfig{
			ID:     src.ID,
			Type:   src.Type,
			Config: src.Config,
		}, snkConfigs, conn.Transformations, conn.TransformationIDs, conn.TransformationGroups)
		if err != nil {
			log.Printf("Worker: failed to start engine for connection %s: %v", conn.ID, err)
			pkgengine.WorkerSyncErrors.WithLabelValues(workerID).Inc()
		}
	}
}

func (w *Worker) isAssigned(connectionID string) bool {
	if w.totalWorkers <= 1 {
		return true
	}

	// Use FNV-1a for better distribution
	h := fnv.New32a()
	h.Write([]byte(connectionID))
	hash := h.Sum32()

	return int(hash)%w.totalWorkers == w.workerID
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
		if err := s.Ping(ctx); err != nil {
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
