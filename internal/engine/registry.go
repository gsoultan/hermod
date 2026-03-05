package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/sijms/go-ora/v2"
	_ "github.com/snowflakedb/gosnowflake"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/governance"
	"github.com/user/hermod/internal/mesh"
	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/optimizer"
	"github.com/user/hermod/internal/storage"
	pkgengine "github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/schema"
	"github.com/user/hermod/pkg/secrets"
	"github.com/user/hermod/pkg/sink/failover"
	"github.com/user/hermod/pkg/source/batchsql"
	sourceform "github.com/user/hermod/pkg/source/form"
	"github.com/user/hermod/pkg/state"
	"github.com/user/hermod/pkg/transformer"
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

type RegistryStorage interface {
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetSink(ctx context.Context, id string) (storage.Sink, error)
	GetWorkflow(ctx context.Context, id string) (storage.Workflow, error)
	ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error)
	ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error)
	ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error)
	ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error)

	CreateFormSubmission(ctx context.Context, sub storage.FormSubmission) error
	ListFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) ([]storage.FormSubmission, int, error)
	UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error
	UpdateWorkflow(ctx context.Context, wf storage.Workflow) error
	UpdateSourceStatus(ctx context.Context, id string, status string) error
	UpdateSinkStatus(ctx context.Context, id string, status string) error
	CreateLog(ctx context.Context, log storage.Log) error
	UpdateSource(ctx context.Context, src storage.Source) error
	UpdateSourceState(ctx context.Context, id string, state map[string]string) error
	UpdateSink(ctx context.Context, snk storage.Sink) error
	UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error
	GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error)
	RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error
	PurgeAuditLogs(ctx context.Context, before time.Time) error
	PurgeMessageTraces(ctx context.Context, before time.Time) error

	CreateApproval(ctx context.Context, app storage.Approval) error
}

type SourceFactory func(SourceConfig) (hermod.Source, error)
type SinkFactory func(SinkConfig) (hermod.Sink, error)

type LiveMessage struct {
	WorkflowID string         `json:"workflow_id"`
	NodeID     string         `json:"node_id"`
	Timestamp  time.Time      `json:"timestamp"`
	Data       map[string]any `json:"data"`
	IsError    bool           `json:"is_error"`
	Error      string         `json:"error,omitempty"`
}

type PIIStats struct {
	Discoveries map[string]uint64 `json:"discoveries"`
	LastUpdated time.Time         `json:"last_updated"`
}

type Registry struct {
	engines    map[string]*activeEngine
	mu         sync.Mutex
	storage    RegistryStorage
	logStorage RegistryStorage
	config     pkgengine.Config

	sourceFactory SourceFactory
	sinkFactory   SinkFactory

	evaluator *evaluator.Evaluator

	statusSubs          map[chan pkgengine.StatusUpdate]bool
	dashboardSubs       map[chan DashboardStats]bool
	logSubs             map[chan storage.Log]bool
	liveMsgSubs         map[chan LiveMessage]bool
	statusSubsMu        sync.RWMutex
	lastDashboardUpdate time.Time
	startTime           time.Time

	notificationService *notification.Service
	nodeStates          map[string]any
	nodeStatesMu        sync.Mutex
	lookupCache         map[string]any
	lookupCacheMu       sync.RWMutex
	dbPool              map[string]*sql.DB
	dbPoolMu            sync.Mutex
	logger              hermod.Logger
	idleMonitorStop     chan struct{}
	stateStore          hermod.StateStore
	secretManager       secrets.Manager
	schemaRegistry      schema.Registry
	optimizer           *optimizer.Optimizer
	dqScorer            *governance.Scorer
	meshManager         *mesh.Manager

	piiStats   map[string]*PIIStats
	piiStatsMu sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
}

type activeEngine struct {
	engine               *pkgengine.Engine
	cancel               context.CancelFunc
	done                 <-chan struct{}
	srcConfigs           []SourceConfig
	snkConfigs           []SinkConfig
	transformationGroups []string
	transformationIDs    []string
	transformations      []storage.Transformation
	isWorkflow           bool
	workflow             storage.Workflow
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
		config:              pkgengine.DefaultConfig(),
		evaluator:           evaluator.NewEvaluator(),
		statusSubs:          make(map[chan pkgengine.StatusUpdate]bool),
		dashboardSubs:       make(map[chan DashboardStats]bool),
		logSubs:             make(map[chan storage.Log]bool),
		liveMsgSubs:         make(map[chan LiveMessage]bool),
		notificationService: ns,
		nodeStates:          make(map[string]any),
		lookupCache:         make(map[string]any),
		dbPool:              make(map[string]*sql.DB),
		logger:              pkgengine.NewDefaultLogger(),
		idleMonitorStop:     make(chan struct{}),
		startTime:           time.Now(),
		secretManager:       &secrets.EnvManager{Prefix: "HERMOD_SECRET_"},
		schemaRegistry:      schema.NewStorageRegistry(s),
		dqScorer:            governance.NewScorer(),
		meshManager:         mesh.NewManager(pkgengine.NewDefaultLogger()),
		piiStats:            make(map[string]*PIIStats),
		ctx:                 ctx,
		cancel:              cancel,
	}

	reg.optimizer = optimizer.NewOptimizer(reg.logger, func(wfID, title, msg string) {
		ctx := context.Background()
		if reg.storage != nil && reg.notificationService != nil {
			wf, err := reg.storage.GetWorkflow(ctx, wfID)
			if err == nil {
				reg.notificationService.Notify(ctx, title, msg, wf)
			}
		}
	})

	// Initialize default state store
	ss, err := state.NewSQLiteStateStore("hermod_state.db")
	if err == nil {
		reg.stateStore = ss
	} else {
		reg.logger.Warn("Failed to initialize state store", "error", err)
	}

	// Start background maintenance routines
	go reg.runIdleMonitor()
	go reg.runRetentionPurge()
	go reg.optimizer.Start(reg.ctx)
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

func (r *Registry) runRetentionPurge() {
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
	}
}

func (r *Registry) runIdleMonitor() {
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
				// 1. Stop the engine locally
				_ = r.stopEngine(wfID, false)

				// 2. Update status to "Parked" in storage
				if r.storage != nil {
					ctx := context.Background()
					if workflow, err := r.storage.GetWorkflow(ctx, wfID); err == nil {
						// Keep Active=true so it stays assigned, but Status=Parked to prevent auto-restart
						workflow.Status = "Parked"
						_ = r.storage.UpdateWorkflow(ctx, workflow)
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

func (r *Registry) SetLogger(logger hermod.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
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

func (r *Registry) getOrOpenDB(src storage.Source) (*sql.DB, error) {
	r.dbPoolMu.Lock()
	defer r.dbPoolMu.Unlock()

	if db, ok := r.dbPool[src.ID]; ok {
		if err := db.Ping(); err == nil {
			return db, nil
		}
		db.Close()
		delete(r.dbPool, src.ID)
	}

	connStr := BuildConnectionString(src.Config, src.Type)
	var db *sql.DB
	var err error

	switch src.Type {
	case "postgres", "yugabyte":
		db, err = sql.Open("pgx", connStr)
	case "mysql", "mariadb":
		db, err = sql.Open("mysql", connStr)
	case "sqlite":
		db, err = sql.Open("sqlite", connStr)
	case "mssql":
		db, err = sql.Open("sqlserver", connStr)
	case "oracle":
		db, err = sql.Open("oracle", connStr)
	case "clickhouse":
		db, err = sql.Open("clickhouse", connStr)
	case "snowflake":
		db, err = sql.Open("snowflake", connStr)
	default:
		return nil, fmt.Errorf("unsupported source type for db_lookup: %s", src.Type)
	}

	if err != nil {
		return nil, err
	}

	// Configure pool with conservative, performant defaults
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(60 * time.Second)

	r.dbPool[src.ID] = db
	return db, nil
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

func (r *Registry) createSource(cfg SourceConfig) (hermod.Source, error) {
	// Resolve secrets in config
	cfg.Config = r.resolveSecrets(context.Background(), cfg.Config)

	r.mu.Lock()
	logger := r.logger
	factory := r.sourceFactory
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
	} else if factory != nil {
		src, err = factory(cfg)
	} else {
		src, err = CreateSource(cfg)
	}

	if err == nil && logger != nil {
		if l, ok := src.(hermod.Loggable); ok {
			l.SetLogger(logger)
		}
	}
	return src, err
}

func (r *Registry) createSourceInternal(cfg SourceConfig) (hermod.Source, error) {
	// Resolve secrets in config
	cfg.Config = r.resolveSecrets(context.Background(), cfg.Config)

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
		src, err = CreateSource(cfg)
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

func (r *Registry) createSink(cfg SinkConfig) (hermod.Sink, error) {
	r.mu.Lock()
	logger := r.logger
	r.mu.Unlock()

	snk, err := r.createSinkInternal(cfg)
	if err == nil && logger != nil {
		if l, ok := snk.(hermod.Loggable); ok {
			l.SetLogger(logger)
		}
	}
	return snk, err
}

func (r *Registry) createSinkInternal(cfg SinkConfig) (hermod.Sink, error) {
	// Resolve secrets in config
	cfg.Config = r.resolveSecrets(context.Background(), cfg.Config)

	if cfg.Type == "failover" {
		primaryID := cfg.Config["primary_id"]
		fallbackIDsStr := cfg.Config["fallback_ids"]
		fallbackIDs := strings.Split(fallbackIDsStr, ",")

		primarySink, err := r.resolveAndCreateSink(primaryID)
		if err != nil {
			return nil, fmt.Errorf("failed to create primary sink %s: %w", primaryID, err)
		}

		strategy, _ := cfg.Config["strategy"]
		if strategy == "" {
			strategy = "failover"
		}

		var fallbacks []hermod.Sink
		for _, id := range fallbackIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			f, err := r.resolveAndCreateSink(id)
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

	return CreateSink(cfg)
}

func (r *Registry) resolveAndCreateSink(id string) (hermod.Sink, error) {
	if r.storage == nil {
		return nil, fmt.Errorf("registry storage is not available")
	}
	ctx := context.Background()
	dbSnk, err := r.storage.GetSink(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get sink %s from storage: %w", id, err)
	}
	snkCfg := SinkConfig{
		ID:     dbSnk.ID,
		Type:   dbSnk.Type,
		Config: dbSnk.Config,
	}
	return r.createSinkInternal(snkCfg)
}

func (r *Registry) SetConfig(cfg pkgengine.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = cfg
}

func (r *Registry) GetLookupCache(key string) (any, bool) {
	r.lookupCacheMu.RLock()
	defer r.lookupCacheMu.RUnlock()
	val, ok := r.lookupCache[key]
	return val, ok
}

func (r *Registry) SetLookupCache(key string, value any, ttl time.Duration) {
	r.lookupCacheMu.Lock()
	r.lookupCache[key] = value
	r.lookupCacheMu.Unlock()

	if ttl > 0 {
		go func() {
			time.Sleep(ttl)
			r.lookupCacheMu.Lock()
			delete(r.lookupCache, key)
			r.lookupCacheMu.Unlock()
		}()
	}
}

func (r *Registry) GetOrOpenDB(src storage.Source) (*sql.DB, error) {
	return r.getOrOpenDB(src)
}

func (r *Registry) GetOrOpenDBByID(ctx context.Context, id string) (*sql.DB, string, error) {
	if r.storage == nil {
		return nil, "", fmt.Errorf("registry storage is not initialized")
	}
	src, err := r.storage.GetSource(ctx, id)
	if err != nil {
		return nil, "", err
	}
	db, err := r.getOrOpenDB(src)
	return db, src.Type, err
}

func (r *Registry) GetSource(ctx context.Context, id string) (storage.Source, error) {
	if r.storage == nil {
		return storage.Source{}, fmt.Errorf("registry storage is not initialized")
	}
	return r.storage.GetSource(ctx, id)
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

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if srcMap, ok := v.(map[string]any); ok {
			if dstMap, ok := dst[k].(map[string]any); ok {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[k] = v
	}
}

func (r *Registry) TestTransformationPipeline(ctx context.Context, transformations []storage.Transformation, msg hermod.Message) ([]hermod.Message, error) {
	results := make([]hermod.Message, len(transformations))
	currentMsg := msg.Clone()
	for i, t := range transformations {
		if currentMsg == nil {
			results[i] = nil
			continue
		}

		config := make(map[string]any)
		for k, v := range t.Config {
			config[k] = v
		}
		res, err := r.applyTransformation(context.Background(), currentMsg.Clone(), t.Type, config)
		if err != nil {
			return nil, err
		}
		results[i] = res
		currentMsg = res
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
		// Pass Registry to transformer if it needs it (like for storage or lookup)
		tctx := context.WithValue(ctx, "registry", r)
		if r.stateStore != nil {
			tctx = context.WithValue(tctx, hermod.StateStoreKey, r.stateStore)
		}
		res, err := t.Transform(tctx, modifiedMsg, config)

		// Record PII discoveries for compliance dashboard
		if transType == "mask" && res != nil {
			go r.recordPIIDiscoveries(res, config)
		}

		return res, err
	}

	return modifiedMsg, nil
}

func (r *Registry) recordPIIDiscoveries(msg hermod.Message, config map[string]any) {
	if msg == nil {
		return
	}
	data := msg.Data()
	if data == nil {
		return
	}

	foundTypes := make(map[string]int)
	field, _ := config["field"].(string)

	if field == "*" || field == "" {
		r.scanForPII(data, foundTypes)
	} else {
		val := evaluator.GetMsgValByPath(msg, field)
		if s, ok := val.(string); ok {
			types := transformer.PIIEngine().Discover(s)
			for _, t := range types {
				foundTypes[t]++
			}
		}
	}

	if len(foundTypes) > 0 {
		workflowID, _ := msg.Metadata()["_hermod_workflow_id"]
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

func (r *Registry) IsEngineRunning(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.engines[id]
	return ok
}

func (r *Registry) GetAllStatuses() []pkgengine.StatusUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()

	statuses := make([]pkgengine.StatusUpdate, 0, len(r.engines))
	for _, ae := range r.engines {
		statuses = append(statuses, ae.engine.GetStatus())
	}
	return statuses
}

type DashboardStats struct {
	ActiveSources   int    `json:"active_sources"`
	ActiveSinks     int    `json:"active_sinks"`
	ActiveWorkflows int    `json:"active_workflows"`
	TotalProcessed  uint64 `json:"total_processed"`
	TotalErrors     uint64 `json:"total_errors"`
	TotalLag        uint64 `json:"total_lag"`
	FailedWorkflows int    `json:"failed_workflows"`
	Uptime          int64  `json:"uptime"`
	ActiveWorkers   int    `json:"active_workers"`
	TotalWorkflows  int    `json:"total_workflows"`
	TotalSources    int    `json:"total_sources"`
	TotalSinks      int    `json:"total_sinks"`
}

func (r *Registry) GetDashboardStats(ctx context.Context, vhost string) (DashboardStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stats := DashboardStats{
		Uptime: int64(time.Since(r.startTime).Seconds()),
	}

	if r.storage != nil {
		_, totalWf, _ := r.storage.ListWorkflows(ctx, storage.CommonFilter{Limit: 1, VHost: vhost})
		stats.TotalWorkflows = totalWf
		_, totalSrc, _ := r.storage.ListSources(ctx, storage.CommonFilter{Limit: 1, VHost: vhost})
		stats.TotalSources = totalSrc
		_, totalSnk, _ := r.storage.ListSinks(ctx, storage.CommonFilter{Limit: 1, VHost: vhost})
		stats.TotalSinks = totalSnk

		workers, _, err := r.storage.ListWorkers(ctx, storage.CommonFilter{Limit: 100})
		if err == nil {
			for _, w := range workers {
				if w.LastSeen != nil && time.Since(*w.LastSeen) < 2*time.Minute {
					stats.ActiveWorkers++
				}
			}
		}
	}

	activeSources := make(map[string]bool)
	activeSinks := make(map[string]bool)

	for _, ae := range r.engines {
		if vhost != "" && vhost != "all" {
			if ae.workflow.VHost != vhost {
				continue
			}
		}

		stats.ActiveWorkflows++
		status := ae.engine.GetStatus()
		stats.TotalProcessed += status.ProcessedCount
		stats.TotalErrors += status.DeadLetterCount

		if status.EngineStatus == "failed" || status.EngineStatus == "error" {
			stats.FailedWorkflows++
		}

		// In a real scenario, we would sum up specific lag metrics if available
		// For now, we'll try to extract lag from NodeMetrics if it exists
		if lag, ok := status.NodeMetrics["source_lag"]; ok {
			stats.TotalLag += lag
		} else if lag, ok := status.NodeMetrics["lag"]; ok {
			stats.TotalLag += lag
		}

		if status.SourceStatus == "running" {
			activeSources[status.SourceID] = true
		}

		for sinkID, sinkStatus := range status.SinkStatuses {
			if sinkStatus == "running" {
				activeSinks[sinkID] = true
			}
		}
	}

	stats.ActiveSources = len(activeSources)
	stats.ActiveSinks = len(activeSinks)

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

func (r *Registry) GetSourceConfigs(id string) ([]SourceConfig, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ae, ok := r.engines[id]
	if !ok {
		return nil, false
	}
	return ae.srcConfigs, true
}

func (r *Registry) GetSinkConfigs(id string) ([]SinkConfig, bool) {
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
		if node.Type == "source" {
			if node.RefID == "" || node.RefID == "new" {
				return fmt.Errorf("source node %s is not configured", r.getNodeName(node))
			}
			if r.storage != nil {
				if _, err := r.storage.GetSource(ctx, node.RefID); err != nil {
					return fmt.Errorf("source node %s refers to missing source %s: %w", r.getNodeName(node), node.RefID, err)
				}
			}
		} else if node.Type == "sink" {
			if node.RefID == "" || node.RefID == "new" {
				return fmt.Errorf("sink node %s is not configured", r.getNodeName(node))
			}
			if r.storage != nil {
				if _, err := r.storage.GetSink(ctx, node.RefID); err != nil {
					return fmt.Errorf("sink node %s refers to missing sink %s: %w", r.getNodeName(node), node.RefID, err)
				}
			}
		}
	}

	// 2. At least one source
	var sourceNodes []*storage.WorkflowNode
	for i, node := range wf.Nodes {
		if node.Type == "source" {
			sourceNodes = append(sourceNodes, &wf.Nodes[i])
		}
	}
	if len(sourceNodes) == 0 {
		return fmt.Errorf("workflow must have at least one source node")
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
				node := findNodeByID(wf.Nodes, nextID)
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

		node := findNodeByID(wf.Nodes, id)
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
		return fmt.Errorf("no sink node reachable from any source")
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
		if findNodeByID(wf.Nodes, edge.SourceID) == nil {
			return fmt.Errorf("edge %s refers to missing source node %s", edge.ID, edge.SourceID)
		}
		if findNodeByID(wf.Nodes, edge.TargetID) == nil {
			return fmt.Errorf("edge %s refers to missing target node %s", edge.ID, edge.TargetID)
		}
	}

	// 6. DLQ Prioritization requirements
	if wf.PrioritizeDLQ {
		if wf.DeadLetterSinkID == "" {
			return fmt.Errorf("PrioritizeDLQ is enabled but no Dead Letter Sink is configured")
		}
		if r.storage != nil {
			dlqSink, err := r.storage.GetSink(ctx, wf.DeadLetterSinkID)
			if err != nil {
				return fmt.Errorf("dead letter sink %s not found: %w", wf.DeadLetterSinkID, err)
			}
			// Verify that the DLQ sink type is also a valid source type.
			// CreateSource will return an error if the type is not supported as a source.
			// We use a dummy config for validation.
			testSrc, err := r.createSourceInternal(SourceConfig{
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
		for _, node := range wf.Nodes {
			if node.Type == "sink" {
				snk, err := r.storage.GetSink(ctx, node.RefID)
				if err == nil {
					if snk.Config["enable_idempotency"] != "true" {
						r.logger.Warn("Workflow has PrioritizeDLQ enabled but sink does not have idempotency enabled; enable idempotency to avoid side effects during re-processing", "workflow_id", wf.ID, "sink_id", snk.ID)
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

type SourceConfig struct {
	ID                 string            `json:"id"`
	Type               string            `json:"type"`
	Config             map[string]string `json:"config"`
	State              map[string]string `json:"state"`
	ReconnectIntervals []time.Duration   `json:"-"`
}

type SinkConfig struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
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
	wfs, _, err := r.storage.ListWorkflows(ctx, storage.CommonFilter{})
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

func BuildConnectionString(cfg map[string]string, sourceType string) string {
	if cs, ok := cfg["connection_string"]; ok && cs != "" {
		return cs
	}
	if cs, ok := cfg["uri"]; ok && cs != "" {
		return cs
	}
	if cs, ok := cfg["url"]; ok && cs != "" {
		return cs
	}

	host := cfg["host"]
	port := cfg["port"]
	user := cfg["user"]
	if user == "" {
		user = cfg["username"]
	}
	password := cfg["password"]
	dbname := cfg["dbname"]

	switch sourceType {
	case "postgres", "yugabyte", "mssql", "oracle", "clickhouse":
		u := &url.URL{
			Scheme: "postgres", // Default
			Host:   fmt.Sprintf("%s:%s", host, port),
			Path:   "/" + dbname,
		}

		if sourceType == "mssql" {
			u.Scheme = "sqlserver"
			u.Path = ""
			q := u.Query()
			q.Set("database", dbname)
			u.RawQuery = q.Encode()
		} else if sourceType == "oracle" {
			u.Scheme = "oracle"
		} else if sourceType == "clickhouse" {
			u.Scheme = "clickhouse"
		} else if sourceType == "yugabyte" {
			u.Scheme = "postgres"
		}

		if user != "" || password != "" {
			u.User = url.UserPassword(user, password)
		}

		if sourceType == "postgres" || sourceType == "yugabyte" {
			sslmode := cfg["sslmode"]
			if sslmode == "" {
				sslmode = "disable"
			}
			q := u.Query()
			q.Set("sslmode", sslmode)
			u.RawQuery = q.Encode()
		}

		return u.String()

	case "mysql", "mariadb":
		// MySQL DSN: [username[:password]@][protocol[(address)]]/dbname[?param1=value1&...&paramN=valueN]
		// Special characters in username and password should be avoided or escaped if they contain @ or /
		// The mysql driver doesn't use standard URL escaping for DSN.
		// However, it's safer to use url.QueryEscape for user/pass if they have special chars
		escapedUser := url.QueryEscape(user)
		escapedPass := url.QueryEscape(password)
		if user != "" && password != "" {
			return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", escapedUser, escapedPass, host, port, dbname)
		} else if user != "" {
			return fmt.Sprintf("%s@tcp(%s:%s)/%s", escapedUser, host, port, dbname)
		}
		return fmt.Sprintf("tcp(%s:%s)/%s", host, port, dbname)

	case "sqlite":
		return cfg["path"]
	case "rabbitmq", "rabbitmq_queue":
		useSSL := strings.EqualFold(cfg["use_ssl"], "true")
		u := &url.URL{
			Scheme: "amqp", // Default for queue
			Host:   fmt.Sprintf("%s:%s", host, port),
			Path:   "/" + dbname, // vhost
		}
		if useSSL {
			u.Scheme = "amqps"
		}

		if sourceType == "rabbitmq" {
			u.Scheme = "rabbitmq-stream"
			if useSSL {
				u.Scheme = "rabbitmq-streams"
			}
			if port == "" {
				p := "5552"
				if useSSL {
					p = "5551"
				}
				u.Host = fmt.Sprintf("%s:%s", host, p)
			}
		} else {
			if port == "" {
				p := "5672"
				if useSSL {
					p = "5671"
				}
				u.Host = fmt.Sprintf("%s:%s", host, p)
			}
		}
		if user != "" || password != "" {
			u.User = url.UserPassword(user, password)
		}
		return u.String()
	default:
		return ""
	}
}

type formStorageAdapter struct {
	storage RegistryStorage
}

func (a *formStorageAdapter) CreateFormSubmission(ctx context.Context, sub sourceform.FormSubmission) error {
	return a.storage.CreateFormSubmission(ctx, storage.FormSubmission{
		ID:        sub.ID,
		Timestamp: sub.Timestamp,
		Path:      sub.Path,
		Data:      sub.Data,
		Status:    sub.Status,
	})
}

func (a *formStorageAdapter) ListFormSubmissions(ctx context.Context, filter sourceform.FormSubmissionFilter) ([]sourceform.FormSubmission, int, error) {
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
	return a.storage.UpdateFormSubmissionStatus(ctx, id, status)
}
