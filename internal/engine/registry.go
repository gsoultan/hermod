package engine

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/buffer"
	"github.com/user/hermod/pkg/engine"
	"github.com/user/hermod/pkg/formatter/json"
	"github.com/user/hermod/pkg/sink/file"
	"github.com/user/hermod/pkg/sink/http"
	sinkkafka "github.com/user/hermod/pkg/sink/kafka"
	"github.com/user/hermod/pkg/sink/kinesis"
	sinkmongodb "github.com/user/hermod/pkg/sink/mongodb"
	sinkmysql "github.com/user/hermod/pkg/sink/mysql"
	sinknats "github.com/user/hermod/pkg/sink/nats"
	sinkpostgres "github.com/user/hermod/pkg/sink/postgres"
	"github.com/user/hermod/pkg/sink/pubsub"
	"github.com/user/hermod/pkg/sink/pulsar"
	sinkrabbitmq "github.com/user/hermod/pkg/sink/rabbitmq"
	sinkredis "github.com/user/hermod/pkg/sink/redis"
	"github.com/user/hermod/pkg/sink/smtp"
	"github.com/user/hermod/pkg/sink/stdout"
	"github.com/user/hermod/pkg/sink/telegram"
	sourcecassandra "github.com/user/hermod/pkg/source/cassandra"
	sourceclickhouse "github.com/user/hermod/pkg/source/clickhouse"
	sourcecsv "github.com/user/hermod/pkg/source/csv"
	"github.com/user/hermod/pkg/source/db2"
	sourcekafka "github.com/user/hermod/pkg/source/kafka"
	"github.com/user/hermod/pkg/source/mariadb"
	sourcemongodb "github.com/user/hermod/pkg/source/mongodb"
	"github.com/user/hermod/pkg/source/mssql"
	"github.com/user/hermod/pkg/source/mysql"
	sourcenats "github.com/user/hermod/pkg/source/nats"
	"github.com/user/hermod/pkg/source/oracle"
	sourcepostgres "github.com/user/hermod/pkg/source/postgres"
	sourcerabbitmq "github.com/user/hermod/pkg/source/rabbitmq"
	sourceredis "github.com/user/hermod/pkg/source/redis"
	"github.com/user/hermod/pkg/source/scylladb"
	sourcesqlite "github.com/user/hermod/pkg/source/sqlite"
	"github.com/user/hermod/pkg/source/yugabyte"
	"github.com/user/hermod/pkg/transformer"
)

type Registry struct {
	engines map[string]*activeEngine
	mu      sync.Mutex
	storage storage.Storage
	config  engine.Config

	statusSubs          map[chan engine.StatusUpdate]bool
	dashboardSubs       map[chan DashboardStats]bool
	statusSubsMu        sync.RWMutex
	lastDashboardUpdate time.Time

	notificationService *notification.Service
}

type activeEngine struct {
	engine               *engine.Engine
	cancel               context.CancelFunc
	done                 <-chan struct{}
	srcConfig            SourceConfig
	snkConfigs           []SinkConfig
	transformations      []storage.Transformation
	transformationIDs    []string
	transformationGroups []storage.TransformationGroup
}

func NewRegistry(s storage.Storage) *Registry {
	ns := notification.NewService(s)
	if s != nil {
		ns.AddProvider(notification.NewUINotificationProvider(s))
		ns.AddProvider(notification.NewEmailNotificationProvider(s))
		ns.AddProvider(notification.NewTelegramNotificationProvider(s))
		ns.AddProvider(notification.NewSlackNotificationProvider(s))
		ns.AddProvider(notification.NewDiscordNotificationProvider(s))
		ns.AddProvider(notification.NewGenericWebhookProvider(s))
	}

	return &Registry{
		engines:             make(map[string]*activeEngine),
		storage:             s,
		config:              engine.DefaultConfig(),
		statusSubs:          make(map[chan engine.StatusUpdate]bool),
		dashboardSubs:       make(map[chan DashboardStats]bool),
		notificationService: ns,
	}
}

func (r *Registry) SubscribeStatus() chan engine.StatusUpdate {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	ch := make(chan engine.StatusUpdate, 100)
	r.statusSubs[ch] = true
	return ch
}

func (r *Registry) UnsubscribeStatus(ch chan engine.StatusUpdate) {
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

func (r *Registry) UnsubscribeDashboardStats(ch chan DashboardStats) {
	r.statusSubsMu.Lock()
	defer r.statusSubsMu.Unlock()
	delete(r.dashboardSubs, ch)
	close(ch)
}

func (r *Registry) broadcastStatus(update engine.StatusUpdate) {
	// Handle notifications for critical status changes
	if r.storage != nil && (strings.Contains(update.EngineStatus, "error") || strings.Contains(update.EngineStatus, "reconnecting")) {
		ctx := context.Background()
		if conn, err := r.storage.GetConnection(ctx, update.ConnectionID); err == nil {
			r.notificationService.Notify(ctx, "Connection Issue Detected",
				fmt.Sprintf("Connection '%s' (ID: %s) entered status: %s", conn.Name, conn.ID, update.EngineStatus),
				conn)
		}
	}

	r.statusSubsMu.Lock()
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
			r.statusSubsMu.Unlock()

			stats, _ := r.GetDashboardStats(context.Background())

			r.statusSubsMu.Lock()
			for ch := range r.dashboardSubs {
				select {
				case ch <- stats:
				default:
				}
			}
		}
	}
	r.statusSubsMu.Unlock()
}

func (r *Registry) SetConfig(cfg engine.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = cfg
}

func (r *Registry) TestSource(ctx context.Context, cfg SourceConfig) error {
	src, err := CreateSource(cfg)
	if err != nil {
		return err
	}
	defer src.Close()
	return src.Ping(ctx)
}

func (r *Registry) TestSink(ctx context.Context, cfg SinkConfig) error {
	if cfg.Type == "stdout" {
		return nil
	}
	snk, err := CreateSink(cfg)
	if err != nil {
		return err
	}
	defer snk.Close()
	return snk.Ping(ctx)
}

func (r *Registry) DiscoverDatabases(ctx context.Context, cfg SourceConfig) ([]string, error) {
	src, err := CreateSource(cfg)
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
	src, err := CreateSource(cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if d, ok := src.(hermod.Discoverer); ok {
		return d.DiscoverTables(ctx)
	}
	return nil, fmt.Errorf("source type %s does not support table discovery", cfg.Type)
}

func (r *Registry) SampleTable(ctx context.Context, cfg SourceConfig, table string) (hermod.Message, error) {
	src, err := CreateSource(cfg)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if s, ok := src.(hermod.Sampler); ok {
		return s.Sample(ctx, table)
	}
	return nil, fmt.Errorf("source type %s does not support sampling", cfg.Type)
}

func (r *Registry) TestTransformation(ctx context.Context, trans storage.Transformation, msg hermod.Message) (hermod.Message, error) {
	t, err := r.createTransformer(ctx, trans)
	if err != nil {
		return nil, err
	}
	defer t.Close()
	return t.Transform(ctx, msg)
}

func (r *Registry) TestTransformationPipeline(ctx context.Context, transformations []storage.Transformation, msg hermod.Message) ([]hermod.Message, error) {
	results := make([]hermod.Message, 0, len(transformations))
	currentMsg := msg

	for _, trans := range transformations {
		t, err := r.createTransformer(ctx, trans)
		if err != nil {
			return nil, err
		}

		nextMsg, err := t.Transform(ctx, currentMsg)
		t.Close()

		if err != nil {
			return nil, err
		}

		if nextMsg == nil {
			// Message was filtered out
			results = append(results, nil)
			break
		}

		// Clone the message because some transformers might reuse the same object
		currentMsg = nextMsg.Clone()
		results = append(results, currentMsg)
	}

	return results, nil
}

func (r *Registry) IsEngineRunning(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.engines[id]
	return ok
}

func (r *Registry) GetAllStatuses() []engine.StatusUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()

	statuses := make([]engine.StatusUpdate, 0, len(r.engines))
	for _, ae := range r.engines {
		statuses = append(statuses, ae.engine.GetStatus())
	}
	return statuses
}

type DashboardStats struct {
	ActiveSources     int    `json:"active_sources"`
	ActiveSinks       int    `json:"active_sinks"`
	ActiveConnections int    `json:"active_connections"`
	TotalProcessed    uint64 `json:"total_processed"`
}

func (r *Registry) GetDashboardStats(ctx context.Context) (DashboardStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stats := DashboardStats{
		ActiveConnections: len(r.engines),
	}

	activeSources := make(map[string]bool)
	activeSinks := make(map[string]bool)

	for _, ae := range r.engines {
		status := ae.engine.GetStatus()
		stats.TotalProcessed += status.ProcessedCount

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

func (r *Registry) GetEngineConfigs(id string) (SourceConfig, []SinkConfig, []storage.Transformation, []string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ae, ok := r.engines[id]
	if !ok {
		return SourceConfig{}, nil, nil, nil, false
	}
	return ae.srcConfig, ae.snkConfigs, ae.transformations, ae.transformationIDs, true
}

func (r *Registry) createTransformers(ctx context.Context, tConfigs []storage.Transformation, tIDs []string) (hermod.Transformer, error) {
	var ts []hermod.Transformer

	// First, add transformations from IDs
	for _, id := range tIDs {
		tcfg, err := r.storage.GetTransformation(ctx, id)
		if err != nil {
			return nil, err
		}
		t, err := r.createTransformer(ctx, tcfg)
		if err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}

	// Then, add inline transformations
	for _, tcfg := range tConfigs {
		t, err := r.createTransformer(ctx, tcfg)
		if err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}

	if len(ts) == 0 {
		return nil, nil
	}
	if len(ts) == 1 {
		return ts[0], nil
	}
	return transformer.NewChain(ts...), nil
}

func (r *Registry) createTransformer(ctx context.Context, cfg storage.Transformation) (hermod.Transformer, error) {
	var t hermod.Transformer
	var err error

	if cfg.Type == "pipeline" {
		var steps []hermod.Transformer
		for _, stepCfg := range cfg.Steps {
			step, err := r.createTransformer(ctx, stepCfg)
			if err != nil {
				return nil, err
			}
			steps = append(steps, step)
		}
		t = transformer.NewChain(steps...)
	} else {
		t, err = transformer.NewTransformer(cfg.Type, cfg.Config)
	}

	if err != nil {
		return nil, err
	}

	if cfg.OnFailure != "" && cfg.OnFailure != "fail" {
		t = transformer.NewRecoveryTransformer(t, cfg.OnFailure)
	}

	if cfg.ExecuteIf != "" {
		t = &transformer.ConditionalTransformer{
			Condition: cfg.ExecuteIf,
			Inner:     t,
		}
	}

	return t, nil
}

func (r *Registry) StartEngine(id string, srcCfg SourceConfig, snkConfigs []SinkConfig, transformations []storage.Transformation, transformationIDs []string, transformationGroups []storage.TransformationGroup) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.engines[id]; ok {
		return fmt.Errorf("engine %s already running", id)
	}

	src, err := CreateSource(srcCfg)
	if err != nil {
		return err
	}

	var sinks []hermod.Sink
	for _, snkCfg := range snkConfigs {
		snk, err := CreateSink(snkCfg)
		if err != nil {
			// Close already created sinks
			for _, s := range sinks {
				s.Close()
			}
			src.Close()
			return err
		}
		sinks = append(sinks, snk)
	}

	buf := buffer.NewRingBuffer(1000)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	eng := engine.NewEngine(src, sinks, buf)
	eng.SetConfig(r.config)

	// Per-source configuration
	sourceCfg := engine.SourceConfig{}
	if val, ok := srcCfg.Config["reconnect_interval"]; ok && val != "" {
		parts := strings.Split(val, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if d, err := parseDuration(part); err == nil {
				sourceCfg.ReconnectIntervals = append(sourceCfg.ReconnectIntervals, d)
			} else if i, err := strconv.Atoi(part); err == nil {
				sourceCfg.ReconnectIntervals = append(sourceCfg.ReconnectIntervals, time.Duration(i)*time.Second)
			}
		}
		if len(sourceCfg.ReconnectIntervals) > 0 {
			sourceCfg.ReconnectInterval = sourceCfg.ReconnectIntervals[0]
		}
	}
	eng.SetSourceConfig(sourceCfg)

	// Per-sink configurations
	sinkConfigs := make([]engine.SinkConfig, len(snkConfigs))
	for i, sc := range snkConfigs {
		if val, ok := sc.Config["max_retries"]; ok && val != "" {
			if r, err := strconv.Atoi(val); err == nil {
				sinkConfigs[i].MaxRetries = r
			}
		}
		if val, ok := sc.Config["retry_interval"]; ok && val != "" {
			parts := strings.Split(val, ",")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if d, err := parseDuration(part); err == nil {
					sinkConfigs[i].RetryIntervals = append(sinkConfigs[i].RetryIntervals, d)
				} else if i, err := strconv.Atoi(part); err == nil {
					sinkConfigs[i].RetryIntervals = append(sinkConfigs[i].RetryIntervals, time.Duration(i)*time.Millisecond)
				}
			}
			if len(sinkConfigs[i].RetryIntervals) > 0 {
				sinkConfigs[i].RetryInterval = sinkConfigs[i].RetryIntervals[0]
			}
		}
	}
	eng.SetSinkConfigs(sinkConfigs)

	if r.storage != nil {
		eng.SetLogger(NewDatabaseLogger(ctx, r.storage))
		eng.SetOnStatusChange(func(update engine.StatusUpdate) {
			// Update connection status in database
			dbCtx := context.Background()
			if conn, err := r.storage.GetConnection(dbCtx, id); err == nil {
				conn.Status = update.EngineStatus
				_ = r.storage.UpdateConnection(dbCtx, conn)
			}
			r.broadcastStatus(update)
		})
	} else {
		eng.SetOnStatusChange(func(update engine.StatusUpdate) {
			r.broadcastStatus(update)
		})
	}

	sinkIDs := make([]string, len(snkConfigs))
	sinkTypes := make([]string, len(snkConfigs))
	for i, cfg := range snkConfigs {
		sinkIDs[i] = cfg.ID
		sinkTypes[i] = cfg.Type
	}
	eng.SetIDs(id, srcCfg.ID, sinkIDs)
	eng.SetSinkTypes(sinkTypes)

	// Transformation Groups
	var engGroups []engine.TransformationGroup
	assignedSinks := make(map[string]bool)

	for _, sg := range transformationGroups {
		eg := engine.TransformationGroup{
			SinkIDs: sg.SinkIDs,
		}
		if len(sg.Transformations) > 0 || len(sg.TransformationIDs) > 0 {
			st, err := r.createTransformers(ctx, sg.Transformations, sg.TransformationIDs)
			if err != nil {
				src.Close()
				for _, s := range sinks {
					s.Close()
				}
				return fmt.Errorf("failed to create transformers for group: %w", err)
			}
			eg.Transformer = st
		}

		// Map SinkIDs to hermod.Sink
		for _, sid := range sg.SinkIDs {
			assignedSinks[sid] = true
			for i, snkID := range sinkIDs {
				if snkID == sid {
					eg.Sinks = append(eg.Sinks, sinks[i])
					break
				}
			}
		}
		engGroups = append(engGroups, eg)
	}

	// Add default group for unassigned sinks
	var defaultSinkIDs []string
	var defaultSinks []hermod.Sink
	for i, sid := range sinkIDs {
		if !assignedSinks[sid] {
			defaultSinkIDs = append(defaultSinkIDs, sid)
			defaultSinks = append(defaultSinks, sinks[i])
		}
	}
	if len(defaultSinks) > 0 {
		engGroups = append(engGroups, engine.TransformationGroup{
			SinkIDs: defaultSinkIDs,
			Sinks:   defaultSinks,
		})
	}
	eng.SetTransformationGroups(engGroups)

	// Transformations (Global)
	t, err := r.createTransformers(ctx, transformations, transformationIDs)
	if err != nil {
		src.Close()
		for _, s := range sinks {
			s.Close()
		}
		// Close transformers in groups
		for _, eg := range engGroups {
			if eg.Transformer != nil {
				eg.Transformer.Close()
			}
		}
		return fmt.Errorf("failed to create transformer: %w", err)
	}
	if t != nil {
		eng.SetTransformations(t)
	}

	r.engines[id] = &activeEngine{
		engine:               eng,
		cancel:               cancel,
		done:                 done,
		srcConfig:            srcCfg,
		snkConfigs:           snkConfigs,
		transformations:      transformations,
		transformationIDs:    transformationIDs,
		transformationGroups: transformationGroups,
	}

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Printf("Engine %s panicked: %v\n", id, rec)
			}
			r.mu.Lock()
			delete(r.engines, id)
			r.mu.Unlock()
			close(done)
		}()

		err := eng.Start(ctx)

		// Check if it was cancelled by us
		select {
		case <-ctx.Done():
			// Cancelled via StopEngine, don't update DB status
			// (StopEngine's caller handles it)
			src.Close()
			for _, snk := range sinks {
				snk.Close()
			}
			if t != nil {
				t.Close()
			}
			return
		default:
			// Stopped by itself (either nil or error)
		}

		if err != nil {
			fmt.Printf("Engine %s failed: %v\n", id, err)
		} else {
			fmt.Printf("Engine %s stopped gracefully\n", id)
		}

		if r.storage != nil {
			// Use Background context as the registry context might be cancelling
			ctx := context.Background()
			if conn, err := r.storage.GetConnection(ctx, id); err == nil {
				conn.Active = false
				conn.Status = ""
				_ = r.storage.UpdateConnection(ctx, conn)

				// Update source
				if src, err := r.storage.GetSource(ctx, conn.SourceID); err == nil {
					if !r.IsResourceInUse(ctx, conn.SourceID, conn.ID, true) {
						src.Active = false
						_ = r.storage.UpdateSource(ctx, src)
					}
				}

				// Update sinks
				for _, sinkID := range conn.SinkIDs {
					if snk, err := r.storage.GetSink(ctx, sinkID); err == nil {
						if !r.IsResourceInUse(ctx, sinkID, conn.ID, false) {
							snk.Active = false
							_ = r.storage.UpdateSink(ctx, snk)
						}
					}
				}
			}
		}

		src.Close()
		for _, snk := range sinks {
			snk.Close()
		}
		if t != nil {
			t.Close()
		}
	}()

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
		_ = r.StopEngine(id)
	}
}

func (r *Registry) StopEngine(id string) error {
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
	<-ae.done

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
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}

type SinkConfig struct {
	ID                string                   `json:"id"`
	Type              string                   `json:"type"`
	Config            map[string]string        `json:"config"`
	Transformations   []storage.Transformation `json:"transformations"`
	TransformationIDs []string                 `json:"transformation_ids"`
}

func (r *Registry) IsResourceInUse(ctx context.Context, resourceID string, excludeConnID string, isSource bool) bool {
	conns, _, err := r.storage.ListConnections(ctx, storage.CommonFilter{})
	if err != nil {
		return false
	}
	for _, c := range conns {
		if c.ID != excludeConnID && c.Active {
			if isSource {
				if c.SourceID == resourceID {
					return true
				}
			} else {
				for _, snkID := range c.SinkIDs {
					if snkID == resourceID {
						return true
					}
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

func CreateSource(cfg SourceConfig) (hermod.Source, error) {
	connString := BuildConnectionString(cfg.Config, cfg.Type)

	tables := []string{}
	if t, ok := cfg.Config["tables"]; ok && t != "" {
		tables = strings.Split(t, ",")
		for i, table := range tables {
			tables[i] = strings.TrimSpace(table)
		}
	}

	switch cfg.Type {
	case "postgres":
		return sourcepostgres.NewPostgresSource(
			connString,
			cfg.Config["slot_name"],
			cfg.Config["publication_name"],
			tables,
		), nil
	case "mssql":
		autoEnable := cfg.Config["auto_enable_cdc"] != "false"
		return mssql.NewMSSQLSource(connString, tables, autoEnable), nil
	case "mysql":
		return mysql.NewMySQLSource(connString), nil
	case "oracle":
		return oracle.NewOracleSource(connString), nil
	case "db2":
		return db2.NewDB2Source(connString), nil
	case "mongodb":
		uri := cfg.Config["uri"]
		if uri == "" {
			host := cfg.Config["host"]
			port := cfg.Config["port"]
			user := cfg.Config["user"]
			password := cfg.Config["password"]
			if user != "" && password != "" {
				uri = fmt.Sprintf("mongodb://%s:%s@%s:%s", url.QueryEscape(user), url.QueryEscape(password), host, port)
			} else {
				uri = fmt.Sprintf("mongodb://%s:%s", host, port)
			}
		}
		return sourcemongodb.NewMongoDBSource(uri, cfg.Config["database"], cfg.Config["collection"]), nil
	case "mariadb":
		return mariadb.NewMariaDBSource(connString), nil
	case "cassandra":
		hosts := []string{"localhost"}
		if h, ok := cfg.Config["hosts"]; ok && h != "" {
			hosts = strings.Split(h, ",")
		}
		return sourcecassandra.NewCassandraSource(hosts), nil
	case "yugabyte":
		return yugabyte.NewYugabyteSource(connString), nil
	case "scylladb":
		hosts := []string{"localhost"}
		if h, ok := cfg.Config["hosts"]; ok && h != "" {
			hosts = strings.Split(h, ",")
		}
		return scylladb.NewScyllaDBSource(hosts), nil
	case "clickhouse":
		return sourceclickhouse.NewClickHouseSource(connString), nil
	case "csv":
		delimiter := ','
		if d, ok := cfg.Config["delimiter"]; ok && d != "" {
			delimiter = rune(d[0])
		}
		hasHeader := cfg.Config["has_header"] == "true"
		return sourcecsv.NewCSVSource(cfg.Config["file_path"], delimiter, hasHeader), nil
	case "sqlite":
		return sourcesqlite.NewSQLiteSource(connString, tables), nil
	case "kafka":
		brokers := strings.Split(cfg.Config["brokers"], ",")
		return sourcekafka.NewKafkaSource(brokers, cfg.Config["topic"], cfg.Config["group_id"], cfg.Config["username"], cfg.Config["password"]), nil
	case "nats":
		return sourcenats.NewNatsJetStreamSource(cfg.Config["url"], cfg.Config["subject"], cfg.Config["queue"], cfg.Config["username"], cfg.Config["password"], cfg.Config["token"])
	case "redis":
		return sourceredis.NewRedisSource(cfg.Config["addr"], cfg.Config["password"], cfg.Config["stream"], cfg.Config["group"]), nil
	case "rabbitmq":
		return sourcerabbitmq.NewRabbitMQStreamSource(cfg.Config["url"], cfg.Config["stream_name"], cfg.Config["consumer_name"])
	case "rabbitmq_queue":
		return sourcerabbitmq.NewRabbitMQQueueSource(cfg.Config["url"], cfg.Config["queue_name"])
	default:
		return nil, fmt.Errorf("unsupported source type: %s", cfg.Type)
	}
}

func CreateSink(cfg SinkConfig) (hermod.Sink, error) {
	var fmttr hermod.Formatter
	format := cfg.Config["format"]
	if format == "payload" {
		f := json.NewJSONFormatter()
		f.SetMode(json.ModePayload)
		fmttr = f
	} else if format == "cdc" || format == "json" {
		fmttr = json.NewJSONFormatter()
	}

	switch cfg.Type {
	case "nats":
		return sinknats.NewNatsJetStreamSink(cfg.Config["url"], cfg.Config["subject"], cfg.Config["username"], cfg.Config["password"], cfg.Config["token"], fmttr)
	case "rabbitmq":
		return sinkrabbitmq.NewRabbitMQStreamSink(cfg.Config["url"], cfg.Config["stream_name"], fmttr)
	case "rabbitmq_queue":
		return sinkrabbitmq.NewRabbitMQQueueSink(cfg.Config["url"], cfg.Config["queue_name"], fmttr)
	case "redis":
		return sinkredis.NewRedisSink(cfg.Config["addr"], cfg.Config["password"], cfg.Config["stream"], fmttr)
	case "file":
		return file.NewFileSink(cfg.Config["filename"], fmttr)
	case "kafka":
		brokers := strings.Split(cfg.Config["brokers"], ",")
		return sinkkafka.NewKafkaSink(brokers, cfg.Config["topic"], cfg.Config["username"], cfg.Config["password"], fmttr), nil
	case "postgres":
		return sinkpostgres.NewPostgresSink(BuildConnectionString(cfg.Config, cfg.Type)), nil
	case "mysql":
		return sinkmysql.NewMySQLSink(BuildConnectionString(cfg.Config, cfg.Type)), nil
	case "mongodb":
		return sinkmongodb.NewMongoDBSink(cfg.Config["uri"], cfg.Config["database"]), nil
	case "pulsar":
		return pulsar.NewPulsarSink(cfg.Config["url"], cfg.Config["topic"], cfg.Config["token"], fmttr)
	case "kinesis":
		return kinesis.NewKinesisSink(cfg.Config["region"], cfg.Config["stream_name"], cfg.Config["access_key"], cfg.Config["secret_key"], fmttr)
	case "pubsub":
		return pubsub.NewPubSubSink(cfg.Config["project_id"], cfg.Config["topic_id"], cfg.Config["credentials_json"], fmttr)
	case "http":
		headers := make(map[string]string)
		if h, ok := cfg.Config["headers"]; ok && h != "" {
			pairs := strings.Split(h, ",")
			for _, pair := range pairs {
				kv := strings.SplitN(pair, ":", 2)
				if len(kv) == 2 {
					headers[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
				}
			}
		}
		return http.NewHttpSink(cfg.Config["url"], fmttr, headers), nil
	case "stdout":
		return stdout.NewStdoutSink(fmttr), nil
	case "smtp":
		port, _ := strconv.Atoi(cfg.Config["port"])
		ssl := cfg.Config["ssl"] == "true"
		to := strings.Split(cfg.Config["to"], ",")
		return smtp.NewSmtpSink(
			cfg.Config["host"],
			port,
			cfg.Config["username"],
			cfg.Config["password"],
			ssl,
			cfg.Config["from"],
			to,
			cfg.Config["subject"],
			fmttr,
		), nil
	case "telegram":
		return telegram.NewTelegramSink(cfg.Config["token"], cfg.Config["chat_id"], fmttr), nil
	default:
		return nil, fmt.Errorf("unsupported sink type: %s", cfg.Type)
	}
}
