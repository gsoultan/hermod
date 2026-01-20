package engine

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gsoultan/gsmail"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/buffer"
	pkgengine "github.com/user/hermod/pkg/engine"
	jsonfmt "github.com/user/hermod/pkg/formatter/json"
	"github.com/user/hermod/pkg/message"
	sinkclickhouse "github.com/user/hermod/pkg/sink/clickhouse"
	"github.com/user/hermod/pkg/sink/file"
	sinkhttp "github.com/user/hermod/pkg/sink/http"
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
	sinksqlite "github.com/user/hermod/pkg/sink/sqlite"
	"github.com/user/hermod/pkg/sink/stdout"
	"github.com/user/hermod/pkg/sink/telegram"
	sourcecassandra "github.com/user/hermod/pkg/source/cassandra"
	sourceclickhouse "github.com/user/hermod/pkg/source/clickhouse"
	"github.com/user/hermod/pkg/source/cron"
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
	"github.com/user/hermod/pkg/source/webhook"
	"github.com/user/hermod/pkg/source/yugabyte"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type RegistryStorage interface {
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetSink(ctx context.Context, id string) (storage.Sink, error)
	GetWorkflow(ctx context.Context, id string) (storage.Workflow, error)
	ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error)
	UpdateWorkflow(ctx context.Context, wf storage.Workflow) error
	CreateLog(ctx context.Context, log storage.Log) error
	UpdateSource(ctx context.Context, src storage.Source) error
	UpdateSourceState(ctx context.Context, id string, state map[string]string) error
	UpdateSink(ctx context.Context, snk storage.Sink) error
}

type SourceFactory func(SourceConfig) (hermod.Source, error)
type SinkFactory func(SinkConfig) (hermod.Sink, error)

type Registry struct {
	engines map[string]*activeEngine
	mu      sync.Mutex
	storage RegistryStorage
	config  pkgengine.Config

	sourceFactory SourceFactory
	sinkFactory   SinkFactory

	statusSubs          map[chan pkgengine.StatusUpdate]bool
	dashboardSubs       map[chan DashboardStats]bool
	logSubs             map[chan storage.Log]bool
	statusSubsMu        sync.RWMutex
	lastDashboardUpdate time.Time

	notificationService *notification.Service
	nodeStates          map[string]interface{}
	nodeStatesMu        sync.Mutex
	lookupCache         map[string]interface{}
	lookupCacheMu       sync.RWMutex
	dbPool              map[string]*sql.DB
	dbPoolMu            sync.Mutex
	logger              hermod.Logger
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
		config:              pkgengine.DefaultConfig(),
		statusSubs:          make(map[chan pkgengine.StatusUpdate]bool),
		dashboardSubs:       make(map[chan DashboardStats]bool),
		logSubs:             make(map[chan storage.Log]bool),
		notificationService: ns,
		nodeStates:          make(map[string]interface{}),
		lookupCache:         make(map[string]interface{}),
		dbPool:              make(map[string]*sql.DB),
		logger:              pkgengine.NewDefaultLogger(),
	}
}

func (r *Registry) SetLogger(logger hermod.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
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
}

func (r *Registry) broadcastLog(engineID, level, msg string) {
	l := storage.Log{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
	}

	r.mu.Lock()
	if eng, ok := r.engines[engineID]; ok && eng.isWorkflow {
		l.WorkflowID = engineID
	}
	r.mu.Unlock()

	_ = r.CreateLog(context.Background(), l)
}

func (r *Registry) CreateLog(ctx context.Context, l storage.Log) error {
	if r.storage != nil {
		err := r.storage.CreateLog(ctx, l)

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

func (r *Registry) SetFactories(sourceFactory SourceFactory, sinkFactory SinkFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sourceFactory = sourceFactory
	r.sinkFactory = sinkFactory
}

func (r *Registry) createSource(cfg SourceConfig) (hermod.Source, error) {
	r.mu.Lock()
	factory := r.sourceFactory
	logger := r.logger
	r.mu.Unlock()

	var src hermod.Source
	var err error
	if factory != nil {
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
	var src hermod.Source
	var err error
	if r.sourceFactory != nil {
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
	if err := s.Source.Ack(ctx, msg); err != nil {
		return err
	}
	if stateful, ok := s.Source.(hermod.Stateful); ok {
		state := stateful.GetState()
		if len(state) > 0 {
			_ = s.registry.storage.UpdateSourceState(ctx, s.sourceID, state)
		}
	}
	return nil
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
	factory := r.sinkFactory
	logger := r.logger
	r.mu.Unlock()

	var snk hermod.Sink
	var err error
	if factory != nil {
		snk, err = factory(cfg)
	} else {
		snk, err = CreateSink(cfg)
	}

	if err == nil && logger != nil {
		if l, ok := snk.(hermod.Loggable); ok {
			l.SetLogger(logger)
		}
	}
	return snk, err
}

func (r *Registry) createSinkInternal(cfg SinkConfig) (hermod.Sink, error) {
	var snk hermod.Sink
	var err error
	if r.sinkFactory != nil {
		snk, err = r.sinkFactory(cfg)
	} else {
		snk, err = CreateSink(cfg)
	}

	if err == nil && r.logger != nil {
		if l, ok := snk.(hermod.Loggable); ok {
			l.SetLogger(r.logger)
		}
	}
	return snk, err
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
	snk, err := r.createSink(cfg)
	if err != nil {
		return nil, err
	}
	defer snk.Close()

	if s, ok := snk.(hermod.Sampler); ok {
		return s.Sample(ctx, table)
	}
	return nil, fmt.Errorf("sink type %s does not support sampling", cfg.Type)
}

type subSource struct {
	nodeID  string
	source  hermod.Source
	running bool
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

func getValByPath(data map[string]interface{}, path string) interface{} {
	if path == "" {
		return nil
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	res := gjson.GetBytes(jsonData, path)
	if !res.Exists() {
		return nil
	}

	return res.Value()
}

func getMsgValByPath(msg hermod.Message, path string) interface{} {
	return getValByPath(msg.Data(), path)
}

func toFloat64(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

func toBool(val interface{}) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(v)
		if s == "true" || s == "1" || s == "yes" || s == "on" {
			return true
		}
		if s == "false" || s == "0" || s == "no" || s == "off" {
			return false
		}
		b, _ := strconv.ParseBool(s)
		return b
	case int, int32, int64, float32, float64:
		f, _ := toFloat64(v)
		return f != 0
	}
	return false
}

func setValByPath(data map[string]interface{}, path string, val interface{}) {
	if path == "" {
		return
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}

	newJSON, err := sjson.SetBytes(jsonData, path, val)
	if err != nil {
		return
	}

	var newData map[string]interface{}
	if err := json.Unmarshal(newJSON, &newData); err == nil {
		for k := range data {
			delete(data, k)
		}
		for k, v := range newData {
			data[k] = v
		}
	}
}

func (r *Registry) resolveTemplate(temp string, data map[string]interface{}) string {
	result := temp
	// Find all {{path}} and replace with values from data
	for {
		start := strings.Index(result, "{{")
		if start == -1 {
			break
		}

		// Find the matching }} but handle nested {{ if any (though not expected)
		end := strings.Index(result[start:], "}}")
		if end == -1 {
			break
		}

		fullTag := result[start : start+end+2]
		path := strings.TrimSpace(result[start+2 : start+end])

		val := getValByPath(data, path)
		valStr := ""
		if val != nil {
			switch v := val.(type) {
			case string:
				valStr = v
			case []byte:
				valStr = string(v)
			default:
				valStr = fmt.Sprintf("%v", v)
			}
		}

		result = strings.Replace(result, fullTag, valStr, 1)
	}
	return result
}

func (r *Registry) evaluateConditions(msg hermod.Message, conditions []map[string]interface{}) bool {
	if len(conditions) == 0 {
		return true
	}

	for _, cond := range conditions {
		field, _ := cond["field"].(string)
		op, _ := cond["operator"].(string)
		val := cond["value"]
		match := false

		fieldValRaw := getMsgValByPath(msg, field)
		fieldVal := fmt.Sprintf("%v", fieldValRaw)
		valStr := fmt.Sprintf("%v", val)

		switch op {
		case "=":
			match = fieldVal == valStr
		case "!=":
			match = fieldVal != valStr
		case ">", ">=", "<", "<=":
			v1, ok1 := toFloat64(fieldValRaw)
			v2, ok2 := toFloat64(val)
			if ok1 && ok2 {
				switch op {
				case ">":
					match = v1 > v2
				case ">=":
					match = v1 >= v2
				case "<":
					match = v1 < v2
				case "<=":
					match = v1 <= v2
				}
			} else {
				// Fallback to string comparison if not numbers
				switch op {
				case ">":
					match = fieldVal > valStr
				case ">=":
					match = fieldVal >= valStr
				case "<":
					match = fieldVal < valStr
				case "<=":
					match = fieldVal <= valStr
				}
			}
		case "contains":
			match = strings.Contains(fieldVal, valStr)
		}

		if !match {
			return false
		}
	}

	return true
}

func (r *Registry) evaluateAdvancedExpression(msg hermod.Message, expr interface{}) interface{} {
	valStr, ok := expr.(string)
	if !ok {
		return expr
	}
	return r.parseAndEvaluate(msg, valStr)
}

func (r *Registry) parseAndEvaluate(msg hermod.Message, expr string) interface{} {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	// Check if it's a function call: func(args...)
	if strings.HasSuffix(expr, ")") {
		openParen := -1
		parenCount := 0
		for i := len(expr) - 1; i >= 0; i-- {
			if expr[i] == ')' {
				parenCount++
			} else if expr[i] == '(' {
				parenCount--
				if parenCount == 0 {
					openParen = i
					break
				}
			}
		}

		if openParen > 0 {
			funcName := strings.TrimSpace(expr[:openParen])
			// Verify it looks like a function name (only alphanumeric and underscore)
			isFunc := true
			for _, c := range funcName {
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
					isFunc = false
					break
				}
			}

			if isFunc {
				argsStr := expr[openParen+1 : len(expr)-1]
				args := r.parseArgs(argsStr)
				evaluatedArgs := make([]interface{}, len(args))
				for i, arg := range args {
					evaluatedArgs[i] = r.parseAndEvaluate(msg, arg)
				}
				return r.callFunction(funcName, evaluatedArgs)
			}
		}
	}

	// Check if it's a string literal: "..." or '...'
	if (strings.HasPrefix(expr, "\"") && strings.HasSuffix(expr, "\"")) ||
		(strings.HasPrefix(expr, "'") && strings.HasSuffix(expr, "'")) {
		if len(expr) >= 2 {
			return expr[1 : len(expr)-1]
		}
		return expr
	}

	// Check if it's a source reference: source.path
	if strings.HasPrefix(expr, "source.") {
		return getMsgValByPath(msg, expr[7:])
	}

	// Try to parse as a number
	if f, err := strconv.ParseFloat(expr, 64); err == nil {
		return f
	}

	// Default to string
	return expr
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
	switch strings.ToLower(name) {
	case "lower":
		if len(args) > 0 {
			return strings.ToLower(fmt.Sprintf("%v", args[0]))
		}
	case "upper":
		if len(args) > 0 {
			return strings.ToUpper(fmt.Sprintf("%v", args[0]))
		}
	case "trim":
		if len(args) > 0 {
			return strings.TrimSpace(fmt.Sprintf("%v", args[0]))
		}
	case "replace":
		if len(args) >= 3 {
			s := fmt.Sprintf("%v", args[0])
			old := fmt.Sprintf("%v", args[1])
			new := fmt.Sprintf("%v", args[2])
			return strings.ReplaceAll(s, old, new)
		}
	case "concat":
		var sb strings.Builder
		for _, arg := range args {
			if arg != nil {
				sb.WriteString(fmt.Sprintf("%v", arg))
			}
		}
		return sb.String()
	case "substring":
		if len(args) >= 2 {
			s := fmt.Sprintf("%v", args[0])
			start, _ := strconv.Atoi(fmt.Sprintf("%v", args[1]))
			end := len(s)
			if len(args) >= 3 {
				end, _ = strconv.Atoi(fmt.Sprintf("%v", args[2]))
			}
			if start < 0 {
				start = 0
			}
			if start > len(s) {
				start = len(s)
			}
			if end > len(s) {
				end = len(s)
			}
			if start > end {
				return ""
			}
			return s[start:end]
		}
	case "date_format":
		if len(args) >= 2 {
			dateStr := fmt.Sprintf("%v", args[0])
			toFormat := fmt.Sprintf("%v", args[1])
			var t time.Time
			var err error
			if len(args) >= 3 {
				fromFormat := fmt.Sprintf("%v", args[2])
				t, err = time.Parse(fromFormat, dateStr)
			} else {
				formats := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02", time.RFC1123, time.RFC1123Z}
				for _, f := range formats {
					t, err = time.Parse(f, dateStr)
					if err == nil {
						break
					}
				}
			}
			if err == nil {
				// Translate common format tokens if needed, but Go uses 2006-01-02
				// For simplicity, we assume Go format strings
				return t.Format(toFormat)
			}
			return dateStr
		}
	case "coalesce":
		for _, arg := range args {
			if arg != nil && fmt.Sprintf("%v", arg) != "<nil>" && fmt.Sprintf("%v", arg) != "" {
				return arg
			}
		}
		return nil
	case "now":
		return time.Now().Format(time.RFC3339)
	case "hash":
		if len(args) >= 1 {
			s := fmt.Sprintf("%v", args[0])
			algo := "sha256"
			if len(args) >= 2 {
				algo = strings.ToLower(fmt.Sprintf("%v", args[1]))
			}
			if algo == "md5" {
				h := md5.New()
				h.Write([]byte(s))
				return hex.EncodeToString(h.Sum(nil))
			}
			h := sha256.New()
			h.Write([]byte(s))
			return hex.EncodeToString(h.Sum(nil))
		}
	case "add":
		if len(args) >= 2 {
			v1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			v2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			return v1 + v2
		}
	case "sub":
		if len(args) >= 2 {
			v1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			v2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			return v1 - v2
		}
	case "mul":
		if len(args) >= 2 {
			v1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			v2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			return v1 * v2
		}
	case "div":
		if len(args) >= 2 {
			v1, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			v2, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			if v2 != 0 {
				return v1 / v2
			}
			return 0.0
		}
	case "abs":
		if len(args) >= 1 {
			v, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			return math.Abs(v)
		}
	case "round":
		if len(args) >= 1 {
			v, _ := strconv.ParseFloat(fmt.Sprintf("%v", args[0]), 64)
			precision := 0.0
			if len(args) >= 2 {
				precision, _ = strconv.ParseFloat(fmt.Sprintf("%v", args[1]), 64)
			}
			ratio := math.Pow(10, precision)
			return math.Round(v*ratio) / ratio
		}
	case "and":
		for _, arg := range args {
			if !toBool(arg) {
				return false
			}
		}
		return true
	case "or":
		for _, arg := range args {
			if toBool(arg) {
				return true
			}
		}
		return false
	case "not":
		if len(args) > 0 {
			return !toBool(args[0])
		}
	case "if":
		if len(args) >= 3 {
			if toBool(args[0]) {
				return args[1]
			}
			return args[2]
		}
	case "eq":
		if len(args) >= 2 {
			return fmt.Sprintf("%v", args[0]) == fmt.Sprintf("%v", args[1])
		}
	case "gt":
		if len(args) >= 2 {
			v1, ok1 := toFloat64(args[0])
			v2, ok2 := toFloat64(args[1])
			if ok1 && ok2 {
				return v1 > v2
			}
			return fmt.Sprintf("%v", args[0]) > fmt.Sprintf("%v", args[1])
		}
	case "lt":
		if len(args) >= 2 {
			v1, ok1 := toFloat64(args[0])
			v2, ok2 := toFloat64(args[1])
			if ok1 && ok2 {
				return v1 < v2
			}
			return fmt.Sprintf("%v", args[0]) < fmt.Sprintf("%v", args[1])
		}
	case "contains":
		if len(args) >= 2 {
			return strings.Contains(fmt.Sprintf("%v", args[0]), fmt.Sprintf("%v", args[1]))
		}
	case "toint":
		if len(args) > 0 {
			v, _ := toFloat64(args[0])
			return int64(v)
		}
	case "tofloat":
		if len(args) > 0 {
			v, _ := toFloat64(args[0])
			return v
		}
	case "tostring":
		if len(args) > 0 {
			return fmt.Sprintf("%v", args[0])
		}
	case "tobool":
		if len(args) > 0 {
			return toBool(args[0])
		}
	}
	return nil
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
		res, err := r.applyTransformation(currentMsg.Clone(), t.Type, config)
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

func (r *Registry) applyTransformation(modifiedMsg hermod.Message, transType string, config map[string]interface{}) (hermod.Message, error) {
	res, err := r.doApplyTransformation(modifiedMsg, transType, config)
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

func (r *Registry) doApplyTransformation(modifiedMsg hermod.Message, transType string, config map[string]interface{}) (hermod.Message, error) {
	if modifiedMsg == nil {
		return nil, nil
	}
	data := modifiedMsg.Data()

	switch transType {
	case "advanced":
		results := make(map[string]interface{})
		for k, v := range config {
			if !strings.HasPrefix(k, "column.") {
				continue
			}
			colPath := strings.TrimPrefix(k, "column.")
			result := r.evaluateAdvancedExpression(modifiedMsg, v)
			if result != nil {
				results[colPath] = result
			}
		}

		modifiedMsg.ClearPayloads()
		for colPath, result := range results {
			modifiedMsg.SetData(colPath, result)
		}
		return modifiedMsg, nil

	case "filter_data", "validate":
		conditionsStr, _ := config["conditions"].(string)
		var conditions []map[string]interface{}
		if conditionsStr != "" {
			_ = json.Unmarshal([]byte(conditionsStr), &conditions)
		}

		// Fallback to old format if no conditions array
		if len(conditions) == 0 {
			field, _ := config["field"].(string)
			op, _ := config["operator"].(string)
			val, _ := config["value"].(string)
			if field != "" {
				conditions = append(conditions, map[string]interface{}{
					"field":    field,
					"operator": op,
					"value":    val,
				})
			}
		}

		isValid := r.evaluateConditions(modifiedMsg, conditions)
		asField := toBool(config["asField"]) || transType == "validate"

		if asField {
			targetField, _ := config["targetField"].(string)
			if targetField == "" {
				targetField = "is_valid"
			}
			modifiedMsg.SetData(targetField, isValid)
			return modifiedMsg, nil
		}

		if isValid {
			return modifiedMsg, nil
		}
		return nil, nil // Filtered

	case "mapping":
		field, _ := config["field"].(string)
		mappingStr, _ := config["mapping"].(string)
		var mapping map[string]interface{}
		_ = json.Unmarshal([]byte(mappingStr), &mapping)

		mappingType := getConfigString(config, "mappingType") // "exact", "range", "regex"
		fieldValRaw := getMsgValByPath(modifiedMsg, field)
		fieldVal := fmt.Sprintf("%v", fieldValRaw)

		if mappingType == "range" {
			val, ok := toFloat64(fieldValRaw)
			if ok {
				for k, v := range mapping {
					if strings.Contains(k, "-") {
						parts := strings.Split(k, "-")
						if len(parts) == 2 {
							low, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
							high, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
							if val >= low && val <= high {
								modifiedMsg.SetData(field, v)
								return modifiedMsg, nil
							}
						}
					} else if strings.HasSuffix(k, "+") {
						low, _ := strconv.ParseFloat(strings.TrimSuffix(k, "+"), 64)
						if val >= low {
							modifiedMsg.SetData(field, v)
							return modifiedMsg, nil
						}
					}
				}
			}
		} else if mappingType == "regex" {
			for k, v := range mapping {
				matched, _ := regexp.MatchString(k, fieldVal)
				if matched {
					modifiedMsg.SetData(field, v)
					return modifiedMsg, nil
				}
			}
		} else {
			// exact (default)
			if newVal, ok := mapping[fieldVal]; ok {
				modifiedMsg.SetData(field, newVal)
			}
		}
		return modifiedMsg, nil

	case "mask":
		field, _ := config["field"].(string)
		maskType, _ := config["maskType"].(string) // "all", "partial", "email"
		fieldVal := fmt.Sprintf("%v", getMsgValByPath(modifiedMsg, field))

		var masked string
		switch maskType {
		case "email":
			parts := strings.Split(fieldVal, "@")
			if len(parts) == 2 {
				if len(parts[0]) > 1 {
					masked = parts[0][0:1] + "****@" + parts[1]
				} else {
					masked = "*@" + parts[1]
				}
			} else {
				masked = "****"
			}
		case "partial":
			if len(fieldVal) > 4 {
				masked = fieldVal[:2] + "****" + fieldVal[len(fieldVal)-2:]
			} else {
				masked = "****"
			}
		default:
			masked = "****"
		}

		modifiedMsg.SetData(field, masked)
		return modifiedMsg, nil

	case "set":
		for k, v := range config {
			if strings.HasPrefix(k, "column.") {
				colPath := strings.TrimPrefix(k, "column.")
				result := r.evaluateAdvancedExpression(modifiedMsg, v)
				modifiedMsg.SetData(colPath, result)
			}
		}
		return modifiedMsg, nil

	case "db_lookup":
		sourceID := getConfigString(config, "sourceId")
		table := getConfigString(config, "table")
		keyColumn := getConfigString(config, "keyColumn")
		valueColumn := getConfigString(config, "valueColumn")
		keyField := getConfigString(config, "keyField")
		targetField := getConfigString(config, "targetField")
		ttlStr := getConfigString(config, "ttl")
		whereClause := getConfigString(config, "whereClause")
		defaultValue := getConfigString(config, "defaultValue")

		if sourceID == "" || (table == "" && whereClause == "") || (keyColumn == "" && whereClause == "") || (keyField == "" && !strings.Contains(whereClause, "{{")) || targetField == "" {
			if sourceID == "" || targetField == "" {
				return modifiedMsg, nil
			}
		}

		keyVal := getMsgValByPath(modifiedMsg, keyField)
		if keyVal == nil && !strings.Contains(whereClause, "{{") {
			return modifiedMsg, nil
		}

		cacheKey := fmt.Sprintf("db:%s:%s:%s:%s:%v:%s", sourceID, table, keyColumn, valueColumn, keyVal, whereClause)
		r.lookupCacheMu.RLock()
		cached, found := r.lookupCache[cacheKey]
		r.lookupCacheMu.RUnlock()

		if found {
			modifiedMsg.SetData(targetField, cached)
			return modifiedMsg, nil
		}

		// Not in cache, lookup in database
		src, err := r.storage.GetSource(context.Background(), sourceID)
		if err != nil {
			return modifiedMsg, fmt.Errorf("failed to get source for lookup: %w", err)
		}

		if src.Type == "mongodb" {
			// MongoDB lookup
			uri := src.Config["uri"]
			if uri == "" {
				host := src.Config["host"]
				port := src.Config["port"]
				user := src.Config["user"]
				password := src.Config["password"]
				if user != "" && password != "" {
					uri = fmt.Sprintf("mongodb://%s:%s@%s:%s", url.QueryEscape(user), url.QueryEscape(password), host, port)
				} else {
					uri = fmt.Sprintf("mongodb://%s:%s", host, port)
				}
			}

			client, err := sourcemongodb.GetClient(uri)
			if err != nil {
				return modifiedMsg, fmt.Errorf("failed to connect to mongodb for lookup: %w", err)
			}

			dbName := src.Config["database"]
			collName := table
			if collName == "" {
				collName = src.Config["collection"]
			}

			coll := client.Database(dbName).Collection(collName)
			filter := bson.M{keyColumn: keyVal}
			if whereClause != "" {
				// Very basic JSON whereClause for mongo
				err = json.Unmarshal([]byte(r.resolveTemplate(whereClause, data)), &filter)
				if err != nil {
					return modifiedMsg, fmt.Errorf("failed to parse mongo whereClause: %w", err)
				}
			}

			var result map[string]interface{}
			err = coll.FindOne(context.Background(), filter).Decode(&result)
			if err != nil {
				if err == mongo.ErrNoDocuments {
					if defaultValue != "" {
						modifiedMsg.SetData(targetField, defaultValue)
					}
					return modifiedMsg, nil
				}
				return modifiedMsg, fmt.Errorf("mongo lookup failed: %w", err)
			}

			var finalResult interface{} = result
			if valueColumn != "" && valueColumn != "*" {
				finalResult = result[valueColumn]
			}

			// Update cache
			r.lookupCacheMu.Lock()
			r.lookupCache[cacheKey] = finalResult
			r.lookupCacheMu.Unlock()

			modifiedMsg.SetData(targetField, finalResult)
			return modifiedMsg, nil
		}

		db, lookupErr := r.getOrOpenDB(src)
		if lookupErr != nil {
			return modifiedMsg, fmt.Errorf("failed to get database for lookup: %w", lookupErr)
		}

		query := ""
		if whereClause != "" {
			query = fmt.Sprintf("SELECT %s FROM %s WHERE %s", valueColumn, table, r.resolveTemplate(whereClause, data))
		} else {
			placeholder := "$1"
			if src.Type == "mysql" || src.Type == "mariadb" {
				placeholder = "?"
			}
			query = fmt.Sprintf("SELECT %s FROM %s WHERE %s = %s LIMIT 1", valueColumn, table, keyColumn, placeholder)
		}

		var resultVal interface{}
		if valueColumn == "*" || strings.Contains(valueColumn, ",") {
			rows, err := db.Query(query, keyVal)
			if err != nil {
				return modifiedMsg, fmt.Errorf("failed to execute lookup query: %w", err)
			}
			defer rows.Close()

			if rows.Next() {
				cols, _ := rows.Columns()
				values := make([]interface{}, len(cols))
				valuePtrs := make([]interface{}, len(cols))
				for i := range values {
					valuePtrs[i] = &values[i]
				}

				if err := rows.Scan(valuePtrs...); err != nil {
					return modifiedMsg, fmt.Errorf("failed to scan lookup results: %w", err)
				}

				rowMap := make(map[string]interface{})
				for i, col := range cols {
					val := values[i]
					if b, ok := val.([]byte); ok {
						rowMap[col] = string(b)
					} else {
						rowMap[col] = val
					}
				}
				resultVal = rowMap
			} else {
				if defaultValue != "" {
					modifiedMsg.SetData(targetField, defaultValue)
				}
				return modifiedMsg, nil
			}
		} else {
			err = db.QueryRow(query, keyVal).Scan(&resultVal)
			if err != nil {
				if err == sql.ErrNoRows {
					if defaultValue != "" {
						modifiedMsg.SetData(targetField, defaultValue)
					}
					return modifiedMsg, nil
				}
				return modifiedMsg, fmt.Errorf("failed to execute lookup query: %w", err)
			}
			if b, ok := resultVal.([]byte); ok {
				resultVal = string(b)
			}
		}

		// Update cache
		r.lookupCacheMu.Lock()
		r.lookupCache[cacheKey] = resultVal
		r.lookupCacheMu.Unlock()

		if ttlStr != "" {
			if ttl, err := time.ParseDuration(ttlStr); err == nil {
				go func() {
					time.Sleep(ttl)
					r.lookupCacheMu.Lock()
					delete(r.lookupCache, cacheKey)
					r.lookupCacheMu.Unlock()
				}()
			}
		}

		modifiedMsg.SetData(targetField, resultVal)
		return modifiedMsg, nil

	case "api_lookup":
		method := getConfigString(config, "method")
		if method == "" {
			method = "GET"
		}
		rawURL := getConfigString(config, "url")
		headersStr := getConfigString(config, "headers")
		bodyTemp := getConfigString(config, "body")
		responsePath := getConfigString(config, "responsePath")
		targetField := getConfigString(config, "targetField")
		timeoutStr := getConfigString(config, "timeout")
		maxRetriesStr := getConfigString(config, "maxRetries")
		retryDelayStr := getConfigString(config, "retryDelay")
		ttlStr := getConfigString(config, "ttl")
		queryParamsStr := getConfigString(config, "queryParams")
		authType := getConfigString(config, "authType") // "basic", "bearer"
		token := getConfigString(config, "token")
		username := getConfigString(config, "username")
		password := getConfigString(config, "password")
		defaultValue := getConfigString(config, "defaultValue")

		if rawURL == "" || targetField == "" {
			return modifiedMsg, nil
		}

		resolvedURL := r.resolveTemplate(rawURL, data)

		// Append query parameters
		if queryParamsStr != "" {
			var qParams map[string]interface{}
			if err := json.Unmarshal([]byte(queryParamsStr), &qParams); err == nil {
				u, err := url.Parse(resolvedURL)
				if err == nil {
					q := u.Query()
					for k, v := range qParams {
						vStr := fmt.Sprintf("%v", v)
						if vs, ok := v.(string); ok {
							vStr = r.resolveTemplate(vs, data)
						}
						q.Set(k, vStr)
					}
					u.RawQuery = q.Encode()
					resolvedURL = u.String()
				}
			}
		}

		resolvedBody := ""
		if bodyTemp != "" {
			resolvedBody = r.resolveTemplate(bodyTemp, data)
		}

		// Cache check
		bodyHash := ""
		if resolvedBody != "" {
			h := sha256.New()
			h.Write([]byte(resolvedBody))
			bodyHash = hex.EncodeToString(h.Sum(nil))
		}
		cacheKey := fmt.Sprintf("api:%s:%s:%s", method, resolvedURL, bodyHash)

		r.lookupCacheMu.RLock()
		cached, found := r.lookupCache[cacheKey]
		r.lookupCacheMu.RUnlock()

		if found {
			modifiedMsg.SetData(targetField, cached)
			return modifiedMsg, nil
		}

		// Execute API call with retries
		timeout := 10 * time.Second
		if timeoutStr != "" {
			if t, err := time.ParseDuration(timeoutStr); err == nil {
				timeout = t
			}
		}

		maxRetries := 0
		if maxRetriesStr != "" {
			maxRetries, _ = strconv.Atoi(maxRetriesStr)
		}

		retryDelay := 1 * time.Second
		if retryDelayStr != "" {
			if d, err := time.ParseDuration(retryDelayStr); err == nil {
				retryDelay = d
			}
		}

		var respData interface{}
		var lastErr error

		for i := 0; i <= maxRetries; i++ {
			if i > 0 {
				time.Sleep(retryDelay)
			}

			var reqBody io.Reader
			if resolvedBody != "" {
				reqBody = strings.NewReader(resolvedBody)
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			req, err := http.NewRequestWithContext(ctx, method, resolvedURL, reqBody)
			if err != nil {
				cancel()
				lastErr = err
				continue
			}

			if headersStr != "" {
				var headers map[string]interface{}
				if err := json.Unmarshal([]byte(headersStr), &headers); err == nil {
					for k, v := range headers {
						vStr := fmt.Sprintf("%v", v)
						if vs, ok := v.(string); ok {
							vStr = r.resolveTemplate(vs, data)
						}
						req.Header.Set(k, vStr)
					}
				}
			}

			// Auth
			if authType == "basic" {
				req.SetBasicAuth(r.resolveTemplate(username, data), r.resolveTemplate(password, data))
			} else if authType == "bearer" {
				req.Header.Set("Authorization", "Bearer "+r.resolveTemplate(token, data))
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				cancel()
				lastErr = err
				continue
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				resp.Body.Close()
				cancel()
				lastErr = fmt.Errorf("api lookup returned status %d", resp.StatusCode)
				if resp.StatusCode >= 500 || resp.StatusCode == 429 {
					continue // Retryable
				}
				break // Non-retryable
			}

			respBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			if err != nil {
				lastErr = err
				continue
			}

			if err := json.Unmarshal(respBytes, &respData); err != nil {
				respData = string(respBytes)
			}

			lastErr = nil
			break
		}

		if lastErr != nil {
			if defaultValue != "" {
				modifiedMsg.SetData(targetField, defaultValue)
				return modifiedMsg, nil
			}
			return modifiedMsg, fmt.Errorf("failed to execute api lookup after %d retries: %w", maxRetries, lastErr)
		}

		var resultVal interface{}
		if responsePath != "" && responsePath != "." {
			if m, ok := respData.(map[string]interface{}); ok {
				resultVal = getValByPath(m, responsePath)
			} else {
				resultVal = respData
			}
		} else {
			resultVal = respData
		}

		if resultVal == nil && defaultValue != "" {
			resultVal = defaultValue
		}

		// Update cache
		r.lookupCacheMu.Lock()
		r.lookupCache[cacheKey] = resultVal
		r.lookupCacheMu.Unlock()

		if ttlStr != "" {
			if ttl, err := time.ParseDuration(ttlStr); err == nil {
				go func() {
					time.Sleep(ttl)
					r.lookupCacheMu.Lock()
					delete(r.lookupCache, cacheKey)
					r.lookupCacheMu.Unlock()
				}()
			}
		}

		modifiedMsg.SetData(targetField, resultVal)
		return modifiedMsg, nil

	default:
		return modifiedMsg, nil
	}
}

func (r *Registry) runWorkflowNode(workflowID string, node *storage.WorkflowNode, msg hermod.Message) (hermod.Message, string, error) {
	if msg == nil {
		return nil, "", nil
	}

	// For efficiency, we only clone if we are actually modifying the message
	// and want to keep the original intact.
	// But most nodes are sequential transformations, so we can modify in-place
	// IF it's a DefaultMessage that supports SetData.

	currentMsg := msg
	data := currentMsg.Data()

	switch node.Type {
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
				modifiedMsg, err = r.applyTransformation(modifiedMsg, st, step)
				if err != nil {
					return nil, "", err
				}
				if modifiedMsg == nil {
					return nil, "", nil // Filtered
				}
			}
			return modifiedMsg, "", nil
		}

		res, err := r.applyTransformation(modifiedMsg, transType, node.Config)
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
			return currentMsg, "true", nil
		}
		return currentMsg, "false", nil

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
					return currentMsg, label, nil
				}
			} else {
				// Fallback to value comparison with the main field
				val, _ := c["value"].(string)
				if val == fieldValStr {
					return currentMsg, label, nil
				}
			}
		}
		return currentMsg, "default", nil

	case "stateful":
		op, _ := node.Config["operation"].(string) // "count", "sum"
		field, _ := node.Config["field"].(string)
		outputField, _ := node.Config["outputField"].(string)
		if outputField == "" {
			outputField = field + "_" + op
		}

		key := workflowID + ":" + node.ID
		r.nodeStatesMu.Lock()
		state, ok := r.nodeStates[key]
		if !ok {
			state = float64(0)
		}

		currentVal := state.(float64)
		switch op {
		case "count":
			currentVal++
		case "sum":
			val := getMsgValByPath(currentMsg, field)
			if v, ok := toFloat64(val); ok {
				currentVal += v
			}
		}
		r.nodeStates[key] = currentVal
		r.nodeStatesMu.Unlock()

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
}

func (r *Registry) GetDashboardStats(ctx context.Context) (DashboardStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stats := DashboardStats{
		ActiveWorkflows: len(r.engines),
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

	return nil
}

func (r *Registry) StartWorkflow(id string, wf storage.Workflow) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.engines[id]; ok {
		return fmt.Errorf("workflow %s already running", id)
	}

	ctx := context.Background()
	if err := r.ValidateWorkflow(ctx, wf); err != nil {
		return fmt.Errorf("workflow validation failed: %w", err)
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
		subSources = append(subSources, &subSource{nodeID: sn.ID, source: src})
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

	buf := buffer.NewRingBuffer(1000)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	eng := pkgengine.NewEngine(ms, sinks, buf)
	eng.SetConfig(r.config)

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
				currMsg, currBranch, err = r.runWorkflowNode(id, currNode, currMsg)
				if err != nil {
					pkgengine.WorkflowNodeErrors.WithLabelValues(id, currNode.ID, currNode.Type).Inc()
					r.broadcastLog(id, "ERROR", fmt.Sprintf("Node %s (%s) error: %v", currNode.ID, currNode.Type, err))
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
	for i, cfg := range snkConfigs {
		sinkIDs[i] = cfg.ID
		sinkTypes[i] = cfg.Type
	}
	eng.SetIDs(id, "multi", sinkIDs)
	eng.SetSinkTypes(sinkTypes)

	if r.storage != nil {
		eng.SetLogger(NewDatabaseLogger(ctx, r, id))
		eng.SetOnStatusChange(func(update pkgengine.StatusUpdate) {
			dbCtx := context.Background()
			if workflow, err := r.storage.GetWorkflow(dbCtx, id); err == nil {
				workflow.Status = update.EngineStatus
				_ = r.storage.UpdateWorkflow(dbCtx, workflow)
			}
			r.broadcastStatus(update)
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

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Printf("Workflow %s panicked: %v\n", id, rec)
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
			// Cancelled via StopEngine
			ms.Close()
			for _, snk := range sinks {
				snk.Close()
			}
			return
		default:
			// Stopped by itself
		}

		if err != nil {
			fmt.Printf("Workflow %s failed: %v\n", id, err)
		} else {
			fmt.Printf("Workflow %s stopped gracefully\n", id)
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
							if src, err := r.storage.GetSource(dbCtx, node.RefID); err == nil {
								if !r.IsResourceInUse(dbCtx, node.RefID, id, true) {
									src.Active = false
									_ = r.storage.UpdateSource(dbCtx, src)
								}
							}
						} else if node.Type == "sink" {
							if snk, err := r.storage.GetSink(dbCtx, node.RefID); err == nil {
								if !r.IsResourceInUse(dbCtx, node.RefID, id, false) {
									snk.Active = false
									_ = r.storage.UpdateSink(dbCtx, snk)
								}
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

func (r *Registry) StopEngineWithoutUpdate(id string) error {
	return r.stopEngine(id, false)
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
					if src, err := r.storage.GetSource(ctx, node.RefID); err == nil {
						if !r.IsResourceInUse(ctx, node.RefID, id, true) {
							src.Active = false
							_ = r.storage.UpdateSource(ctx, src)
						}
					}
				} else if node.Type == "sink" {
					if snk, err := r.storage.GetSink(ctx, node.RefID); err == nil {
						if !r.IsResourceInUse(ctx, node.RefID, id, false) {
							snk.Active = false
							_ = r.storage.UpdateSink(ctx, snk)
						}
					}
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
		if wf.ID != excludeID && wf.Active {
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

func CreateSource(cfg SourceConfig) (hermod.Source, error) {
	connString := BuildConnectionString(cfg.Config, cfg.Type)

	tables := []string{}
	if t, ok := cfg.Config["tables"]; ok && t != "" {
		tables = strings.Split(t, ",")
		for i, table := range tables {
			tables[i] = strings.TrimSpace(table)
		}
	}

	useCDC := cfg.Config["use_cdc"] != "false"

	var src hermod.Source
	var err error

	switch cfg.Type {
	case "postgres":
		src = sourcepostgres.NewPostgresSource(
			connString,
			cfg.Config["slot_name"],
			cfg.Config["publication_name"],
			tables,
			useCDC,
		)
	case "mssql":
		autoEnable := cfg.Config["auto_enable_cdc"] != "false"
		src = mssql.NewMSSQLSource(connString, tables, autoEnable, useCDC)
	case "mysql":
		src = mysql.NewMySQLSource(connString, useCDC)
	case "oracle":
		src = oracle.NewOracleSource(connString, useCDC)
	case "db2":
		src = db2.NewDB2Source(connString, useCDC)
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
		src = sourcemongodb.NewMongoDBSource(uri, cfg.Config["database"], cfg.Config["collection"], useCDC)
	case "mariadb":
		src = mariadb.NewMariaDBSource(connString, useCDC)
	case "cassandra":
		hosts := []string{"localhost"}
		if h, ok := cfg.Config["hosts"]; ok && h != "" {
			hosts = strings.Split(h, ",")
		}
		src = sourcecassandra.NewCassandraSource(hosts, useCDC)
	case "yugabyte":
		src = yugabyte.NewYugabyteSource(connString, useCDC)
	case "scylladb":
		hosts := []string{"localhost"}
		if h, ok := cfg.Config["hosts"]; ok && h != "" {
			hosts = strings.Split(h, ",")
		}
		src = scylladb.NewScyllaDBSource(hosts, useCDC)
	case "clickhouse":
		src = sourceclickhouse.NewClickHouseSource(connString, useCDC)
	case "csv":
		delimiter := ','
		if d, ok := cfg.Config["delimiter"]; ok && d != "" {
			delimiter = rune(d[0])
		}
		hasHeader := cfg.Config["has_header"] == "true"
		sourceType := cfg.Config["source_type"]
		if sourceType == "http" {
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
			src = sourcecsv.NewHTTPCSVSource(cfg.Config["url"], delimiter, hasHeader, headers)
		} else if sourceType == "s3" {
			src = sourcecsv.NewS3CSVSource(
				cfg.Config["s3_region"],
				cfg.Config["s3_bucket"],
				cfg.Config["s3_key"],
				cfg.Config["s3_endpoint"],
				cfg.Config["s3_access_key"],
				cfg.Config["s3_secret_key"],
				delimiter,
				hasHeader,
			)
		} else {
			src = sourcecsv.NewCSVSource(cfg.Config["file_path"], delimiter, hasHeader)
		}
	case "sqlite":
		src = sourcesqlite.NewSQLiteSource(connString, tables, useCDC)
	case "kafka":
		brokers := strings.Split(cfg.Config["brokers"], ",")
		src = sourcekafka.NewKafkaSource(brokers, cfg.Config["topic"], cfg.Config["group_id"], cfg.Config["username"], cfg.Config["password"])
	case "nats":
		src, err = sourcenats.NewNatsJetStreamSource(cfg.Config["url"], cfg.Config["subject"], cfg.Config["queue"], cfg.Config["durable_name"], cfg.Config["username"], cfg.Config["password"], cfg.Config["token"])
	case "redis":
		src = sourceredis.NewRedisSource(cfg.Config["addr"], cfg.Config["password"], cfg.Config["stream"], cfg.Config["group"])
	case "rabbitmq":
		src, err = sourcerabbitmq.NewRabbitMQStreamSource(cfg.Config["url"], cfg.Config["stream_name"], cfg.Config["consumer_name"])
	case "rabbitmq_queue":
		src, err = sourcerabbitmq.NewRabbitMQQueueSource(cfg.Config["url"], cfg.Config["queue_name"])
	case "webhook":
		src = webhook.NewWebhookSource(cfg.Config["path"])
	case "cron":
		src = cron.NewCronSource(cfg.Config["schedule"], cfg.Config["payload"])
	default:
		return nil, fmt.Errorf("unsupported source type: %s", cfg.Type)
	}

	if err != nil {
		return nil, err
	}

	if src != nil && cfg.State != nil {
		if s, ok := src.(hermod.Stateful); ok {
			s.SetState(cfg.State)
		}
	}

	return src, nil
}

func CreateSink(cfg SinkConfig) (hermod.Sink, error) {
	var fmttr hermod.Formatter
	format := cfg.Config["format"]
	if format == "payload" {
		f := jsonfmt.NewJSONFormatter()
		f.SetMode(jsonfmt.ModePayload)
		fmttr = f
	} else if format == "cdc" || format == "json" {
		fmttr = jsonfmt.NewJSONFormatter()
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
	case "postgres", "yugabyte", "mssql", "oracle":
		return sinkpostgres.NewPostgresSink(BuildConnectionString(cfg.Config, cfg.Type)), nil
	case "mysql", "mariadb":
		return sinkmysql.NewMySQLSink(BuildConnectionString(cfg.Config, cfg.Type)), nil
	case "sqlite":
		return sinksqlite.NewSQLiteSink(BuildConnectionString(cfg.Config, cfg.Type)), nil
	case "clickhouse":
		return sinkclickhouse.NewClickHouseSink(cfg.Config["addr"], cfg.Config["database"]), nil
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
		return sinkhttp.NewHttpSink(cfg.Config["url"], fmttr, headers), nil
	case "stdout":
		return stdout.NewStdoutSink(fmttr), nil
	case "smtp":
		port, _ := strconv.Atoi(cfg.Config["port"])
		ssl := cfg.Config["ssl"] == "true"
		to := strings.Split(cfg.Config["to"], ",")
		s3Config := gsmail.S3Config{
			Region:    cfg.Config["s3_region"],
			Bucket:    cfg.Config["s3_bucket"],
			Key:       cfg.Config["s3_key"],
			Endpoint:  cfg.Config["s3_endpoint"],
			AccessKey: cfg.Config["s3_access_key"],
			SecretKey: cfg.Config["s3_secret_key"],
		}
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
			cfg.Config["template_source"],
			cfg.Config["template"],
			cfg.Config["template_url"],
			s3Config,
		), nil
	case "telegram":
		return telegram.NewTelegramSink(cfg.Config["token"], cfg.Config["chat_id"], fmttr), nil
	default:
		return nil, fmt.Errorf("unsupported sink type: %s", cfg.Type)
	}
}
