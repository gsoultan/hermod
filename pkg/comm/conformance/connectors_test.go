package conformance_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/conformance"
	jsonfmt "github.com/gsoultan/Hermod/pkg/comm/formatter/json"
	"github.com/gsoultan/Hermod/pkg/infra/sqlutil"
	"github.com/gsoultan/gsmail"

	sinkcassandra "github.com/gsoultan/Hermod/pkg/comm/sink/cassandra"
	sinkclickhouse "github.com/gsoultan/Hermod/pkg/comm/sink/clickhouse"
	sinkdiscord "github.com/gsoultan/Hermod/pkg/comm/sink/discord"
	sinkdynamics365 "github.com/gsoultan/Hermod/pkg/comm/sink/dynamics365"
	sinkelasticsearch "github.com/gsoultan/Hermod/pkg/comm/sink/elasticsearch"
	sinkfacebook "github.com/gsoultan/Hermod/pkg/comm/sink/facebook"
	sinkfailover "github.com/gsoultan/Hermod/pkg/comm/sink/failover"
	sinkfcm "github.com/gsoultan/Hermod/pkg/comm/sink/fcm"
	sinkfile "github.com/gsoultan/Hermod/pkg/comm/sink/file"
	sinkftp "github.com/gsoultan/Hermod/pkg/comm/sink/ftp"
	sinkgooglesheets "github.com/gsoultan/Hermod/pkg/comm/sink/googlesheets"
	sinkhttp "github.com/gsoultan/Hermod/pkg/comm/sink/http"
	sinkinstagram "github.com/gsoultan/Hermod/pkg/comm/sink/instagram"
	sinkkafka "github.com/gsoultan/Hermod/pkg/comm/sink/kafka"
	sinkkinesis "github.com/gsoultan/Hermod/pkg/comm/sink/kinesis"
	sinklinkedin "github.com/gsoultan/Hermod/pkg/comm/sink/linkedin"
	sinkmilvus "github.com/gsoultan/Hermod/pkg/comm/sink/milvus"
	sinkmongodb "github.com/gsoultan/Hermod/pkg/comm/sink/mongodb"
	sinkmqtt "github.com/gsoultan/Hermod/pkg/comm/sink/mqtt"
	sinkmssql "github.com/gsoultan/Hermod/pkg/comm/sink/mssql"
	sinkmysql "github.com/gsoultan/Hermod/pkg/comm/sink/mysql"
	sinknats "github.com/gsoultan/Hermod/pkg/comm/sink/nats"
	sinkoracle "github.com/gsoultan/Hermod/pkg/comm/sink/oracle"
	sinkpgvector "github.com/gsoultan/Hermod/pkg/comm/sink/pgvector"
	sinkpinecone "github.com/gsoultan/Hermod/pkg/comm/sink/pinecone"
	sinkpostgres "github.com/gsoultan/Hermod/pkg/comm/sink/postgres"
	sinkpubsub "github.com/gsoultan/Hermod/pkg/comm/sink/pubsub"
	sinkpulsar "github.com/gsoultan/Hermod/pkg/comm/sink/pulsar"
	sinkrabbitmq "github.com/gsoultan/Hermod/pkg/comm/sink/rabbitmq"
	sinkredis "github.com/gsoultan/Hermod/pkg/comm/sink/redis"
	sinks3 "github.com/gsoultan/Hermod/pkg/comm/sink/s3"
	sinks3parquet "github.com/gsoultan/Hermod/pkg/comm/sink/s3parquet"
	sinksalesforce "github.com/gsoultan/Hermod/pkg/comm/sink/salesforce"
	sinksap "github.com/gsoultan/Hermod/pkg/comm/sink/sap"
	sinkservicenow "github.com/gsoultan/Hermod/pkg/comm/sink/servicenow"
	sinkslack "github.com/gsoultan/Hermod/pkg/comm/sink/slack"
	sinksmtp "github.com/gsoultan/Hermod/pkg/comm/sink/smtp"
	sinksnowflake "github.com/gsoultan/Hermod/pkg/comm/sink/snowflake"
	sinksqlite "github.com/gsoultan/Hermod/pkg/comm/sink/sqlite"
	sinksse "github.com/gsoultan/Hermod/pkg/comm/sink/sse"
	sinkstdout "github.com/gsoultan/Hermod/pkg/comm/sink/stdout"
	sinktelegram "github.com/gsoultan/Hermod/pkg/comm/sink/telegram"
	sinktiktok "github.com/gsoultan/Hermod/pkg/comm/sink/tiktok"
	sinktwitter "github.com/gsoultan/Hermod/pkg/comm/sink/twitter"
	sinkwebsocket "github.com/gsoultan/Hermod/pkg/comm/sink/websocket"

	srcbatchsql "github.com/gsoultan/Hermod/pkg/comm/source/batchsql"
	srccassandra "github.com/gsoultan/Hermod/pkg/comm/source/cassandra"
	srccdc "github.com/gsoultan/Hermod/pkg/comm/source/cdc"
	srcclickhouse "github.com/gsoultan/Hermod/pkg/comm/source/clickhouse"
	srccron "github.com/gsoultan/Hermod/pkg/comm/source/cron"
	srcdb2 "github.com/gsoultan/Hermod/pkg/comm/source/db2"
	srcdiscord "github.com/gsoultan/Hermod/pkg/comm/source/discord"
	srcdynamics365 "github.com/gsoultan/Hermod/pkg/comm/source/dynamics365"
	srcexcel "github.com/gsoultan/Hermod/pkg/comm/source/excel"
	srcfacebook "github.com/gsoultan/Hermod/pkg/comm/source/facebook"
	srcfile "github.com/gsoultan/Hermod/pkg/comm/source/file"
	srcfirebase "github.com/gsoultan/Hermod/pkg/comm/source/firebase"
	srcform "github.com/gsoultan/Hermod/pkg/comm/source/form"
	srcgoogleanalytics "github.com/gsoultan/Hermod/pkg/comm/source/googleanalytics"
	srcgooglesheets "github.com/gsoultan/Hermod/pkg/comm/source/googlesheets"
	srcgraphql "github.com/gsoultan/Hermod/pkg/comm/source/graphql"
	srcgrpc "github.com/gsoultan/Hermod/pkg/comm/source/grpc"
	srchttp "github.com/gsoultan/Hermod/pkg/comm/source/http"
	srcinstagram "github.com/gsoultan/Hermod/pkg/comm/source/instagram"
	srckafka "github.com/gsoultan/Hermod/pkg/comm/source/kafka"
	srclinkedin "github.com/gsoultan/Hermod/pkg/comm/source/linkedin"
	srcmainframe "github.com/gsoultan/Hermod/pkg/comm/source/mainframe"
	srcmariadb "github.com/gsoultan/Hermod/pkg/comm/source/mariadb"
	srcmongodb "github.com/gsoultan/Hermod/pkg/comm/source/mongodb"
	srcmqtt "github.com/gsoultan/Hermod/pkg/comm/source/mqtt"
	srcmssql "github.com/gsoultan/Hermod/pkg/comm/source/mssql"
	srcmysql "github.com/gsoultan/Hermod/pkg/comm/source/mysql"
	srcnats "github.com/gsoultan/Hermod/pkg/comm/source/nats"
	srcoracle "github.com/gsoultan/Hermod/pkg/comm/source/oracle"
	srcpostgres "github.com/gsoultan/Hermod/pkg/comm/source/postgres"
	srcrabbitmq "github.com/gsoultan/Hermod/pkg/comm/source/rabbitmq"
	srcredis "github.com/gsoultan/Hermod/pkg/comm/source/redis"
	srcsap "github.com/gsoultan/Hermod/pkg/comm/source/sap"
	srcscylladb "github.com/gsoultan/Hermod/pkg/comm/source/scylladb"
	srcslack "github.com/gsoultan/Hermod/pkg/comm/source/slack"
	srcsqlite "github.com/gsoultan/Hermod/pkg/comm/source/sqlite"
	srctiktok "github.com/gsoultan/Hermod/pkg/comm/source/tiktok"
	srctwitter "github.com/gsoultan/Hermod/pkg/comm/source/twitter"
	srcwebhook "github.com/gsoultan/Hermod/pkg/comm/source/webhook"
	srcwebsocket "github.com/gsoultan/Hermod/pkg/comm/source/websocket"
	srcyugabyte "github.com/gsoultan/Hermod/pkg/comm/source/yugabyte"
)

// This file is the connector conformance registry.
//
// Adding a connector is one line. Every connector listed here is constructed
// UNCONNECTED — no address is expected to resolve and no server is expected to
// be running. The suite asserts the contract that holds regardless.
//
// Endpoints below point at loopback on a port nothing can be listening on, so
// every dial fails immediately with ECONNREFUSED.
//
// The obvious alternative — an unroutable address such as TEST-NET-1
// (192.0.2.0/24, RFC 5737) — models a black-holed network more faithfully, and
// was tried first. It made the suite unusable: each dial becomes a full TCP SYN
// retry chain, and with every connector dialling at once the kernel serialises
// them into a five-minute run. A suite that slow does not get run.
//
// Refused-immediately still exercises what this suite is for: no server is
// present, so every operation must return inside its deadline, surface an
// error rather than panic, and stay safe to Close twice. Behaviour under a
// silently dropped connection is a real concern, but it belongs in the
// integration tests where a firewall rule can create it deliberately.
const (
	// deadHost is loopback; port 1 requires root to bind, so nothing is there.
	deadHost = "127.0.0.1"
	deadAddr = deadHost + ":1"
	deadURL  = "http://127.0.0.1:1/x"
)

func fmtr() hermod.Formatter { return jsonfmt.NewJSONFormatter() }

// noMappings is the empty column-mapping set every SQL sink takes.
var noMappings []sqlutil.ColumnMapping

// sqlSinkArgs is the argument tail shared by every SQL sink constructor:
// mappings, useExistingTable, deleteStrategy, softDeleteColumn,
// softDeleteValue, operationMode, autoTruncate, autoSync.
//
// Spelled out once so a registry entry stays one readable line.
type sqlSinkCtor func(conn, table string) hermod.Sink

// sinkOrSkip registers a sink whose constructor returns an error.
//
// Some connectors dial, authenticate, or parse credentials during construction,
// so against a dead endpoint they never produce an instance to test. That is a
// legitimate design, not a failure — but it does mean the contract below cannot
// be checked without live infrastructure, and saying so beats silently omitting
// the connector and leaving the registry looking more complete than it is.
func sinkOrSkip(t *testing.T, name string, ctor func() (hermod.Sink, error)) {
	t.Helper()

	probe, err := ctor()
	if err != nil {
		t.Run(name+"/Constructible", func(t *testing.T) {
			t.Skipf("constructor needs live infrastructure or credentials, so the "+
				"contract suite cannot reach this connector: %v", err)
		})
		return
	}
	if probe != nil {
		_ = probe.Close()
	}

	conformance.RunSinkSuite(t, name, func() hermod.Sink {
		s, err := ctor()
		if err != nil {
			t.Fatalf("%s: constructor became non-deterministic: %v", name, err)
		}
		return s
	})
}

// sourceOrSkip is sourceOrSkip's counterpart for sources.
func sourceOrSkip(t *testing.T, name string, ctor func() (hermod.Source, error)) {
	t.Helper()

	probe, err := ctor()
	if err != nil {
		t.Run(name+"/Constructible", func(t *testing.T) {
			t.Skipf("constructor needs live infrastructure or credentials, so the "+
				"contract suite cannot reach this connector: %v", err)
		})
		return
	}
	if probe != nil {
		_ = probe.Close()
	}

	conformance.RunSourceSuite(t, name, func() hermod.Source {
		s, err := ctor()
		if err != nil {
			t.Fatalf("%s: constructor became non-deterministic: %v", name, err)
		}
		return s
	})
}

func TestSinkConformance(t *testing.T) {
	// SQL sinks share one long constructor shape; bind it once per engine.
	sqlSinks := map[string]sqlSinkCtor{
		"postgres": func(c, tbl string) hermod.Sink {
			return sinkpostgres.NewPostgresSink(c, tbl, noMappings, false, "", "", "", "", false, false)
		},
		"mysql": func(c, tbl string) hermod.Sink {
			return sinkmysql.NewMySQLSink(c, tbl, noMappings, false, "", "", "", "", false, false)
		},
		"mssql": func(c, tbl string) hermod.Sink {
			return sinkmssql.NewMSSQLSink(c, tbl, noMappings, false, "", "", "", "", false, false)
		},
		"oracle": func(c, tbl string) hermod.Sink {
			return sinkoracle.NewOracleSink(c, tbl, noMappings, false, "", "", "", "", false, false)
		},
		"clickhouse": func(c, tbl string) hermod.Sink {
			return sinkclickhouse.NewClickHouseSink(c, "d", tbl, noMappings, false, "", "", "", "", false, false)
		},
		"snowflake": func(c, tbl string) hermod.Sink {
			return sinksnowflake.NewSink(c, fmtr(), tbl, noMappings, false, "", "", "", "", false, false)
		},
	}
	for name, ctor := range sqlSinks {
		conformance.RunSinkSuite(t, name, func() hermod.Sink {
			return ctor("postgres://u:p@"+deadAddr+"/d", "t")
		})
	}

	// SQLite writes to a real local file; give it a scratch directory.
	dbPath := filepath.Join(t.TempDir(), "sink.db")
	conformance.RunSinkSuite(t, "sqlite", func() hermod.Sink {
		return sinksqlite.NewSQLiteSink(dbPath, "t", noMappings, false, "", "", "", "", false, false)
	})

	conformance.RunSinkSuite(t, "stdout", func() hermod.Sink {
		return sinkstdout.NewStdoutSink(fmtr())
	})
	conformance.RunSinkSuite(t, "http", func() hermod.Sink {
		return sinkhttp.NewHttpSink(deadURL, fmtr(), nil)
	})
	conformance.RunSinkSuite(t, "kafka", func() hermod.Sink {
		return sinkkafka.NewKafkaSink([]string{deadAddr}, "t", "", "", fmtr())
	})
	conformance.RunSinkSuite(t, "telegram", func() hermod.Sink {
		return sinktelegram.NewTelegramSink("token", "chat", fmtr())
	})
	conformance.RunSinkSuite(t, "slack", func() hermod.Sink {
		return sinkslack.NewSlackSink(deadURL, "", "", fmtr())
	})
	conformance.RunSinkSuite(t, "discord", func() hermod.Sink {
		return sinkdiscord.NewDiscordSink(deadURL, "", "", fmtr())
	})
	conformance.RunSinkSuite(t, "sse", func() hermod.Sink {
		return sinksse.NewSSESink("s", fmtr())
	})
	conformance.RunSinkSuite(t, "mongodb", func() hermod.Sink {
		return sinkmongodb.NewMongoDBSink("mongodb://"+deadAddr, "d", "t", noMappings, "", "", "", "")
	})
	conformance.RunSinkSuite(t, "cassandra", func() hermod.Sink {
		return sinkcassandra.NewCassandraSink([]string{deadHost}, "ks", "t", noMappings, false, "", "", "", "", false, false)
	})
	conformance.RunSinkSuite(t, "pgvector", func() hermod.Sink {
		return sinkpgvector.NewSink("postgres://u:p@"+deadAddr+"/d", "t", "v", "id", "meta", noMappings, false, "", "", "")
	})
	conformance.RunSinkSuite(t, "milvus", func() hermod.Sink {
		return sinkmilvus.NewSink(sinkmilvus.Config{Address: deadAddr, CollectionName: "c"})
	})
	// NOT REGISTERED: pinecone.
	//
	// It builds its endpoint as https://controller.<env>.pinecone.io/... from
	// config values, with no injectable base URL, so any construction here dials
	// the real internet. Registering it made the whole suite slow and flaky —
	// live DNS and TLS from one connector degrades every other connector's dial.
	//
	// That is a testability defect in the connector, not a reason to pretend it
	// is covered: give pinecone (and any connector that hardcodes a hostname) an
	// optional base-URL override and it can join the registry. Until then it is
	// Experimental and untested, which is what README.md says.
	conformance.RunSinkSuite(t, "servicenow", func() hermod.Sink {
		return sinkservicenow.NewSink(sinkservicenow.Config{InstanceURL: deadURL, Table: "t"})
	})
	conformance.RunSinkSuite(t, "salesforce", func() hermod.Sink {
		return sinksalesforce.NewSalesforceSink("cid", "sec", "u", "p", "tok", "Account", "insert", "")
	})
	conformance.RunSinkSuite(t, "googlesheets", func() hermod.Sink {
		return sinkgooglesheets.NewGoogleSheetsSink("sheet", "A1:B2", "append", "{}", "", "")
	})
	conformance.RunSinkSuite(t, "websocket", func() hermod.Sink {
		return sinkwebsocket.New("ws://"+deadAddr, nil, nil, time.Second, time.Second, 0, false, fmtr())
	})
	conformance.RunSinkSuite(t, "failover", func() hermod.Sink {
		return sinkfailover.NewFailoverSink(
			sinkstdout.NewStdoutSink(fmtr()),
			[]hermod.Sink{sinkstdout.NewStdoutSink(fmtr())},
		)
	})

	// Constructors that dial or validate credentials up front.
	sinkOrSkip(t, "file", func() (hermod.Sink, error) {
		return sinkfile.NewFileSink(filepath.Join(t.TempDir(), "out.jsonl"), fmtr())
	})
	sinkOrSkip(t, "elasticsearch", func() (hermod.Sink, error) {
		return sinkelasticsearch.NewElasticsearchSink([]string{deadURL}, "", "", "", "i", fmtr())
	})
	sinkOrSkip(t, "redis", func() (hermod.Sink, error) {
		return sinkredis.NewRedisSink(deadAddr, "", "s", fmtr())
	})
	sinkOrSkip(t, "nats", func() (hermod.Sink, error) {
		return sinknats.NewNatsJetStreamSink("nats://"+deadAddr, "s", "", "", "", fmtr())
	})
	sinkOrSkip(t, "mqtt", func() (hermod.Sink, error) {
		return sinkmqtt.New(map[string]string{"broker": "tcp://" + deadAddr, "topic": "t"}, fmtr())
	})
	sinkOrSkip(t, "rabbitmq", func() (hermod.Sink, error) {
		return sinkrabbitmq.NewRabbitMQQueueSink("amqp://"+deadAddr, "q", fmtr())
	})
	sinkOrSkip(t, "pulsar", func() (hermod.Sink, error) {
		return sinkpulsar.NewPulsarSink("pulsar://"+deadAddr, "t", "", fmtr())
	})
	sinkOrSkip(t, "kinesis", func() (hermod.Sink, error) {
		return sinkkinesis.NewKinesisSink("us-east-1", "s", "ak", "sk", fmtr())
	})
	sinkOrSkip(t, "pubsub", func() (hermod.Sink, error) {
		return sinkpubsub.NewPubSubSink("p", "t", "{}", fmtr())
	})
	sinkOrSkip(t, "ftp", func() (hermod.Sink, error) {
		return sinkftp.NewFTPSink(deadHost, 1, "u", "p", false, time.Second, "/", "", "f", "overwrite", false, fmtr())
	})

	// These take their endpoint as configuration, so they need no override.
	conformance.RunSinkSuite(t, "dynamics365", func() hermod.Sink {
		return sinkdynamics365.NewSink(sinkdynamics365.Config{
			Resource: deadURL, TenantID: "t", ClientID: "c", ClientSecret: "s",
			Entity: "accounts", Operation: "create",
		}, nil)
	})
	conformance.RunSinkSuite(t, "sap", func() hermod.Sink {
		return sinksap.NewSink(sinksap.Config{
			Host: deadURL, Client: "100", Protocol: "odata",
			Service: "API_TEST_SRV", Entity: "A_Test",
		}, nil)
	})
	conformance.RunSinkSuite(t, "smtp", func() hermod.Sink {
		return sinksmtp.NewSmtpSink(deadHost, 1, "u", "p", false, "from@example.test",
			[]string{"to@example.test"}, "subject", fmtr(), "", "", "", gsmail.S3Config{}, false)
	})

	// Vendor-API sinks. Each hardcodes its hostname in the constructor and now
	// exposes SetBaseURL, which is the seam that lets them be exercised offline.
	// Without it they dialled the live internet, which kept them untested and
	// degraded every other connector's dial in the same run.
	vendorSinks := map[string]func() hermod.Sink{
		"facebook":  func() hermod.Sink { return sinkfacebook.NewFacebookSink("tok", "page", fmtr()) },
		"instagram": func() hermod.Sink { return sinkinstagram.NewInstagramSink("tok", "igid", fmtr()) },
		"linkedin":  func() hermod.Sink { return sinklinkedin.NewLinkedInSink("tok", "urn:li:person:x", fmtr()) },
		"tiktok":    func() hermod.Sink { return sinktiktok.NewTikTokSink("tok", fmtr()) },
		"twitter":   func() hermod.Sink { return sinktwitter.NewTwitterSink("tok", fmtr()) },
		"pinecone": func() hermod.Sink {
			return sinkpinecone.NewSink(sinkpinecone.Config{APIKey: "k", Environment: "dev", IndexName: "i"})
		},
	}
	for name, ctor := range vendorSinks {
		conformance.RunSinkSuite(t, name, func() hermod.Sink {
			s := ctor()
			s.(interface{ SetBaseURL(string) }).SetBaseURL(deadURL)
			return s
		})
	}

	sinkOrSkip(t, "fcm", func() (hermod.Sink, error) {
		return sinkfcm.NewFCMSink("{}", fmtr())
	})
	sinkOrSkip(t, "s3", func() (hermod.Sink, error) {
		return sinks3.NewS3Sink(context.Background(), "us-east-1", "b", "p/", "ak", "sk", deadURL, fmtr(), ".json", "application/json", false)
	})
	sinkOrSkip(t, "s3parquet", func() (hermod.Sink, error) {
		return sinks3parquet.NewS3ParquetSink(context.Background(), "us-east-1", "b", "p/", "ak", "sk", deadURL, "", 1)
	})
}

func TestSourceConformance(t *testing.T) {
	const poll = 50 * time.Millisecond
	tables := []string{"t"}

	conformance.RunSourceSuite(t, "postgres", func() hermod.Source {
		return srcpostgres.NewPostgresSource("postgres://u:p@"+deadAddr+"/d", "slot", "pub", tables, true, "", poll)
	})
	conformance.RunSourceSuite(t, "mssql", func() hermod.Source {
		return srcmssql.NewMSSQLSource("sqlserver://u:p@"+deadAddr+"?database=d", tables, false, true)
	})
	conformance.RunSourceSuite(t, "mysql", func() hermod.Source {
		return srcmysql.NewMySQLSource("u:p@tcp("+deadAddr+")/d", true)
	})
	conformance.RunSourceSuite(t, "oracle", func() hermod.Source {
		return srcoracle.NewOracleSource("oracle://u:p@"+deadAddr+"/svc", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "db2", func() hermod.Source {
		return srcdb2.NewDB2Source("HOSTNAME="+deadHost+";PORT=1;DATABASE=d;UID=u;PWD=p", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "mariadb", func() hermod.Source {
		return srcmariadb.NewMariaDBSource("u:p@tcp("+deadAddr+")/d", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "clickhouse", func() hermod.Source {
		return srcclickhouse.NewClickHouseSource("clickhouse://"+deadAddr+"/d", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "cassandra", func() hermod.Source {
		return srccassandra.NewCassandraSource([]string{deadHost}, tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "scylladb", func() hermod.Source {
		return srcscylladb.NewScyllaDBSource([]string{deadHost}, tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "mongodb", func() hermod.Source {
		return srcmongodb.NewMongoDBSource("mongodb://"+deadAddr, "d", "c", true)
	})
	conformance.RunSourceSuite(t, "redis", func() hermod.Source {
		return srcredis.NewRedisSource(deadAddr, "", "s", "g")
	})
	conformance.RunSourceSuite(t, "http", func() hermod.Source {
		return srchttp.NewHTTPSource(deadURL, "GET", nil, poll, "")
	})
	conformance.RunSourceSuite(t, "graphql", func() hermod.Source {
		return srcgraphql.NewGraphQLSource("/gql")
	})
	conformance.RunSourceSuite(t, "webhook", func() hermod.Source {
		return srcwebhook.NewWebhookSource("/hook")
	})
	conformance.RunSourceSuite(t, "cron", func() hermod.Source {
		return srccron.NewCronSource("* * * * *", "{}")
	})
	conformance.RunSourceSuite(t, "excel", func() hermod.Source {
		return srcexcel.New(t.TempDir(), "*.xlsx", "Sheet1", 1, 2, 100)
	})
	conformance.RunSourceSuite(t, "websocket", func() hermod.Source {
		return srcwebsocket.New("ws://"+deadAddr, nil, nil, time.Second, time.Second, 0, 0, 0, 1<<20)
	})

	// SQLite operates on a real local file.
	dbPath := filepath.Join(t.TempDir(), "src.db")
	conformance.RunSourceSuite(t, "sqlite", func() hermod.Source {
		return srcsqlite.NewSQLiteSource(dbPath, tables, true)
	})

	csvPath := filepath.Join(t.TempDir(), "in.csv")
	conformance.RunSourceSuite(t, "file", func() hermod.Source {
		return srcfile.NewCSVSource(csvPath, ',', true)
	})

	sourceOrSkip(t, "nats", func() (hermod.Source, error) {
		return srcnats.NewNatsJetStreamSource("nats://"+deadAddr, "s", "", "", "", "", "")
	})
	sourceOrSkip(t, "mqtt", func() (hermod.Source, error) {
		return srcmqtt.NewSource(map[string]string{"broker": "tcp://" + deadAddr, "topic": "t"})
	})
	sourceOrSkip(t, "rabbitmq", func() (hermod.Source, error) {
		return srcrabbitmq.NewRabbitMQQueueSource("amqp://"+deadAddr, "q")
	})

	conformance.RunSourceSuite(t, "yugabyte", func() hermod.Source {
		return srcyugabyte.NewYugabyteSource("postgres://u:p@"+deadAddr+"/d", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "kafka", func() hermod.Source {
		return srckafka.NewKafkaSource([]string{deadAddr}, "t", "g", "", "")
	})
	conformance.RunSourceSuite(t, "cdc", func() hermod.Source {
		return srccdc.NewSource("postgres", map[string]any{"conn": "postgres://u:p@" + deadAddr + "/d"})
	})
	conformance.RunSourceSuite(t, "mainframe", func() hermod.Source {
		return srcmainframe.NewSource(srcmainframe.Config{
			Host: deadHost, Port: 1, User: "u", Password: "p",
			Database: "d", Table: "t", Type: "db2", Interval: "1s",
		}, nil)
	})
	conformance.RunSourceSuite(t, "sap", func() hermod.Source {
		return srcsap.NewSource(srcsap.SourceConfig{
			Host: deadURL, Client: "100", Service: "API_TEST_SRV",
			Entity: "A_Test", PollInterval: "1s", IDField: "id",
		}, nil)
	})
	conformance.RunSourceSuite(t, "dynamics365", func() hermod.Source {
		return srcdynamics365.NewSource(srcdynamics365.SourceConfig{
			Resource: deadURL, TenantID: "t", ClientID: "c", ClientSecret: "s",
			Entity: "accounts", PollInterval: "1s", IDField: "modifiedon",
		}, nil)
	})

	// Vendor-API pollers. Same story as the sinks above: the hostname is baked
	// into the constructor, and SetBaseURL is what makes them testable offline.
	vendorSources := map[string]func() hermod.Source{
		"discord":   func() hermod.Source { return srcdiscord.NewDiscordSource("tok", "chan", poll) },
		"slack":     func() hermod.Source { return srcslack.NewSlackSource("tok", "chan", poll) },
		"facebook":  func() hermod.Source { return srcfacebook.NewFacebookSource("tok", "page", poll, "feed") },
		"instagram": func() hermod.Source { return srcinstagram.NewInstagramSource("tok", "igid", poll, "media") },
		"linkedin":  func() hermod.Source { return srclinkedin.NewLinkedInSource("tok", "urn:li:person:x", poll) },
		"tiktok":    func() hermod.Source { return srctiktok.NewTikTokSource("tok", poll, "video") },
		"twitter":   func() hermod.Source { return srctwitter.NewTwitterSource("tok", "hermod", poll, "search") },
	}
	for name, ctor := range vendorSources {
		conformance.RunSourceSuite(t, name, func() hermod.Source {
			s := ctor()
			s.(interface{ SetBaseURL(string) }).SetBaseURL(deadURL)
			return s
		})
	}

	// Connectors needing a collaborator.
	//
	// These were excluded at first on the grounds that faking a DBProvider, a
	// submission Storage or a .proto would test the fake rather than the
	// connector. That reasoning holds for the data path and not for this suite:
	// what is being checked is the connector's own lifecycle, nil-safety and
	// context handling, and a stub collaborator is simply the seam that lets it
	// be constructed. Excluding them left six connectors with no coverage at all
	// to avoid a theoretical weakness in coverage they did not have.
	conformance.RunSourceSuite(t, "batchsql", func() hermod.Source {
		return srcbatchsql.NewBatchSQLSource(deadDBProvider{}, srcbatchsql.Config{
			SourceID: "s1", Cron: "@every 1h", Queries: "SELECT 1",
		})
	})
	conformance.RunSourceSuite(t, "form", func() hermod.Source {
		return srcform.NewFormSource("/signup", noopFormStorage{})
	})

	// Google-backed sources. The SDK client is built lazily, so construction
	// succeeds with placeholder credentials and the contract is still checkable;
	// only the data path needs real ones.
	conformance.RunSourceSuite(t, "firebase", func() hermod.Source {
		return srcfirebase.NewFirebaseSource("proj", "coll", "{}", "ts", poll)
	})
	conformance.RunSourceSuite(t, "googlesheets", func() hermod.Source {
		return srcgooglesheets.NewGoogleSheetsSource("sheet", "A1:B2", "{}", poll)
	})
	conformance.RunSourceSuite(t, "googleanalytics", func() hermod.Source {
		return srcgoogleanalytics.NewGoogleAnalyticsSource("prop", "{}", "activeUsers", "country", poll)
	})

	sourceOrSkip(t, "grpc", func() (hermod.Source, error) {
		// Wraps another source and loads a descriptor off disk at construction.
		inner := srchttp.NewHTTPSource(deadURL, "GET", nil, poll, "")
		return srcgrpc.NewGenericProtoSource("testdata/conformance.proto", "conformance.Event", inner)
	})
}

// TestRegistryCoversFormatter is a smoke check that the shared formatter used
// throughout this file behaves, so a formatter fault cannot be misread as a
// connector fault.
func TestRegistryCoversFormatter(t *testing.T) {
	if fmtr() == nil {
		t.Fatal("json formatter constructor returned nil")
	}
	_ = context.Background()
}

// deadDBProvider satisfies the batchsql source's collaborator with a pool
// pointed at a closed port, so the connector is constructible and its own
// lifecycle behaviour is what gets exercised.
type deadDBProvider struct{}

func (deadDBProvider) GetOrOpenDBByID(_ context.Context, _ string) (*sql.DB, string, error) {
	db, err := sql.Open("pgx", "postgres://u:p@"+deadAddr+"/d")
	return db, "pgx", err
}

// noopFormStorage satisfies the form source's persistence collaborator. The
// form source receives submissions over HTTP; storage is where they land, and
// none of the contract under test reaches it.
type noopFormStorage struct{}

func (noopFormStorage) CreateFormSubmission(context.Context, srcform.FormSubmission) error {
	return nil
}

func (noopFormStorage) ListFormSubmissions(context.Context, srcform.FormSubmissionFilter) ([]srcform.FormSubmission, int, error) {
	return nil, 0, nil
}

func (noopFormStorage) UpdateFormSubmissionStatus(context.Context, string, string) error { return nil }
