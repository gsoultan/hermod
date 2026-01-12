package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/user/hermod"
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
	"github.com/user/hermod/pkg/sink/stdout"
	sourcecassandra "github.com/user/hermod/pkg/source/cassandra"
	sourceclickhouse "github.com/user/hermod/pkg/source/clickhouse"
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
}

type activeEngine struct {
	engine            *engine.Engine
	cancel            context.CancelFunc
	done              <-chan struct{}
	srcConfig         SourceConfig
	snkConfigs        []SinkConfig
	transformations   []storage.Transformation
	transformationIDs []string
}

func NewRegistry(s storage.Storage) *Registry {
	return &Registry{
		engines: make(map[string]*activeEngine),
		storage: s,
	}
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

func (r *Registry) IsEngineRunning(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.engines[id]
	return ok
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
	if cfg.Type == "pipeline" {
		var steps []hermod.Transformer
		for _, stepCfg := range cfg.Steps {
			step, err := r.createTransformer(ctx, stepCfg)
			if err != nil {
				return nil, err
			}
			steps = append(steps, step)
		}
		return transformer.NewChain(steps...), nil
	}
	return transformer.NewTransformer(cfg.Type, cfg.Config)
}

func (r *Registry) StartEngine(id string, srcCfg SourceConfig, snkConfigs []SinkConfig, transformations []storage.Transformation, transformationIDs []string) error {
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
	if r.storage != nil {
		eng.SetLogger(NewDatabaseLogger(ctx, r.storage))
	}

	sinkIDs := make([]string, len(snkConfigs))
	for i, cfg := range snkConfigs {
		sinkIDs[i] = cfg.ID
	}
	eng.SetIDs(id, srcCfg.ID, sinkIDs)

	// Transformations
	t, err := r.createTransformers(ctx, transformations, transformationIDs)
	if err != nil {
		return fmt.Errorf("failed to create transformer: %w", err)
	}
	if t != nil {
		eng.SetTransformations(t)
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
	}()

	r.engines[id] = &activeEngine{
		engine:            eng,
		cancel:            cancel,
		done:              done,
		srcConfig:         srcCfg,
		snkConfigs:        snkConfigs,
		transformations:   transformations,
		transformationIDs: transformationIDs,
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
		_ = r.StopEngine(id)
	}
}

func (r *Registry) StopEngine(id string) error {
	r.mu.Lock()
	ae, ok := r.engines[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("engine %s not running", id)
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

type SourceConfig struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}

type SinkConfig struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}

func (r *Registry) IsResourceInUse(ctx context.Context, resourceID string, excludeConnID string, isSource bool) bool {
	conns, err := r.storage.ListConnections(ctx)
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

	host := cfg["host"]
	port := cfg["port"]
	user := cfg["user"]
	password := cfg["password"]
	dbname := cfg["dbname"]

	switch sourceType {
	case "postgres", "yugabyte":
		sslmode := cfg["sslmode"]
		if sslmode == "" {
			sslmode = "disable"
		}
		return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", user, password, host, port, dbname, sslmode)
	case "mysql", "mariadb":
		return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", user, password, host, port, dbname)
	case "mssql":
		return fmt.Sprintf("sqlserver://%s:%s@%s:%s?database=%s", user, password, host, port, dbname)
	case "oracle":
		return fmt.Sprintf("oracle://%s:%s@%s:%s/%s", user, password, host, port, dbname)
	case "db2":
		return fmt.Sprintf("HOSTNAME=%s;PORT=%s;UID=%s;PWD=%s;DATABASE=%s;PROTOCOL=TCPIP", host, port, user, password, dbname)
	case "clickhouse":
		return fmt.Sprintf("clickhouse://%s:%s@%s:%s/%s", user, password, host, port, dbname)
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
				uri = fmt.Sprintf("mongodb://%s:%s@%s:%s", user, password, host, port)
			} else {
				uri = fmt.Sprintf("mongodb://%s:%s", host, port)
			}
		}
		return sourcemongodb.NewMongoDBSource(uri), nil
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
	if cfg.Config["format"] == "json" {
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
	default:
		return nil, fmt.Errorf("unsupported sink type: %s", cfg.Type)
	}
}
