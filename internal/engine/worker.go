package engine

import (
	"context"
	"hash/fnv"
	"log"
	"reflect"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
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
	UpdateWorkflowStatus(ctx context.Context, id string, status string) error
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetSink(ctx context.Context, id string) (storage.Sink, error)
	ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error)
	ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error)
	ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error)
	UpdateSource(ctx context.Context, src storage.Source) error
	UpdateSink(ctx context.Context, snk storage.Sink) error
	UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error
	CreateLog(ctx context.Context, log storage.Log) error
	AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error)
	RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error)
	ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error
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
	leaseTTLSeconds   int
	renewMu           sync.Mutex
	renewCancel       map[string]context.CancelFunc
	workerCache       []storage.Worker
	workerCacheTime   time.Time
}

// NewWorker creates a new worker.
func NewWorker(storage WorkerStorage, registry *Registry) *Worker {
	return &Worker{
		storage:         storage,
		registry:        registry,
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

// SetLeaseTTL allows configuring the lease TTL in seconds (default 30).
func (w *Worker) SetLeaseTTL(ttlSeconds int) {
	if ttlSeconds <= 0 {
		ttlSeconds = 30
	}
	w.leaseTTLSeconds = ttlSeconds
}

// SetSyncInterval sets how often the worker reconciles workflows from storage.
// Useful for tests and tuning; values below 200ms are coerced to 200ms.
func (w *Worker) SetSyncInterval(d time.Duration) {
	if d < 200*time.Millisecond {
		d = 200 * time.Millisecond
	}
	w.interval = d
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

	// Ensure cleanup on shutdown
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		w.ReleaseAllLeases(cleanupCtx)
	}()

	for {
		select {
		case <-ctx.Done():
			log.Println("Worker: stopping background loop...")
			return ctx.Err()
		case <-ticker.C:
			w.sync(ctx, false)
			w.checkHealth(ctx)
		}
	}
}

// ReleaseAllLeases releases all workflow leases held by this worker.
func (w *Worker) ReleaseAllLeases(ctx context.Context) {
	if w.workerGUID == "" {
		return
	}

	log.Printf("Worker: releasing all leases for %s...", w.workerGUID)

	workflows, _, err := w.storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		log.Printf("Worker: failed to list workflows for lease release: %v", err)
		return
	}

	released := 0
	for _, wf := range workflows {
		if wf.OwnerID == w.workerGUID {
			err := w.storage.ReleaseWorkflowLease(ctx, wf.ID, w.workerGUID)
			if err != nil {
				log.Printf("Worker: failed to release lease for workflow %s: %v", wf.ID, err)
			} else {
				released++
			}
		}
	}
	if released > 0 {
		log.Printf("Worker: released %d leases", released)
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
	// If error is something other than not found, surface it (avoid blind create on transient errors)
	if err != nil && err != storage.ErrNotFound {
		log.Printf("Worker: self-registration lookup failed for %s: %v", w.workerGUID, err)
		return err
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
	ownedLeases := 0
	for _, wf := range workflows {
		if wf.Active {
			assigned := false
			if wf.WorkerID != "" {
				if w.workerGUID != "" && wf.WorkerID == w.workerGUID {
					assigned = true
				}
			} else if w.isAssigned(wf.ID, wf.OwnerID) {
				assigned = true
			}
			if assigned {
				assignedActiveCount++
				if w.workerGUID != "" && wf.OwnerID == w.workerGUID && wf.LeaseUntil != nil && time.Now().Before(*wf.LeaseUntil) {
					ownedLeases++
				}
			} else {
				// If not assigned to this worker, but it's currently running here (stale state)
				if w.registry.IsEngineRunning(wf.ID) {
					log.Printf("Worker: detected stale workflow %s running on this worker, scheduling graceful stop...", wf.ID)
					go func(id string) {
						defer func() {
							if r := recover(); r != nil {
								log.Printf("Panic in stale workflow stopper: %v", r)
							}
						}()
						_ = w.registry.StopEngineWithoutUpdate(id)
						w.stopLeaseRenewal(id)
						if w.workerGUID != "" {
							_ = w.storage.ReleaseWorkflowLease(ctx, id, w.workerGUID)
						}
					}(wf.ID)
				}
			}
		}
	}
	pkgengine.WorkerActiveWorkflows.WithLabelValues(workerID).Set(float64(assignedActiveCount))
	pkgengine.WorkerLeasesOwned.WithLabelValues(workerID).Set(float64(ownedLeases))

	if initial {
		log.Printf("Worker: found %d active workflows assigned to this worker", assignedActiveCount)
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit to 10 concurrent sync operations

	for i := range workflows {
		wf := workflows[i]
		wg.Go(func() {
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			w.syncWorkflow(ctx, wf, workerID, sourceMap, sinkMap)
		})
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
		if w.isAssigned(wf.ID, wf.OwnerID) {
			assigned = true
		}
	}

	if !assigned {
		// If it's running but no longer assigned, stop it and release lease if owned
		if w.registry.IsEngineRunning(wf.ID) {
			log.Printf("Worker: workflow %s is being handed off, stopping gracefully...", wf.ID)
			go func(id string) {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("Panic in hand-off workflow stopper: %v", r)
					}
				}()
				_ = w.registry.StopEngineWithoutUpdate(id)
				w.stopLeaseRenewal(id)
				if w.workerGUID != "" {
					_ = w.storage.ReleaseWorkflowLease(ctx, id, w.workerGUID)
				}
			}(wf.ID)
		}
		return
	}

	isRunning := w.registry.IsEngineRunning(wf.ID)

	// Lease-based ownership: only attempt if worker has GUID
	if w.workerGUID != "" {
		owned := wf.OwnerID == w.workerGUID && wf.LeaseUntil != nil && time.Now().Before(*wf.LeaseUntil)
		if !owned {
			// Try to acquire (may steal if expired)
			acquired, err := w.storage.AcquireWorkflowLease(ctx, wf.ID, w.workerGUID, w.leaseTTLSeconds)
			if err != nil {
				log.Printf("Worker: lease acquire error for %s: %v", wf.ID, err)
			}
			if acquired {
				if wf.OwnerID != "" && wf.OwnerID != w.workerGUID {
					pkgengine.LeaseStealTotal.WithLabelValues(workerID).Inc()
				} else {
					pkgengine.LeaseAcquireTotal.WithLabelValues(workerID).Inc()
				}
				owned = true
			}
		}

		if !owned {
			// We don't own the workflow; ensure it's not running here
			if isRunning {
				log.Printf("Worker: stopping workflow %s (lease lost/expired), stopping gracefully...", wf.ID)
				go func(id string) {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("Panic in lease-lost workflow stopper: %v", r)
						}
					}()
					_ = w.registry.StopEngineWithoutUpdate(id)
					w.stopLeaseRenewal(id)
				}(wf.ID)
			}
			return
		}
	}
	if isRunning {
		if !wf.Active {
			log.Printf("Worker: stopping workflow %s (marked inactive in storage)", wf.ID)
			_ = w.registry.StopEngine(wf.ID)
			w.stopLeaseRenewal(wf.ID)
			if w.workerGUID != "" {
				_ = w.storage.ReleaseWorkflowLease(ctx, wf.ID, w.workerGUID)
			}
		} else {
			// Check if configuration has changed
			curWf, ok := w.registry.GetWorkflowConfig(wf.ID)
			if ok {
				configChanged := curWf.Name != wf.Name ||
					curWf.VHost != wf.VHost ||
					curWf.DeadLetterSinkID != wf.DeadLetterSinkID ||
					curWf.PrioritizeDLQ != wf.PrioritizeDLQ ||
					curWf.MaxRetries != wf.MaxRetries ||
					curWf.RetryInterval != wf.RetryInterval ||
					curWf.ReconnectInterval != wf.ReconnectInterval ||
					curWf.DryRun != wf.DryRun ||
					curWf.IdleTimeout != wf.IdleTimeout ||
					curWf.Tier != wf.Tier ||
					curWf.SchemaType != wf.SchemaType ||
					curWf.Schema != wf.Schema ||
					curWf.Cron != wf.Cron ||
					curWf.DLQThreshold != wf.DLQThreshold ||
					curWf.TraceSampleRate != wf.TraceSampleRate ||
					curWf.TraceRetention != wf.TraceRetention ||
					curWf.AuditRetention != wf.AuditRetention ||
					curWf.WorkspaceID != wf.WorkspaceID ||
					curWf.CPURequest != wf.CPURequest ||
					curWf.MemoryRequest != wf.MemoryRequest ||
					curWf.ThroughputRequest != wf.ThroughputRequest ||
					!reflect.DeepEqual(curWf.Nodes, wf.Nodes) ||
					!reflect.DeepEqual(curWf.Edges, wf.Edges) ||
					!reflect.DeepEqual(curWf.Tags, wf.Tags)

				if !configChanged {
					// Check if underlying sources or sinks have changed
					if w.hasResourceConfigChanged(wf.ID, sourceMap, sinkMap) {
						configChanged = true
					}
				}

				if configChanged {
					log.Printf("Worker: configuration changed for workflow %s, restarting gracefully...", wf.ID)
					// Mark as Restarting in storage for better UI visibility
					_ = w.storage.UpdateWorkflowStatus(ctx, wf.ID, "Restarting")

					// Stop synchronously to ensure clean state before starting new instance
					_ = w.registry.StopEngineWithoutUpdate(wf.ID)
					w.stopLeaseRenewal(wf.ID)
					isRunning = false
				}
			}
		}
	}

	if wf.Active && !isRunning {
		if wf.Status == "Parked" {
			// Do not start parked workflows automatically.
			// They will be woken up on-demand.
			return
		}
		log.Printf("Worker: starting workflow %s...", wf.ID)
		err := w.registry.StartWorkflow(wf.ID, wf)
		if err != nil {
			log.Printf("Worker: failed to start workflow %s: %v", wf.ID, err)
			pkgengine.WorkerSyncErrors.WithLabelValues(workerID).Inc()
		} else if w.workerGUID != "" {
			w.startLeaseRenewal(wf.ID)
		}
	}
}

func (w *Worker) startLeaseRenewal(workflowID string) {
	w.renewMu.Lock()
	if cancel, ok := w.renewCancel[workflowID]; ok && cancel != nil {
		// already running
		w.renewMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.renewCancel[workflowID] = cancel
	ttl := w.leaseTTLSeconds
	if ttl <= 0 {
		ttl = 30
	}
	interval := time.Duration(ttl/2) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	w.renewMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Panic in lease renewal: %v", r)
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if w.workerGUID == "" {
					continue
				}
				ok, err := w.storage.RenewWorkflowLease(ctx, workflowID, w.workerGUID, w.leaseTTLSeconds)
				if err != nil || !ok {
					pkgengine.LeaseRenewErrorsTotal.WithLabelValues(w.workerGUID).Inc()
					log.Printf("Worker: lease renew failed for %s (ok=%v err=%v), stopping engine", workflowID, ok, err)
					_ = w.registry.StopEngineWithoutUpdate(workflowID)
					w.stopLeaseRenewal(workflowID)
					return
				}
			}
		}
	}()
}

func (w *Worker) stopLeaseRenewal(workflowID string) {
	w.renewMu.Lock()
	if cancel, ok := w.renewCancel[workflowID]; ok && cancel != nil {
		cancel()
		delete(w.renewCancel, workflowID)
	}
	w.renewMu.Unlock()
}

func (w *Worker) isAssigned(resourceID string, currentOwnerID string) bool {
	if w.workerGUID == "" {
		// Legacy behavior if no GUID: simple ID-based sharding
		if w.totalWorkers <= 1 {
			return true
		}
		h := fnv.New32a()
		h.Write([]byte(resourceID))
		return int(h.Sum32())%w.totalWorkers == w.workerID
	}

	// Resource-Aware Sharding
	ctx := context.Background()
	if time.Since(w.workerCacheTime) > 10*time.Second {
		workers, _, err := w.storage.ListWorkers(ctx, storage.CommonFilter{})
		if err == nil {
			var online []storage.Worker
			now := time.Now()
			for _, wrk := range workers {
				// Worker is online if seen in the last 60 seconds
				if wrk.LastSeen != nil && now.Sub(*wrk.LastSeen) < 1*time.Minute {
					online = append(online, wrk)
				}
			}
			// Stable sort by ID
			sort.Slice(online, func(i, j int) bool { return online[i].ID < online[j].ID })
			w.workerCache = online
			w.workerCacheTime = now
		}
	}

	online := w.workerCache
	if len(online) <= 1 {
		return true
	}

	// Rendezvous Hashing (Highest Random Weight) with Resource-Aware Weights and Hysteresis
	var bestID string
	var maxScore float64 = -1.0

	for _, wrk := range online {
		h := fnv.New32a()
		h.Write([]byte(wrk.ID + ":" + resourceID))

		// Combined weight from available CPU and Memory
		// Higher free resources = higher weight
		cpuWeight := 1.1 - wrk.CPUUsage
		memWeight := 1.1 - wrk.MemoryUsage

		// Ensure weight is positive
		if cpuWeight < 0.05 {
			cpuWeight = 0.05
		}
		if memWeight < 0.05 {
			memWeight = 0.05
		}

		weight := cpuWeight * memWeight

		// Hysteresis: Give a 15% bonus to the current owner to prevent flapping due to minor load changes
		if currentOwnerID != "" && wrk.ID == currentOwnerID {
			weight *= 1.15
		}

		score := float64(h.Sum32()) * weight
		if score > maxScore {
			maxScore = score
			bestID = wrk.ID
		}
	}

	return bestID == w.workerGUID
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

func (w *Worker) getMetrics() (float64, float64) {
	// Memory usage
	v, err := mem.VirtualMemory()
	memUsage := 0.0
	if err == nil {
		memUsage = v.UsedPercent / 100.0
	} else {
		// Fallback to runtime.MemStats if gopsutil fails
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		// Assuming 8GB total if we can't get it, or just use Alloc as a ratio
		memUsage = float64(m.Alloc) / (1024 * 1024 * 1024 * 8) // 8GB baseline
	}

	// CPU usage
	// We use a short interval to get a real reading without blocking the heartbeat too long.
	// 100ms is usually enough for a decent delta.
	c, err := cpu.Percent(100*time.Millisecond, false)
	cpuUsage := 0.0
	if err == nil && len(c) > 0 {
		cpuUsage = c[0] / 100.0
	} else {
		// Fallback to goroutine-based proxy
		numCPU := float64(runtime.NumCPU())
		numGoroutine := float64(runtime.NumGoroutine())
		cpuUsage = numGoroutine / (numCPU * 100.0)
	}

	if cpuUsage > 1.0 {
		cpuUsage = 1.0
	}
	if memUsage > 1.0 {
		memUsage = 1.0
	}

	return cpuUsage, memUsage
}

func (w *Worker) checkHealth(ctx context.Context) {
	if time.Since(w.lastHealthCheck) < 30*time.Second {
		return
	}
	w.lastHealthCheck = time.Now()

	// Update worker heartbeat
	if w.workerGUID != "" {
		cpuUsage, memUsage := w.getMetrics()
		if err := w.storage.UpdateWorkerHeartbeat(ctx, w.workerGUID, cpuUsage, memUsage); err != nil {
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
			wg.Go(func() {
				semaphore <- struct{}{}
				defer func() { <-semaphore }()
				w.checkSourceHealth(ctx, src)
			})
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
			wg.Go(func() {
				semaphore <- struct{}{}
				defer func() { <-semaphore }()
				w.checkSinkHealth(ctx, snk)
			})
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
	} else if w.isAssigned(src.ID, src.WorkerID) {
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
	} else if w.isAssigned(snk.ID, snk.WorkerID) {
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
