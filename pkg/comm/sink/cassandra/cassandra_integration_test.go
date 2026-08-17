//go:build integration

package cassandra

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

// The Cassandra sink, against a real node.
//
// Cassandra was the last connector recorded as unreachable from an arm64
// workstation. The official image publishes arm64 and runs fine under Apple's
// container runtime — it is heavy and slow to start, which is a different
// problem from impossible.
//
// Beyond "does a row land", the thing worth a node is the table name. When the
// sink is not pinned to a table it takes one from msg.Table() and interpolates
// it into CQL, exactly as the ClickHouse sink did before that was fixed. A
// message's table originates upstream.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 CASSANDRA_HOSTS=127.0.0.1 \
//	go test -tags=integration ./pkg/comm/sink/cassandra/

const testKeyspace = "hermod_it"

func requireCassandra(t *testing.T) ([]string, string, *gocql.Session) {
	t.Helper()
	raw := os.Getenv("CASSANDRA_HOSTS")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || raw == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q CASSANDRA_HOSTS=%q in CI, where a node is "+
				"started for exactly this", os.Getenv("HERMOD_INTEGRATION"), raw)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and CASSANDRA_HOSTS to run")
	}
	hosts := strings.Split(raw, ",")

	cluster := gocql.NewCluster(hosts...)
	cluster.Keyspace = testKeyspace
	cluster.Timeout = 15 * time.Second
	cluster.ConnectTimeout = 15 * time.Second
	session, err := cluster.CreateSession()
	if err != nil {
		t.Fatalf("Cassandra at %s is not reachable: %v", raw, err)
	}
	t.Cleanup(session.Close)

	table := "c_" + strings.ToLower(t.Name())
	drop := func() {
		_ = session.Query(fmt.Sprintf("DROP TABLE IF EXISTS %s.%s", testKeyspace, table)).Exec()
	}
	drop()
	t.Cleanup(drop)
	return hosts, table, session
}

func cMsg(t *testing.T, id, name string, op hermod.Operation) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	t.Cleanup(m.Release)
	m.SetID(id)
	m.SetOperation(op)
	m.SetData("id", id)
	m.SetData("name", name)
	return m
}

// A row lands and a delete removes it.
func TestARowLandsAndIsDeleted(t *testing.T) {
	hosts, table, session := requireCassandra(t)

	if err := session.Query(fmt.Sprintf(
		"CREATE TABLE %s.%s (id text PRIMARY KEY, data blob)", testKeyspace, table)).Exec(); err != nil {
		t.Fatalf("create: %v", err)
	}

	sink := NewCassandraSink(hosts, testKeyspace, table, nil, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	if err := sink.Write(t.Context(), cMsg(t, "a", "ada", hermod.OpCreate)); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var n int
	if err := session.Query(fmt.Sprintf(
		"SELECT count(*) FROM %s.%s WHERE id = ?", testKeyspace, table), "a").Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 1 {
		t.Fatalf("the row landed %d times, want 1", n)
	}

	if err := sink.Write(t.Context(), cMsg(t, "a", "ada", hermod.OpDelete)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := session.Query(fmt.Sprintf(
		"SELECT count(*) FROM %s.%s WHERE id = ?", testKeyspace, table), "a").Scan(&n); err != nil {
		t.Fatalf("counting after delete: %v", err)
	}
	if n != 0 {
		t.Errorf("the deleted row is still present %d time(s)", n)
	}
}

// The table name can come from the message, and nothing checked it.
//
// With no table configured the sink uses msg.Table() and interpolates it into
// INSERT and DELETE. A message's table originates upstream — on the wire, for a
// webhook or a generic source — so this is an identifier from outside going
// into CQL unexamined. The PostgreSQL and ClickHouse sinks both validate this
// same input; this one did not.
func TestAnUnsafeTableNameFromAMessageIsRefused(t *testing.T) {
	hosts, _, session := requireCassandra(t)

	// Not pinned, so the name comes from the message.
	sink := NewCassandraSink(hosts, testKeyspace, "", nil, true, "", "", "", "", false, false)
	t.Cleanup(func() { _ = sink.Close() })

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetID("a")
	msg.SetOperation(hermod.OpCreate)
	msg.SetPayload([]byte(`{"v":1}`))
	msg.SetTable(`pwned (id, data) VALUES ('x', null) --`)

	err := sink.Write(t.Context(), msg)
	if err == nil {
		t.Fatal("a message carrying an unsafe table name was written without complaint")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "table") {
		t.Errorf("the write failed, but not because the sink refused the name: %v\n"+
			"that is Cassandra rejecting one payload rather than the name being kept out "+
			"of the statement", err)
	}

	// And nothing of that shape should exist.
	var count int
	_ = session.Query(
		"SELECT count(*) FROM system_schema.tables WHERE keyspace_name = ? AND table_name = ?",
		testKeyspace, "pwned").Scan(&count)
	if count != 0 {
		t.Errorf("a table matching the injected name exists")
	}
}
