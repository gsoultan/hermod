// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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

// oneIf is one(...) when ok holds, and nothing otherwise.
func oneIf(ok bool, kind, system, locator, object string) []asset {
	if !ok {
		return nil
	}
	return one(kind, system, locator, object)
}

// reader derives the assets of one connector type from its config.
type reader func(typ string, c cfgMap) []asset

// sourceAssets are the assets a source reads. sources resolves a batch_sql
// delegate; it is nil-safe.
func sourceAssets(src storage.Source, sources map[string]storage.Source) []asset {
	c := cfgMap(src.Config)
	if db, ok := sqlDatabase(src.Type, c); ok {
		return tables(db, list(c.get("tables"))...)
	}
	if src.Type == "batch_sql" {
		return batchSQLAssets(src, c, sources)
	}
	if read, ok := sourceReaders[src.Type]; ok {
		return read(src.Type, c)
	}
	return messagingAssets(src.Type, c, true)
}

// batchSQLAssets is the database of a batch_sql source's delegate: its
// queries are free SQL, so the database is what is known.
func batchSQLAssets(src storage.Source, c cfgMap, sources map[string]storage.Source) []asset {
	d, ok := sources[c.get("source_id")]
	if !ok || d.ID == src.ID {
		return nil
	}
	if db, ok := sqlDatabase(d.Type, cfgMap(d.Config)); ok {
		return []asset{db}
	}
	return nil
}

// sourceReaders are the source types whose keys differ from their sink's.
var sourceReaders = map[string]reader{
	"mongodb": func(_ string, c cfgMap) []asset {
		loc, ok := connLocator(cfgMap{"uri": c.get("uri")}, false)
		if !ok {
			loc = hostPort(c.get("host"), c.get("port"))
		}
		return oneIf(loc != "" && c.get("collection") != "", KindCollection, "mongodb", loc, c.get("database")+"."+c.get("collection"))
	},
	"cassandra": cassandraSource,
	"scylladb":  cassandraSource,
	"mainframe": func(_ string, c cfgMap) []asset {
		hp := hostPort(c.get("host"), c.get("port"))
		return oneIf(hp != "" && c.get("table") != "", KindTable, "mainframe", joinPath(hp, c.get("database")), joinDot(c.get("schema"), c.get("table")))
	},
	"eventstore": func(_ string, c cfgMap) []asset {
		db, ok := eventStoreDB(c)
		if !ok {
			return nil
		}
		if s := c.get("stream_id"); s != "" {
			db.kind, db.object = KindStream, s
		}
		return []asset{db}
	},
	"excel": func(_ string, c cfgMap) []asset {
		return fileAssets(strings.ToLower(c.get("source_type")), c, joinFile(c.first("base_path", "local_path"), c.get("pattern")))
	},
	"file": func(_ string, c cfgMap) []asset {
		backend := strings.ToLower(c.get("source_type"))
		base := ""
		if c.get("format") == "csv" && backend != "http" && backend != "s3" {
			// The CSV reader takes file_path only (sourcefile.NewCSVSource).
			backend, base = "local", c.get("file_path")
		}
		return fileAssets(backend, c, base)
	},
}

func cassandraSource(typ string, c cfgMap) []asset {
	hosts := sortedList(c.get("hosts"), strings.ToLower)
	var out []asset
	for _, t := range list(c.get("tables")) {
		out = append(out, one(KindTable, typ, hosts, t)...)
	}
	return out
}

// sinkAssets are the assets a sink writes. sinks resolves the members of a
// failover or txgroup sink.
func sinkAssets(snk storage.Sink, sinks map[string]storage.Sink, seen map[string]bool) []asset {
	c := cfgMap(snk.Config)
	if snk.Type == "failover" || snk.Type == "txgroup" {
		return memberAssets(snk, c, sinks, seen)
	}
	if read, ok := sinkReaders[snk.Type]; ok {
		return read(snk.Type, c)
	}
	if db, ok := sqlDatabase(snk.Type, c); ok {
		return tables(db, c.get("table"))
	}
	if snk.Type == "ml_dataset" {
		if d := c.get("dataset"); d != "" {
			return []asset{datasetAsset(snk.VHost, d)}
		}
		return nil
	}
	return messagingAssets(snk.Type, c, false)
}

// memberAssets are the assets of every member of a failover or txgroup sink,
// each composite visited once.
func memberAssets(snk storage.Sink, c cfgMap, sinks map[string]storage.Sink, seen map[string]bool) []asset {
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
}

// sinkReaders are the sink types whose keys differ from their source's.
var sinkReaders = map[string]reader{
	"clickhouse": func(_ string, c cfgMap) []asset {
		loc := joinPath(strings.ToLower(c.get("addr")), c.get("database"))
		return oneIf(loc != "" && c.get("table") != "", KindTable, "clickhouse", loc, c.get("table"))
	},
	"mongodb": func(_ string, c cfgMap) []asset {
		loc, ok := connLocator(cfgMap{"uri": c.get("uri")}, false)
		return oneIf(ok && c.get("table") != "", KindCollection, "mongodb", loc, c.get("database")+"."+c.get("table"))
	},
	"cassandra": func(_ string, c cfgMap) []asset {
		t := c.get("table")
		return oneIf(t != "", KindTable, "cassandra", sortedList(c.get("hosts"), strings.ToLower), joinDot(c.get("keyspace"), t))
	},
	"eventstore": func(_ string, c cfgMap) []asset {
		if db, ok := eventStoreDB(c); ok {
			return []asset{db}
		}
		return nil
	},
	"s3":         s3Sink,
	"s3-parquet": s3Sink,
	"file": func(_ string, c cfgMap) []asset {
		f := c.get("filename")
		return oneIf(f != "", KindFile, "file", "", f)
	},
	"ftp": func(_ string, c cfgMap) []asset {
		port := c.get("port")
		if port == "" || port == "0" {
			port = "21"
		}
		return oneIf(c.get("host") != "", KindFile, "ftp", hostPort(c.get("host"), port), joinFile(c.get("root_dir"), c.get("path_template")))
	},
	"salesforce": func(_ string, c cfgMap) []asset {
		o := c.get("object")
		return oneIf(o != "", KindEntity, "salesforce", "", o)
	},
	"servicenow": func(_ string, c cfgMap) []asset {
		h := hostOf(c.get("instance_url"))
		return oneIf(h != "" && c.get("table") != "", KindTable, "servicenow", h, c.get("table"))
	},
	"metis": func(_ string, c cfgMap) []asset {
		h := hostOf(c.get("base_url"))
		o := c.first("definition_key", "message_name", "signal_name")
		return oneIf(h != "" && o != "", KindProcess, "metis", h, o)
	},
	"telegram": func(_ string, c cfgMap) []asset {
		id := c.get("chat_id")
		return oneIf(id != "", KindChannel, "telegram", id, "")
	},
	"sse": func(_ string, c cfgMap) []asset {
		s := c.get("stream")
		return oneIf(s != "", KindStream, "sse", "", s)
	},
	"pulsar": func(_ string, c cfgMap) []asset {
		loc, ok := urlLocator(c.get("url"))
		return oneIf(ok && c.get("topic") != "", KindTopic, "pulsar", loc, c.get("topic"))
	},
	"kinesis": func(_ string, c cfgMap) []asset {
		s := c.get("stream_name")
		return oneIf(s != "", KindStream, "kinesis", c.get("region"), s)
	},
	"pubsub": func(_ string, c cfgMap) []asset {
		t := c.get("topic_id")
		return oneIf(t != "", KindTopic, "pubsub", c.get("project_id"), t)
	},
	"elasticsearch": func(_ string, c cfgMap) []asset {
		addrs := sortedList(c.get("addresses"), func(a string) string {
			if loc, ok := urlLocator(a); ok {
				return loc
			}
			return strings.ToLower(a)
		})
		return oneIf(addrs != "" && c.get("index") != "", KindIndex, "elasticsearch", addrs, c.get("index"))
	},
}

func s3Sink(_ string, c cfgMap) []asset {
	b := c.get("bucket")
	return oneIf(b != "", KindObject, "s3", b, c.get("key_prefix"))
}

// messagingAssets covers the types whose sources and sinks read the same keys.
func messagingAssets(typ string, c cfgMap, isSource bool) []asset {
	if read, ok := sharedReaders[typ]; ok {
		return read(typ, c)
	}
	switch typ {
	case "mqtt":
		return mqttAssets(c, isSource)
	case "webhook", "chat", "graphql", "grpc", "form":
		// An inbound path exists only on the receiving side.
		p := c.get("path")
		return oneIf(p != "" && isSource, KindEndpoint, typ, "", p)
	case "metis":
		h := hostOf(c.get("base_url"))
		return oneIf(h != "" && isSource && c.get("stream") != "", KindStream, "metis", h, c.get("stream"))
	}
	return nil
}

func mqttAssets(c cfgMap, isSource bool) []asset {
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
}

// sharedReaders are the types whose sources and sinks read the same keys the
// same way.
var sharedReaders = map[string]reader{
	"kafka": func(_ string, c cfgMap) []asset {
		t := c.get("topic")
		return oneIf(t != "", KindTopic, "kafka", sortedList(c.get("brokers"), strings.ToLower), t)
	},
	"nats": func(_ string, c cfgMap) []asset {
		loc, ok := urlLocator(c.get("url"))
		return oneIf(ok && c.get("subject") != "", KindSubject, "nats", loc, c.get("subject"))
	},
	"redis": func(_ string, c cfgMap) []asset {
		s := c.get("stream")
		return oneIf(s != "", KindStream, "redis", strings.ToLower(c.get("addr")), s)
	},
	"redis_set": func(_ string, c cfgMap) []asset {
		// The key is per record, so the database is what is known. An addr
		// held in a secret names nothing that can be shown here.
		addr := strings.ToLower(c.get("addr"))
		return oneIf(addr != "" && !strings.Contains(addr, "{{"), KindDatabase, "redis", addr, redisDBNumber(c.get("db")))
	},
	"rabbitmq": func(_ string, c cfgMap) []asset {
		loc, ok := connLocator(c, true)
		return oneIf(ok && c.get("stream_name") != "", KindStream, "rabbitmq", loc, c.get("stream_name"))
	},
	"rabbitmq_queue": func(_ string, c cfgMap) []asset {
		loc, ok := connLocator(c, true)
		return oneIf(ok && c.get("queue_name") != "", KindQueue, "rabbitmq", loc, c.get("queue_name"))
	},
	"http":      urlEndpoint,
	"websocket": urlEndpoint,
	"googlesheets": func(_ string, c cfgMap) []asset {
		id := c.get("spreadsheet_id")
		return oneIf(id != "", KindSheet, "googlesheets", id, c.get("range"))
	},
	"sap": func(_ string, c cfgMap) []asset {
		h := hostOf(c.get("host"))
		return oneIf(h != "" && c.get("entity") != "", KindEntity, "sap", joinPath(h, c.get("service")), c.get("entity"))
	},
	"dynamics365": func(_ string, c cfgMap) []asset {
		h := hostOf(c.get("resource"))
		return oneIf(h != "" && c.get("entity") != "", KindEntity, "dynamics365", h, c.get("entity"))
	},
	"metis_task": func(_ string, c cfgMap) []asset {
		h := hostOf(c.get("base_url"))
		return oneIf(h != "" && c.get("topic") != "", KindTopic, "metis", h, c.get("topic"))
	},
	"firebase": func(_ string, c cfgMap) []asset {
		p := c.get("project_id")
		return oneIf(p != "" && c.get("collection") != "", KindCollection, "firebase", p, c.get("collection"))
	},
	"googleanalytics": func(_ string, c cfgMap) []asset {
		p := c.get("property_id")
		return oneIf(p != "", KindEndpoint, "googleanalytics", p, "")
	},
	"slack":   channelID,
	"discord": channelID,
	"facebook": func(_ string, c cfgMap) []asset {
		p := c.get("page_id")
		return oneIf(p != "", KindChannel, "facebook", p, "")
	},
}

func urlEndpoint(typ string, c cfgMap) []asset {
	loc, ok := urlLocator(c.get("url"))
	return oneIf(ok, KindEndpoint, typ, loc, "")
}

func channelID(typ string, c cfgMap) []asset {
	ch := c.get("channel_id")
	return oneIf(ch != "", KindChannel, typ, ch, "")
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
