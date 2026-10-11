package lineage

import (
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// The config keys below are the ones internal/factory/factory.go
// (createSourceBase, createSinkBase, BuildConnectionString) and the registry
// (batch_sql, failover, txgroup) read for each type. A type missing here, or a
// config that names no reachable object, becomes a record-scoped connector
// asset rather than a guess.

// sqlFamily maps a connector type to the database family whose connection it
// opens. yugabyte and pgvector open postgres connections (BuildConnectionString
// gives yugabyte the postgres scheme, and the pgvector sink calls it with
// "postgres"); mariadb shares the mysql sink and DSN.
var sqlFamily = map[string]string{
	"postgres": "postgres", "yugabyte": "postgres", "pgvector": "postgres",
	"mysql": "mysql", "mariadb": "mysql",
	"mssql": "mssql", "oracle": "oracle", "clickhouse": "clickhouse",
	"db2": "db2", "sqlite": "sqlite", "snowflake": "snowflake",
}

// keyBuilt are the types BuildConnectionString builds from host, port and
// dbname when no connection string is set.
var keyBuilt = map[string]bool{
	"postgres": true, "yugabyte": true, "pgvector": true, "mssql": true, "oracle": true,
	"clickhouse": true, "mysql": true, "mariadb": true,
}

// sqlDatabase is the database a SQL source or sink connects to.
func sqlDatabase(typ string, c cfgMap) (asset, bool) {
	family, ok := sqlFamily[typ]
	if !ok {
		return asset{}, false
	}
	if typ == "sqlite" {
		path := c.first("connection_string", "path")
		if path == "" || strings.Contains(path, "{{") {
			return asset{}, false
		}
		return asset{kind: KindDatabase, system: family, locator: path, resolved: true}, true
	}
	loc, ok := connLocator(c, keyBuilt[typ])
	if !ok {
		return asset{}, false
	}
	return asset{kind: KindDatabase, system: family, locator: loc, resolved: true}, true
}

// tables returns one table asset per name on the database, or the database
// itself when no table is named (a query-based source).
func tables(db asset, names ...string) []asset {
	var out []asset
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			t := db
			t.kind, t.object = KindTable, n
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return []asset{db}
	}
	return out
}

func one(kind, system, locator, object string) []asset {
	return []asset{{kind: kind, system: system, locator: locator, object: object, resolved: true}}
}

// sourceAssets are the assets a source reads. sources resolves a batch_sql
// delegate; it is nil-safe.
func sourceAssets(src storage.Source, sources map[string]storage.Source) []asset {
	c := cfgMap(src.Config)
	if db, ok := sqlDatabase(src.Type, c); ok {
		return tables(db, list(c.get("tables"))...)
	}
	var out []asset
	switch src.Type {
	case "batch_sql":
		// batch_sql's queries are free SQL; the database is what is known.
		if d, ok := sources[c.get("source_id")]; ok && d.ID != src.ID {
			if db, ok := sqlDatabase(d.Type, cfgMap(d.Config)); ok {
				out = []asset{db}
			}
		}
	case "mongodb":
		loc, ok := connLocator(cfgMap{"uri": c.get("uri")}, false)
		if !ok {
			loc = hostPort(c.get("host"), c.get("port"))
		}
		if loc != "" && c.get("collection") != "" {
			out = one(KindCollection, "mongodb", loc, c.get("database")+"."+c.get("collection"))
		}
	case "cassandra", "scylladb":
		hosts := sortedList(c.get("hosts"), strings.ToLower)
		for _, t := range list(c.get("tables")) {
			out = append(out, one(KindTable, src.Type, hosts, t)...)
		}
	case "mainframe":
		if hp := hostPort(c.get("host"), c.get("port")); hp != "" && c.get("table") != "" {
			out = one(KindTable, "mainframe", joinPath(hp, c.get("database")), joinDot(c.get("schema"), c.get("table")))
		}
	case "eventstore":
		if db, ok := eventStoreDB(c); ok {
			out = []asset{db}
			if s := c.get("stream_id"); s != "" {
				out[0].kind, out[0].object = KindStream, s
			}
		}
	case "excel":
		out = fileAssets(strings.ToLower(c.get("source_type")), c, joinFile(c.first("base_path", "local_path"), c.get("pattern")))
	case "file":
		backend := strings.ToLower(c.get("source_type"))
		base := ""
		if c.get("format") == "csv" && backend != "http" && backend != "s3" {
			// The CSV reader takes file_path only (sourcefile.NewCSVSource).
			backend, base = "local", c.get("file_path")
		}
		out = fileAssets(backend, c, base)
	default:
		out = messagingAssets(src.Type, c, true)
	}
	return out
}

// sinkAssets are the assets a sink writes. sinks resolves the members of a
// failover or txgroup sink.
func sinkAssets(snk storage.Sink, sinks map[string]storage.Sink, seen map[string]bool) []asset {
	c := cfgMap(snk.Config)
	switch snk.Type {
	case "failover", "txgroup":
		if seen[snk.ID] {
			return nil
		}
		seen[snk.ID] = true
		ids := list(c.get("members"))
		if snk.Type == "failover" {
			ids = append(list(c.get("primary_id")), list(c.get("fallback_ids"))...)
		}
		var out []asset
		for _, id := range ids {
			if m, ok := sinks[id]; ok && !seen[id] {
				out = append(out, sinkAssets(m, sinks, seen)...)
			}
		}
		return out
	case "clickhouse":
		if loc := joinPath(strings.ToLower(c.get("addr")), c.get("database")); loc != "" && c.get("table") != "" {
			return one(KindTable, "clickhouse", loc, c.get("table"))
		}
		return nil
	}
	if db, ok := sqlDatabase(snk.Type, c); ok {
		return tables(db, c.get("table"))
	}
	switch snk.Type {
	case "mongodb":
		if loc, ok := connLocator(cfgMap{"uri": c.get("uri")}, false); ok && c.get("table") != "" {
			return one(KindCollection, "mongodb", loc, c.get("database")+"."+c.get("table"))
		}
	case "cassandra":
		if t := c.get("table"); t != "" {
			return one(KindTable, "cassandra", sortedList(c.get("hosts"), strings.ToLower), joinDot(c.get("keyspace"), t))
		}
	case "eventstore":
		if db, ok := eventStoreDB(c); ok {
			return []asset{db}
		}
	case "s3", "s3-parquet":
		if b := c.get("bucket"); b != "" {
			return one(KindObject, "s3", b, c.get("key_prefix"))
		}
	case "file":
		if f := c.get("filename"); f != "" {
			return one(KindFile, "file", "", f)
		}
	case "ftp":
		port := c.get("port")
		if port == "" || port == "0" {
			port = "21"
		}
		if hp := hostPort(c.get("host"), port); c.get("host") != "" {
			return one(KindFile, "ftp", hp, joinFile(c.get("root_dir"), c.get("path_template")))
		}
	case "ml_dataset":
		if d := c.get("dataset"); d != "" {
			return []asset{datasetAsset(snk.VHost, d)}
		}
	case "salesforce":
		if o := c.get("object"); o != "" {
			return one(KindEntity, "salesforce", "", o)
		}
	case "servicenow":
		if h := hostOf(c.get("instance_url")); h != "" && c.get("table") != "" {
			return one(KindTable, "servicenow", h, c.get("table"))
		}
	case "metis":
		if h := hostOf(c.get("base_url")); h != "" {
			if o := c.first("definition_key", "message_name", "signal_name"); o != "" {
				return one(KindProcess, "metis", h, o)
			}
		}
	case "telegram":
		if id := c.get("chat_id"); id != "" {
			return one(KindChannel, "telegram", id, "")
		}
	case "sse":
		if s := c.get("stream"); s != "" {
			return one(KindStream, "sse", "", s)
		}
	case "pulsar":
		if loc, ok := urlLocator(c.get("url")); ok && c.get("topic") != "" {
			return one(KindTopic, "pulsar", loc, c.get("topic"))
		}
	case "kinesis":
		if s := c.get("stream_name"); s != "" {
			return one(KindStream, "kinesis", c.get("region"), s)
		}
	case "pubsub":
		if t := c.get("topic_id"); t != "" {
			return one(KindTopic, "pubsub", c.get("project_id"), t)
		}
	case "elasticsearch":
		addrs := sortedList(c.get("addresses"), func(a string) string {
			if loc, ok := urlLocator(a); ok {
				return loc
			}
			return strings.ToLower(a)
		})
		if addrs != "" && c.get("index") != "" {
			return one(KindIndex, "elasticsearch", addrs, c.get("index"))
		}
	default:
		return messagingAssets(snk.Type, c, false)
	}
	return nil
}

// messagingAssets covers the types whose sources and sinks read the same keys.
func messagingAssets(typ string, c cfgMap, isSource bool) []asset {
	switch typ {
	case "kafka":
		if t := c.get("topic"); t != "" {
			return one(KindTopic, "kafka", sortedList(c.get("brokers"), strings.ToLower), t)
		}
	case "nats":
		if loc, ok := urlLocator(c.get("url")); ok && c.get("subject") != "" {
			return one(KindSubject, "nats", loc, c.get("subject"))
		}
	case "redis":
		if s := c.get("stream"); s != "" {
			return one(KindStream, "redis", strings.ToLower(c.get("addr")), s)
		}
	case "rabbitmq", "rabbitmq_queue":
		loc, ok := connLocator(c, true)
		if !ok {
			return nil
		}
		if typ == "rabbitmq" && c.get("stream_name") != "" {
			return one(KindStream, "rabbitmq", loc, c.get("stream_name"))
		}
		if typ == "rabbitmq_queue" && c.get("queue_name") != "" {
			return one(KindQueue, "rabbitmq", loc, c.get("queue_name"))
		}
	case "mqtt":
		loc, ok := urlLocator(c.first("broker_url", "url"))
		if !ok {
			return nil
		}
		topics := []string{c.get("topic")}
		if isSource {
			if ts := list(c.get("topics")); len(ts) > 0 {
				topics = ts
			}
		}
		var out []asset
		for _, t := range topics {
			if t != "" {
				out = append(out, one(KindTopic, "mqtt", loc, t)...)
			}
		}
		return out
	case "webhook", "chat", "graphql", "grpc", "form":
		if p := c.get("path"); p != "" && isSource {
			return one(KindEndpoint, typ, "", p)
		}
	case "http":
		if loc, ok := urlLocator(c.get("url")); ok {
			return one(KindEndpoint, "http", loc, "")
		}
	case "websocket":
		if loc, ok := urlLocator(c.get("url")); ok {
			return one(KindEndpoint, "websocket", loc, "")
		}
	case "googlesheets":
		if id := c.get("spreadsheet_id"); id != "" {
			return one(KindSheet, "googlesheets", id, c.get("range"))
		}
	case "sap":
		if h := hostOf(c.get("host")); h != "" && c.get("entity") != "" {
			return one(KindEntity, "sap", joinPath(h, c.get("service")), c.get("entity"))
		}
	case "dynamics365":
		if h := hostOf(c.get("resource")); h != "" && c.get("entity") != "" {
			return one(KindEntity, "dynamics365", h, c.get("entity"))
		}
	case "metis":
		if h := hostOf(c.get("base_url")); h != "" && isSource && c.get("stream") != "" {
			return one(KindStream, "metis", h, c.get("stream"))
		}
	case "metis_task":
		if h := hostOf(c.get("base_url")); h != "" && c.get("topic") != "" {
			return one(KindTopic, "metis", h, c.get("topic"))
		}
	case "firebase":
		if p := c.get("project_id"); p != "" && c.get("collection") != "" {
			return one(KindCollection, "firebase", p, c.get("collection"))
		}
	case "googleanalytics":
		if p := c.get("property_id"); p != "" {
			return one(KindEndpoint, "googleanalytics", p, "")
		}
	case "slack", "discord":
		if ch := c.get("channel_id"); ch != "" {
			return one(KindChannel, typ, ch, "")
		}
	case "facebook":
		if p := c.get("page_id"); p != "" {
			return one(KindChannel, "facebook", p, "")
		}
	}
	return nil
}

// fileAssets is where a file or excel source reads, by its backend
// (source_type). base is the local path to use; empty reads local_path or
// file_path as the factory does.
func fileAssets(backend string, c cfgMap, base string) []asset {
	switch backend {
	case "http":
		if loc, ok := urlLocator(c.get("url")); ok {
			return one(KindEndpoint, "http", loc, "")
		}
	case "s3":
		if b := c.get("s3_bucket"); b != "" {
			return one(KindObject, "s3", b, c.get("s3_key"))
		}
	case "ftp", "sftp":
		system, port := "ftp", "21"
		if backend == "sftp" || c.get("use_sftp") == "true" {
			system, port = "sftp", "22"
		}
		if p := c.get("ftp_port"); p != "" {
			port = p
		}
		if c.get("ftp_host") != "" {
			return one(KindFile, system, hostPort(c.get("ftp_host"), port), c.get("ftp_root"))
		}
	default:
		if base == "" {
			base = joinFile(c.first("local_path", "file_path"), c.get("pattern"))
		}
		if base != "" {
			return one(KindFile, "file", "", base)
		}
	}
	return nil
}

// eventStoreDB is the database an event store keeps its log in: its dsn, or
// BuildConnectionString(config, driver).
func eventStoreDB(c cfgMap) (asset, bool) {
	driver := c.get("driver")
	loc, ok := connLocator(cfgMap{"dsn": c.get("dsn")}, false)
	if !ok {
		loc, ok = connLocator(cfgMap{"host": c.get("host"), "port": c.get("port"), "dbname": c.get("dbname")}, keyBuilt[driver])
	}
	if !ok {
		return asset{}, false
	}
	return asset{kind: KindDatabase, system: "eventstore", locator: loc, resolved: true}, true
}

// joinFile joins a directory and a file pattern, keeping an absolute path
// absolute.
func joinFile(dir, name string) string {
	switch {
	case name == "":
		return dir
	case dir == "":
		return name
	}
	return strings.TrimRight(dir, "/") + "/" + strings.TrimLeft(name, "/")
}

func joinDot(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

// vhostOrDefault is the vhost a record with none belongs to, as the ML
// registry and the Predict node read it.
func vhostOrDefault(v string) string {
	if v == "" {
		return "default"
	}
	return v
}

func datasetAsset(vhost, name string) asset {
	return asset{kind: KindDataset, system: "ml", locator: vhostOrDefault(vhost), object: name, resolved: true}
}

func modelAsset(vhost, name string) asset {
	return asset{kind: KindModel, system: "ml", locator: vhostOrDefault(vhost), object: name, resolved: true}
}
