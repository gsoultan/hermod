package conformance_test

import (
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/conformance"
	jsonfmt "github.com/user/hermod/pkg/comm/formatter/json"

	sinkdiscord "github.com/user/hermod/pkg/comm/sink/discord"
	sinkhttp "github.com/user/hermod/pkg/comm/sink/http"
	sinkkafka "github.com/user/hermod/pkg/comm/sink/kafka"
	sinkslack "github.com/user/hermod/pkg/comm/sink/slack"
	sinkstdout "github.com/user/hermod/pkg/comm/sink/stdout"
	sinktelegram "github.com/user/hermod/pkg/comm/sink/telegram"

	srccassandra "github.com/user/hermod/pkg/comm/source/cassandra"
	srcclickhouse "github.com/user/hermod/pkg/comm/source/clickhouse"
	srcdb2 "github.com/user/hermod/pkg/comm/source/db2"
	srcmariadb "github.com/user/hermod/pkg/comm/source/mariadb"
	srcoracle "github.com/user/hermod/pkg/comm/source/oracle"
	srcscylladb "github.com/user/hermod/pkg/comm/source/scylladb"
	srcyugabyte "github.com/user/hermod/pkg/comm/source/yugabyte"
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

func TestSinkConformance(t *testing.T) {

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
}

func TestSourceConformance(t *testing.T) {

	const poll = 50 * time.Millisecond
	tables := []string{"t"}

	// DSNs are syntactically valid but point at an unroutable host, so
	// connection attempts fail rather than hang on DNS or reach a real server.
	conformance.RunSourceSuite(t, "oracle", func() hermod.Source {
		return srcoracle.NewOracleSource("oracle://u:p@"+deadAddr+"/svc", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "db2", func() hermod.Source {
		return srcdb2.NewDB2Source("HOSTNAME="+deadHost+";PORT=9;DATABASE=d;UID=u;PWD=p", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "mariadb", func() hermod.Source {
		return srcmariadb.NewMariaDBSource("u:p@tcp("+deadAddr+")/d", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "clickhouse", func() hermod.Source {
		return srcclickhouse.NewClickHouseSource("clickhouse://"+deadAddr+"/d", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "yugabyte", func() hermod.Source {
		return srcyugabyte.NewYugabyteSource("postgres://u:p@"+deadAddr+"/d", tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "cassandra", func() hermod.Source {
		return srccassandra.NewCassandraSource([]string{deadHost}, tables, "id", poll, true)
	})
	conformance.RunSourceSuite(t, "scylladb", func() hermod.Source {
		return srcscylladb.NewScyllaDBSource([]string{deadHost}, tables, "id", poll, true)
	})
}
