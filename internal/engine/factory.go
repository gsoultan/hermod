package engine

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/gsmail"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/pkg/compression"
	"github.com/user/hermod/pkg/eventstore"
	jsonfmt "github.com/user/hermod/pkg/formatter/json"
	"github.com/user/hermod/pkg/idempotency"
	sinkclickhouse "github.com/user/hermod/pkg/sink/clickhouse"
	"github.com/user/hermod/pkg/sink/elasticsearch"
	"github.com/user/hermod/pkg/sink/file"
	sinkftp "github.com/user/hermod/pkg/sink/ftp"
	sinkgooglesheets "github.com/user/hermod/pkg/sink/googlesheets"
	sinkhttp "github.com/user/hermod/pkg/sink/http"
	sinkkafka "github.com/user/hermod/pkg/sink/kafka"
	"github.com/user/hermod/pkg/sink/kinesis"
	sinkmongodb "github.com/user/hermod/pkg/sink/mongodb"
	sinkmysql "github.com/user/hermod/pkg/sink/mysql"
	sinknats "github.com/user/hermod/pkg/sink/nats"
	"github.com/user/hermod/pkg/sink/pgvector"
	sinkpostgres "github.com/user/hermod/pkg/sink/postgres"
	"github.com/user/hermod/pkg/sink/pubsub"
	"github.com/user/hermod/pkg/sink/pulsar"
	sinkrabbitmq "github.com/user/hermod/pkg/sink/rabbitmq"
	sinkredis "github.com/user/hermod/pkg/sink/redis"
	"github.com/user/hermod/pkg/sink/s3"
	"github.com/user/hermod/pkg/sink/s3parquet"
	"github.com/user/hermod/pkg/sink/salesforce"
	sinksap "github.com/user/hermod/pkg/sink/sap"
	"github.com/user/hermod/pkg/sink/servicenow"
	"github.com/user/hermod/pkg/sink/smtp"
	"github.com/user/hermod/pkg/sink/snowflake"
	sinksqlite "github.com/user/hermod/pkg/sink/sqlite"
	"github.com/user/hermod/pkg/sink/stdout"
	"github.com/user/hermod/pkg/sink/telegram"
	sourcecassandra "github.com/user/hermod/pkg/source/cassandra"
	sourceclickhouse "github.com/user/hermod/pkg/source/clickhouse"
	"github.com/user/hermod/pkg/source/cron"
	sourcecsv "github.com/user/hermod/pkg/source/csv"
	"github.com/user/hermod/pkg/source/db2"
	sourceform "github.com/user/hermod/pkg/source/form"
	sourcegooglesheets "github.com/user/hermod/pkg/source/googlesheets"
	sourcegraphql "github.com/user/hermod/pkg/source/graphql"
	grpcsource "github.com/user/hermod/pkg/source/grpc"
	sourcekafka "github.com/user/hermod/pkg/source/kafka"
	sourcemainframe "github.com/user/hermod/pkg/source/mainframe"
	"github.com/user/hermod/pkg/source/mariadb"
	sourcemongodb "github.com/user/hermod/pkg/source/mongodb"
	"github.com/user/hermod/pkg/source/mssql"
	"github.com/user/hermod/pkg/source/mysql"
	sourcenats "github.com/user/hermod/pkg/source/nats"
	"github.com/user/hermod/pkg/source/oracle"
	sourcepostgres "github.com/user/hermod/pkg/source/postgres"
	sourcerabbitmq "github.com/user/hermod/pkg/source/rabbitmq"
	sourceredis "github.com/user/hermod/pkg/source/redis"
	sourcesap "github.com/user/hermod/pkg/source/sap"
	sourcescylladb "github.com/user/hermod/pkg/source/scylladb"
	sourcesqlite "github.com/user/hermod/pkg/source/sqlite"
	"github.com/user/hermod/pkg/source/webhook"
	"github.com/user/hermod/pkg/source/yugabyte"
	"github.com/user/hermod/pkg/transformer"
)

// smtpIdemAdapter adapts the SQLite idempotency store to the SMTP sink interface.
type smtpIdemAdapter struct{ s *idempotency.SQLiteStore }

func (a smtpIdemAdapter) Claim(ctx context.Context, key string) (bool, error) {
	return a.s.Claim(ctx, key)
}
func (a smtpIdemAdapter) MarkSent(ctx context.Context, key string) error {
	return a.s.MarkSent(ctx, key)
}

type wasmSinkAdapter struct {
	transformer transformer.Transformer
	config      map[string]string
}

func (a *wasmSinkAdapter) Write(ctx context.Context, msg hermod.Message) error {
	// Convert map[string]string to map[string]interface{}
	cfg := make(map[string]interface{})
	for k, v := range a.config {
		cfg[k] = v
	}
	_, err := a.transformer.Transform(ctx, msg, cfg)
	return err
}

func (a *wasmSinkAdapter) Ping(ctx context.Context) error {
	return nil
}

func (a *wasmSinkAdapter) Close() error {
	return nil
}

func CreateSource(cfg SourceConfig) (hermod.Source, error) {
	// Substitute environment variables in config
	for k, v := range cfg.Config {
		cfg.Config[k] = config.SubstituteEnvVars(v)
	}

	connString := BuildConnectionString(cfg.Config, cfg.Type)

	tables := []string{}
	if t, ok := cfg.Config["tables"]; ok && t != "" {
		tables = strings.Split(t, ",")
		for i, table := range tables {
			tables[i] = strings.TrimSpace(table)
		}
	}

	useCDC := cfg.Config["use_cdc"] != "false"
	idField := cfg.Config["id_field"]
	pollInterval, _ := time.ParseDuration(cfg.Config["poll_interval"])

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
		src = oracle.NewOracleSource(connString, tables, idField, pollInterval, useCDC)
	case "db2":
		src = db2.NewDB2Source(connString, tables, idField, pollInterval, useCDC)
	case "mainframe":
		mfCfg := sourcemainframe.Config{
			Host:     cfg.Config["host"],
			Port:     80, // Default or parse from config
			User:     cfg.Config["user"],
			Password: cfg.Config["password"],
			Database: cfg.Config["database"],
			Schema:   cfg.Config["schema"],
			Table:    cfg.Config["table"],
			Type:     cfg.Config["type"],
			Interval: cfg.Config["interval"],
		}
		if p, ok := cfg.Config["port"]; ok {
			var port int
			fmt.Sscanf(p, "%d", &port)
			mfCfg.Port = port
		}
		src = sourcemainframe.NewSource(mfCfg, nil)
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
		src = sourcemongodb.NewMongoDBSource(uri, cfg.Config["database"], cfg.Config["collection"], useCDC)
	case "mariadb":
		src = mariadb.NewMariaDBSource(connString, tables, idField, pollInterval, useCDC)
	case "cassandra":
		hosts := []string{"localhost"}
		if h, ok := cfg.Config["hosts"]; ok && h != "" {
			hosts = strings.Split(h, ",")
		}
		src = sourcecassandra.NewCassandraSource(hosts, tables, idField, pollInterval, useCDC)
	case "yugabyte":
		src = yugabyte.NewYugabyteSource(connString, tables, idField, pollInterval, useCDC)
	case "scylladb":
		hosts := []string{"localhost"}
		if h, ok := cfg.Config["hosts"]; ok && h != "" {
			hosts = strings.Split(h, ",")
		}
		src = sourcescylladb.NewScyllaDBSource(hosts, tables, idField, pollInterval, useCDC)
	case "clickhouse":
		src = sourceclickhouse.NewClickHouseSource(connString, tables, idField, pollInterval, useCDC)
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
	case "eventstore":
		driver := cfg.Config["driver"]
		dsn := cfg.Config["dsn"]
		if dsn == "" {
			dsn = BuildConnectionString(cfg.Config, driver)
		}
		db, err := sql.Open(driver, dsn)
		if err != nil {
			return nil, err
		}
		store, err := eventstore.NewSQLStore(db, driver)
		if err != nil {
			return nil, err
		}
		fromOffset, _ := strconv.ParseInt(cfg.Config["from_offset"], 10, 64)
		esSource := eventstore.NewEventStoreSource(store, fromOffset)
		if sid, ok := cfg.Config["stream_id"]; ok && sid != "" {
			esSource.SetStreamID(sid)
		}
		if pi, ok := cfg.Config["poll_interval"]; ok && pi != "" {
			if dur, err := time.ParseDuration(pi); err == nil {
				esSource.SetPollInterval(dur)
			}
		}
		src = esSource
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
	case "graphql":
		src = sourcegraphql.NewGraphQLSource(cfg.Config["path"])
	case "grpc":
		src = grpcsource.NewGrpcSource(cfg.Config["path"])
	case "form":
		src = sourceform.NewFormSource(cfg.Config["path"], nil) // Storage will be injected by Registry
	case "cron":
		src = cron.NewCronSource(cfg.Config["schedule"], cfg.Config["payload"])
	case "sap":
		sapCfg := sourcesap.SourceConfig{
			Host:         cfg.Config["host"],
			Client:       cfg.Config["client"],
			Username:     cfg.Config["username"],
			Password:     cfg.Config["password"],
			Service:      cfg.Config["service"],
			Entity:       cfg.Config["entity"],
			PollInterval: cfg.Config["poll_interval"],
			Filter:       cfg.Config["filter"],
		}
		src = sourcesap.NewSource(sapCfg, nil)
	case "googlesheets":
		pollInterval, _ := time.ParseDuration(cfg.Config["poll_interval"])
		src = sourcegooglesheets.NewGoogleSheetsSource(
			cfg.Config["spreadsheet_id"],
			cfg.Config["range"],
			cfg.Config["credentials_json"],
			pollInterval,
		)
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
	// Substitute environment variables in config
	for k, v := range cfg.Config {
		cfg.Config[k] = config.SubstituteEnvVars(v)
	}

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
		return sinkkafka.NewKafkaSink(brokers, cfg.Config["topic"], cfg.Config["username"], cfg.Config["password"], fmttr, cfg.Config["transactional_id"]), nil
	case "postgres", "yugabyte", "mssql", "oracle":
		return sinkpostgres.NewPostgresSink(BuildConnectionString(cfg.Config, cfg.Type)), nil
	case "pgvector":
		connString := BuildConnectionString(cfg.Config, "postgres")
		return pgvector.NewSink(
			connString,
			cfg.Config["table"],
			cfg.Config["vector_column"],
			cfg.Config["id_column"],
			cfg.Config["metadata_column"],
		), nil
	case "mysql", "mariadb":
		return sinkmysql.NewMySQLSink(BuildConnectionString(cfg.Config, cfg.Type)), nil
	case "sqlite":
		return sinksqlite.NewSQLiteSink(BuildConnectionString(cfg.Config, cfg.Type)), nil
	case "eventstore":
		driver := cfg.Config["driver"]
		dsn := cfg.Config["dsn"]
		if dsn == "" {
			dsn = BuildConnectionString(cfg.Config, driver)
		}
		db, err := sql.Open(driver, dsn)
		if err != nil {
			return nil, err
		}
		store, err := eventstore.NewSQLStore(db, driver)
		if err != nil {
			return nil, err
		}
		store.SetTemplates(cfg.Config["stream_id_tpl"], cfg.Config["event_type_tpl"])
		return store, nil
	case "clickhouse":
		return sinkclickhouse.NewClickHouseSink(cfg.Config["addr"], cfg.Config["database"]), nil
	case "mongodb":
		return sinkmongodb.NewMongoDBSink(cfg.Config["uri"], cfg.Config["database"]), nil
	case "snowflake":
		return snowflake.NewSink(cfg.Config["connection_string"], fmttr), nil
	case "wasm":
		t, ok := transformer.Get("wasm")
		if !ok {
			return nil, fmt.Errorf("wasm transformer not registered")
		}
		return &wasmSinkAdapter{
			transformer: t,
			config:      cfg.Config,
		}, nil
	case "sap":
		sapCfg := sinksap.Config{
			Host:     cfg.Config["host"],
			Client:   cfg.Config["client"],
			Protocol: cfg.Config["protocol"],
			BAPIName: cfg.Config["bapi_name"],
			IDOCName: cfg.Config["idoc_name"],
			Username: cfg.Config["username"],
			Password: cfg.Config["password"],
			Service:  cfg.Config["service"],
			Entity:   cfg.Config["entity"],
		}
		return sinksap.NewSink(sapCfg, nil), nil
	case "salesforce":
		return salesforce.NewSalesforceSink(
			cfg.Config["client_id"],
			cfg.Config["client_secret"],
			cfg.Config["username"],
			cfg.Config["password"],
			cfg.Config["security_token"],
			cfg.Config["object"],
			cfg.Config["operation"],
			cfg.Config["external_id"],
		), nil
	case "servicenow":
		return servicenow.NewSink(servicenow.Config{
			InstanceURL: cfg.Config["instance_url"],
			Username:    cfg.Config["username"],
			Password:    cfg.Config["password"],
			Table:       cfg.Config["table"],
		}), nil
	case "elasticsearch":
		addresses := strings.Split(cfg.Config["addresses"], ",")
		return elasticsearch.NewElasticsearchSink(
			addresses,
			cfg.Config["username"],
			cfg.Config["password"],
			cfg.Config["api_key"],
			cfg.Config["index"],
			fmttr,
		)
	case "pulsar":
		return pulsar.NewPulsarSink(cfg.Config["url"], cfg.Config["topic"], cfg.Config["token"], fmttr)
	case "kinesis":
		return kinesis.NewKinesisSink(cfg.Config["region"], cfg.Config["stream_name"], cfg.Config["access_key"], cfg.Config["secret_key"], fmttr)
	case "s3":
		return s3.NewS3Sink(
			context.Background(),
			cfg.Config["region"],
			cfg.Config["bucket"],
			cfg.Config["key_prefix"],
			cfg.Config["access_key"],
			cfg.Config["secret_key"],
			cfg.Config["endpoint"],
			fmttr,
			cfg.Config["suffix"],
			cfg.Config["content_type"],
		)
	case "s3-parquet":
		parallelizer, _ := strconv.ParseInt(cfg.Config["parallelizer"], 10, 64)
		return s3parquet.NewS3ParquetSink(
			context.Background(),
			cfg.Config["region"],
			cfg.Config["bucket"],
			cfg.Config["key_prefix"],
			cfg.Config["access_key"],
			cfg.Config["secret_key"],
			cfg.Config["endpoint"],
			cfg.Config["schema"],
			parallelizer,
		)
	case "ftp":
		// Defaults
		port, _ := strconv.Atoi(cfg.Config["port"])
		if port == 0 {
			port = 21
		}
		tls := cfg.Config["tls"] == "true"
		mkdirs := cfg.Config["mkdirs"] != "false"
		timeout := 30 * time.Second
		if t, ok := cfg.Config["timeout"]; ok && t != "" {
			if d, err := time.ParseDuration(t); err == nil {
				timeout = d
			}
		}
		sink, err := sinkftp.NewFTPSink(
			cfg.Config["host"],
			port,
			cfg.Config["username"],
			cfg.Config["password"],
			tls,
			timeout,
			cfg.Config["root_dir"],
			cfg.Config["path_template"],
			cfg.Config["filename_template"],
			cfg.Config["write_mode"],
			mkdirs,
			fmttr,
		)
		if err != nil {
			return nil, err
		}
		return sink, nil
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
		sink := sinkhttp.NewHttpSink(cfg.Config["url"], fmttr, headers)
		if algo := cfg.Config["compression"]; algo != "" {
			if comp, err := compression.NewCompressor(compression.Algorithm(algo)); err == nil {
				sink.SetCompressor(comp)
			}
		}
		return sink, nil
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
		s := smtp.NewSmtpSink(
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
			cfg.Config["outlook_compatible"] == "true",
		)

		// Wire idempotency settings if enabled
		if cfg.Config["enable_idempotency"] == "true" {
			// default to local hermod.db if not provided
			dsn := cfg.Config["idempotency_dsn"]
			if dsn == "" {
				dsn = "hermod.db"
			}
			store, err := idempotency.NewSQLiteStore(dsn)
			if err != nil {
				return nil, fmt.Errorf("init idempotency store: %w", err)
			}
			s.EnableIdempotency(true)
			s.SetIdempotencyStore(smtpIdemAdapter{s: store})
			s.SetIdempotencyKeyTemplate(cfg.Config["idempotency_key_template"])
		}

		return s, nil
	case "telegram":
		return telegram.NewTelegramSink(cfg.Config["token"], cfg.Config["chat_id"], fmttr), nil
	case "googlesheets":
		return sinkgooglesheets.NewGoogleSheetsSink(
			cfg.Config["spreadsheet_id"],
			cfg.Config["range"],
			cfg.Config["operation"],
			cfg.Config["credentials_json"],
			cfg.Config["row_index"],
			cfg.Config["column_index"],
		), nil
	default:
		return nil, fmt.Errorf("unsupported sink type: %s", cfg.Type)
	}
}
