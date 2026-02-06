package engine

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"crypto/tls"
	"crypto/x509"

	"github.com/gsoultan/gsmail"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/pkg/compression"
	"github.com/user/hermod/pkg/eventstore"
	jsonfmt "github.com/user/hermod/pkg/formatter/json"
	"github.com/user/hermod/pkg/idempotency"
	sinkclickhouse "github.com/user/hermod/pkg/sink/clickhouse"
	"github.com/user/hermod/pkg/sink/discord"
	sinkdynamics365 "github.com/user/hermod/pkg/sink/dynamics365"
	"github.com/user/hermod/pkg/sink/elasticsearch"
	"github.com/user/hermod/pkg/sink/facebook"
	sinkfcm "github.com/user/hermod/pkg/sink/fcm"
	"github.com/user/hermod/pkg/sink/file"
	sinkftp "github.com/user/hermod/pkg/sink/ftp"
	sinkgooglesheets "github.com/user/hermod/pkg/sink/googlesheets"
	sinkhttp "github.com/user/hermod/pkg/sink/http"
	"github.com/user/hermod/pkg/sink/instagram"
	sinkkafka "github.com/user/hermod/pkg/sink/kafka"
	"github.com/user/hermod/pkg/sink/kinesis"
	"github.com/user/hermod/pkg/sink/linkedin"
	sinkmongodb "github.com/user/hermod/pkg/sink/mongodb"
	sinkmqtt "github.com/user/hermod/pkg/sink/mqtt"
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
	"github.com/user/hermod/pkg/sink/slack"
	"github.com/user/hermod/pkg/sink/smtp"
	"github.com/user/hermod/pkg/sink/snowflake"
	sinksqlite "github.com/user/hermod/pkg/sink/sqlite"
	"github.com/user/hermod/pkg/sink/sse"
	"github.com/user/hermod/pkg/sink/stdout"
	"github.com/user/hermod/pkg/sink/telegram"
	sinktiktok "github.com/user/hermod/pkg/sink/tiktok"
	"github.com/user/hermod/pkg/sink/twitter"
	sinkws "github.com/user/hermod/pkg/sink/websocket"
	sourcecassandra "github.com/user/hermod/pkg/source/cassandra"
	sourceclickhouse "github.com/user/hermod/pkg/source/clickhouse"
	"github.com/user/hermod/pkg/source/cron"
	"github.com/user/hermod/pkg/source/db2"
	sourcediscord "github.com/user/hermod/pkg/source/discord"
	sourcedynamics365 "github.com/user/hermod/pkg/source/dynamics365"
	sourcefacebook "github.com/user/hermod/pkg/source/facebook"
	sourcefile "github.com/user/hermod/pkg/source/file"
	"github.com/user/hermod/pkg/source/firebase"
	sourceform "github.com/user/hermod/pkg/source/form"
	"github.com/user/hermod/pkg/source/googleanalytics"
	sourcegooglesheets "github.com/user/hermod/pkg/source/googlesheets"
	sourcegraphql "github.com/user/hermod/pkg/source/graphql"
	grpcsource "github.com/user/hermod/pkg/source/grpc"
	sourcehttp "github.com/user/hermod/pkg/source/http"
	sourceinstagram "github.com/user/hermod/pkg/source/instagram"
	sourcekafka "github.com/user/hermod/pkg/source/kafka"
	sourcelinkedin "github.com/user/hermod/pkg/source/linkedin"
	sourcemainframe "github.com/user/hermod/pkg/source/mainframe"
	"github.com/user/hermod/pkg/source/mariadb"
	sourcemongodb "github.com/user/hermod/pkg/source/mongodb"
	sourcemqtt "github.com/user/hermod/pkg/source/mqtt"
	"github.com/user/hermod/pkg/source/mssql"
	"github.com/user/hermod/pkg/source/mysql"
	sourcenats "github.com/user/hermod/pkg/source/nats"
	"github.com/user/hermod/pkg/source/oracle"
	sourcepostgres "github.com/user/hermod/pkg/source/postgres"
	sourcerabbitmq "github.com/user/hermod/pkg/source/rabbitmq"
	sourceredis "github.com/user/hermod/pkg/source/redis"
	sourcesap "github.com/user/hermod/pkg/source/sap"
	sourcescylladb "github.com/user/hermod/pkg/source/scylladb"
	sourceslack "github.com/user/hermod/pkg/source/slack"
	sourcesqlite "github.com/user/hermod/pkg/source/sqlite"
	sourcetiktok "github.com/user/hermod/pkg/source/tiktok"
	sourcetwitter "github.com/user/hermod/pkg/source/twitter"
	"github.com/user/hermod/pkg/source/webhook"
	sourcews "github.com/user/hermod/pkg/source/websocket"
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
	case "mqtt":
		return sourcemqtt.NewSource(cfg.Config)
	case "file":
		format := cfg.Config["format"]
		if format == "csv" {
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
				src = sourcefile.NewHTTPCSVSource(cfg.Config["url"], delimiter, hasHeader, headers)
			} else if sourceType == "s3" {
				src = sourcefile.NewS3CSVSource(
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
				src = sourcefile.NewCSVSource(cfg.Config["file_path"], delimiter, hasHeader)
			}
		} else {
			// Generic file ingestion (raw payloads, multi-backend)
			poll, _ := time.ParseDuration(cfg.Config["poll_interval"])
			backend := strings.ToLower(cfg.Config["source_type"])
			gcfg := sourcefile.GenericConfig{
				PollInterval: poll,
				Format:       sourcefile.Format("raw"),
			}
			if f := strings.ToLower(cfg.Config["format"]); f != "" {
				gcfg.Format = sourcefile.Format(f)
			}
			gcfg.Pattern = cfg.Config["pattern"]
			gcfg.Recursive = cfg.Config["recursive"] == "true"
			switch backend {
			case "local", "file":
				gcfg.Backend = sourcefile.BackendLocal
				if v := cfg.Config["local_path"]; v != "" {
					gcfg.LocalPath = v
				} else {
					gcfg.LocalPath = cfg.Config["file_path"]
				}
			case "http":
				gcfg.Backend = sourcefile.BackendHTTP
				gcfg.URL = cfg.Config["url"]
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
				gcfg.Headers = headers
			case "ftp":
				gcfg.Backend = sourcefile.BackendFTP
				host := cfg.Config["ftp_host"]
				port := cfg.Config["ftp_port"]
				if port == "" {
					port = "21"
				}
				gcfg.FTPAddr = fmt.Sprintf("%s:%s", host, port)
				gcfg.FTPUser = cfg.Config["ftp_user"]
				gcfg.FTPPass = cfg.Config["ftp_password"]
				gcfg.FTPRootDir = cfg.Config["ftp_root"]
			case "s3":
				gcfg.Backend = sourcefile.BackendS3
				gcfg.S3Region = cfg.Config["s3_region"]
				gcfg.S3Bucket = cfg.Config["s3_bucket"]
				gcfg.S3Prefix = cfg.Config["s3_key"]
				gcfg.S3Endpoint = cfg.Config["s3_endpoint"]
				gcfg.S3AccessKey = cfg.Config["s3_access_key"]
				gcfg.S3SecretKey = cfg.Config["s3_secret_key"]
			default:
				gcfg.Backend = sourcefile.BackendLocal
				if v := cfg.Config["local_path"]; v != "" {
					gcfg.LocalPath = v
				} else {
					gcfg.LocalPath = cfg.Config["file_path"]
				}
			}
			src = sourcefile.NewGenericFileSource(gcfg)
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
		// Conservative pool defaults for event store connections
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(10)
		db.SetConnMaxIdleTime(60 * time.Second)
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
	case "dynamics365":
		d365Cfg := sourcedynamics365.SourceConfig{
			Resource:     cfg.Config["resource"],
			TenantID:     cfg.Config["tenant_id"],
			ClientID:     cfg.Config["client_id"],
			ClientSecret: cfg.Config["client_secret"],
			Entity:       cfg.Config["entity"],
			PollInterval: cfg.Config["poll_interval"],
			Filter:       cfg.Config["filter"],
			IDField:      cfg.Config["id_field"],
		}
		src = sourcedynamics365.NewSource(d365Cfg, nil)
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
		interval, _ := time.ParseDuration(cfg.Config["poll_interval"])
		src = sourcehttp.NewHTTPSource(
			cfg.Config["url"],
			cfg.Config["method"],
			headers,
			interval,
			cfg.Config["data_path"],
		)
	case "googlesheets":
		pollInterval, _ := time.ParseDuration(cfg.Config["poll_interval"])
		src = sourcegooglesheets.NewGoogleSheetsSource(
			cfg.Config["spreadsheet_id"],
			cfg.Config["range"],
			cfg.Config["credentials_json"],
			pollInterval,
		)
	case "discord":
		src = sourcediscord.NewDiscordSource(
			cfg.Config["token"],
			cfg.Config["channel_id"],
			pollInterval,
		)
	case "slack":
		src = sourceslack.NewSlackSource(
			cfg.Config["token"],
			cfg.Config["channel_id"],
			pollInterval,
		)
	case "twitter":
		src = sourcetwitter.NewTwitterSource(
			cfg.Config["token"],
			cfg.Config["query"],
			pollInterval,
			cfg.Config["mode"],
		)
	case "facebook":
		src = sourcefacebook.NewFacebookSource(
			cfg.Config["access_token"],
			cfg.Config["page_id"],
			pollInterval,
			cfg.Config["mode"],
		)
	case "instagram":
		src = sourceinstagram.NewInstagramSource(
			cfg.Config["access_token"],
			cfg.Config["ig_user_id"],
			pollInterval,
			cfg.Config["mode"],
		)
	case "tiktok":
		src = sourcetiktok.NewTikTokSource(
			cfg.Config["access_token"],
			pollInterval,
			cfg.Config["mode"],
		)
	case "linkedin":
		src = sourcelinkedin.NewLinkedInSource(
			cfg.Config["access_token"],
			cfg.Config["person_urn"],
			pollInterval,
		)
	case "googleanalytics":
		pollInterval, _ := time.ParseDuration(cfg.Config["poll_interval"])
		src = googleanalytics.NewGoogleAnalyticsSource(
			cfg.Config["property_id"],
			cfg.Config["credentials_json"],
			cfg.Config["metrics"],
			cfg.Config["dimensions"],
			pollInterval,
		)
	case "firebase":
		pollInterval, _ := time.ParseDuration(cfg.Config["poll_interval"])
		src = firebase.NewFirebaseSource(
			cfg.Config["project_id"],
			cfg.Config["collection"],
			cfg.Config["credentials_json"],
			cfg.Config["timestamp_field"],
			pollInterval,
		)
	case "websocket":
		// Headers: "K:V,K2:V2"
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
		var subprotocols []string
		if sp := strings.TrimSpace(cfg.Config["subprotocols"]); sp != "" {
			for _, p := range strings.Split(sp, ",") {
				if t := strings.TrimSpace(p); t != "" {
					subprotocols = append(subprotocols, t)
				}
			}
		}
		ct, _ := time.ParseDuration(cfg.Config["connect_timeout"])
		rt, _ := time.ParseDuration(cfg.Config["read_timeout"])
		hb, _ := time.ParseDuration(cfg.Config["heartbeat_interval"])
		rb, _ := time.ParseDuration(cfg.Config["reconnect_base"])
		rm, _ := time.ParseDuration(cfg.Config["reconnect_max"])
		var maxBytes int64
		if v := strings.TrimSpace(cfg.Config["max_message_bytes"]); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				maxBytes = n
			}
		}
		src = sourcews.New(
			cfg.Config["url"],
			headers,
			subprotocols,
			ct,
			rt,
			hb,
			rb,
			rm,
			maxBytes,
		)
		// Optional TLS configuration for WS client
		if tlsCfg, pin := buildWSTLSConfig(cfg.Config); tlsCfg != nil {
			if ws, ok := src.(interface{ SetTLSConfig(*tls.Config, string) }); ok {
				ws.SetTLSConfig(tlsCfg, pin)
			}
		}
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
	case "mqtt":
		return sinkmqtt.New(cfg.Config, fmttr)
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
	case "dynamics365":
		d365Cfg := sinkdynamics365.Config{
			Resource:     cfg.Config["resource"],
			TenantID:     cfg.Config["tenant_id"],
			ClientID:     cfg.Config["client_id"],
			ClientSecret: cfg.Config["client_secret"],
			Entity:       cfg.Config["entity"],
			Operation:    cfg.Config["operation"],
			ExternalID:   cfg.Config["external_id"],
		}
		return sinkdynamics365.NewSink(d365Cfg, nil), nil
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
	case "websocket":
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
		var subprotocols []string
		if sp := strings.TrimSpace(cfg.Config["subprotocols"]); sp != "" {
			for _, p := range strings.Split(sp, ",") {
				if t := strings.TrimSpace(p); t != "" {
					subprotocols = append(subprotocols, t)
				}
			}
		}
		ct, _ := time.ParseDuration(cfg.Config["connect_timeout"])
		wt, _ := time.ParseDuration(cfg.Config["write_timeout"])
		hb, _ := time.ParseDuration(cfg.Config["heartbeat_interval"])
		requireAck := cfg.Config["require_ack"] == "true"
		s := sinkws.New(
			cfg.Config["url"],
			headers,
			subprotocols,
			ct,
			wt,
			hb,
			requireAck,
			fmttr,
		)
		if tlsCfg, pin := buildWSTLSConfig(cfg.Config); tlsCfg != nil {
			s.SetTLSConfig(tlsCfg, pin)
		}
		return s, nil
	case "stdout":
		return stdout.NewStdoutSink(fmttr), nil
	case "sse":
		stream := cfg.Config["stream"]
		return sse.NewSSESink(stream, fmttr), nil
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
			// optional namespace -> table suffix
			table := "smtp_idempotency"
			if ns := cfg.Config["idempotency_namespace"]; ns != "" {
				// sanitize namespace to alnum/underscore
				sanitized := make([]rune, 0, len(ns))
				for _, r := range ns {
					if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
						sanitized = append(sanitized, r)
					}
				}
				if len(sanitized) > 0 {
					table = fmt.Sprintf("smtp_idempotency_%s", string(sanitized))
				}
			}
			store, err := idempotency.NewSQLiteStoreWithTable(dsn, table)
			if err != nil {
				return nil, fmt.Errorf("init idempotency store: %w", err)
			}
			s.EnableIdempotency(true)
			s.SetIdempotencyStore(smtpIdemAdapter{s: store})
			s.SetIdempotencyKeyTemplate(cfg.Config["idempotency_key_template"])

			// optional TTL cleanup in background
			if ttlStr := cfg.Config["idempotency_ttl"]; ttlStr != "" {
				if ttl, err := time.ParseDuration(ttlStr); err == nil && ttl > 0 {
					go func() {
						ticker := time.NewTicker(1 * time.Hour)
						defer ticker.Stop()
						ctx := context.Background()
						// initial cleanup
						_ = store.CleanupTTL(ctx, ttl)
						for range ticker.C {
							_ = store.CleanupTTL(ctx, ttl)
						}
					}()
				}
			}
		}

		return s, nil
	case "telegram":
		return telegram.NewTelegramSink(cfg.Config["token"], cfg.Config["chat_id"], fmttr), nil
	case "discord":
		return discord.NewDiscordSink(
			cfg.Config["webhook_url"],
			cfg.Config["token"],
			cfg.Config["channel_id"],
			fmttr,
		), nil
	case "slack":
		return slack.NewSlackSink(
			cfg.Config["webhook_url"],
			cfg.Config["token"],
			cfg.Config["channel_id"],
			fmttr,
		), nil
	case "twitter":
		return twitter.NewTwitterSink(cfg.Config["token"], fmttr), nil
	case "facebook":
		return facebook.NewFacebookSink(cfg.Config["access_token"], cfg.Config["page_id"], fmttr), nil
	case "instagram":
		return instagram.NewInstagramSink(cfg.Config["access_token"], cfg.Config["ig_user_id"], fmttr), nil
	case "linkedin":
		return linkedin.NewLinkedInSink(cfg.Config["access_token"], cfg.Config["person_urn"], fmttr), nil
	case "tiktok":
		return sinktiktok.NewTikTokSink(cfg.Config["access_token"], fmttr), nil
	case "fcm":
		return sinkfcm.NewFCMSinkWithDefaults(
			cfg.Config["credentials_json"],
			cfg.Config["device_token"],
			cfg.Config["topic"],
			cfg.Config["condition"],
			fmttr,
		)
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

// buildWSTLSConfig constructs a tls.Config from common WebSocket TLS keys.
// Supported keys:
// - insecure_skip_verify: "true" to skip verification (not recommended)
// - server_name: SNI override
// - ca_cert_pem: custom root certificate(s) in PEM format
// - pin_sha256: base64-encoded SHA256 of the peer leaf certificate (returned separately)
func buildWSTLSConfig(m map[string]string) (*tls.Config, string) {
	insecure := strings.EqualFold(strings.TrimSpace(m["insecure_skip_verify"]), "true")
	serverName := strings.TrimSpace(m["server_name"])
	caPEM := strings.TrimSpace(m["ca_cert_pem"])
	pin := strings.TrimSpace(m["pin_sha256"]) // return separately

	if !insecure && serverName == "" && caPEM == "" {
		// No TLS customization; let defaults apply
		if pin == "" {
			return nil, ""
		}
		// Pinning without other options still requires a tls.Config instance
		return &tls.Config{}, pin
	}

	cfg := &tls.Config{}
	if insecure {
		cfg.InsecureSkipVerify = true
	}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	if caPEM != "" {
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM([]byte(caPEM)); ok {
			cfg.RootCAs = pool
		}
	}
	return cfg, pin
}
