package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/sijms/go-ora/v2"
	_ "github.com/snowflakedb/gosnowflake"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/ai"
	"github.com/user/hermod/internal/discovery/service"
	"github.com/user/hermod/internal/engine/registry/interfaces"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/governance"
	"github.com/user/hermod/internal/mesh"
	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/optimizer"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/sink/failover"
	"github.com/user/hermod/pkg/comm/source/batchsql"
	sourceform "github.com/user/hermod/pkg/comm/source/form"
	"github.com/user/hermod/pkg/comm/transformer"
	pkgengine "github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/engine/config"
	"github.com/user/hermod/pkg/engine/telemetry"
	"github.com/user/hermod/pkg/infra/evaluator"
	"github.com/user/hermod/pkg/infra/pgxutil"
	"github.com/user/hermod/pkg/infra/schema"
	"github.com/user/hermod/pkg/infra/sqlutil"
	"github.com/user/hermod/pkg/security/secrets"
	"go.opentelemetry.io/otel"
	_ "modernc.org/sqlite"
)

var tracer = otel.Tracer("hermod-registry")

func init() {
	// Register pgx as postgres driver if not already registered
	found := false
	for _, d := range sql.Drivers() {
		if d == "postgres" {
			found = true
			break
		}
	}
	if !found {
		sql.Register("postgres", stdlib.GetDefaultDriver())
	}
}

type SourceFactory func(factory.SourceConfig) (hermod.Source, error)
type SinkFactory func(factory.SinkConfig) (hermod.Sink, error)

type LiveMessage struct {
	WorkflowID string         `json:"workflow_id"`
	NodeID     string         `json:"node_id"`
	Timestamp  time.Time      `json:"timestamp"`
	Data       map[string]any `json:"data"`
	IsError    bool           `json:"is_error"`
	Error      string         `json:"error,omitempty"`
}

type DebuggerEvent struct {
	WorkflowID string         `json:"workflow_id"`
	NodeID     string         `json:"node_id"`
	MsgID      string         `json:"msg_id"`
	Data       map[string]any `json:"data"`
	State      string         `json:"state"` // "paused", "resumed", "aborted"
}

type PIIStats struct {
	Discoveries map[string]uint64 `json:"discoveries"`
	LastUpdated time.Time         `json:"last_updated"`
}

type Registry struct {
	engines    map[string]*activeEngine
	mu         sync.RWMutex
	storage    interfaces.RegistryStorage
	logStorage interfaces.RegistryStorage
	config     config.Config

	sourceFactory SourceFactory
	sinkFactory   SinkFactory

	evaluator *evaluator.Evaluator

	statusSubs          map[chan telemetry.StatusUpdate]bool
	workflowStatusSubs  map[string]map[chan telemetry.StatusUpdate]bool
	dashboardSubs       map[string]map[chan storage.DashboardStats]bool
	logSubs             map[chan storage.Log]bool
	workflowLogSubs     map[string]map[chan storage.Log]bool
	liveMsgSubs         map[chan LiveMessage]bool
	workflowLiveMsgSubs map[string]map[chan LiveMessage]bool
	debuggerSubs        map[string]map[chan DebuggerEvent]bool
	statusSubsMu        sync.RWMutex
	debuggerSubsMu      sync.RWMutex
	debugChans          map[string]chan string
	debugChansMu        sync.Mutex
	lastDashboardUpdate time.Time
	startTime           time.Time

	notificationService *notification.Service
	nodeStates          map[string]any
	nodeStatesMu        sync.Mutex
	lookupCache         map[string]lookupCacheEntry
	lookupCacheMu       sync.RWMutex
	dbPool              map[string]*sql.DB
	dbPoolMu            sync.RWMutex
	logger              hermod.Logger
	idleMonitorStop     chan struct{}
	stateStore          hermod.StateStore
	secretManager       secrets.Manager
	schemaRegistry      schema.Registry
	optimizer           *optimizer.Optimizer
	dqScorer            *governance.Scorer
	meshManager         *mesh.Manager

	discoveryService *service.DiscoveryService

	piiStats   map[string]*PIIStats
	piiStatsMu sync.RWMutex

	sourceCache   map[string]storage.Source
	sinkCache     map[string]storage.Sink
	sourceCacheMu sync.RWMutex

	// reconciling tracks suspended messages currently being resumed so overlapping
	// reconciliation ticks on the same worker cannot process the same message twice.
	reconciling   map[string]struct{}
	reconcilingMu sync.Mutex

	sf singleflight.Group

	// backgroundTasks bounds concurrent background work (tracing, PII discovery)
	backgroundTasks chan struct{}

	// Atomic flags for fast-path check of active observers/subscribers
	hasLiveSubs   atomic.Int32
	hasStatusSubs atomic.Int32

	ctx    context.Context
	cancel context.CancelFunc
}

type activeEngine struct {
	engine        *pkgengine.Engine
	cancel        context.CancelFunc
	done          <-chan struct{}
	srcConfigs    []factory.SourceConfig
	snkConfigs    []factory.SinkConfig
	isWorkflow    bool
	workflow      storage.Workflow
	baseProcessed uint64
	baseErrors    uint64
	baseLag       uint64
	startTime     time.Time

	// Live components
	sinks []hermod.Sink

	// Workflow maps for resuming
	nodeMap         map[string]*storage.WorkflowNode
	adj             map[string][]string
	nodeIndex       map[string]int
	edgeLabels      map[string]string
	edgeBreakpoints map[string]bool
	inDegree        map[string]int
	sinkNodeToIndex map[string]int
}

func (r *Registry) CreateSource(ctx context.Context, cfg factory.SourceConfig) (hermod.Source, error) {
	return r.createSource(ctx, cfg)
}

func (r *Registry) Logger() hermod.Logger {
	return r.logger
}

func (r *Registry) CreateSink(ctx context.Context, cfg factory.SinkConfig) (hermod.Sink, error) {
	return r.createSink(ctx, cfg)
}

func (r *Registry) SetSourceFactory(f SourceFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sourceFactory = f
}

func (r *Registry) SetSinkFactory(f SinkFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sinkFactory = f
}

func (r *Registry) GetSourceFactoryConfig(ctx context.Context, id string) (factory.SourceConfig, error) {
	src, err := r.GetSourceConfig(ctx, id)
	if err != nil {
		return factory.SourceConfig{}, err
	}
	return factory.SourceConfig{
		ID:     src.ID,
		Type:   src.Type,
		Config: src.Config,
	}, nil
}

func NewRegistry(s storage.Storage, ls ...storage.Storage) *Registry {
	ns := notification.NewService(s)
	if s != nil {
		ns.AddProvider(notification.NewUINotificationProvider(s))
		ns.AddProvider(notification.NewEmailNotificationProvider(s))
		ns.AddProvider(notification.NewTelegramNotificationProvider(s))
		ns.AddProvider(notification.NewSlackNotificationProvider(s))
		ns.AddProvider(notification.NewDiscordNotificationProvider(s))
		ns.AddProvider(notification.NewGenericWebhookProvider(s))
	}

	var logStore storage.Storage
	if len(ls) > 0 {
		logStore = ls[0]
	}
	if logStore == nil {
		logStore = s
	}

	ctx, cancel := context.WithCancel(context.Background())
	reg := &Registry{
		engines:             make(map[string]*activeEngine),
		storage:             s,
		logStorage:          logStore,
		config:              config.DefaultConfig(),
		evaluator:           evaluator.NewEvaluator(),
		statusSubs:          make(map[chan telemetry.StatusUpdate]bool),
		workflowStatusSubs:  make(map[string]map[chan telemetry.StatusUpdate]bool),
		dashboardSubs:       make(map[string]map[chan storage.DashboardStats]bool),
		logSubs:             make(map[chan storage.Log]bool),
		workflowLogSubs:     make(map[string]map[chan storage.Log]bool),
		liveMsgSubs:         make(map[chan LiveMessage]bool),
		workflowLiveMsgSubs: make(map[string]map[chan LiveMessage]bool),
		debuggerSubs:        make(map[string]map[chan DebuggerEvent]bool),
		debugChans:          make(map[string]chan string),
		notificationService: ns,
		nodeStates:          make(map[string]any),
		lookupCache:         make(map[string]lookupCacheEntry),
		dbPool:              make(map[string]*sql.DB),
		logger:              telemetry.NewDefaultLogger(),
		idleMonitorStop:     make(chan struct{}),
		startTime:           time.Now(),
		secretManager:       &secrets.EnvManager{Prefix: "HERMOD_SECRET_"},
		schemaRegistry:      schema.NewStorageRegistry(s),
		dqScorer:            governance.NewScorer(),
		meshManager:         mesh.NewManager(telemetry.NewDefaultLogger()),
		piiStats:            make(map[string]*PIIStats),
		sourceCache:         make(map[string]storage.Source),
		sinkCache:           make(map[string]storage.Sink),
		reconciling:         make(map[string]struct{}),
		sf:                  singleflight.Group{},
		backgroundTasks:     make(chan struct{}, 1000), // Max 1000 concurrent background tasks
		ctx:                 ctx,
		cancel:              cancel,
	}
	reg.discoveryService = service.NewDiscoveryService(reg)

	reg.dqScorer.SetNotifier(func(wfID, title, msg string) {
		ctx := context.Background()
		if reg.storage != nil && reg.notificationService != nil {
			wf, err := reg.storage.GetWorkflow(ctx, wfID)
			if err == nil {
				reg.notificationService.Notify(ctx, title, msg, wf)
			}
		}
	})

	reg.optimizer = optimizer.NewOptimizer(reg.logger, optimizer.NewAIOptimizer(ai.NewSelfHealingService(reg.logger), reg.logger), func(wfID, title, msg string) {
		ctx := context.Background()
		if reg.storage != nil && reg.notificationService != nil {
			wf, err := reg.storage.GetWorkflow(ctx, wfID)
			if err == nil {
				reg.notificationService.Notify(ctx, title, msg, wf)
			}
		}
	})

	// Start background maintenance routines
	go reg.runIdleMonitor()
	go reg.runRetentionPurge()
	go reg.startReconciliationLoop(ctx)
	go reg.optimizer.Start(reg.ctx)
	go reg.runStatusFlusher()
	return reg
}

func (r *Registry) Close() {
	r.cancel()

	r.dbPoolMu.Lock()
	defer r.dbPoolMu.Unlock()
	for id, db := range r.dbPool {
		r.logger.Info("Closing database connection pool", "source_id", id)
		_ = db.Close()
	}
	r.dbPool = make(map[string]*sql.DB)
}

func (r *Registry) runStatusFlusher() {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("Registry: status flusher panicked", "panic", p)
		}
	}()
	// Flush every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.flushStatsToStorage()
		}
	}
}

func (r *Registry) flushStatsToStorage() {
	if r.storage == nil {
		return
	}

	r.mu.Lock()
	engines := make([]*activeEngine, 0, len(r.engines))
	for _, ae := range r.engines {
		engines = append(engines, ae)
	}
	r.mu.Unlock()

	for _, ae := range engines {
		status := ae.engine.GetStatus()
		processed := ae.baseProcessed + status.ProcessedCount
		errors := ae.baseErrors + status.DeadLetterCount
		var lag uint64
		if l, ok := status.NodeMetrics["source_lag"]; ok {
			lag = l
		} else if l, ok := status.NodeMetrics["lag"]; ok {
			lag = l
		}

		// Update stats in DB (fast path)
		_ = r.storage.UpdateWorkflowStats(r.ctx, ae.workflow.ID, processed, errors, lag)

		// Also update status if it changed significantly (e.g. to/from error)
		// We'll let the synchronous callback handle immediate status changes for notifications,
		// but we ensure the DB is synced here too.
		_ = r.storage.UpdateWorkflowStatus(r.ctx, ae.workflow.ID, status.EngineStatus)
	}
}

func (r *Registry) runRetentionPurge() {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("Registry: retention purge panicked", "panic", p)
		}
	}()
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run once on startup
	r.purgeRetention()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.purgeRetention()
		}
	}
}

func (r *Registry) purgeRetention() {
	if r.storage == nil {
		return
	}

	ctx := context.Background()
	workflows, _, err := r.storage.ListWorkflows(ctx, storage.CommonFilter{Limit: 1000})
	if err != nil {
		return
	}

	for _, wf := range workflows {
		// Purge Traces
		if wf.TraceRetention != "" && wf.TraceRetention != "0" {
			duration, err := time.ParseDuration(wf.TraceRetention)
			if err == nil {
				before := time.Now().Add(-duration)
				if r.logStorage != nil {
					_ = r.logStorage.PurgeMessageTraces(ctx, before)
				}
			}
		}

		// Purge Audit Logs (if we decide to per-workflow, but usually it's global or per-workflow entity)
		// For now, let's use global if per-workflow is not specified, or just per-workflow if set.
		if wf.AuditRetention != "" && wf.AuditRetention != "0" {
			duration, err := time.ParseDuration(wf.AuditRetention)
			if err == nil {
				before := time.Now().Add(-duration)
				if r.logStorage != nil {
					_ = r.logStorage.PurgeAuditLogs(ctx, before)
				}
			}
		}

		// Purge Workflow Logs
		retentionDays := 30 // Default 30 days
		if wf.RetentionDays != nil {
			retentionDays = *wf.RetentionDays
		}
		if retentionDays > 0 {
			before := time.Now().AddDate(0, 0, -retentionDays)
			if r.logStorage != nil {
				_ = r.logStorage.DeleteLogs(ctx, storage.LogFilter{
					CommonFilter: storage.CommonFilter{Until: before},
					WorkflowID:   wf.ID,
				})
			}
		}
	}

	// Global purge for logs without workflow (system logs)
	if r.logStorage != nil {
		_ = r.logStorage.DeleteLogs(ctx, storage.LogFilter{
			CommonFilter:    storage.CommonFilter{Until: time.Now().AddDate(0, 0, -30)},
			WithoutWorkflow: true,
		})
	}
}

func (r *Registry) runIdleMonitor() {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("Registry: idle monitor panicked", "panic", p)
		}
	}()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.checkIdleWorkflows()
		}
	}
}

func (r *Registry) checkIdleWorkflows() {
	r.mu.Lock()
	engines := make(map[string]*activeEngine)
	for id, ae := range r.engines {
		engines[id] = ae
	}
	r.mu.Unlock()

	for id, ae := range engines {
		if !ae.isWorkflow || ae.workflow.IdleTimeout == "" || ae.workflow.Tier == storage.WorkflowTierHot {
			continue
		}

		timeout, err := parseDuration(ae.workflow.IdleTimeout)
		if err != nil || timeout <= 0 {
			continue
		}

		lastActivity := ae.engine.LastMsgTime()
		if lastActivity.IsZero() {
			// If it never had a message, check when it was started (if we tracked it)
			// For now, let's just skip it to be safe, or we could track start time in activeEngine.
			continue
		}

		if time.Since(lastActivity) > timeout {
			r.logger.Info("Workflow idle timeout exceeded, parking...",
				"workflow_id", id,
				"idle_duration", time.Since(lastActivity).String(),
				"timeout", ae.workflow.IdleTimeout,
			)

			// Park the workflow
			go func(wfID string) {
				ctx := context.Background() // Background since it's an async parking task
				// 1. Stop the engine locally
				_ = r.stopEngine(ctx, wfID, false)

				// 2. Update status to "Parked" in storage
				s := r.GetStorage()
				if s != nil {
					if workflow, err := s.GetWorkflow(ctx, wfID); err == nil {
						// Keep Active=true so it stays assigned, but Status=Parked to prevent auto-restart
						workflow.Status = "Parked"
						_ = s.UpdateWorkflow(ctx, workflow)
					}
				}
			}(id)
		}
	}
}

func (r *Registry) GetLogger() hermod.Logger {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.logger
}

func (r *Registry) GetMeshManager() *mesh.Manager {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.meshManager
}

func (r *Registry) GetStorage() storage.Storage {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.storage.(storage.Storage); ok {
		return s
	}
	return nil
}

func (r *Registry) GetLogStorage() storage.Storage {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.logStorage.(storage.Storage); ok {
		return s
	}
	return nil
}

func (r *Registry) SetLogger(logger hermod.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
}

func (r *Registry) SetStorage(s storage.Storage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.storage = s
	if r.notificationService != nil {
		r.notificationService.SetStorage(s)
	}
	if r.schemaRegistry != nil {
		if sr, ok := r.schemaRegistry.(*schema.StorageRegistry); ok {
			sr.SetStorage(s)
		}
	}
}

func (r *Registry) SetLogStorage(s storage.Storage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logStorage = s
	if dl, ok := r.logger.(*DatabaseLogger); ok {
		dl.UpdateStorage(s)
	}
}

func (r *Registry) SetSecretManager(mgr secrets.Manager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secretManager = mgr
}

func (r *Registry) SetStateStore(ss hermod.StateStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stateStore = ss
}

func getConfigString(config map[string]any, key string) string {
	if val, ok := config[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", val)
	}
	return ""
}

// openSQLDB builds a *sql.DB for the given canonical driver. For Postgres
// (the pgx stdlib driver) it parses the DSN through pgxutil so that the custom
// pgbouncer/pool_mode markers are stripped and, when a pooler is detected, the
// connection is switched to the simple/exec protocol with the prepared-statement
// and description caches disabled (mandatory for PgBouncer transaction/statement
// pooling). All other drivers fall back to the standard sql.Open behaviour.
func openSQLDB(driverName, connStr string) (*sql.DB, error) {
	if driverName == "pgx" {
		cfg, _, err := pgxutil.ParseConfig(connStr)
		if err != nil {
			return nil, fmt.Errorf("parse postgres connection config: %w", err)
		}
		return stdlib.OpenDB(*cfg), nil
	}
	return sql.Open(driverName, connStr)
}

func (r *Registry) getOrOpenDB(src storage.Source) (*sql.DB, error) {
	// 1. Fast path: check existing pool with RLock (no bottleneck for active pools)
	r.dbPoolMu.RLock()
	db, ok := r.dbPool[src.ID]
	r.dbPoolMu.RUnlock()

	if ok {
		// Ping outside the lock to avoid blocking other sources during network I/O
		if err := db.Ping(); err == nil {
			return db, nil
		}
	}

	// 2. Slow path: open or reopen the pool using singleflight to prevent
	// redundant connection attempts (thundering herd) during heavy startup.
	val, err, _ := r.sf.Do("db:"+src.ID, func() (any, error) {
		// Re-check existing pool under RLock after acquiring singleflight
		r.dbPoolMu.RLock()
		db, ok := r.dbPool[src.ID]
		r.dbPoolMu.RUnlock()

		if ok {
			if err := db.Ping(); err == nil {
				return db, nil
			}
			// Ping failed: close the stale pool and prepare to reopen
			db.Close()
		}

		sourceType := src.Type
		config := src.Config

		if sourceType == "batch_sql" {
			// Resolve the underlying database source
			underlyingID := config["source_id"]
			if underlying, err := r.GetSourceConfig(context.Background(), underlyingID); err == nil {
				sourceType = underlying.Type
				config = underlying.Config
			}
		}

		// Resolve secrets in config
		resolvedConfig := r.resolveSecrets(context.Background(), config)
		connStr := factory.BuildConnectionString(resolvedConfig, sourceType)

		// Resolve the actual database/sql driver name from the user-facing type using
		// the single source of truth shared with placeholder generation.
		driverName, ok := sqlutil.CanonicalDriver(sourceType)
		if !ok {
			return nil, fmt.Errorf("unsupported source type for generic sql: %s", sourceType)
		}

		newDB, err := openSQLDB(driverName, connStr)
		if err != nil {
			return nil, err
		}

		// Configure pool with conservative, performant defaults
		newDB.SetMaxOpenConns(20)
		newDB.SetMaxIdleConns(10)
		newDB.SetConnMaxIdleTime(60 * time.Second)

		r.dbPoolMu.Lock()
		r.dbPool[src.ID] = newDB
		r.dbPoolMu.Unlock()

		return newDB, nil
	})

	if err != nil {
		return nil, err
	}
	return val.(*sql.DB), nil
}

func (r *Registry) SetFactories(sourceFactory SourceFactory, sinkFactory SinkFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sourceFactory = sourceFactory
	r.sinkFactory = sinkFactory
}

func (r *Registry) resolveSecrets(ctx context.Context, config map[string]string) map[string]string {
	if r.secretManager == nil || config == nil {
		return config
	}
	resolved := make(map[string]string)
	for k, v := range config {
		resolved[k] = secrets.ResolveSecret(ctx, r.secretManager, v)
	}
	return resolved
}

func (r *Registry) createSource(ctx context.Context, cfg factory.SourceConfig) (hermod.Source, error) {
	// Resolve secrets in config
	cfg.Config = r.resolveSecrets(ctx, cfg.Config)

	r.mu.Lock()
	logger := r.logger
	srcFactory := r.sourceFactory
	r.mu.Unlock()

	var src hermod.Source
	var err error

	if cfg.Type == "batch_sql" {
		batchCfg := batchsql.Config{
			SourceID:          cfg.Config["source_id"],
			Cron:              cfg.Config["cron"],
			Queries:           cfg.Config["queries"],
			IncrementalColumn: cfg.Config["incremental_column"],
		}
		src = batchsql.NewBatchSQLSource(r, batchCfg)
	} else if cfg.Type == "form" {
		src = sourceform.NewFormSource(cfg.Config["path"], &formStorageAdapter{storage: r.storage})
	} else if srcFactory != nil {
		src, err = srcFactory(cfg)
	} else {
		src, err = factory.CreateSource(cfg)
	}

	if err == nil && logger != nil {
		if l, ok := src.(hermod.Loggable); ok {
			l.SetLogger(logger)
		}
	}
	return src, err
}

func (r *Registry) createSourceInternal(ctx context.Context, cfg factory.SourceConfig) (hermod.Source, error) {
	// Resolve secrets in config
	cfg.Config = r.resolveSecrets(ctx, cfg.Config)

	var src hermod.Source
	var err error

	if cfg.Type == "batch_sql" {
		batchCfg := batchsql.Config{
			SourceID:          cfg.Config["source_id"],
			Cron:              cfg.Config["cron"],
			Queries:           cfg.Config["queries"],
			IncrementalColumn: cfg.Config["incremental_column"],
		}
		src = batchsql.NewBatchSQLSource(r, batchCfg)
	} else if cfg.Type == "form" {
		src = sourceform.NewFormSource(cfg.Config["path"], &formStorageAdapter{storage: r.storage})
	} else if r.sourceFactory != nil {
		src, err = r.sourceFactory(cfg)
	} else {
		src, err = factory.CreateSource(cfg)
	}

	if err == nil {
		if r.logger != nil {
			if l, ok := src.(hermod.Loggable); ok {
				l.SetLogger(r.logger)
			}
		}
		// Set initial state if source is stateful
		if st, ok := src.(hermod.Stateful); ok && cfg.State != nil {
			st.SetState(cfg.State)
		}
		// Wrap in stateful source if it's a persistent source
		src = &statefulSource{
			Source:   src,
			registry: r,
			sourceID: cfg.ID,
		}
	}
	return src, err
}

func (r *Registry) createSink(ctx context.Context, cfg factory.SinkConfig) (hermod.Sink, error) {
	r.mu.Lock()
	logger := r.logger
	r.mu.Unlock()

	snk, err := r.createSinkInternal(ctx, cfg)
	if err == nil && logger != nil {
		if l, ok := snk.(hermod.Loggable); ok {
			l.SetLogger(logger)
		}
	}
	return snk, err
}

func (r *Registry) createSinkInternal(ctx context.Context, cfg factory.SinkConfig) (hermod.Sink, error) {
	// Resolve secrets in config
	cfg.Config = r.resolveSecrets(ctx, cfg.Config)

	if cfg.Type == "failover" {
		primaryID := cfg.Config["primary_id"]
		fallbackIDsStr := cfg.Config["fallback_ids"]
		fallbackIDs := strings.Split(fallbackIDsStr, ",")

		primarySink, err := r.resolveAndCreateSink(ctx, primaryID)
		if err != nil {
			return nil, fmt.Errorf("failed to create primary sink %s: %w", primaryID, err)
		}

		strategy := cfg.Config["strategy"]
		if strategy == "" {
			strategy = "failover"
		}

		var fallbacks []hermod.Sink
		for _, id := range fallbackIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			f, err := r.resolveAndCreateSink(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("failed to create fallback sink %s: %w", id, err)
			}
			fallbacks = append(fallbacks, f)
		}
		return failover.NewFailoverSinkWithStrategy(primarySink, fallbacks, strategy), nil
	}

	if r.sinkFactory != nil {
		return r.sinkFactory(cfg)
	}

	return factory.CreateSink(cfg)
}

func (r *Registry) resolveAndCreateSink(ctx context.Context, id string) (hermod.Sink, error) {
	if r.storage == nil {
		return nil, errors.New("registry storage is not available")
	}
	dbSnk, err := r.GetSinkConfig(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get sink %s from storage: %w", id, err)
	}
	snkCfg := factory.SinkConfig{
		ID:     dbSnk.ID,
		Type:   dbSnk.Type,
		Config: dbSnk.Config,
	}
	return r.createSinkInternal(ctx, snkCfg)
}

func (r *Registry) SetConfig(cfg config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = cfg
}

// maxLookupCacheSize bounds the number of entries kept in the in-memory lookup
// cache so it cannot grow without limit and leak memory.
const MaxLookupCacheSize = 10000

// lookupCacheEntry is a cached value with an optional expiry time. A zero
// expiry means the entry never expires.
type lookupCacheEntry struct {
	value  any
	expiry time.Time
}

func (r *Registry) GetLookupCacheSize() (int, int) {
	r.lookupCacheMu.RLock()
	defer r.lookupCacheMu.RUnlock()
	return len(r.lookupCache), MaxLookupCacheSize
}

func (r *Registry) GetLookupCache(key string) (any, bool) {
	r.lookupCacheMu.RLock()
	entry, ok := r.lookupCache[key]
	r.lookupCacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	if !entry.expiry.IsZero() && time.Now().After(entry.expiry) {
		// Lazily evict the expired entry.
		r.lookupCacheMu.Lock()
		if cur, still := r.lookupCache[key]; still && cur.expiry == entry.expiry {
			delete(r.lookupCache, key)
		}
		r.lookupCacheMu.Unlock()
		return nil, false
	}
	return entry.value, true
}

// SetLookupCache stores a value with an optional TTL. Expired entries are
// reclaimed lazily on read and during a bounded sweep on write, so no
// per-key goroutine is spawned (which previously leaked goroutines and memory
// under high lookup throughput).
func (r *Registry) SetLookupCache(key string, value any, ttl time.Duration) {
	var expiry time.Time
	if ttl > 0 {
		expiry = time.Now().Add(ttl)
	}

	r.lookupCacheMu.Lock()
	defer r.lookupCacheMu.Unlock()

	if _, exists := r.lookupCache[key]; !exists && len(r.lookupCache) >= MaxLookupCacheSize {
		r.evictLookupCacheLocked()
	}
	r.lookupCache[key] = lookupCacheEntry{value: value, expiry: expiry}
}

// evictLookupCacheLocked frees space in the lookup cache. It first drops all
// expired entries; if that is not enough it removes the entry with the nearest
// expiry (and, failing that, an arbitrary entry) to enforce the size bound.
// Callers must hold lookupCacheMu.
func (r *Registry) evictLookupCacheLocked() {
	now := time.Now()
	for k, e := range r.lookupCache {
		if !e.expiry.IsZero() && now.After(e.expiry) {
			delete(r.lookupCache, k)
		}
	}
	if len(r.lookupCache) < MaxLookupCacheSize {
		return
	}
	var victim string
	var victimExpiry time.Time
	for k, e := range r.lookupCache {
		if victim == "" || (!e.expiry.IsZero() && (victimExpiry.IsZero() || e.expiry.Before(victimExpiry))) {
			victim = k
			victimExpiry = e.expiry
		}
	}
	if victim != "" {
		delete(r.lookupCache, victim)
	}
}

func (r *Registry) GetOrOpenDB(src storage.Source) (*sql.DB, error) {
	return r.getOrOpenDB(src)
}

func (r *Registry) GetOrOpenDBByID(ctx context.Context, id string) (*sql.DB, string, error) {
	if r.storage == nil {
		return nil, "", errors.New("registry storage is not initialized")
	}
	src, err := r.GetSourceConfig(ctx, id)
	if err != nil {
		return nil, "", err
	}
	db, err := r.getOrOpenDB(src)
	return db, src.Type, err
}

func (r *Registry) GetSourceConfig(ctx context.Context, id string) (storage.Source, error) {
	if r.storage == nil {
		return storage.Source{}, errors.New("registry storage is not initialized")
	}

	r.sourceCacheMu.RLock()
	if src, ok := r.sourceCache[id]; ok {
		r.sourceCacheMu.RUnlock()
		return src, nil
	}
	r.sourceCacheMu.RUnlock()

	// Use singleflight to avoid redundant DB hits
	v, err, _ := r.sf.Do("source:"+id, func() (any, error) {
		src, err := r.storage.GetSource(ctx, id)
		if err != nil {
			return storage.Source{}, err
		}
		r.sourceCacheMu.Lock()
		r.sourceCache[id] = src
		r.sourceCacheMu.Unlock()
		return src, nil
	})

	if err != nil {
		return storage.Source{}, err
	}
	return v.(storage.Source), nil
}

func (r *Registry) GetSinkConfig(ctx context.Context, id string) (storage.Sink, error) {
	if r.storage == nil {
		return storage.Sink{}, errors.New("registry storage is not initialized")
	}

	r.sourceCacheMu.RLock()
	if snk, ok := r.sinkCache[id]; ok {
		r.sourceCacheMu.RUnlock()
		return snk, nil
	}
	r.sourceCacheMu.RUnlock()

	v, err, _ := r.sf.Do("sink:"+id, func() (any, error) {
		snk, err := r.storage.GetSink(ctx, id)
		if err != nil {
			return storage.Sink{}, err
		}
		r.sourceCacheMu.Lock()
		r.sinkCache[id] = snk
		r.sourceCacheMu.Unlock()
		return snk, nil
	})

	if err != nil {
		return storage.Sink{}, err
	}
	return v.(storage.Sink), nil
}

func (r *Registry) UpdateSource(ctx context.Context, src storage.Source) error {
	if err := r.storage.UpdateSource(ctx, src); err != nil {
		return err
	}
	r.sourceCacheMu.Lock()
	delete(r.sourceCache, src.ID)
	r.sourceCacheMu.Unlock()
	return nil
}

func (r *Registry) UpdateSink(ctx context.Context, snk storage.Sink) error {
	if err := r.storage.UpdateSink(ctx, snk); err != nil {
		return err
	}
	r.sourceCacheMu.Lock()
	delete(r.sinkCache, snk.ID)
	r.sourceCacheMu.Unlock()
	return nil
}

func (r *Registry) UpdateSourceStatus(ctx context.Context, id string, status string) error {
	if err := r.storage.UpdateSourceStatus(ctx, id, status); err != nil {
		return err
	}
	r.sourceCacheMu.Lock()
	delete(r.sourceCache, id)
	r.sourceCacheMu.Unlock()
	return nil
}

func (r *Registry) UpdateSinkStatus(ctx context.Context, id string, status string) error {
	if err := r.storage.UpdateSinkStatus(ctx, id, status); err != nil {
		return err
	}
	r.sourceCacheMu.Lock()
	delete(r.sinkCache, id)
	r.sourceCacheMu.Unlock()
	return nil
}

func (r *Registry) mergeData(dst, src map[string]any, strategy string) {
	if strategy == "" {
		strategy = "deep"
	}
	switch strategy {
	case "overwrite":
		for k, v := range src {
			dst[k] = v
		}
	case "if_missing":
		for k, v := range src {
			if _, ok := dst[k]; !ok {
				dst[k] = v
			}
		}
	case "shallow":
		for k, v := range src {
			dst[k] = v
		}
	case "deep":
		fallthrough
	default:
		for k, v := range src {
			if srcMap, ok := v.(map[string]any); ok {
				if dstMap, ok := dst[k].(map[string]any); ok {
					r.mergeData(dstMap, srcMap, "deep")
					continue
				}
			}
			dst[k] = v
		}
	}
}

func (r *Registry) TestTransformationPipeline(ctx context.Context, transformations []storage.Transformation, msg hermod.Message) ([]hermod.Message, error) {
	results := make([]hermod.Message, len(transformations))
	// currentInput tracks the message state to be passed to the next transformation.
	// We start with a clone to avoid any side effects on the original input message.
	currentInput := msg.Clone()

	// Optimization: Pass a shared snapshot pointer in context to avoid redundant ToMap() calls
	// during the pipeline traversal. doApplyTransformation will update this pointer.
	var lastSnapshot map[string]any
	pipelineCtx := context.WithValue(ctx, hermod.LastTraceSnapshotKey, &lastSnapshot)

	for i, t := range transformations {
		if currentInput == nil {
			results[i] = nil
			continue
		}

		res, err := r.applyTransformation(pipelineCtx, currentInput, t.Type, t.Config)
		if err != nil {
			// On error, we release all successfully created results so far,
			// and also the currentInput which is not in results yet.
			for _, m := range results {
				if m != nil {
					m.Release()
				}
			}
			if currentInput != nil {
				currentInput.Release()
			}
			return nil, err
		}

		results[i] = res
		if res != nil {
			// For the next step, we need a fresh clone because the next transformation
			// might modify it, and we want to preserve results[i] as a snapshot.
			currentInput = res.Clone()
		} else {
			currentInput = nil
		}
	}

	// currentInput was a clone of the last result (or nil), so we must release it
	// as it is not part of the returned results slice.
	if currentInput != nil {
		currentInput.Release()
	}

	return results, nil
}

type WorkflowStepResult struct {
	NodeID   string            `json:"node_id"`
	NodeType string            `json:"node_type"`
	Payload  map[string]any    `json:"payload,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Error    string            `json:"error,omitempty"`
	Filtered bool              `json:"filtered,omitempty"`
	Branch   string            `json:"branch,omitempty"`
}

func (r *Registry) applyTransformation(ctx context.Context, modifiedMsg hermod.Message, transType string, config map[string]any) (hermod.Message, error) {
	res, err := r.doApplyTransformation(ctx, modifiedMsg, transType, config)
	if err != nil {
		onError := getConfigString(config, "onError")
		statusField := getConfigString(config, "statusField")

		if statusField != "" {
			modifiedMsg.SetData(statusField, "error")
			modifiedMsg.SetData(statusField+"_error", err.Error())
		}

		switch onError {
		case "continue":
			return modifiedMsg, nil
		case "drop":
			return nil, nil
		default: // "fail"
			return res, err
		}
	} else {
		statusField := getConfigString(config, "statusField")
		if statusField != "" {
			modifiedMsg.SetData(statusField, "success")
		}
	}
	return res, nil
}

func (r *Registry) doApplyTransformation(ctx context.Context, modifiedMsg hermod.Message, transType string, config map[string]any) (hermod.Message, error) {
	if modifiedMsg == nil {
		return nil, nil
	}

	// Try to use the new Transformer Registry
	if t, ok := transformer.Get(transType); ok {
		workflowID := modifiedMsg.Metadata()["_hermod_workflow_id"]
		tracingEnabled := workflowID != "" && r.shouldTrace(workflowID, modifiedMsg)

		var beforeData map[string]any
		if tracingEnabled {
			// Optimization: Reuse the snapshot from the previous transformation in the same pipeline
			if last, ok := ctx.Value(hermod.LastTraceSnapshotKey).(*map[string]any); ok && *last != nil {
				beforeData = *last
			} else {
				beforeData = modifiedMsg.ToMap()
			}
		}

		start := time.Now()

		// Pass Registry to transformer if it needs it (like for storage or lookup)
		tctx := context.WithValue(ctx, hermod.RegistryKey, r)
		if r.stateStore != nil {
			tctx = context.WithValue(tctx, hermod.StateStoreKey, r.stateStore)
		}
		res, err := t.Transform(tctx, modifiedMsg, config)

		// Record trace step with before/after snapshots
		if tracingEnabled {
			var afterData map[string]any
			if res != nil {
				afterData = res.ToMap()
			}
			mID := modifiedMsg.ID()

			// Optimization: Update the snapshot in context for the next step in the pipeline
			if last, ok := ctx.Value(hermod.LastTraceSnapshotKey).(*map[string]any); ok {
				*last = afterData
			}

			// Record asynchronously to avoid blocking the pipeline.
			// Bounding trace goroutines ensures system stability under heavy load.
			select {
			case r.backgroundTasks <- struct{}{}:
				go func(wID, id string, bData, aData map[string]any, tStart time.Time, tErr error) {
					defer func() { <-r.backgroundTasks }()
					step := hermod.TraceStep{
						NodeID:    transType,
						Timestamp: tStart,
						Duration:  time.Since(tStart),
						Before:    bData,
						After:     aData,
					}
					if tErr != nil {
						step.Error = tErr.Error()
					}
					// Use a background context to ensure recording finishes even if the
					// main workflow node finishes first.
					r.RecordStep(context.Background(), wID, id, step)
				}(workflowID, mID, beforeData, afterData, start, err)
			default:
				// Dropping trace step due to background task pressure
			}
		}

		// Record PII discoveries for compliance dashboard
		if transType == "mask" && res != nil {
			res.Retain()
			select {
			case r.backgroundTasks <- struct{}{}:
				go func() {
					defer func() { <-r.backgroundTasks }()
					r.recordPIIDiscoveries(res, config)
				}()
			default:
				res.Release()
			}
		}

		return res, err
	}

	return modifiedMsg, nil
}

func (r *Registry) shouldTrace(workflowID string, msg hermod.Message) bool {
	if msg == nil {
		return false
	}
	r.mu.RLock()
	ae, ok := r.engines[workflowID]
	r.mu.RUnlock()
	if !ok || ae.workflow.TraceSampleRate <= 0 {
		return false
	}
	if ae.workflow.TraceSampleRate >= 1.0 {
		return true
	}
	// Use deterministic sampling based on Message ID (same as Engine.RecordTraceStep)
	h := fnv.New32a()
	_, _ = h.Write([]byte(msg.ID()))
	sampleValue := float64(h.Sum32()) / float64(0xFFFFFFFF)
	return sampleValue <= ae.workflow.TraceSampleRate
}

func (r *Registry) recordPIIDiscoveries(msg hermod.Message, config map[string]any) {
	if msg == nil {
		return
	}
	defer msg.Release()

	data := msg.DataRef()
	if data == nil {
		return
	}

	foundTypes := make(map[string]int)
	field, _ := config["field"].(string)

	if field == "*" || field == "" {
		r.scanForPII(data, foundTypes)
	} else {
		val := evaluator.EvaluateField(msg, field)
		if s, ok := val.(string); ok {
			types := transformer.PIIEngine().Discover(s)
			for _, t := range types {
				foundTypes[t]++
			}
		}
	}

	if len(foundTypes) > 0 {
		workflowID := msg.MetadataRef()["_hermod_workflow_id"]
		if workflowID == "" {
			return
		}

		r.piiStatsMu.Lock()
		stats, ok := r.piiStats[workflowID]
		if !ok {
			stats = &PIIStats{Discoveries: make(map[string]uint64)}
			r.piiStats[workflowID] = stats
		}
		for t, count := range foundTypes {
			stats.Discoveries[t] += uint64(count)
		}
		stats.LastUpdated = time.Now()
		r.piiStatsMu.Unlock()
	}
}

func (r *Registry) scanForPII(data map[string]any, found map[string]int) {
	for _, v := range data {
		switch val := v.(type) {
		case string:
			types := transformer.PIIEngine().Discover(val)
			for _, t := range types {
				found[t]++
			}
		case map[string]any:
			r.scanForPII(val, found)
		case []any:
			for _, item := range val {
				if m, ok := item.(map[string]any); ok {
					r.scanForPII(m, found)
				} else if s, ok := item.(string); ok {
					types := transformer.PIIEngine().Discover(s)
					for _, t := range types {
						found[t]++
					}
				}
			}
		}
	}
}

func (r *Registry) GetEngine(id string) (*pkgengine.Engine, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ae, ok := r.engines[id]
	if !ok {
		return nil, false
	}
	return ae.engine, true
}

func (r *Registry) IsEngineRunning(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.engines[id]
	return ok
}

// GetWorkflowHealth returns a real-time health summary for a running workflow.
func (r *Registry) GetWorkflowHealth(id string) (storage.WorkflowHealth, bool) {
	r.mu.RLock()
	ae, ok := r.engines[id]
	r.mu.RUnlock()

	if !ok {
		return storage.WorkflowHealth{}, false
	}

	status := ae.engine.GetStatus()

	health := storage.WorkflowHealth{
		WorkflowID: id,
		Status:     "healthy",
		Uptime:     time.Since(ae.startTime),
		Processed:  status.ProcessedCount,
		Errors:     status.DeadLetterCount,
		MPS:        status.Throughput,
		Lag:        status.Lag,
		Latency:    status.AvgLatency,
	}

	if status.EngineStatus == "error" || status.SourceStatus == "error" {
		health.Status = "error"
		health.Issues = append(health.Issues, fmt.Sprintf("Engine or source reported error state (source: %s, engine: %s)", status.SourceStatus, status.EngineStatus))
	}

	if status.ProcessedCount > 0 && status.DeadLetterCount > 0 {
		errorRate := float64(status.DeadLetterCount) / float64(status.ProcessedCount+status.DeadLetterCount)
		if errorRate > 0.1 {
			if health.Status != "error" {
				health.Status = "degraded"
			}
			health.Issues = append(health.Issues, fmt.Sprintf("High DLQ rate detected: %.1f%%", errorRate*100))
		}
	}

	return health, true
}

func (r *Registry) GetAllStatuses() []telemetry.StatusUpdate {
	r.mu.RLock()
	defer r.mu.RUnlock()

	statuses := make([]telemetry.StatusUpdate, 0, len(r.engines))
	for _, ae := range r.engines {
		statuses = append(statuses, ae.engine.GetStatus())
	}
	return statuses
}

func (r *Registry) GetWorkflowStatus(id string) (telemetry.StatusUpdate, bool) {
	r.mu.RLock()
	ae, ok := r.engines[id]
	r.mu.RUnlock()
	if !ok {
		return telemetry.StatusUpdate{}, false
	}
	return ae.engine.GetStatus(), true
}

func (r *Registry) GetDashboardStats(ctx context.Context, vhost string) (storage.DashboardStats, error) {
	if r.storage == nil {
		return storage.DashboardStats{
			Uptime: int64(time.Since(r.startTime).Seconds()),
		}, nil
	}

	stats, err := r.storage.GetDashboardStats(ctx, vhost)
	if err != nil {
		return stats, err
	}

	// Enrich with local uptime and real-time throughput
	stats.Uptime = int64(time.Since(r.startTime).Seconds())

	r.mu.RLock()
	for _, ae := range r.engines {
		if vhost != "" && vhost != "all" && ae.workflow.VHost != vhost {
			continue
		}
		status := ae.engine.GetStatus()
		stats.Throughput += status.Throughput

		// For TotalLag, if it's 0 in DB (not supported yet by all workers),
		// we fall back to local engines to at least show something.
		if stats.TotalLag == 0 {
			if lag, ok := status.NodeMetrics["source_lag"]; ok {
				stats.TotalLag += lag
			} else if lag, ok := status.NodeMetrics["lag"]; ok {
				stats.TotalLag += lag
			}
		}
	}
	r.mu.RUnlock()

	return stats, nil
}

func (r *Registry) GetWorkflowConfig(id string) (storage.Workflow, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ae, ok := r.engines[id]
	if !ok || !ae.isWorkflow {
		return storage.Workflow{}, false
	}
	return ae.workflow, true
}

func (r *Registry) GetSourceConfigs(id string) ([]factory.SourceConfig, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ae, ok := r.engines[id]
	if !ok {
		return nil, false
	}
	return ae.srcConfigs, true
}

func (r *Registry) GetSinkConfigs(id string) ([]factory.SinkConfig, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ae, ok := r.engines[id]
	if !ok {
		return nil, false
	}
	return ae.snkConfigs, true
}

func (r *Registry) getNodeName(node storage.WorkflowNode) string {
	if label, ok := node.Config["label"].(string); ok && label != "" {
		return fmt.Sprintf("%s (%s)", label, node.ID)
	}
	return node.ID
}

func (r *Registry) ValidateWorkflow(ctx context.Context, wf storage.Workflow) error {
	// 1. Check if all nodes are configured and exist
	for _, node := range wf.Nodes {
		switch node.Type {
		case "source":
			if node.RefID == "" || node.RefID == "new" {
				return fmt.Errorf("source node %s is not configured", r.getNodeName(node))
			}
			if r.storage != nil {
				if _, err := r.GetSourceConfig(ctx, node.RefID); err != nil {
					return fmt.Errorf("source node %s refers to missing source %s: %w", r.getNodeName(node), node.RefID, err)
				}
			}
		case "sink":
			if node.RefID == "" || node.RefID == "new" {
				return fmt.Errorf("sink node %s is not configured", r.getNodeName(node))
			}
			if r.storage != nil {
				if _, err := r.GetSinkConfig(ctx, node.RefID); err != nil {
					return fmt.Errorf("sink node %s refers to missing sink %s: %w", r.getNodeName(node), node.RefID, err)
				}
			}
		}
	}

	// 2. At least one source
	nodeMap := make(map[string]*storage.WorkflowNode)
	for i := range wf.Nodes {
		nodeMap[wf.Nodes[i].ID] = &wf.Nodes[i]
	}

	var sourceNodes []*storage.WorkflowNode
	for i, node := range wf.Nodes {
		if node.Type == "source" {
			sourceNodes = append(sourceNodes, &wf.Nodes[i])
		}
	}
	if len(sourceNodes) == 0 {
		return errors.New("workflow must have at least one source node")
	}

	// 3. Reachability and cycle detection
	adj := make(map[string][]string)
	for _, edge := range wf.Edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
	}

	visited := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited
	var hasSink bool

	var check func(string) error
	check = func(id string) error {
		visited[id] = 1
		for _, nextID := range adj[id] {
			if visited[nextID] == 1 {
				node := nodeMap[nextID]
				if node != nil {
					return fmt.Errorf("cycle detected at node %s", r.getNodeName(*node))
				}
				return fmt.Errorf("cycle detected at node %s", nextID)
			}
			if visited[nextID] == 0 {
				if err := check(nextID); err != nil {
					return err
				}
			}
		}
		visited[id] = 2

		node := nodeMap[id]
		if node != nil && node.Type == "sink" {
			hasSink = true
		}
		return nil
	}

	for _, sn := range sourceNodes {
		if err := check(sn.ID); err != nil {
			return err
		}
	}

	if !hasSink {
		return errors.New("no sink node reachable from any source")
	}

	// 4. Check for disconnected nodes (optional, but good for production)
	for _, node := range wf.Nodes {
		if visited[node.ID] == 0 {
			// return fmt.Errorf("node %s is unreachable from source", node.ID)
			// Warning instead? Or error? Let's just log it for now.
		}
	}

	// 5. Edge integrity
	for _, edge := range wf.Edges {
		if nodeMap[edge.SourceID] == nil {
			return fmt.Errorf("edge %s refers to missing source node %s", edge.ID, edge.SourceID)
		}
		if nodeMap[edge.TargetID] == nil {
			return fmt.Errorf("edge %s refers to missing target node %s", edge.ID, edge.TargetID)
		}
	}

	// 6. DLQ Prioritization requirements
	if wf.PrioritizeDLQ {
		if wf.DeadLetterSinkID == "" {
			return errors.New("PrioritizeDLQ is enabled but no Dead Letter Sink is configured")
		}
		if r.storage != nil {
			dlqSink, err := r.GetSinkConfig(ctx, wf.DeadLetterSinkID)
			if err != nil {
				return fmt.Errorf("dead letter sink %s not found: %w", wf.DeadLetterSinkID, err)
			}
			// Verify that the DLQ sink type is also a valid source type.
			// CreateSource will return an error if the type is not supported as a source.
			// We use a dummy config for validation.
			testSrc, err := r.createSourceInternal(context.Background(), factory.SourceConfig{
				Type:   dlqSink.Type,
				Config: dlqSink.Config,
			})
			if err != nil {
				return fmt.Errorf("dead letter sink %s (type %s) cannot be used as a source for PrioritizeDLQ: %w", wf.DeadLetterSinkID, dlqSink.Type, err)
			}
			if testSrc != nil {
				testSrc.Close()
			}
		}

		// Check for idempotency on sinks
		if r.storage != nil {
			for _, node := range wf.Nodes {
				if node.Type == "sink" {
					snk, err := r.GetSinkConfig(ctx, node.RefID)
					if err == nil {
						if snk.Config["enable_idempotency"] != "true" {
							r.logger.Warn("Workflow has PrioritizeDLQ enabled but sink does not have idempotency enabled; enable idempotency to avoid side effects during re-processing", "workflow_id", wf.ID, "sink_id", snk.ID)
						}
					}
				}
			}
		}
	}

	return nil
}

func (r *Registry) GetPIIStats() map[string]*PIIStats {
	r.piiStatsMu.RLock()
	defer r.piiStatsMu.RUnlock()

	// Return a copy
	res := make(map[string]*PIIStats)
	for k, v := range r.piiStats {
		res[k] = v
	}
	return res
}

func (r *Registry) GetDQScorer() *governance.Scorer {
	return r.dqScorer
}

func (r *Registry) TriggerSnapshot(ctx context.Context, sourceID string, tables ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	triggered := false
	var lastErr error
	foundNotSnap := false

	for _, ae := range r.engines {
		source := ae.engine.GetSource()
		if source == nil {
			continue
		}

		// Check if it's a multiSource (workflows)
		if ms, ok := source.(*multiSource); ok {
			for _, ss := range ms.sources {
				if ss.sourceID == sourceID {
					if snappable, ok := ss.source.(hermod.Snapshottable); ok {
						if err := snappable.Snapshot(ctx, tables...); err != nil {
							lastErr = err
						} else {
							triggered = true
						}
					} else {
						foundNotSnap = true
					}
				}
			}
		} else {
			// Check if it's a direct source (if we support that)
			// We need to know the source ID for this engine if it's not a workflow
			// activeEngine has srcConfigs
			for _, cfg := range ae.srcConfigs {
				if cfg.ID == sourceID {
					if snappable, ok := source.(hermod.Snapshottable); ok {
						if err := snappable.Snapshot(ctx, tables...); err != nil {
							lastErr = err
						} else {
							triggered = true
						}
					} else {
						foundNotSnap = true
					}
				}
			}
		}
	}

	if lastErr != nil {
		return lastErr
	}
	if triggered {
		return nil
	}
	if foundNotSnap {
		return fmt.Errorf("source %s does not support manual snapshots", sourceID)
	}
	// Provide a clearer message: snapshots require the source to be part of a running workflow engine.
	// This often happens when trying to trigger a snapshot before starting the workflow.
	return fmt.Errorf("source %s is not currently running in any engine. Start the workflow that uses this source and try again", sourceID)
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	if strings.HasSuffix(s, "d") {
		val := strings.TrimSuffix(s, "d")
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %s: %w", s, err)
		}
		return time.Duration(f * float64(24*time.Hour)), nil
	}

	return time.ParseDuration(s)
}

func (r *Registry) IsResourceInUse(ctx context.Context, resourceID string, excludeID string, isSource bool) bool {
	// 1. Check local running engines (Immediate Source of Truth for this node)
	r.mu.Lock()
	for id, ae := range r.engines {
		if id == excludeID {
			continue
		}
		if isSource {
			for _, cfg := range ae.srcConfigs {
				if cfg.ID == resourceID {
					r.mu.Unlock()
					return true
				}
			}
		} else {
			for _, cfg := range ae.snkConfigs {
				if cfg.ID == resourceID {
					r.mu.Unlock()
					return true
				}
			}
		}
	}
	r.mu.Unlock()

	// 2. Fallback to Storage (to see if other workers are using it)
	if r.storage == nil {
		return false
	}
	active := true
	wfs, _, err := r.storage.ListWorkflows(ctx, storage.CommonFilter{Active: &active})
	if err != nil {
		// FAIL-SAFE: If we can't reach storage, assume it is in use.
		// This prevents the health checker from disrupting potentially active workflows.
		return true
	}

	for _, wf := range wfs {
		if wf.ID != excludeID {
			for _, node := range wf.Nodes {
				if isSource && node.Type == "source" && node.RefID == resourceID {
					return true
				} else if !isSource && node.Type == "sink" && node.RefID == resourceID {
					return true
				}
			}
		}
	}

	return false
}

type formStorageAdapter struct {
	storage interfaces.RegistryStorage
}

func (a *formStorageAdapter) CreateFormSubmission(ctx context.Context, sub sourceform.FormSubmission) error {
	if a.storage == nil {
		return nil
	}
	return a.storage.CreateFormSubmission(ctx, storage.FormSubmission{
		ID:        sub.ID,
		Timestamp: sub.Timestamp,
		Path:      sub.Path,
		Data:      sub.Data,
		Status:    sub.Status,
	})
}

func (a *formStorageAdapter) ListFormSubmissions(ctx context.Context, filter sourceform.FormSubmissionFilter) ([]sourceform.FormSubmission, int, error) {
	if a.storage == nil {
		return nil, 0, nil
	}
	subs, total, err := a.storage.ListFormSubmissions(ctx, storage.FormSubmissionFilter{
		CommonFilter: storage.CommonFilter{
			Page:  filter.Page,
			Limit: filter.Limit,
		},
		Path:   filter.Path,
		Status: filter.Status,
	})
	if err != nil {
		return nil, 0, err
	}
	res := make([]sourceform.FormSubmission, len(subs))
	for i, s := range subs {
		res[i] = sourceform.FormSubmission{
			ID:        s.ID,
			Timestamp: s.Timestamp,
			Path:      s.Path,
			Data:      s.Data,
			Status:    s.Status,
		}
	}
	return res, total, nil
}

func (a *formStorageAdapter) UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error {
	if a.storage == nil {
		return nil
	}
	return a.storage.UpdateFormSubmissionStatus(ctx, id, status)
}
