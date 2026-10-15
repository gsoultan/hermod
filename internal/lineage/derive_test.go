// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package lineage

import (
	"slices"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// readsOf returns the ids of the assets the workflow reads, sorted.
func readsOf(g Graph, workflowID string) []string {
	var out []string
	for _, e := range g.Edges {
		if e.To == WorkflowNodeID(workflowID) {
			out = append(out, e.From)
		}
	}
	slices.Sort(out)
	return out
}

// writesOf returns the ids of the assets the workflow writes, sorted.
func writesOf(g Graph, workflowID string) []string {
	var out []string
	for _, e := range g.Edges {
		if e.From == WorkflowNodeID(workflowID) {
			out = append(out, e.To)
		}
	}
	slices.Sort(out)
	return out
}

func sourceWorkflow(src storage.Source) Inputs {
	return Inputs{
		Sources: []storage.Source{src},
		Workflows: []storage.Workflow{{
			ID: "wf", Name: "wf", VHost: src.VHost,
			Nodes: []storage.WorkflowNode{{ID: "n-src", Type: "source", RefID: src.ID}},
		}},
	}
}

func sinkWorkflow(snks ...storage.Sink) Inputs {
	return Inputs{
		Sinks: snks,
		Workflows: []storage.Workflow{{
			ID: "wf", Name: "wf",
			Nodes: []storage.WorkflowNode{{ID: "n-snk", Type: "sink", RefID: snks[0].ID}},
		}},
	}
}

// Each source type is read with the config keys internal/factory/factory.go
// (createSourceBase, BuildConnectionString) and the registry read for it.
func TestDeriveSourceAssets(t *testing.T) {
	cases := []struct {
		name string
		typ  string
		cfg  hermod.StringMap
		want []string
	}{
		{"postgres host keys, two tables", "postgres",
			hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop", "user": "u", "password": "pw", "tables": "public.orders, public.items"},
			[]string{"table:postgres://db:5432/shop/public.items", "table:postgres://db:5432/shop/public.orders"}},
		{"postgres connection string", "postgres",
			hermod.StringMap{"connection_string": "postgres://u:s3cret@DB:5432/shop?sslmode=disable", "tables": "public.orders"},
			[]string{"table:postgres://db:5432/shop/public.orders"}},
		{"postgres query without tables is the database", "postgres",
			hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop", "query": "select 1"},
			[]string{"database:postgres://db:5432/shop"}},
		{"yugabyte speaks postgres", "yugabyte",
			hermod.StringMap{"host": "yb", "port": "5433", "dbname": "app", "tables": "t"},
			[]string{"table:postgres://yb:5433/app/t"}},
		{"mariadb is the mysql family", "mariadb",
			hermod.StringMap{"host": "maria", "port": "3306", "dbname": "crm", "tables": "contacts"},
			[]string{"table:mysql://maria:3306/crm/contacts"}},
		{"mysql", "mysql",
			hermod.StringMap{"host": "my", "port": "3306", "dbname": "crm", "tables": "contacts"},
			[]string{"table:mysql://my:3306/crm/contacts"}},
		{"mssql connection string keeps the database", "mssql",
			hermod.StringMap{"connection_string": "sqlserver://sa:pw@sql:1433?database=erp", "tables": "dbo.invoices"},
			[]string{"table:mssql://sql:1433/erp/dbo.invoices"}},
		{"oracle", "oracle",
			hermod.StringMap{"host": "ora", "port": "1521", "dbname": "XE", "tables": "HR.EMP"},
			[]string{"table:oracle://ora:1521/XE/HR.EMP"}},
		{"db2 has only its connection string", "db2",
			hermod.StringMap{"connection_string": "db2://u:p@db2host:50000/SAMPLE", "tables": "ORDERS"},
			[]string{"table:db2://db2host:50000/SAMPLE/ORDERS"}},
		{"sqlite path", "sqlite",
			hermod.StringMap{"path": "/data/app.db", "tables": "events"},
			[]string{"table:sqlite:///data/app.db/events"}},
		{"clickhouse source", "clickhouse",
			hermod.StringMap{"host": "ch", "port": "9000", "dbname": "analytics", "tables": "events"},
			[]string{"table:clickhouse://ch:9000/analytics/events"}},
		{"mongodb uri", "mongodb",
			hermod.StringMap{"uri": "mongodb://u:p@mongo:27017", "database": "app", "collection": "users"},
			[]string{"collection:mongodb://mongo:27017/app.users"}},
		{"mongodb host keys", "mongodb",
			hermod.StringMap{"host": "mongo", "port": "27017", "database": "app", "collection": "users"},
			[]string{"collection:mongodb://mongo:27017/app.users"}},
		{"cassandra hosts are sorted", "cassandra",
			hermod.StringMap{"hosts": "c2, c1", "tables": "ks.t"},
			[]string{"table:cassandra://c1,c2/ks.t"}},
		{"scylladb", "scylladb",
			hermod.StringMap{"hosts": "s1", "tables": "ks.t"},
			[]string{"table:scylladb://s1/ks.t"}},
		{"mainframe", "mainframe",
			hermod.StringMap{"host": "mf", "port": "446", "database": "DB1", "schema": "S", "table": "T"},
			[]string{"table:mainframe://mf:446/DB1/S.T"}},
		{"kafka brokers sorted", "kafka",
			hermod.StringMap{"brokers": "b2:9092,b1:9092", "topic": "orders", "password": "pw"},
			[]string{"topic:kafka://b1:9092,b2:9092/orders"}},
		{"nats", "nats",
			hermod.StringMap{"url": "nats://u:p@nats:4222", "subject": "orders.created"},
			[]string{"subject:nats://nats:4222/orders.created"}},
		{"redis", "redis",
			hermod.StringMap{"addr": "redis:6379", "stream": "events", "password": "pw"},
			[]string{"stream:redis://redis:6379/events"}},
		{"rabbitmq stream", "rabbitmq",
			hermod.StringMap{"host": "mq", "port": "5552", "dbname": "vh", "stream_name": "s1"},
			[]string{"stream:rabbitmq://mq:5552/vh/s1"}},
		{"rabbitmq queue", "rabbitmq_queue",
			hermod.StringMap{"host": "mq", "port": "5672", "queue_name": "q1"},
			[]string{"queue:rabbitmq://mq:5672/q1"}},
		{"mqtt topics", "mqtt",
			hermod.StringMap{"broker_url": "tcp://u:p@broker:1883", "topics": "a/b, c"},
			[]string{"topic:mqtt://broker:1883/a/b", "topic:mqtt://broker:1883/c"}},
		{"mqtt single topic via url", "mqtt",
			hermod.StringMap{"url": "tcp://broker:1883", "topic": "a"},
			[]string{"topic:mqtt://broker:1883/a"}},
		{"webhook path", "webhook", hermod.StringMap{"path": "/hooks/orders"},
			[]string{"endpoint:webhook:///hooks/orders"}},
		{"chat path", "chat", hermod.StringMap{"path": "support"},
			[]string{"endpoint:chat:///support"}},
		{"graphql path", "graphql", hermod.StringMap{"path": "gql"}, []string{"endpoint:graphql:///gql"}},
		{"grpc path", "grpc", hermod.StringMap{"path": "ingest"}, []string{"endpoint:grpc:///ingest"}},
		{"form path", "form", hermod.StringMap{"path": "signup"}, []string{"endpoint:form:///signup"}},
		{"http drops credentials and query", "http",
			hermod.StringMap{"url": "https://user:pw@API.example.com/v1/orders?api_key=abc", "method": "GET"},
			[]string{"endpoint:http://api.example.com/v1/orders"}},
		{"websocket", "websocket", hermod.StringMap{"url": "wss://feed.example.com/ticks"},
			[]string{"endpoint:websocket://feed.example.com/ticks"}},
		{"googlesheets", "googlesheets",
			hermod.StringMap{"spreadsheet_id": "sheet1", "range": "A1:C9", "credentials_json": "{}"},
			[]string{"sheet:googlesheets://sheet1/A1:C9"}},
		{"csv on local disk", "file",
			hermod.StringMap{"format": "csv", "file_path": "/in/orders.csv"},
			[]string{"file:file:///in/orders.csv"}},
		{"csv on s3", "file",
			hermod.StringMap{"format": "csv", "source_type": "s3", "s3_bucket": "lake", "s3_key": "raw/orders.csv", "s3_secret_key": "x"},
			[]string{"object:s3://lake/raw/orders.csv"}},
		{"csv over http", "file",
			hermod.StringMap{"format": "csv", "source_type": "http", "url": "https://files.example.com/o.csv"},
			[]string{"endpoint:http://files.example.com/o.csv"}},
		{"generic file local_path", "file",
			hermod.StringMap{"format": "json", "source_type": "local", "local_path": "/in", "pattern": "*.json"},
			[]string{"file:file:///in/*.json"}},
		{"generic file on sftp", "file",
			hermod.StringMap{"format": "raw", "source_type": "sftp", "ftp_host": "ftp", "ftp_root": "/drop", "ftp_password": "x"},
			[]string{"file:sftp://ftp:22/drop"}},
		{"generic file on ftp with sftp flag", "file",
			hermod.StringMap{"format": "raw", "source_type": "ftp", "use_sftp": "true", "ftp_host": "ftp", "ftp_port": "2222", "ftp_root": "/drop"},
			[]string{"file:sftp://ftp:2222/drop"}},
		{"generic file on ftp", "file",
			hermod.StringMap{"format": "raw", "source_type": "ftp", "ftp_host": "ftp", "ftp_root": "/drop"},
			[]string{"file:ftp://ftp:21/drop"}},
		{"excel on disk", "excel",
			hermod.StringMap{"base_path": "/x", "pattern": "*.xlsx", "sheet": "S1"},
			[]string{"file:file:///x/*.xlsx"}},
		{"excel on s3", "excel",
			hermod.StringMap{"source_type": "s3", "s3_bucket": "lake", "s3_key": "xl/"},
			[]string{"object:s3://lake/xl/"}},
		{"sap", "sap", hermod.StringMap{"host": "https://sap.example.com", "service": "API_SALES", "entity": "Orders", "password": "pw"},
			[]string{"entity:sap://sap.example.com/API_SALES/Orders"}},
		{"dynamics365", "dynamics365", hermod.StringMap{"resource": "https://org.crm.dynamics.com", "entity": "accounts", "client_secret": "x"},
			[]string{"entity:dynamics365://org.crm.dynamics.com/accounts"}},
		{"metis stream", "metis", hermod.StringMap{"base_url": "https://metis.example.com", "stream": "incidents"},
			[]string{"stream:metis://metis.example.com/incidents"}},
		{"metis external task", "metis_task", hermod.StringMap{"base_url": "https://metis.example.com", "topic": "approve"},
			[]string{"topic:metis://metis.example.com/approve"}},
		{"eventstore", "eventstore", hermod.StringMap{"driver": "postgres", "dsn": "postgres://u:p@es:5432/events", "stream_id": "orders"},
			[]string{"stream:eventstore://es:5432/events/orders"}},
		{"firebase", "firebase", hermod.StringMap{"project_id": "p1", "collection": "users"},
			[]string{"collection:firebase://p1/users"}},
		{"googleanalytics", "googleanalytics", hermod.StringMap{"property_id": "123"},
			[]string{"endpoint:googleanalytics://123"}},
		{"slack channel", "slack", hermod.StringMap{"channel_id": "C1", "token": "x"},
			[]string{"channel:slack://C1"}},
		{"discord channel", "discord", hermod.StringMap{"channel_id": "D1"},
			[]string{"channel:discord://D1"}},
		{"facebook page", "facebook", hermod.StringMap{"page_id": "P1"},
			[]string{"channel:facebook://P1"}},
		{"cron has no data asset", "cron", hermod.StringMap{"schedule": "* * * * *"},
			[]string{"connector:cron://source/src"}},
		{"unknown type is identified by its record", "brand_new", hermod.StringMap{"x": "y"},
			[]string{"connector:brand_new://source/src"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := Derive(sourceWorkflow(storage.Source{ID: "src", Name: "the source", Type: tc.typ, Config: tc.cfg}))
			if got := readsOf(g, "wf"); !slices.Equal(got, tc.want) {
				t.Fatalf("reads = %v, want %v", got, tc.want)
			}
			for _, id := range tc.want {
				n, ok := g.Node(id)
				if !ok {
					t.Fatalf("no node %s", id)
				}
				if len(n.Refs) != 1 || n.Refs[0].ID != "src" || n.Refs[0].Kind != "source" {
					t.Errorf("refs of %s = %+v, want the source record", id, n.Refs)
				}
				if strings.HasPrefix(id, "connector:") == n.Resolved {
					t.Errorf("%s: resolved = %v", id, n.Resolved)
				}
			}
		})
	}
}

func TestDeriveSinkAssets(t *testing.T) {
	cases := []struct {
		name string
		typ  string
		cfg  hermod.StringMap
		want []string
	}{
		{"postgres", "postgres", hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop", "table": "public.orders"},
			[]string{"table:postgres://db:5432/shop/public.orders"}},
		{"pgvector is a postgres table", "pgvector", hermod.StringMap{"host": "db", "port": "5432", "dbname": "rag", "table": "docs"},
			[]string{"table:postgres://db:5432/rag/docs"}},
		{"yugabyte", "yugabyte", hermod.StringMap{"host": "yb", "port": "5433", "dbname": "app", "table": "t"},
			[]string{"table:postgres://yb:5433/app/t"}},
		{"mysql", "mysql", hermod.StringMap{"host": "my", "port": "3306", "dbname": "crm", "table": "contacts"},
			[]string{"table:mysql://my:3306/crm/contacts"}},
		{"mysql DSN", "mariadb", hermod.StringMap{"connection_string": "u:p@tcp(maria:3306)/crm?parseTime=true", "table": "contacts"},
			[]string{"table:mysql://maria:3306/crm/contacts"}},
		{"mssql", "mssql", hermod.StringMap{"host": "sql", "port": "1433", "dbname": "erp", "table": "dbo.invoices"},
			[]string{"table:mssql://sql:1433/erp/dbo.invoices"}},
		{"oracle", "oracle", hermod.StringMap{"host": "ora", "port": "1521", "dbname": "XE", "table": "HR.EMP"},
			[]string{"table:oracle://ora:1521/XE/HR.EMP"}},
		{"sqlite", "sqlite", hermod.StringMap{"path": "/data/app.db", "table": "events"},
			[]string{"table:sqlite:///data/app.db/events"}},
		{"clickhouse addr", "clickhouse", hermod.StringMap{"addr": "ch:9000", "database": "analytics", "table": "events"},
			[]string{"table:clickhouse://ch:9000/analytics/events"}},
		{"cassandra keyspace", "cassandra", hermod.StringMap{"hosts": "c1,c2", "keyspace": "ks", "table": "t"},
			[]string{"table:cassandra://c1,c2/ks.t"}},
		{"mongodb", "mongodb", hermod.StringMap{"uri": "mongodb://mongo:27017", "database": "app", "table": "users"},
			[]string{"collection:mongodb://mongo:27017/app.users"}},
		{"snowflake", "snowflake", hermod.StringMap{"connection_string": "user:pw@acct/db/schema?warehouse=wh", "table": "FACTS"},
			[]string{"table:snowflake://acct/db/schema/FACTS"}},
		{"kafka", "kafka", hermod.StringMap{"brokers": "b1:9092,b2:9092", "topic": "orders"},
			[]string{"topic:kafka://b1:9092,b2:9092/orders"}},
		{"nats", "nats", hermod.StringMap{"url": "nats://nats:4222", "subject": "orders.created"},
			[]string{"subject:nats://nats:4222/orders.created"}},
		{"redis", "redis", hermod.StringMap{"addr": "redis:6379", "stream": "events"},
			[]string{"stream:redis://redis:6379/events"}},
		{"rabbitmq stream", "rabbitmq", hermod.StringMap{"host": "mq", "port": "5552", "dbname": "vh", "stream_name": "s1"},
			[]string{"stream:rabbitmq://mq:5552/vh/s1"}},
		{"rabbitmq queue", "rabbitmq_queue", hermod.StringMap{"host": "mq", "port": "5672", "queue_name": "q1"},
			[]string{"queue:rabbitmq://mq:5672/q1"}},
		{"mqtt", "mqtt", hermod.StringMap{"broker_url": "tcp://broker:1883", "topic": "a/b"},
			[]string{"topic:mqtt://broker:1883/a/b"}},
		{"pulsar", "pulsar", hermod.StringMap{"url": "pulsar://pulsar:6650", "topic": "persistent://t/n/orders"},
			[]string{"topic:pulsar://pulsar:6650/persistent://t/n/orders"}},
		{"kinesis", "kinesis", hermod.StringMap{"region": "eu-west-1", "stream_name": "clicks", "secret_key": "x"},
			[]string{"stream:kinesis://eu-west-1/clicks"}},
		{"pubsub", "pubsub", hermod.StringMap{"project_id": "p1", "topic_id": "events"},
			[]string{"topic:pubsub://p1/events"}},
		{"elasticsearch", "elasticsearch", hermod.StringMap{"addresses": "http://es2:9200,http://es1:9200", "index": "orders", "api_key": "k"},
			[]string{"index:elasticsearch://es1:9200,es2:9200/orders"}},
		{"s3", "s3", hermod.StringMap{"bucket": "lake", "key_prefix": "raw/", "secret_key": "x"},
			[]string{"object:s3://lake/raw/"}},
		{"s3-parquet", "s3-parquet", hermod.StringMap{"bucket": "lake", "key_prefix": "pq/"},
			[]string{"object:s3://lake/pq/"}},
		{"file", "file", hermod.StringMap{"filename": "/out/o.jsonl"},
			[]string{"file:file:///out/o.jsonl"}},
		{"ftp", "ftp", hermod.StringMap{"host": "ftp", "root_dir": "/up", "password": "x"},
			[]string{"file:ftp://ftp:21/up"}},
		{"http", "http", hermod.StringMap{"url": "https://hooks.example.com/in?token=abc"},
			[]string{"endpoint:http://hooks.example.com/in"}},
		{"websocket", "websocket", hermod.StringMap{"url": "wss://push.example.com/feed"},
			[]string{"endpoint:websocket://push.example.com/feed"}},
		{"sse", "sse", hermod.StringMap{"stream": "live"}, []string{"stream:sse:///live"}},
		{"ml_dataset belongs to the sink's vhost", "ml_dataset", hermod.StringMap{"dataset": "churn"},
			[]string{"dataset:ml://default/churn"}},
		{"salesforce", "salesforce", hermod.StringMap{"object": "Account", "client_secret": "x"},
			[]string{"entity:salesforce:///Account"}},
		{"servicenow", "servicenow", hermod.StringMap{"instance_url": "https://acme.service-now.com", "table": "incident"},
			[]string{"table:servicenow://acme.service-now.com/incident"}},
		{"sap", "sap", hermod.StringMap{"host": "https://sap.example.com", "service": "API_SALES", "entity": "Orders"},
			[]string{"entity:sap://sap.example.com/API_SALES/Orders"}},
		{"dynamics365", "dynamics365", hermod.StringMap{"resource": "https://org.crm.dynamics.com", "entity": "accounts"},
			[]string{"entity:dynamics365://org.crm.dynamics.com/accounts"}},
		{"googlesheets", "googlesheets", hermod.StringMap{"spreadsheet_id": "sheet1", "range": "A1"},
			[]string{"sheet:googlesheets://sheet1/A1"}},
		{"eventstore", "eventstore", hermod.StringMap{"driver": "postgres", "host": "es", "port": "5432", "dbname": "events"},
			[]string{"database:eventstore://es:5432/events"}},
		{"metis", "metis", hermod.StringMap{"base_url": "https://metis.example.com", "action": "start_process", "definition_key": "onboard"},
			[]string{"process:metis://metis.example.com/onboard"}},
		{"telegram chat", "telegram", hermod.StringMap{"chat_id": "42", "token": "x"},
			[]string{"channel:telegram://42"}},
		{"slack channel", "slack", hermod.StringMap{"channel_id": "C1", "webhook_url": "https://hooks.slack.com/services/secret"},
			[]string{"channel:slack://C1"}},
		{"slack webhook only stays record scoped", "slack", hermod.StringMap{"webhook_url": "https://hooks.slack.com/services/secret"},
			[]string{"connector:slack://sink/snk"}},
		{"smtp recipients are not exposed", "smtp", hermod.StringMap{"host": "smtp", "to": "a@example.com"},
			[]string{"connector:smtp://sink/snk"}},
		{"stdout", "stdout", hermod.StringMap{}, []string{"connector:stdout://sink/snk"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := Derive(sinkWorkflow(storage.Sink{ID: "snk", Name: "the sink", Type: tc.typ, Config: tc.cfg}))
			if got := writesOf(g, "wf"); !slices.Equal(got, tc.want) {
				t.Fatalf("writes = %v, want %v", got, tc.want)
			}
		})
	}
}

// failover and txgroup sinks hold no connection of their own: what they write
// is what their member sinks write (Registry.createSinkInternal, txgroup.go).
func TestDeriveCompositeSinksWriteThroughTheirMembers(t *testing.T) {
	a := storage.Sink{ID: "a", Type: "kafka", Config: hermod.StringMap{"brokers": "b:9092", "topic": "a"}}
	b := storage.Sink{ID: "b", Type: "kafka", Config: hermod.StringMap{"brokers": "b:9092", "topic": "b"}}
	c := storage.Sink{ID: "c", Type: "postgres", Config: hermod.StringMap{"host": "db", "port": "5432", "dbname": "x", "table": "t"}}
	t.Run("failover", func(t *testing.T) {
		fo := storage.Sink{ID: "fo", Type: "failover", Config: hermod.StringMap{"primary_id": "a", "fallback_ids": "b, c"}}
		g := Derive(sinkWorkflow(fo, a, b, c))
		want := []string{"table:postgres://db:5432/x/t", "topic:kafka://b:9092/a", "topic:kafka://b:9092/b"}
		if got := writesOf(g, "wf"); !slices.Equal(got, want) {
			t.Fatalf("writes = %v, want %v", got, want)
		}
	})
	t.Run("txgroup", func(t *testing.T) {
		tx := storage.Sink{ID: "tx", Type: "txgroup", Config: hermod.StringMap{"members": "a,c"}}
		g := Derive(sinkWorkflow(tx, a, c))
		want := []string{"table:postgres://db:5432/x/t", "topic:kafka://b:9092/a"}
		if got := writesOf(g, "wf"); !slices.Equal(got, want) {
			t.Fatalf("writes = %v, want %v", got, want)
		}
	})
	t.Run("a group naming itself does not loop", func(t *testing.T) {
		self := storage.Sink{ID: "self", Type: "txgroup", Config: hermod.StringMap{"members": "self,a"}}
		g := Derive(sinkWorkflow(self, a))
		want := []string{"topic:kafka://b:9092/a"}
		if got := writesOf(g, "wf"); !slices.Equal(got, want) {
			t.Fatalf("writes = %v, want %v", got, want)
		}
	})
}

// batch_sql has no connection of its own; it queries its delegate source
// (Registry.createSource: batchsql.Config.SourceID = source_id).
func TestDeriveBatchSQLReadsItsDelegatesDatabase(t *testing.T) {
	delegate := storage.Source{ID: "pg", Type: "postgres", Config: hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop", "tables": "public.orders"}}
	batch := storage.Source{ID: "b", Type: "batch_sql", Config: hermod.StringMap{"source_id": "pg", "queries": "select * from x"}}
	in := sourceWorkflow(batch)
	in.Sources = append(in.Sources, delegate)
	g := Derive(in)
	want := []string{"database:postgres://db:5432/shop"}
	if got := readsOf(g, "wf"); !slices.Equal(got, want) {
		t.Fatalf("reads = %v, want %v", got, want)
	}
}

// Two workflows connect through an asset when one writes what the other reads,
// even though each names its own source and sink record.
func TestDeriveJoinsWorkflowsThroughASharedAsset(t *testing.T) {
	in := Inputs{
		Sources: []storage.Source{
			{ID: "s1", Type: "postgres", Config: hermod.StringMap{"host": "db", "port": "5432", "dbname": "shop", "tables": "orders"}},
			{ID: "s2", Type: "kafka", Config: hermod.StringMap{"brokers": "b:9092", "topic": "orders"}},
		},
		Sinks: []storage.Sink{
			{ID: "k1", Type: "kafka", Config: hermod.StringMap{"brokers": "b:9092", "topic": "orders"}},
		},
		Workflows: []storage.Workflow{
			{ID: "w1", Nodes: []storage.WorkflowNode{{ID: "a", Type: "source", RefID: "s1"}, {ID: "b", Type: "sink", RefID: "k1"}}},
			{ID: "w2", Nodes: []storage.WorkflowNode{{ID: "a", Type: "source", RefID: "s2"}}},
		},
	}
	g := Derive(in)
	topic := "topic:kafka://b:9092/orders"
	n, ok := g.Node(topic)
	if !ok {
		t.Fatalf("no node %s in %+v", topic, g.Nodes)
	}
	if len(n.Refs) != 2 {
		t.Errorf("refs = %+v, want the sink k1 and the source s2", n.Refs)
	}
	if got := writesOf(g, "w1"); !slices.Equal(got, []string{topic}) {
		t.Errorf("w1 writes %v", got)
	}
	if got := readsOf(g, "w2"); !slices.Equal(got, []string{topic}) {
		t.Errorf("w2 reads %v", got)
	}
}

// A node that names a source or sink the caller cannot see (another vhost's,
// or a deleted one) contributes nothing: the asset is not invented.
func TestDeriveIgnoresReferencesOutsideTheInputs(t *testing.T) {
	g := Derive(Inputs{Workflows: []storage.Workflow{{ID: "wf", Nodes: []storage.WorkflowNode{
		{ID: "a", Type: "source", RefID: "elsewhere"}, {ID: "b", Type: "sink", RefID: "gone"},
	}}}})
	if len(g.Edges) != 0 || len(g.Nodes) != 1 {
		t.Fatalf("graph = %+v, want just the workflow", g)
	}
}

func TestDeriveNeverCarriesCredentials(t *testing.T) {
	in := Inputs{
		Sources: []storage.Source{
			{ID: "s1", Type: "postgres", Config: hermod.StringMap{"connection_string": "postgres://admin:TopSecret1@db:5432/shop?password=TopSecret2", "tables": "t"}},
			{ID: "s2", Type: "http", Config: hermod.StringMap{"url": "https://u:TopSecret3@api.example.com/x?token=TopSecret4"}},
			{ID: "s3", Type: "mysql", Config: hermod.StringMap{"connection_string": "root:TopSecret5@tcp(my:3306)/crm", "tables": "t"}},
		},
		Workflows: []storage.Workflow{{ID: "wf", Nodes: []storage.WorkflowNode{
			{ID: "a", Type: "source", RefID: "s1"}, {ID: "b", Type: "source", RefID: "s2"}, {ID: "c", Type: "source", RefID: "s3"},
			{ID: "d", Type: "transformation", Config: map[string]any{"transType": "ai_retrieve", "store": "pgvector", "connectionString": "postgres://u:TopSecret6@vec:5432/rag", "table": "docs"}},
		}}},
	}
	g := Derive(in)
	for _, n := range g.Nodes {
		blob := n.ID + n.Label + n.Locator
		if strings.Contains(blob, "TopSecret") {
			t.Errorf("node %+v carries a credential", n)
		}
	}
}
