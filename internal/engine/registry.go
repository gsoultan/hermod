package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/governance"
	"github.com/user/hermod/internal/mesh"
	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/optimizer"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/buffer"
	"github.com/user/hermod/pkg/compression"
	pkgengine "github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/schema"
	"github.com/user/hermod/pkg/secrets"
	"github.com/user/hermod/pkg/sink/failover"
	"github.com/user/hermod/pkg/source/batchsql"
	sourceform "github.com/user/hermod/pkg/source/form"
	"github.com/user/hermod/pkg/state"
	"github.com/user/hermod/pkg/transformer"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("hermod-registry")

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
	UpdateNodeState(ctx context.Context, workflowID, nodeID string, state interface{}) error
	GetNodeStates(ctx context.Context, workflowID string) (map[string]interface{}, error)
	RecordTraceStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) error
	PurgeAuditLogs(ctx context.Context, before time.Time) error
	PurgeMessageTraces(ctx context.Context, before time.Time) error
}

type SourceFactory func(SourceConfig) (hermod.Source, error)
type SinkFactory func(SinkConfig) (hermod.Sink, error)

type LiveMessage struct {
	WorkflowID string                 `json:"workflow_id"`
	NodeID     string                 `json:"node_id"`
	Timestamp  time.Time              `json:"timestamp"`
	Data       map[string]interface{} `json:"data"`
	IsError    bool                   `json:"is_error"`
	Error      string                 `json:"error,omitempty"`
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
	nodeStates          map[string]interface{}
	nodeStatesMu        sync.Mutex
	lookupCache         map[string]interface{}
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
		nodeStates:          make(map[string]interface{}),
		lookupCache:         make(map[string]interface{}),
		dbPool:              make(map[string]*sql.DB),
		logger:              pkgengine.NewDefaultLogger(),
		idleMonitorStop:     make(chan struct{}),
		startTime:           time.Now(),
		secretManager:       &secrets.EnvManager{Prefix: "HERMOD_SECRET_"},
		schemaRegistry:      schema.NewStorageRegistry(s),
		dqScorer:            governance.NewScorer(),
		meshManager:         mesh.NewManager(pkgengine.NewDefaultLogger()),
		piiStats:            make(map[string]*PIIStats),
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
		log.Printf("Warning: failed to initialize state store: %v", err)
	}

	// Start background maintenance routines
	go reg.runIdleMonitor()
	go reg.runRetentionPurge()
	go reg.optimizer.Start(context.Background())
	return reg
}

func (r *Registry) runRetentionPurge() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run once on startup
	r.purgeRetention()

	for {
		select {
		case <-r.idleMonitorStop:
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
		case <-r.idleMonitorStop:
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

func (r *Registry) SubscribeStatus() chan pkgengine.StatusUpdate {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan pkgengine.StatusUpdate, 100)
	r.statusSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeStatus(ch chan pkgengine.StatusUpdate) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.statusSubs, ch)
	close(ch)
}

func (r *Registry) SubscribeDashboardStats() chan DashboardStats {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan DashboardStats, 100)
	r.dashboardSubs[ch] = true
	return ch
}

func getConfigString(config map[string]interface{}, key string) string {
	if val, ok := config[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", val)
	}
	return ""
}

func (r *Registry) UnsubscribeDashboardStats(ch chan DashboardStats) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.dashboardSubs, ch)
	close(ch)
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
		db, err = sql.Open("postgres", connStr)
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
	default:
		return nil, fmt.Errorf("unsupported source type for db_lookup: %s", src.Type)
	}

	if err != nil {
		return nil, err
	}

	// Configure pool
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	r.dbPool[src.ID] = db
	return db, nil
}

func (r *Registry) SubscribeLogs() chan storage.Log {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan storage.Log, 1000)
	r.logSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeLogs(ch chan storage.Log) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.logSubs, ch)
	close(ch)
}

func (r *Registry) SubscribeLiveMessages() chan LiveMessage {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan LiveMessage, 1000)
	r.liveMsgSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeLiveMessages(ch chan LiveMessage) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.liveMsgSubs, ch)
	close(ch)
}

func (r *Registry) broadcastStatus(update pkgengine.StatusUpdate) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()

	for ch := range r.statusSubs {
		select {
		case ch <- update:
		default:
			// Buffer full, skip or handle
		}
	}

	// Also broadcast aggregate dashboard stats
	if len(r.dashboardSubs) > 0 {
		now := time.Now()
		// Throttle dashboard broadcasts to at most once per second to reduce overhead during high throughput
		if now.Sub(r.lastDashboardUpdate) > 1*time.Second {
			r.lastDashboardUpdate = now

			// Release statusSubsMu to avoid deadlock if GetDashboardStats needs it (it doesn't, but good practice)
			// Actually GetDashboardStats locks r.mu.
			r.statusSubsMu.Unlock()
			stats, _ := r.GetDashboardStats(context.Background(), "all")
			r.statusSubsMu.Lock()

			for ch := range r.dashboardSubs {
				select {
				case ch <- stats:
				default:
				}
			}
		}
	}
}

func (r *Registry) broadcastLog(engineID, level, msg string) {
	r.broadcastLogWithData(engineID, level, msg, "")
}

func (r *Registry) broadcastLogWithData(engineID, level, msg, data string) {
	l := storage.Log{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
		Data:      data,
	}

	r.mu.Lock()
	if eng, ok := r.engines[engineID]; ok && eng.isWorkflow {
		l.WorkflowID = engineID
	}
	r.mu.Unlock()

	_ = r.CreateLog(context.Background(), l)
}

func (r *Registry) CreateLog(ctx context.Context, l storage.Log) error {
	if r.logStorage != nil {
		err := r.logStorage.CreateLog(ctx, l)

		r.statusSubsMu.Lock()
		for ch := range r.logSubs {
			select {
			case ch <- l:
			default:
			}
		}
		r.statusSubsMu.Unlock()

		return err
	}
	return nil
}

func (r *Registry) broadcastLiveMessage(msg LiveMessage) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()

	for ch := range r.liveMsgSubs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (r *Registry) RecordStep(ctx context.Context, workflowID, messageID string, step hermod.TraceStep) {
	if r.logStorage != nil {
		_ = r.logStorage.RecordTraceStep(ctx, workflowID, messageID, step)
	}
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

type statefulSource struct {
	hermod.Source
	registry *Registry
	sourceID string
}

func (s *statefulSource) Ack(ctx context.Context, msg hermod.Message) error {
	return s.Source.Ack(ctx, msg)
}

func (s *statefulSource) GetState() map[string]string {
	if st, ok := s.Source.(hermod.Stateful); ok {
		return st.GetState()
	}
	return nil
}

func (s *statefulSource) SetState(state map[string]string) {
	if st, ok := s.Source.(hermod.Stateful); ok {
		st.SetState(state)
	}
}

func (s *statefulSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if d, ok := s.Source.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, fmt.Errorf("source does not support database discovery")
}

func (s *statefulSource) DiscoverTables(ctx context.Context) ([]string, error) {
	if d, ok := s.Source.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("source does not support table discovery")
}

func (s *statefulSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if sm, ok := s.Source.(hermod.Sampler); ok {
		return sm.Sample(ctx, table)
	}
	return nil, fmt.Errorf("source does not support sampling")
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

func (r *Registry) TestSource(ctx context.Context, cfg SourceConfig) error {
	src, err := r.createSource(cfg)
	if err != nil {
		return err
	}
	defer src.Close()

	if readyChecker, ok := src.(hermod.ReadyChecker); ok {
		return readyChecker.IsReady(ctx)
	}
	return src.Ping(ctx)
}

func (r *Registry) TestSink(ctx context.Context, cfg SinkConfig) error {
	if cfg.Type == "stdout" {
		return nil
	}
	snk, err := r.createSink(cfg)
	if err != nil {
		return err
	}
	defer snk.Close()
	return snk.Ping(ctx)
}

func (r *Registry) DiscoverDatabases(ctx context.Context, cfg SourceConfig) ([]string, error) {
	src, err := r.createSource(cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, fmt.Errorf("source type %s does not support database discovery", cfg.Type)
}

func (r *Registry) DiscoverTables(ctx context.Context, cfg SourceConfig) ([]string, error) {
	src, err := r.createSource(cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("source type %s does not support table discovery", cfg.Type)
}

func (r *Registry) DiscoverSinkDatabases(ctx context.Context, cfg SinkConfig) ([]string, error) {
	snk, err := r.createSink(cfg)
	if err != nil {
		return nil, err
	}
	defer snk.Close()

	if d, ok := snk.(hermod.Discoverer); ok {
		return d.DiscoverDatabases(ctx)
	}
	return nil, fmt.Errorf("sink type %s does not support database discovery", cfg.Type)
}

func (r *Registry) DiscoverSinkTables(ctx context.Context, cfg SinkConfig) ([]string, error) {
	snk, err := r.createSink(cfg)
	if err != nil {
		return nil, err
	}
	defer snk.Close()

	if d, ok := snk.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("sink type %s does not support table discovery", cfg.Type)
}

func (r *Registry) SampleTable(ctx context.Context, cfg SourceConfig, table string) (hermod.Message, error) {
	src, err := r.createSource(cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if s, ok := src.(hermod.Sampler); ok {
		return s.Sample(ctx, table)
	}
	return nil, fmt.Errorf("source type %s does not support sampling", cfg.Type)
}

func (r *Registry) SampleSinkTable(ctx context.Context, cfg SinkConfig, table string) (hermod.Message, error) {
	msgs, err := r.BrowseSinkTable(ctx, cfg, table, 1)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no data found in sink table %s", table)
	}
	return msgs[0], nil
}

func (r *Registry) BrowseSinkTable(ctx context.Context, cfg SinkConfig, table string, limit int) ([]hermod.Message, error) {
	snk, err := r.createSink(cfg)
	if err != nil {
		return nil, err
	}
	defer snk.Close()

	if b, ok := snk.(hermod.Browser); ok {
		return b.Browse(ctx, table, limit)
	}

	if s, ok := snk.(hermod.Sampler); ok && limit == 1 {
		msg, err := s.Sample(ctx, table)
		if err != nil {
			return nil, err
		}
		return []hermod.Message{msg}, nil
	}

	return nil, fmt.Errorf("sink type %s does not support browsing", cfg.Type)
}

type subSource struct {
	nodeID   string
	sourceID string
	source   hermod.Source
	running  bool
}

type multiSource struct {
	sources []*subSource
	msgChan chan hermod.Message
	errChan chan error
	mu      sync.Mutex
	closed  bool
}

func (m *multiSource) Read(ctx context.Context) (hermod.Message, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("multiSource closed")
	}

	for _, s := range m.sources {
		if !s.running {
			s.running = true
			go func(ss *subSource) {
				defer func() {
					m.mu.Lock()
					ss.running = false
					m.mu.Unlock()
				}()
				for {
					msg, err := ss.source.Read(ctx)
					if err != nil {
						if ctx.Err() == nil {
							select {
							case m.errChan <- err:
							case <-ctx.Done():
							}
						}
						return
					}
					if msg != nil {
						msg.SetMetadata("_source_node_id", ss.nodeID)
						select {
						case m.msgChan <- msg:
						case <-ctx.Done():
							return
						}
					}
				}
			}(s)
		}
	}
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-m.errChan:
		return nil, err
	case msg := <-m.msgChan:
		return msg, nil
	}
}

func (m *multiSource) Ack(ctx context.Context, msg hermod.Message) error {
	nodeID := msg.Metadata()["_source_node_id"]
	for _, s := range m.sources {
		if s.nodeID == nodeID {
			return s.source.Ack(ctx, msg)
		}
	}
	return nil
}

func (m *multiSource) GetState() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()

	combined := make(map[string]string)
	for _, s := range m.sources {
		if st, ok := s.source.(hermod.Stateful); ok {
			state := st.GetState()
			for k, v := range state {
				if len(m.sources) == 1 {
					combined[k] = v
				} else {
					combined[s.nodeID+":"+k] = v
				}
			}
		}
	}
	return combined
}

func (m *multiSource) SetState(state map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.sources {
		if st, ok := s.source.(hermod.Stateful); ok {
			if len(m.sources) == 1 {
				st.SetState(state)
			} else {
				// Filter state for this source
				sourceState := make(map[string]string)
				prefix := s.nodeID + ":"
				for k, v := range state {
					if strings.HasPrefix(k, prefix) {
						sourceState[strings.TrimPrefix(k, prefix)] = v
					}
				}
				if len(sourceState) > 0 {
					st.SetState(sourceState)
				}
			}
		}
	}
}

func (m *multiSource) Ping(ctx context.Context) error {
	for _, s := range m.sources {
		var err error
		if readyChecker, ok := s.source.(hermod.ReadyChecker); ok {
			err = readyChecker.IsReady(ctx)
		} else {
			err = s.source.Ping(ctx)
		}
		if err != nil {
			return fmt.Errorf("source %s health check failed: %w", s.nodeID, err)
		}
	}
	return nil
}

func (m *multiSource) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var errs []string
	for _, s := range m.sources {
		if err := s.source.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing sources: %s", strings.Join(errs, ", "))
	}
	return nil
}

func toFloat64(val interface{}) (float64, bool) {
	return evaluator.ToFloat64(val)
}

func toBool(val interface{}) bool {
	return evaluator.ToBool(val)
}

func getValByPath(data map[string]interface{}, path string) interface{} {
	return evaluator.GetValByPath(data, path)
}

func getMsgValByPath(msg hermod.Message, path string) interface{} {
	return evaluator.GetMsgValByPath(msg, path)
}

func setValByPath(data map[string]interface{}, path string, val interface{}) {
	evaluator.SetValByPath(data, path, val)
}

func (r *Registry) getValByPath(data map[string]interface{}, path string) interface{} {
	return evaluator.GetValByPath(data, path)
}

func (r *Registry) getMsgValByPath(msg hermod.Message, path string) interface{} {
	return evaluator.GetMsgValByPath(msg, path)
}

func (r *Registry) toFloat64(val interface{}) (float64, bool) {
	return evaluator.ToFloat64(val)
}

func (r *Registry) toBool(val interface{}) bool {
	return evaluator.ToBool(val)
}

func (r *Registry) setValByPath(data map[string]interface{}, path string, val interface{}) {
	evaluator.SetValByPath(data, path, val)
}

func (r *Registry) resolveTemplate(temp string, data map[string]interface{}) string {
	return evaluator.ResolveTemplate(temp, data)
}

func (r *Registry) evaluateConditions(msg hermod.Message, conditions []map[string]interface{}) bool {
	return evaluator.EvaluateConditions(msg, conditions)
}

func (r *Registry) evaluateAdvancedExpression(msg hermod.Message, expr interface{}) interface{} {
	return r.evaluator.EvaluateAdvancedExpression(msg, expr)
}

func (r *Registry) GetLookupCache(key string) (interface{}, bool) {
	r.lookupCacheMu.RLock()
	defer r.lookupCacheMu.RUnlock()
	val, ok := r.lookupCache[key]
	return val, ok
}

func (r *Registry) SetLookupCache(key string, value interface{}, ttl time.Duration) {
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
	src, err := r.storage.GetSource(ctx, id)
	if err != nil {
		return nil, "", err
	}
	db, err := r.getOrOpenDB(src)
	return db, src.Type, err
}

func (r *Registry) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return r.storage.GetSource(ctx, id)
}

func (r *Registry) parseAndEvaluate(msg hermod.Message, expr string) interface{} {
	return r.evaluator.ParseAndEvaluate(msg, expr)
}

func (r *Registry) parseArgs(argsStr string) []string {
	var args []string
	var current strings.Builder
	parenCount := 0
	inQuotes := false
	var quoteChar byte

	for i := 0; i < len(argsStr); i++ {
		c := argsStr[i]
		if (c == '"' || c == '\'') && (i == 0 || argsStr[i-1] != '\\') {
			if !inQuotes {
				inQuotes = true
				quoteChar = c
			} else if c == quoteChar {
				inQuotes = false
			}
			current.WriteByte(c)
		} else if !inQuotes && c == '(' {
			parenCount++
			current.WriteByte(c)
		} else if !inQuotes && c == ')' {
			parenCount--
			current.WriteByte(c)
		} else if !inQuotes && parenCount == 0 && c == ',' {
			args = append(args, strings.TrimSpace(current.String()))
			current.Reset()
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 || len(args) > 0 {
		args = append(args, strings.TrimSpace(current.String()))
	}
	return args
}

func (r *Registry) callFunction(name string, args []interface{}) interface{} {
	return r.evaluator.CallFunction(name, args)
}

func (r *Registry) mergeData(dst, src map[string]interface{}, strategy string) {
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
			if srcMap, ok := v.(map[string]interface{}); ok {
				if dstMap, ok := dst[k].(map[string]interface{}); ok {
					r.mergeData(dstMap, srcMap, "deep")
					continue
				}
			}
			dst[k] = v
		}
	}
}

func deepMerge(dst, src map[string]interface{}) {
	for k, v := range src {
		if srcMap, ok := v.(map[string]interface{}); ok {
			if dstMap, ok := dst[k].(map[string]interface{}); ok {
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

		config := make(map[string]interface{})
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
	NodeID   string                 `json:"node_id"`
	NodeType string                 `json:"node_type"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
	Metadata map[string]string      `json:"metadata,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Filtered bool                   `json:"filtered,omitempty"`
	Branch   string                 `json:"branch,omitempty"`
}

func (r *Registry) applyTransformation(ctx context.Context, modifiedMsg hermod.Message, transType string, config map[string]interface{}) (hermod.Message, error) {
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

func (r *Registry) doApplyTransformation(ctx context.Context, modifiedMsg hermod.Message, transType string, config map[string]interface{}) (hermod.Message, error) {
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

func (r *Registry) recordPIIDiscoveries(msg hermod.Message, config map[string]interface{}) {
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

func (r *Registry) scanForPII(data map[string]interface{}, found map[string]int) {
	for _, v := range data {
		switch val := v.(type) {
		case string:
			types := transformer.PIIEngine().Discover(val)
			for _, t := range types {
				found[t]++
			}
		case map[string]interface{}:
			r.scanForPII(val, found)
		case []interface{}:
			for _, item := range val {
				if m, ok := item.(map[string]interface{}); ok {
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

func (r *Registry) runWorkflowNode(workflowID string, node *storage.WorkflowNode, msg hermod.Message) (hermod.Message, string, error) {
	if msg == nil {
		return nil, "", nil
	}

	ctx, span := tracer.Start(context.Background(), "RunWorkflowNode", trace.WithAttributes(
		attribute.String("workflow_id", workflowID),
		attribute.String("node_id", node.ID),
		attribute.String("node_type", node.Type),
		attribute.String("message_id", msg.ID()),
	))
	defer span.End()

	// For efficiency, we only clone if we are actually modifying the message
	// and want to keep the original intact.
	// But most nodes are sequential transformations, so we can modify in-place
	// IF it's a DefaultMessage that supports SetData.

	currentMsg := msg
	data := currentMsg.Data()

	// Broadcast live message for observability
	// Clone data to avoid race conditions as the message continues through the pipeline
	dataClone := make(map[string]interface{})
	for k, v := range data {
		dataClone[k] = v
	}
	go r.broadcastLiveMessage(LiveMessage{
		WorkflowID: workflowID,
		NodeID:     node.ID,
		Timestamp:  time.Now(),
		Data:       dataClone,
	})

	switch node.Type {
	case "validator":
		// Use the new ValidatorTransformer via Registry
		res, err := r.applyTransformation(ctx, currentMsg, "validator", node.Config)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if res == nil {
			span.SetAttributes(attribute.Bool("filtered", true))
		}
		return res, "", err

	case "transformation":
		transType, _ := node.Config["transType"].(string)
		modifiedMsg := currentMsg.Clone()

		if transType == "pipeline" {
			stepsStr, _ := node.Config["steps"].(string)
			var steps []map[string]interface{}
			_ = json.Unmarshal([]byte(stepsStr), &steps)

			var err error
			for _, step := range steps {
				st, _ := step["transType"].(string)
				modifiedMsg, err = r.applyTransformation(ctx, modifiedMsg, st, step)
				if err != nil {
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
					return nil, "", err
				}
				if modifiedMsg == nil {
					span.SetAttributes(attribute.Bool("filtered", true))
					return nil, "", nil // Filtered
				}
			}
			return modifiedMsg, "", nil
		}

		res, err := r.applyTransformation(ctx, modifiedMsg, transType, node.Config)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			go r.broadcastLiveMessage(LiveMessage{
				WorkflowID: workflowID,
				NodeID:     node.ID,
				Timestamp:  time.Now(),
				Data:       dataClone,
				IsError:    true,
				Error:      err.Error(),
			})
		} else if res == nil {
			span.SetAttributes(attribute.Bool("filtered", true))
		}
		return res, "", err

	case "condition":
		conditionsStr, _ := node.Config["conditions"].(string)
		var conditions []map[string]interface{}
		if conditionsStr != "" {
			_ = json.Unmarshal([]byte(conditionsStr), &conditions)
		}

		// Fallback to old format
		if len(conditions) == 0 {
			field, _ := node.Config["field"].(string)
			op, _ := node.Config["operator"].(string)
			val, _ := node.Config["value"].(string)
			if field != "" {
				conditions = append(conditions, map[string]interface{}{
					"field":    field,
					"operator": op,
					"value":    val,
				})
			}
		}

		if r.evaluateConditions(currentMsg, conditions) {
			span.SetAttributes(attribute.String("branch", "true"))
			return currentMsg, "true", nil
		}
		span.SetAttributes(attribute.String("branch", "false"))
		return currentMsg, "false", nil

	case "router":
		// rules is stored as a JSON array string in Config["rules"]
		rulesStr, _ := node.Config["rules"].(string)
		var rules []map[string]interface{}
		_ = json.Unmarshal([]byte(rulesStr), &rules)

		for _, rule := range rules {
			label, _ := rule["label"].(string)

			// Rule can have multiple conditions
			var ruleConditions []map[string]interface{}
			if condsRaw, ok := rule["conditions"].([]interface{}); ok {
				for _, cr := range condsRaw {
					if condMap, ok := cr.(map[string]interface{}); ok {
						ruleConditions = append(ruleConditions, condMap)
					}
				}
			}

			// If no conditions array, try single condition fields
			if len(ruleConditions) == 0 {
				field, _ := rule["field"].(string)
				op, _ := rule["operator"].(string)
				val := rule["value"]
				if field != "" && op != "" {
					ruleConditions = append(ruleConditions, map[string]interface{}{
						"field":    field,
						"operator": op,
						"value":    val,
					})
				}
			}

			if len(ruleConditions) > 0 {
				if r.evaluateConditions(currentMsg, ruleConditions) {
					span.SetAttributes(attribute.String("branch", label))
					return currentMsg, label, nil
				}
			}
		}
		span.SetAttributes(attribute.String("branch", "default"))
		return currentMsg, "default", nil

	case "switch":
		// cases is stored as a JSON array string in Config["cases"]
		casesStr, _ := node.Config["cases"].(string)
		var cases []map[string]interface{}
		_ = json.Unmarshal([]byte(casesStr), &cases)

		field, _ := node.Config["field"].(string)
		fieldValStr := fmt.Sprintf("%v", getMsgValByPath(currentMsg, field))

		for _, c := range cases {
			label, _ := c["label"].(string)

			// Check for conditions array in the case
			var caseConditions []map[string]interface{}
			if condsRaw, ok := c["conditions"].([]interface{}); ok {
				for _, cr := range condsRaw {
					if condMap, ok := cr.(map[string]interface{}); ok {
						caseConditions = append(caseConditions, condMap)
					}
				}
			}

			if len(caseConditions) > 0 {
				if r.evaluateConditions(currentMsg, caseConditions) {
					span.SetAttributes(attribute.String("branch", label))
					return currentMsg, label, nil
				}
			} else {
				// Fallback to value comparison with the main field
				val, _ := c["value"].(string)
				if val == fieldValStr {
					span.SetAttributes(attribute.String("branch", label))
					return currentMsg, label, nil
				}
			}
		}
		span.SetAttributes(attribute.String("branch", "default"))
		return currentMsg, "default", nil

	case "stateful":
		op, _ := node.Config["operation"].(string) // "count", "sum"
		field, _ := node.Config["field"].(string)
		outputField, _ := node.Config["outputField"].(string)
		if outputField == "" {
			outputField = field + "_" + op
		}

		key := workflowID + ":" + node.ID
		var currentVal float64

		if r.stateStore != nil {
			valBytes, err := r.stateStore.Get(ctx, "node:"+key)
			if err == nil && valBytes != nil {
				currentVal, _ = strconv.ParseFloat(string(valBytes), 64)
			}
		} else {
			r.nodeStatesMu.Lock()
			state, ok := r.nodeStates[key]
			fmt.Printf("DEBUG: stateful node key=%s, ok=%v, state=%v\n", key, ok, state)
			if !ok {
				state = float64(0)
			}
			currentVal = state.(float64)
			r.nodeStatesMu.Unlock()
		}

		switch op {
		case "count":
			currentVal++
		case "sum":
			val := getMsgValByPath(currentMsg, field)
			if v, ok := toFloat64(val); ok {
				currentVal += v
			}
		}

		fmt.Printf("DEBUG: stateful node key=%s, new currentVal=%v\n", key, currentVal)

		if r.stateStore != nil {
			_ = r.stateStore.Set(ctx, "node:"+key, []byte(fmt.Sprintf("%f", currentVal)))
		} else {
			r.nodeStatesMu.Lock()
			r.nodeStates[key] = currentVal
			r.nodeStatesMu.Unlock()
		}

		span.SetAttributes(attribute.Float64("current_val", currentVal))

		modifiedMsg := currentMsg.Clone()
		dm, isDefault := modifiedMsg.(*message.DefaultMessage)
		data = modifiedMsg.Data()

		if isDefault {
			dm.SetData(outputField, currentVal)
		} else {
			setValByPath(data, outputField, currentVal)
		}
		return modifiedMsg, "", nil

	case "merge":
		// Merge is largely handled by the router which combines messages
		// Here we just ensure data is in sync
		return currentMsg, "", nil

	case "sink", "source":
		return currentMsg, "", nil
	default:
		return currentMsg, "", nil
	}
}

func (r *Registry) TestWorkflow(ctx context.Context, wf storage.Workflow, msg hermod.Message) ([]WorkflowStepResult, error) {
	if err := r.ValidateWorkflow(ctx, wf); err != nil {
		return nil, err
	}

	var steps []WorkflowStepResult
	adj := make(map[string][]string)
	inDegree := make(map[string]int)
	for _, edge := range wf.Edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
		inDegree[edge.TargetID]++
	}

	// Map edges to labels for easy lookup
	edgeLabels := make(map[string]string)
	for _, edge := range wf.Edges {
		if l, ok := edge.Config["label"].(string); ok && l != "" {
			edgeLabels[edge.SourceID+":"+edge.TargetID] = l
		}
	}

	// Find Source nodes
	var sourceNodes []*storage.WorkflowNode
	for i, node := range wf.Nodes {
		if node.Type == "source" {
			sourceNodes = append(sourceNodes, &wf.Nodes[i])
		}
	}

	if len(sourceNodes) == 0 {
		return nil, fmt.Errorf("no source node found")
	}

	currentMessages := make(map[string]hermod.Message)
	for _, sn := range sourceNodes {
		currentMessages[sn.ID] = msg.Clone()
	}

	receivedCount := make(map[string]int)

	for _, sn := range sourceNodes {
		steps = append(steps, WorkflowStepResult{
			NodeID:   sn.ID,
			NodeType: "source",
			Payload:  msg.Data(),
			Metadata: msg.Metadata(),
		})
	}

	visited := make(map[string]bool)
	queue := []string{}
	for _, sn := range sourceNodes {
		queue = append(queue, sn.ID)
	}

	for len(queue) > 0 {
		currID := queue[0]
		queue = queue[1:]

		if visited[currID] {
			continue
		}
		visited[currID] = true

		currMsg := currentMessages[currID]

		// Run current node if it's not the source (already handled)
		currNode := findNodeByID(wf.Nodes, currID)
		var currBranch string
		if currNode.Type != "source" {
			var err error
			currMsg, currBranch, err = r.runWorkflowNode("test", currNode, currMsg)
			if err != nil {
				steps = append(steps, WorkflowStepResult{
					NodeID:   currID,
					NodeType: currNode.Type,
					Error:    err.Error(),
				})
				// Even if error, we should probably continue to satisfy in-degrees
				// but let's keep it simple for now as error usually stops the flow.
			}
			if currMsg == nil {
				steps = append(steps, WorkflowStepResult{
					NodeID:   currID,
					NodeType: currNode.Type,
					Filtered: true,
					Branch:   currBranch,
				})
				// Propagate nil msg below
			} else {
				// Update step with output
				found := false
				for i := range steps {
					if steps[i].NodeID == currID {
						steps[i].Payload = currMsg.Data()
						steps[i].Metadata = currMsg.Metadata()
						steps[i].Branch = currBranch
						found = true
						break
					}
				}
				if !found {
					steps = append(steps, WorkflowStepResult{
						NodeID:   currID,
						NodeType: currNode.Type,
						Payload:  currMsg.Data(),
						Metadata: currMsg.Metadata(),
						Branch:   currBranch,
					})
				}
			}
		}

		for _, targetID := range adj[currID] {
			edgeLabel := edgeLabels[currID+":"+targetID]

			match := true
			if currNode.Type == "condition" || currNode.Type == "switch" {
				if edgeLabel != "" && edgeLabel != currBranch {
					match = false
				}
			}

			receivedCount[targetID]++

			if match && currMsg != nil {
				strategy := ""
				targetNode := findNodeByID(wf.Nodes, targetID)
				if targetNode != nil {
					strategy, _ = targetNode.Config["strategy"].(string)
				}
				if currentMessages[targetID] == nil {
					currentMessages[targetID] = currMsg.Clone()
				} else {
					// Merge
					r.mergeData(currentMessages[targetID].Data(), currMsg.Data(), strategy)
					if dm, ok := currentMessages[targetID].(interface{ ClearCachedPayload() }); ok {
						dm.ClearCachedPayload()
					}
				}
			}

			if receivedCount[targetID] == inDegree[targetID] {
				queue = append(queue, targetID)
			}
		}
	}

	return steps, nil
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
			now := time.Now()
			for _, w := range workers {
				if w.LastSeen != nil && now.Sub(*w.LastSeen) < 2*time.Minute {
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

func (r *Registry) ValidateWorkflow(ctx context.Context, wf storage.Workflow) error {
	// 1. Check if all nodes are configured and exist
	for _, node := range wf.Nodes {
		if node.Type == "source" {
			if node.RefID == "" || node.RefID == "new" {
				return fmt.Errorf("source node %s is not configured", node.ID)
			}
			if r.storage != nil {
				if _, err := r.storage.GetSource(ctx, node.RefID); err != nil {
					return fmt.Errorf("source node %s refers to missing source %s: %w", node.ID, node.RefID, err)
				}
			}
		} else if node.Type == "sink" {
			if node.RefID == "" || node.RefID == "new" {
				return fmt.Errorf("sink node %s is not configured", node.ID)
			}
			if r.storage != nil {
				if _, err := r.storage.GetSink(ctx, node.RefID); err != nil {
					return fmt.Errorf("sink node %s refers to missing sink %s: %w", node.ID, node.RefID, err)
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
						log.Printf("Registry Warning: Workflow %s has PrioritizeDLQ enabled, but sink %s does not have idempotency enabled. It is highly recommended to enable idempotency to avoid side effects during re-processing.", wf.ID, snk.ID)
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
					}
				}
			}
		}
	}

	if lastErr != nil {
		return lastErr
	}
	if !triggered {
		// Provide a clearer message: snapshots require the source to be part of a running workflow engine.
		// This often happens when trying to trigger a snapshot before starting the workflow.
		return fmt.Errorf("source %s is not currently running in any engine. Start the workflow that uses this source and try again", sourceID)
	}
	return nil
}

func (r *Registry) StartWorkflow(id string, wf storage.Workflow) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.engines[id]; ok {
		return fmt.Errorf("workflow %s already running", id)
	}

	ctx := context.Background()
	if r.storage == nil {
		log.Printf("DEBUG: r.storage is NIL for workflow %s", id)
	}
	if err := r.ValidateWorkflow(ctx, wf); err != nil {
		return fmt.Errorf("workflow validation failed: %w", err)
	}

	// Load node states for stateful transformations
	nodeStates, err := r.storage.GetNodeStates(ctx, id)
	if err == nil {
		r.nodeStatesMu.Lock()
		for nodeID, state := range nodeStates {
			r.nodeStates[id+":"+nodeID] = state
		}
		r.nodeStatesMu.Unlock()
	}

	// 1. Find Source nodes
	var sourceNodes []*storage.WorkflowNode
	for i, node := range wf.Nodes {
		if node.Type == "source" {
			sourceNodes = append(sourceNodes, &wf.Nodes[i])
		}
	}

	if len(sourceNodes) == 0 {
		return fmt.Errorf("workflow must have at least one source node")
	}

	var srcConfigs []SourceConfig
	var subSources []*subSource
	for _, sn := range sourceNodes {
		dbSrc, err := r.storage.GetSource(ctx, sn.RefID)
		if err != nil {
			// Close already created sources
			for _, ss := range subSources {
				ss.source.Close()
			}
			return fmt.Errorf("failed to get source %s: %w", sn.RefID, err)
		}

		srcCfg := SourceConfig{
			ID:     dbSrc.ID,
			Type:   dbSrc.Type,
			Config: dbSrc.Config,
			State:  dbSrc.State,
		}

		if val, ok := dbSrc.Config["reconnect_intervals"]; ok && val != "" {
			parts := strings.Split(val, ",")
			var intervals []time.Duration
			for _, p := range parts {
				if d, err := parseDuration(strings.TrimSpace(p)); err == nil {
					intervals = append(intervals, d)
				}
			}
			if len(intervals) > 0 {
				srcCfg.ReconnectIntervals = intervals
			}
		} else if val, ok := dbSrc.Config["reconnect_interval"]; ok && val != "" {
			// Backward compatibility: migrate reconnect_interval to intervals list
			if d, err := parseDuration(val); err == nil {
				srcCfg.ReconnectIntervals = []time.Duration{d}
			}
		}

		srcConfigs = append(srcConfigs, srcCfg)

		src, err := r.createSourceInternal(srcCfg)
		if err != nil {
			for _, ss := range subSources {
				ss.source.Close()
			}
			return err
		}
		subSources = append(subSources, &subSource{nodeID: sn.ID, sourceID: sn.RefID, source: src})
	}

	ms := &multiSource{
		sources: subSources,
		msgChan: make(chan hermod.Message, 100),
		errChan: make(chan error, len(subSources)),
	}

	// 2. Map nodes and edges to build the pipeline
	// Find Sinks
	adj := make(map[string][]string)
	for _, edge := range wf.Edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
	}

	var sinks []hermod.Sink
	var snkConfigs []SinkConfig
	sinkNodeToIndex := make(map[string]int)

	// BFS/DFS from all sources to find all sinks
	queue := []string{}
	visited := make(map[string]bool)
	for _, sn := range sourceNodes {
		queue = append(queue, sn.ID)
		visited[sn.ID] = true
	}

	for len(queue) > 0 {
		currID := queue[0]
		queue = queue[1:]

		node := findNodeByID(wf.Nodes, currID)
		if node == nil {
			continue
		}

		if node.Type == "sink" {
			dbSnk, err := r.storage.GetSink(context.Background(), node.RefID)
			if err != nil {
				for _, s := range sinks {
					s.Close()
				}
				ms.Close()
				return fmt.Errorf("failed to get sink %s: %w", node.RefID, err)
			}
			snkCfg := SinkConfig{
				ID:     dbSnk.ID,
				Type:   dbSnk.Type,
				Config: dbSnk.Config,
			}
			snk, err := r.createSinkInternal(snkCfg)
			if err != nil {
				for _, s := range sinks {
					s.Close()
				}
				ms.Close()
				return err
			}
			sinkNodeToIndex[node.ID] = len(sinks)
			sinks = append(sinks, snk)
			snkConfigs = append(snkConfigs, snkCfg)
		}

		for _, nextID := range adj[currID] {
			if !visited[nextID] {
				visited[nextID] = true
				queue = append(queue, nextID)
			}
		}
	}

	if len(sinks) == 0 {
		ms.Close()
		return fmt.Errorf("workflow must have at least one sink node reachable from sources")
	}

	// Buffer selection: prefer env-configured combined buffer when requested.
	// Falls back to ring buffer if unspecified.
	var buf hermod.Producer
	bufType := strings.ToLower(strings.TrimSpace(os.Getenv("HERMOD_BUFFER_TYPE")))
	switch bufType {
	case "combined_buffer", "combined":
		ringCap := 1000
		if v := strings.TrimSpace(os.Getenv("HERMOD_BUFFER_RING_CAP")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				ringCap = n
			}
		}
		fileDir := strings.TrimSpace(os.Getenv("HERMOD_BUFFER_DIR"))
		fileSize := 0
		if v := strings.TrimSpace(os.Getenv("HERMOD_FILEBUFFER_SIZE")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				fileSize = n
			}
		}

		compAlgo := compression.Algorithm(strings.ToLower(strings.TrimSpace(os.Getenv("HERMOD_BUFFER_COMPRESSION"))))
		compressor, _ := compression.NewCompressor(compAlgo)

		cb, err := buffer.NewCombinedBuffer(ringCap, fileDir, fileSize, &buffer.CombinedOptions{
			Compressor: compressor,
		})
		if err != nil {
			log.Printf("Registry: failed to create CombinedBuffer, falling back to ring: %v", err)
			buf = buffer.NewRingBuffer(ringCap)
		} else {
			buf = cb
		}
	case "file_buffer", "file":
		// Allow opting into file-only buffer via env
		fileDir := strings.TrimSpace(os.Getenv("HERMOD_BUFFER_DIR"))
		if fileDir == "" {
			fileDir = ".hermod-buffer"
		}
		fileSize := 0
		if v := strings.TrimSpace(os.Getenv("HERMOD_FILEBUFFER_SIZE")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				fileSize = n
			}
		}

		compAlgo := compression.Algorithm(strings.ToLower(strings.TrimSpace(os.Getenv("HERMOD_BUFFER_COMPRESSION"))))
		compressor, _ := compression.NewCompressor(compAlgo)

		fb, err := buffer.NewFileBufferWithCompressor(fileDir, fileSize, compressor)
		if err != nil {
			log.Printf("Registry: failed to create FileBuffer, falling back to ring: %v", err)
			buf = buffer.NewRingBuffer(1000)
		} else {
			buf = fb
		}
	default:
		buf = buffer.NewRingBuffer(1000)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	eng := pkgengine.NewEngine(ms, sinks, buf)
	eng.SetConfig(r.config)

	// Apply workflow level overrides
	engCfg := r.config
	if wf.MaxRetries > 0 {
		engCfg.MaxRetries = wf.MaxRetries
	}
	if wf.RetryInterval != "" {
		if d, err := parseDuration(wf.RetryInterval); err == nil {
			engCfg.RetryInterval = d
		}
	}
	if wf.ReconnectInterval != "" {
		if d, err := parseDuration(wf.ReconnectInterval); err == nil {
			engCfg.ReconnectInterval = d
		}
	}
	engCfg.PrioritizeDLQ = wf.PrioritizeDLQ
	engCfg.DryRun = wf.DryRun
	eng.SetConfig(engCfg)

	// Set Dead Letter Sink if configured
	if wf.DeadLetterSinkID != "" {
		dbDls, err := r.storage.GetSink(ctx, wf.DeadLetterSinkID)
		if err == nil {
			dlsCfg := SinkConfig{
				ID:     dbDls.ID,
				Type:   dbDls.Type,
				Config: dbDls.Config,
			}
			dls, err := r.createSinkInternal(dlsCfg)
			if err == nil {
				eng.SetDeadLetterSink(dls)
			} else {
				log.Printf("Registry: failed to create dead letter sink %s: %v", wf.DeadLetterSinkID, err)
			}
		} else {
			log.Printf("Registry: failed to get dead letter sink %s: %v", wf.DeadLetterSinkID, err)
		}
	}

	// Set source config for engine reconnect loop
	if len(srcConfigs) > 0 {
		eng.SetSourceConfig(pkgengine.SourceConfig{
			ReconnectIntervals: srcConfigs[0].ReconnectIntervals,
		})
	}

	// Pre-map nodes and edges for performance
	nodeMap := make(map[string]*storage.WorkflowNode)
	for i := range wf.Nodes {
		nodeMap[wf.Nodes[i].ID] = &wf.Nodes[i]
	}

	edgeLabels := make(map[string]string)
	inDegree := make(map[string]int)
	for _, edge := range wf.Edges {
		if l, ok := edge.Config["label"].(string); ok && l != "" {
			edgeLabels[edge.SourceID+":"+edge.TargetID] = l
		}
		inDegree[edge.TargetID]++
	}

	eng.SetTraceRecorder(r)
	engCfg.TraceSampleRate = wf.TraceSampleRate

	// Set Workflow Router
	eng.SetRouter(func(ctx context.Context, msg hermod.Message) ([]pkgengine.RoutedMessage, error) {
		var routed []pkgengine.RoutedMessage

		sourceNodeID := msg.Metadata()["_source_node_id"]
		if sourceNodeID == "" && len(sourceNodes) > 0 {
			sourceNodeID = sourceNodes[0].ID
		}

		// Map for current messages at each node
		currentMessages := make(map[string]hermod.Message)
		currentMessages[sourceNodeID] = msg

		receivedCount := make(map[string]int)
		q := []string{sourceNodeID}
		vis := make(map[string]bool)

		for len(q) > 0 {
			currID := q[0]
			q = q[1:]

			if vis[currID] {
				continue
			}
			vis[currID] = true

			currMsg := currentMessages[currID]

			// Run current node if not source
			currNode := nodeMap[currID]
			if currNode == nil {
				continue
			}

			var currBranch string
			if currNode.Type != "source" {
				var err error
				start := time.Now()
				inputMsg := currMsg
				currMsg, currBranch, err = r.runWorkflowNode(id, currNode, inputMsg)
				if currMsg != nil {
					eng.RecordTraceStep(ctx, currMsg, currNode.ID, start, err)
				} else {
					eng.RecordTraceStep(ctx, inputMsg, currNode.ID, start, err)
				}

				if err != nil {
					pkgengine.WorkflowNodeErrors.WithLabelValues(id, currNode.ID, currNode.Type).Inc()
					eng.UpdateNodeErrorMetric(currNode.ID, 1)
					msgID := ""
					if currMsg != nil {
						msgID = currMsg.ID()
					}
					r.broadcastLogWithData(id, "ERROR", fmt.Sprintf("Node %s (%s) error: %v", currNode.ID, currNode.Type, err), msgID)
					currBranch = "error"
				}

				if currMsg != nil {
					// Record metrics and sample
					pkgengine.WorkflowNodeProcessed.WithLabelValues(id, currNode.ID, currNode.Type).Inc()
					eng.UpdateNodeMetric(currNode.ID, 1)
					eng.UpdateNodeSample(currNode.ID, currMsg.Data())
				}
				// currMsg could be nil if filtered
			} else {
				// Source node
				eng.RecordTraceStep(ctx, currMsg, currNode.ID, time.Now(), nil)
				pkgengine.WorkflowNodeProcessed.WithLabelValues(id, currNode.ID, currNode.Type).Inc()
				eng.UpdateNodeMetric(currNode.ID, 1)
				eng.UpdateNodeSample(currNode.ID, currMsg.Data())
			}

			if currNode.Type == "sink" && currMsg != nil {
				if idx, ok := sinkNodeToIndex[currID]; ok {
					routed = append(routed, pkgengine.RoutedMessage{
						SinkIndex: idx,
						Message:   currMsg,
					})
				}
			}

			targets := adj[currID]
			for _, targetID := range targets {
				// Check edge label for conditions/switch
				edgeLabel := edgeLabels[currID+":"+targetID]

				match := true
				if currBranch == "error" {
					if edgeLabel != "error" {
						match = false
					}
				} else {
					if edgeLabel == "error" {
						match = false
					} else if currNode.Type == "condition" || currNode.Type == "switch" {
						if edgeLabel != "" && edgeLabel != currBranch {
							match = false
						}
					}
				}

				receivedCount[targetID]++

				if match && currMsg != nil {
					strategy := ""
					targetNode := nodeMap[targetID]
					if targetNode != nil {
						strategy, _ = targetNode.Config["strategy"].(string)
					}

					if currentMessages[targetID] == nil {
						currentMessages[targetID] = currMsg.Clone()
					} else {
						// Merge
						r.mergeData(currentMessages[targetID].Data(), currMsg.Data(), strategy)
						if dm, ok := currentMessages[targetID].(interface{ ClearCachedPayload() }); ok {
							dm.ClearCachedPayload()
						}
					}
				}

				if receivedCount[targetID] == inDegree[targetID] {
					q = append(q, targetID)
				}
			}
		}

		return routed, nil
	})

	// Per-source configuration
	sourceEngineCfg := pkgengine.SourceConfig{}
	for _, sn := range sourceNodes {
		dbSrc, _ := r.storage.GetSource(ctx, sn.RefID)

		val := dbSrc.Config["reconnect_intervals"]
		if val == "" {
			val = dbSrc.Config["reconnect_interval"]
		}

		if val != "" {
			parts := strings.Split(val, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if d, err := parseDuration(part); err == nil {
					sourceEngineCfg.ReconnectIntervals = append(sourceEngineCfg.ReconnectIntervals, d)
				}
			}
		}
	}
	eng.SetSourceConfig(sourceEngineCfg)

	// Set IDs
	sinkIDs := make([]string, len(snkConfigs))
	sinkTypes := make([]string, len(snkConfigs))
	pkgSnkConfigs := make([]pkgengine.SinkConfig, len(snkConfigs))

	for i, cfg := range snkConfigs {
		sinkIDs[i] = cfg.ID
		sinkTypes[i] = cfg.Type

		// Map to pkgengine.SinkConfig
		psc := pkgengine.SinkConfig{}
		if val, ok := cfg.Config["max_retries"]; ok && val != "" {
			if n, err := strconv.Atoi(val); err == nil {
				psc.MaxRetries = n
			}
		}
		if val, ok := cfg.Config["retry_interval"]; ok && val != "" {
			if d, err := parseDuration(val); err == nil {
				psc.RetryInterval = d
			}
		}
		if val, ok := cfg.Config["batch_size"]; ok && val != "" {
			if n, err := strconv.Atoi(val); err == nil {
				psc.BatchSize = n
			}
		}
		if val, ok := cfg.Config["batch_timeout"]; ok && val != "" {
			if d, err := parseDuration(val); err == nil {
				psc.BatchTimeout = d
			}
		}
		if val, ok := cfg.Config["circuit_threshold"]; ok && val != "" {
			if n, err := strconv.Atoi(val); err == nil {
				psc.CircuitBreakerThreshold = n
			}
		}
		if val, ok := cfg.Config["circuit_interval"]; ok && val != "" {
			if d, err := parseDuration(val); err == nil {
				psc.CircuitBreakerInterval = d
			}
		}
		if val, ok := cfg.Config["circuit_cool_off"]; ok && val != "" {
			if d, err := parseDuration(val); err == nil {
				psc.CircuitBreakerCoolDown = d
			}
		}
		if val, ok := cfg.Config["retry_intervals"]; ok && val != "" {
			parts := strings.Split(val, ",")
			for _, p := range parts {
				if d, err := parseDuration(strings.TrimSpace(p)); err == nil {
					psc.RetryIntervals = append(psc.RetryIntervals, d)
				}
			}
		}
		if val, ok := cfg.Config["backpressure_strategy"]; ok && val != "" {
			psc.BackpressureStrategy = pkgengine.BackpressureStrategy(val)
		}
		if val, ok := cfg.Config["backpressure_buffer"]; ok && val != "" {
			if n, err := strconv.Atoi(val); err == nil {
				psc.BackpressureBuffer = n
			}
		}
		if val, ok := cfg.Config["sampling_rate"]; ok && val != "" {
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				psc.SamplingRate = f
			}
		}
		if val, ok := cfg.Config["spill_path"]; ok && val != "" {
			psc.SpillPath = val
		}
		if val, ok := cfg.Config["spill_max_size"]; ok && val != "" {
			if n, err := strconv.Atoi(val); err == nil {
				psc.SpillMaxSize = n
			}
		}
		pkgSnkConfigs[i] = psc
	}
	eng.SetIDs(id, "multi", sinkIDs)
	eng.SetSinkTypes(sinkTypes)
	eng.SetSinkConfigs(pkgSnkConfigs)

	if wf.Schema != "" && wf.SchemaType != "" {
		var v schema.Validator
		var err error

		if strings.HasPrefix(wf.Schema, "registry:") {
			schemaName := strings.TrimPrefix(wf.Schema, "registry:")
			v, _, err = r.schemaRegistry.GetLatestValidator(ctx, schemaName)
		} else {
			v, err = schema.NewValidator(schema.SchemaConfig{
				Type:   schema.SchemaType(wf.SchemaType),
				Schema: wf.Schema,
			})
		}

		if err != nil {
			r.broadcastLog(id, "ERROR", fmt.Sprintf("Failed to initialize schema validator: %v", err))
		} else {
			eng.SetValidator(v)
			r.broadcastLog(id, "INFO", fmt.Sprintf("Schema validation enabled (Type: %s)", wf.SchemaType))
		}
	}

	if r.storage != nil {
		eng.SetLogger(NewDatabaseLogger(ctx, r, id))
		eng.SetOnStatusChange(func(update pkgengine.StatusUpdate) {
			dbCtx := context.Background()
			if workflow, err := r.storage.GetWorkflow(dbCtx, id); err == nil {
				prevStatus := workflow.Status
				workflow.Status = update.EngineStatus
				_ = r.storage.UpdateWorkflow(dbCtx, workflow)

				// Update Source status if it changed
				if update.SourceID != "" {
					_ = r.storage.UpdateSourceStatus(dbCtx, update.SourceID, update.SourceStatus)
				}

				// Update Sink statuses if they changed
				for sinkID, status := range update.SinkStatuses {
					_ = r.storage.UpdateSinkStatus(dbCtx, sinkID, status)
				}

				// Notify on error status
				if strings.Contains(strings.ToLower(update.EngineStatus), "error") &&
					!strings.Contains(strings.ToLower(prevStatus), "error") &&
					r.notificationService != nil {
					r.notificationService.Notify(dbCtx, "Workflow Error",
						fmt.Sprintf("Workflow '%s' (ID: %s) entered error state: %s",
							workflow.Name, workflow.ID, update.EngineStatus), workflow)
				}
			}
			r.broadcastStatus(update)

			// Special handling for Circuit Breaker alerts
			if strings.Contains(strings.ToLower(update.EngineStatus), "circuit_breaker_open") && r.notificationService != nil {
				dbCtx := context.Background()
				if workflow, err := r.storage.GetWorkflow(dbCtx, id); err == nil {
					r.notificationService.Notify(dbCtx, "Circuit Breaker Alert",
						fmt.Sprintf("Circuit breaker opened for a sink in workflow '%s' (ID: %s)",
							workflow.Name, workflow.ID), workflow)
				}
			}

			// DLQ Threshold Alert
			if r.notificationService != nil && update.DeadLetterCount > 0 {
				dbCtx := context.Background()
				if workflow, err := r.storage.GetWorkflow(dbCtx, id); err == nil && workflow.DLQThreshold > 0 {
					if update.DeadLetterCount >= uint64(workflow.DLQThreshold) {
						r.notificationService.Notify(dbCtx, "DLQ Threshold Exceeded",
							fmt.Sprintf("Workflow '%s' (ID: %s) has %d messages in DLQ, exceeding threshold of %d",
								workflow.Name, workflow.ID, update.DeadLetterCount, workflow.DLQThreshold), workflow)
					}
				}
			}
		})

		// Set Checkpoint Handler to persist stateful transformation states
		eng.SetCheckpointHandler(func(ctx context.Context, sourceState map[string]string) error {
			// Persist source state if provided
			if sourceState != nil {
				// We need to find the source ID for this workflow.
				// In StartWorkflow, id is the workflow ID.
				// We can find the source node's RefID.
				for _, node := range wf.Nodes {
					if node.Type == "source" {
						if err := r.storage.UpdateSourceState(ctx, node.RefID, sourceState); err != nil {
							r.broadcastLog(id, "ERROR", fmt.Sprintf("Failed to persist source state: %v", err))
						} else if r.logger != nil {
							r.logger.Info("Persisted source state during checkpoint", "workflow_id", id, "source_id", node.RefID, "state", sourceState)
						}
						break
					}
				}
			} else if r.logger != nil {
				r.logger.Debug("No source state to persist during checkpoint", "workflow_id", id)
			}

			r.nodeStatesMu.Lock()
			defer r.nodeStatesMu.Unlock()

			prefix := id + ":"
			for key, state := range r.nodeStates {
				if strings.HasPrefix(key, prefix) {
					nodeID := strings.TrimPrefix(key, prefix)
					if err := r.storage.UpdateNodeState(ctx, id, nodeID, state); err != nil {
						return err
					}
				}
			}
			return nil
		})
	} else {
		eng.SetOnStatusChange(func(update pkgengine.StatusUpdate) {
			r.broadcastStatus(update)
		})
	}

	r.engines[id] = &activeEngine{
		engine:     eng,
		cancel:     cancel,
		done:       done,
		srcConfigs: srcConfigs,
		snkConfigs: snkConfigs,
		isWorkflow: true,
		workflow:   wf,
	}

	if r.optimizer != nil {
		r.optimizer.Register(id, eng)
	}

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Printf("Workflow %s panicked: %v\n", id, rec)
				debug.PrintStack()
			}
			r.mu.Lock()
			delete(r.engines, id)
			r.mu.Unlock()
			if r.optimizer != nil {
				r.optimizer.Unregister(id)
			}
			close(done)
		}()

		err := eng.Start(ctx)

		// Check if it was cancelled by us
		select {
		case <-ctx.Done():
			// Cancelled via StopEngine
			ms.Close()
			for _, snk := range sinks {
				snk.Close()
			}
			return
		default:
			// Stopped by itself or failed to start
		}

		if err != nil {
			fmt.Printf("Workflow %s failed: %v\n", id, err)
			r.broadcastLog(id, "ERROR", fmt.Sprintf("Workflow failed: %v", err))
		} else {
			fmt.Printf("Workflow %s stopped gracefully\n", id)
			r.broadcastLog(id, "INFO", "Workflow stopped gracefully")
		}

		if r.storage != nil {
			dbCtx := context.Background()
			if workflow, errGet := r.storage.GetWorkflow(dbCtx, id); errGet == nil {
				if err != nil {
					workflow.Status = "Error: " + err.Error()
					// Keep Active = true so reconciliation restarts it
					log.Printf("Workflow %s failed: %v. Keeping active for reconciliation.", id, err)
				} else {
					workflow.Active = false
					workflow.Status = "Completed"
					log.Printf("Workflow %s completed successfully.", id)

					// Update source and sinks only if we are deactivating
					for _, node := range workflow.Nodes {
						if node.Type == "source" {
							if !r.IsResourceInUse(dbCtx, node.RefID, id, true) {
								_ = r.storage.UpdateSourceStatus(dbCtx, node.RefID, "")
							}
						} else if node.Type == "sink" {
							if !r.IsResourceInUse(dbCtx, node.RefID, id, false) {
								_ = r.storage.UpdateSinkStatus(dbCtx, node.RefID, "")
							}
						}
					}
				}
				_ = r.storage.UpdateWorkflow(dbCtx, workflow)
			}
		}

		ms.Close()
		for _, snk := range sinks {
			snk.Close()
		}
	}()

	return nil
}

func findNodeByID(nodes []storage.WorkflowNode, id string) *storage.WorkflowNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func (r *Registry) StopAll() {
	r.mu.Lock()
	ids := make([]string, 0, len(r.engines))
	for id := range r.engines {
		ids = append(ids, id)
	}
	r.mu.Unlock()

	for _, id := range ids {
		_ = r.stopEngine(id, false)
	}
}

func (r *Registry) StopEngine(id string) error {
	return r.stopEngine(id, true)
}

func (r *Registry) DrainWorkflowDLQ(id string) error {
	r.mu.Lock()
	ae, ok := r.engines[id]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("workflow engine %s not running on this worker", id)
	}

	return ae.engine.DrainDLQ(context.Background())
}

func (r *Registry) StopEngineWithoutUpdate(id string) error {
	return r.stopEngine(id, false)
}

func (r *Registry) RebuildWorkflow(ctx context.Context, workflowID string, fromOffset int64) error {
	wf, err := r.storage.GetWorkflow(ctx, workflowID)
	if err != nil {
		return err
	}

	// 1. Find Event Store sink
	var eventStoreNode *storage.WorkflowNode
	var eventStoreSink *storage.Sink
	for i, node := range wf.Nodes {
		if node.Type == "sink" {
			snk, err := r.storage.GetSink(ctx, node.RefID)
			if err == nil && snk.Type == "eventstore" {
				eventStoreNode = &wf.Nodes[i]
				eventStoreSink = &snk
				break
			}
		}
	}

	if eventStoreNode == nil {
		return fmt.Errorf("no eventstore sink found in workflow %s", workflowID)
	}

	// 2. Prepare Sinks
	var sinks []hermod.Sink
	sinkNodeToIndex := make(map[string]int)
	nodeMap := make(map[string]*storage.WorkflowNode)
	adj := make(map[string][]string)

	for i := range wf.Nodes {
		nodeMap[wf.Nodes[i].ID] = &wf.Nodes[i]
	}
	for _, edge := range wf.Edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
	}

	for _, node := range wf.Nodes {
		if node.Type == "sink" && node.ID != eventStoreNode.ID {
			dbSnk, err := r.storage.GetSink(ctx, node.RefID)
			if err == nil {
				snk, err := r.createSinkInternal(SinkConfig{ID: dbSnk.ID, Type: dbSnk.Type, Config: dbSnk.Config})
				if err == nil {
					sinkNodeToIndex[node.ID] = len(sinks)
					sinks = append(sinks, snk)
				}
			}
		}
	}
	defer func() {
		for _, s := range sinks {
			s.Close()
		}
	}()

	// 3. Create Event Store source
	srcCfg := SourceConfig{
		ID:     eventStoreSink.ID,
		Type:   "eventstore",
		Config: eventStoreSink.Config,
	}
	if srcCfg.Config == nil {
		srcCfg.Config = make(map[string]string)
	}
	srcCfg.Config["from_offset"] = fmt.Sprintf("%d", fromOffset)

	src, err := r.createSourceInternal(srcCfg)
	if err != nil {
		return err
	}
	defer src.Close()

	// 4. Replay loop
	for {
		msg, err := src.Read(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "no more events") {
				break
			}
			return err
		}

		// Find source nodes and start traversal
		for _, node := range wf.Nodes {
			if node.Type == "source" {
				for _, targetID := range adj[node.ID] {
					targetNode := nodeMap[targetID]
					if targetNode != nil {
						r.runWorkflowNodeFromReplay(workflowID, targetNode, msg, eventStoreNode.ID, wf, nodeMap, adj, sinks, sinkNodeToIndex)
					}
				}
			}
		}
	}
	return nil
}

func (r *Registry) runWorkflowNodeFromReplay(workflowID string, node *storage.WorkflowNode, msg hermod.Message, skipNodeID string, wf storage.Workflow, nodeMap map[string]*storage.WorkflowNode, adj map[string][]string, sinks []hermod.Sink, sinkNodeToIndex map[string]int) {
	if node.ID == skipNodeID {
		return
	}

	// Clone message to avoid side effects between branches
	m := msg.Clone()

	processedMsg, branch, err := r.runWorkflowNode(workflowID, node, m)
	if err != nil {
		r.broadcastLog(workflowID, "error", fmt.Sprintf("Node %s error: %v", node.ID, err))
		return
	}

	if processedMsg == nil {
		return
	}

	if node.Type == "sink" {
		idx, ok := sinkNodeToIndex[node.ID]
		if ok && idx < len(sinks) {
			sinks[idx].Write(context.Background(), processedMsg)
		}
		return
	}

	// Determine next nodes based on branch
	var targets []string
	if branch != "" {
		// Find edges with this label
		for _, edge := range wf.Edges {
			if edge.SourceID == node.ID && edge.Config["label"] == branch {
				targets = append(targets, edge.TargetID)
			}
		}
	} else {
		targets = adj[node.ID]
	}

	for _, targetID := range targets {
		targetNode := nodeMap[targetID]
		if targetNode != nil {
			r.runWorkflowNodeFromReplay(workflowID, targetNode, processedMsg, skipNodeID, wf, nodeMap, adj, sinks, sinkNodeToIndex)
		}
	}
}

func (r *Registry) stopEngine(id string, updateStorage bool) error {
	r.mu.Lock()
	ae, ok := r.engines[id]
	if !ok {
		r.mu.Unlock()
		return nil // Engine not running, no error
	}

	ae.cancel()
	// Release lock to allow other operations while waiting for engine to stop
	r.mu.Unlock()

	// Wait for engine to gracefully shutdown
	select {
	case <-ae.done:
	case <-time.After(30 * time.Second):
		fmt.Printf("Warning: Engine %s stop timeout\n", id)
		// Attempt a hard stop to ensure the workflow actually halts
		if ae.engine != nil {
			ae.engine.HardStop()
		}
		// Give a short grace period after hard stop
		select {
		case <-ae.done:
		case <-time.After(2 * time.Second):
		}
	}

	if updateStorage && r.storage != nil {
		ctx := context.Background()
		if workflow, err := r.storage.GetWorkflow(ctx, id); err == nil {
			workflow.Active = false
			workflow.Status = ""
			_ = r.storage.UpdateWorkflow(ctx, workflow)

			// Update source and sinks
			for _, node := range workflow.Nodes {
				if node.Type == "source" {
					_ = r.storage.UpdateSourceStatus(ctx, node.RefID, "")
				} else if node.Type == "sink" {
					_ = r.storage.UpdateSinkStatus(ctx, node.RefID, "")
				}
			}
		}
	}

	r.mu.Lock()
	delete(r.engines, id)
	r.mu.Unlock()

	return nil
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

	host := cfg["host"]
	port := cfg["port"]
	user := cfg["user"]
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
