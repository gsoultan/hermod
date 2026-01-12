package engine

import (
	"context"
	"log"
	"reflect"
	"time"

	"github.com/user/hermod/internal/storage"
)

// Storage interface subset needed by worker.
type WorkerStorage interface {
	GetWorker(ctx context.Context, id string) (storage.Worker, error)
	CreateWorker(ctx context.Context, worker storage.Worker) error
	ListConnections(ctx context.Context) ([]storage.Connection, error)
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetSink(ctx context.Context, id string) (storage.Sink, error)
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
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Worker: sync panicked: %v", r)
		}
	}()

	connections, err := w.storage.ListConnections(ctx)
	if err != nil {
		log.Printf("Worker: failed to list connections: %v", err)
		return
	}

	activeCount := 0
	for _, c := range connections {
		if c.Active {
			activeCount++
		}
	}

	if initial {
		log.Printf("Worker: found %d active connections in storage", activeCount)
	}

	for i := range connections {
		conn := connections[i]
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
			continue
		}

		isRunning := w.registry.IsEngineRunning(conn.ID)
		if isRunning {
			if !conn.Active {
				log.Printf("Worker: stopping connection %s (%s)", conn.ID, conn.Name)
				err = w.registry.StopEngine(conn.ID)
				if err != nil {
					log.Printf("Worker: failed to stop engine for connection %s: %v", conn.ID, err)
				}
				continue
			}

			// Check if configuration has changed
			curSrcCfg, curSnkConfigs, curTransformations, curTransformationIDs, ok := w.registry.GetEngineConfigs(conn.ID)
			if ok {
				// Fetch latest source and sink configs from storage
				src, err := w.storage.GetSource(ctx, conn.SourceID)
				if err != nil {
					log.Printf("Worker: failed to get source %s for connection %s: %v", conn.SourceID, conn.ID, err)
					continue
				}

				newSrcCfg := SourceConfig{
					ID:     src.ID,
					Type:   src.Type,
					Config: src.Config,
				}

				newSnkConfigs := make([]SinkConfig, 0, len(conn.SinkIDs))
				skipConfigCheck := false
				for _, sinkID := range conn.SinkIDs {
					snk, err := w.storage.GetSink(ctx, sinkID)
					if err != nil {
						log.Printf("Worker: failed to get sink %s for connection %s: %v", sinkID, conn.ID, err)
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
					continue
				} else {
					// Configuration is the same, no action needed
					continue
				}
			}
		}

		if conn.Active && !isRunning {
			log.Printf("Worker: starting connection %s (%s)", conn.ID, conn.Name)

			src, err := w.storage.GetSource(ctx, conn.SourceID)
			if err != nil {
				log.Printf("Worker: failed to get source %s for connection %s: %v", conn.SourceID, conn.ID, err)
				continue
			}

			// If source is assigned to another worker, this connection shouldn't run here
			if src.WorkerID != "" && w.workerGUID != "" && src.WorkerID != w.workerGUID {
				log.Printf("Worker: skipping connection %s because source %s is assigned to worker %s", conn.ID, src.ID, src.WorkerID)
				continue
			}

			snkConfigs := make([]SinkConfig, 0, len(conn.SinkIDs))
			skipConnection := false
			for _, sinkID := range conn.SinkIDs {
				snk, err := w.storage.GetSink(ctx, sinkID)
				if err != nil {
					log.Printf("Worker: failed to get sink %s for connection %s: %v", sinkID, conn.ID, err)
					continue
				}

				// If any sink is assigned to another worker, skip this connection for now
				// (Alternatively, we could filter sinks, but connections usually require all sinks or a specific set)
				if snk.WorkerID != "" && w.workerGUID != "" && snk.WorkerID != w.workerGUID {
					log.Printf("Worker: skipping connection %s because sink %s is assigned to worker %s", conn.ID, snk.ID, snk.WorkerID)
					skipConnection = true
					break
				}

				snkConfigs = append(snkConfigs, SinkConfig{
					ID:     snk.ID,
					Type:   snk.Type,
					Config: snk.Config,
				})
			}

			if skipConnection {
				continue
			}

			err = w.registry.StartEngine(conn.ID, SourceConfig{
				ID:     src.ID,
				Type:   src.Type,
				Config: src.Config,
			}, snkConfigs, conn.Transformations, conn.TransformationIDs)
			if err != nil {
				log.Printf("Worker: failed to start engine for connection %s: %v", conn.ID, err)
			}
		}
	}
}

func (w *Worker) isAssigned(connectionID string) bool {
	if w.totalWorkers <= 1 {
		return true
	}

	// Simple hash-based sharding
	hash := 0
	for _, char := range connectionID {
		hash += int(char)
	}

	return hash%w.totalWorkers == w.workerID
}
