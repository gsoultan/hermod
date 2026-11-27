// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package scale

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/jackc/pgx/v5"

	// The steps the workflows run register themselves when imported, as they
	// do in cmd/hermod.
	_ "github.com/gsoultan/hermod/internal/engine/registry/nodes/core"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/advanced"
	_ "github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// harnessApp names the harness's own connections, which are left out of the
// connection counts: they load and read the database, Hermod does not.
const harnessApp = "hermod-scale-harness"

// sharedDBConfig is one topology: N workflows reading from Sources tables and
// writing to Sinks tables of one PostgreSQL database, with every LookupEvery-th
// workflow running a DB Lookup against it on the way.
type sharedDBConfig struct {
	Workflows    int
	Sources      int
	Sinks        int
	LookupEvery  int
	RowsPerSec   int // per source table
	Duration     time.Duration
	PollInterval time.Duration
	Drain        time.Duration
}

// sharedDBResult is what one step of TestScaleSharedDatabase measured.
type sharedDBResult struct {
	Workflows        int     `json:"workflows"`
	Sources          int     `json:"sources"`
	Sinks            int     `json:"sinks"`
	Expected         int64   `json:"expected_deliveries"`
	Delivered        int64   `json:"delivered"`
	Duplicates       int64   `json:"duplicates"`
	SuccessRate      float64 `json:"success_rate"`
	DelayP50Millis   float64 `json:"delay_p50_ms"`
	DelayP95Millis   float64 `json:"delay_p95_ms"`
	DelayP99Millis   float64 `json:"delay_p99_ms"`
	PeakConnections  int     `json:"peak_connections"`
	PeakWaitingLocks int     `json:"peak_waiting_on_locks"`
	MaxConnections   int     `json:"server_max_connections"`
	Goroutines       int     `json:"goroutines"`
	HeapBytes        int64   `json:"heap_bytes"`
}

// TestScaleSharedDatabase runs many workflows that share sources and sinks on
// one real PostgreSQL database through today's real connectors, and measures
// what the scaling plan is about: how many messages arrive (success rate), how
// late (delay percentiles), and how many connections Hermod holds on the
// database to do it.
//
// It needs HERMOD_SCALE_DSN, a PostgreSQL URL for a database the test may
// create and drop the schema hermod_scale in. Point it at PgBouncer to measure
// the path production uses. Tune it with:
//
//	HERMOD_SCALE_STEPS          workflow counts, default "100,1000"
//	HERMOD_SCALE_SOURCES        source tables shared by the workflows, default 10
//	HERMOD_SCALE_SINKS          sink tables shared by the workflows, default 10
//	HERMOD_SCALE_LOOKUP_EVERY   every Nth workflow runs a DB Lookup, default 2
//	HERMOD_SCALE_ROWS_PER_SEC   rows inserted per source table per second, default 5
//	HERMOD_SCALE_DURATION       how long rows are inserted, default 30s
//	HERMOD_SCALE_POLL_INTERVAL  each source's poll interval, default 1s
//	HERMOD_SCALE_DRAIN          how long to wait for the last rows, default 60s
//	HERMOD_SCALE_MIN_SUCCESS    fail below this success rate, 0..1, default 0
//
// By default it reports the success rate rather than failing on it, because
// at a size the machine cannot carry a low rate is the measurement, not a
// defect. CI sets HERMOD_SCALE_MIN_SUCCESS=1 on a size it can carry, so a
// change that loses messages there fails.
func TestScaleSharedDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("load test; runs without -short")
	}
	dsn := strings.TrimSpace(os.Getenv("HERMOD_SCALE_DSN"))
	if dsn == "" {
		t.Skip("HERMOD_SCALE_DSN is not set")
	}
	base := sharedDBConfig{
		Sources:      intFromEnv(t, "HERMOD_SCALE_SOURCES", 10),
		Sinks:        intFromEnv(t, "HERMOD_SCALE_SINKS", 10),
		LookupEvery:  intFromEnv(t, "HERMOD_SCALE_LOOKUP_EVERY", 2),
		RowsPerSec:   intFromEnv(t, "HERMOD_SCALE_ROWS_PER_SEC", 5),
		Duration:     durationFromEnv(t, "HERMOD_SCALE_DURATION", 30*time.Second),
		PollInterval: durationFromEnv(t, "HERMOD_SCALE_POLL_INTERVAL", time.Second),
		Drain:        durationFromEnv(t, "HERMOD_SCALE_DRAIN", 60*time.Second),
	}

	minSuccess := floatFromEnv(t, "HERMOD_SCALE_MIN_SUCCESS", 0)

	var results []sharedDBResult
	for _, n := range stepsFromEnv(t, "HERMOD_SCALE_STEPS", []int{100, 1000}) {
		cfg := base
		cfg.Workflows = n
		res := runSharedDB(t, dsn, cfg)
		results = append(results, res)
		t.Logf("%6d workflows on %d sources / %d sinks: success %.2f%% (%d of %d, %d duplicates), delay p50 %.0fms p95 %.0fms p99 %.0fms, peak %d of %d connections, %d goroutines, %s heap",
			res.Workflows, res.Sources, res.Sinks, res.SuccessRate*100, res.Delivered, res.Expected, res.Duplicates,
			res.DelayP50Millis, res.DelayP95Millis, res.DelayP99Millis, res.PeakConnections, res.MaxConnections,
			res.Goroutines, mib(res.HeapBytes))
		if res.SuccessRate < minSuccess {
			t.Errorf("%d workflows: success rate %.4f is below HERMOD_SCALE_MIN_SUCCESS %.4f", n, res.SuccessRate, minSuccess)
		}
	}
	writeReport(t, "shared_database", results)
}

func runSharedDB(t *testing.T, dsn string, cfg sharedDBConfig) sharedDBResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), cfg.Duration+cfg.Drain+10*time.Minute)
	defer cancel()

	admin := harnessConn(ctx, t, dsn)
	defer admin.Close(context.Background())
	prepareSchema(ctx, t, admin, cfg)

	store := newScaleStorage()
	conn := connectionConfig(t, dsn)
	for i := range cfg.Sources {
		store.putSource(pollingSource(i, conn, cfg.PollInterval))
	}
	for j := range cfg.Sinks {
		store.putSink(mappedSink(j, conn))
	}

	reg := registry.NewRegistry(store)
	defer reg.Close()

	ids := make([]string, cfg.Workflows)
	for i := range cfg.Workflows {
		wf := sharedWorkflow(i, cfg)
		store.putWorkflow(wf)
		if err := reg.StartWorkflow(wf.ID, wf); err != nil {
			t.Fatalf("starting workflow %d of %d: %v", i+1, cfg.Workflows, err)
		}
		ids[i] = wf.ID
	}

	sampler := startSampler(ctx, t, dsn)
	inserted := loadSources(ctx, t, dsn, cfg)
	// Measured while every workflow is still running: after they stop it
	// would only describe the harness.
	g, h := settledFootprint()
	expected := expectedDeliveries(cfg, inserted)
	waitForDeliveries(ctx, t, admin, expected, cfg.Drain)
	peaks := sampler.stop()

	stopAll(t, reg, ids)

	res := readDeliveries(ctx, t, admin)
	res.Workflows, res.Sources, res.Sinks = cfg.Workflows, cfg.Sources, cfg.Sinks
	res.Expected = expected
	if expected > 0 {
		res.SuccessRate = float64(res.Delivered) / float64(expected)
	}
	res.PeakConnections, res.PeakWaitingLocks, res.MaxConnections = peaks.connections, peaks.lockWaits, peaks.maxConnections
	res.Goroutines, res.HeapBytes = g, h
	return res
}

func harnessConn(ctx context.Context, t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("HERMOD_SCALE_DSN: %v", err)
	}
	cfg.RuntimeParams["application_name"] = harnessApp
	// Simple protocol, so the harness also works through PgBouncer in
	// transaction mode.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting to HERMOD_SCALE_DSN: %v", err)
	}
	return conn
}

// prepareSchema recreates hermod_scale: Sources source tables, Sinks sink
// tables and a customers table for the lookups.
func prepareSchema(ctx context.Context, t *testing.T, conn *pgx.Conn, cfg sharedDBConfig) {
	t.Helper()
	stmts := []string{
		"DROP SCHEMA IF EXISTS hermod_scale CASCADE",
		"CREATE SCHEMA hermod_scale",
		"CREATE TABLE hermod_scale.customers (id int PRIMARY KEY, name text NOT NULL)",
		"INSERT INTO hermod_scale.customers SELECT g, 'customer ' || g FROM generate_series(1, 1000) g",
	}
	for i := range cfg.Sources {
		stmts = append(stmts, fmt.Sprintf(
			"CREATE TABLE hermod_scale.src_%d (id bigserial PRIMARY KEY, customer_id int NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp())", i))
	}
	for j := range cfg.Sinks {
		stmts = append(stmts, fmt.Sprintf(
			"CREATE TABLE hermod_scale.snk_%d (wf text NOT NULL, src_id bigint NOT NULL, created_at timestamptz NOT NULL, customer_name text, received_at timestamptz NOT NULL DEFAULT clock_timestamp())", j))
	}
	views := make([]string, cfg.Sinks)
	for j := range cfg.Sinks {
		views[j] = fmt.Sprintf("SELECT wf, src_id, created_at, received_at FROM hermod_scale.snk_%d", j)
	}
	stmts = append(stmts, "CREATE VIEW hermod_scale.deliveries AS "+strings.Join(views, " UNION ALL "))
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("preparing the schema: %s: %v", s, err)
		}
	}
}

// connectionConfig turns the DSN into the host/port/user fields a saved
// source holds, which is how a real source reaches its database.
func connectionConfig(t *testing.T, dsn string) map[string]string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		t.Fatalf("HERMOD_SCALE_DSN must be a postgres:// URL: %v", err)
	}
	pw, _ := u.User.Password()
	cfg := map[string]string{
		"host":     u.Hostname(),
		"port":     u.Port(),
		"user":     u.User.Username(),
		"password": pw,
		"dbname":   strings.TrimPrefix(u.Path, "/"),
	}
	if cfg["port"] == "" {
		cfg["port"] = "5432"
	}
	if mode := u.Query().Get("sslmode"); mode != "" {
		cfg["sslmode"] = mode
	}
	return cfg
}

func pollingSource(i int, conn map[string]string, poll time.Duration) storage.Source {
	cfg := maps(conn)
	cfg["use_cdc"] = "false"
	cfg["poll_interval"] = poll.String()
	cfg["query"] = fmt.Sprintf("SELECT id, customer_id, created_at FROM hermod_scale.src_%d WHERE id > $1 ORDER BY id LIMIT 500", i)
	return storage.Source{ID: fmt.Sprintf("src-%d", i), Name: fmt.Sprintf("src_%d", i), Type: "postgres", Active: true, Config: cfg}
}

func mappedSink(j int, conn map[string]string) storage.Sink {
	cfg := maps(conn)
	cfg["table"] = fmt.Sprintf("hermod_scale.snk_%d", j)
	cfg["use_existing_table"] = "true"
	cfg["operation_mode"] = "insert"
	mappings, _ := json.Marshal([]map[string]string{
		{"source_field": "wf", "target_column": "wf"},
		{"source_field": "id", "target_column": "src_id"},
		{"source_field": "created_at", "target_column": "created_at"},
		{"source_field": "customer_name", "target_column": "customer_name"},
	})
	cfg["column_mappings"] = string(mappings)
	return storage.Sink{ID: fmt.Sprintf("snk-%d", j), Name: fmt.Sprintf("snk_%d", j), Type: "postgres", Active: true, Config: cfg}
}

func maps(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+4)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// sharedWorkflow is workflow i: source i%Sources, a step that stamps the
// workflow's own id on the message, a DB Lookup on every LookupEvery-th one,
// and sink i%Sinks.
func sharedWorkflow(i int, cfg sharedDBConfig) storage.Workflow {
	id := fmt.Sprintf("wf-%d", i)
	srcID := fmt.Sprintf("src-%d", i%cfg.Sources)
	nodes := []storage.WorkflowNode{
		{ID: "src", Type: "source", RefID: srcID},
		{ID: "stamp", Type: "transformation", Config: map[string]any{"transType": "set", "column.wf": "'" + id + "'"}},
	}
	edges := []storage.WorkflowEdge{{ID: "e1", SourceID: "src", TargetID: "stamp"}}
	last := "stamp"
	if cfg.LookupEvery > 0 && i%cfg.LookupEvery == 0 {
		nodes = append(nodes, storage.WorkflowNode{ID: "lookup", Type: "transformation", Config: map[string]any{
			"transType":   "db_lookup",
			"sourceId":    srcID,
			"table":       "hermod_scale.customers",
			"keyColumn":   "id",
			"valueColumn": "name",
			"keyField":    "customer_id",
			"targetField": "customer_name",
		}})
		edges = append(edges, storage.WorkflowEdge{ID: "e2", SourceID: last, TargetID: "lookup"})
		last = "lookup"
	}
	nodes = append(nodes, storage.WorkflowNode{ID: "snk", Type: "sink", RefID: fmt.Sprintf("snk-%d", i%cfg.Sinks)})
	edges = append(edges, storage.WorkflowEdge{ID: "e3", SourceID: last, TargetID: "snk"})
	return storage.Workflow{ID: id, Name: id, Active: true, Nodes: nodes, Edges: edges}
}

// loadSources inserts RowsPerSec rows a second into every source table for
// Duration, and returns how many went into each.
func loadSources(ctx context.Context, t *testing.T, dsn string, cfg sharedDBConfig) []int64 {
	t.Helper()
	conn := harnessConn(ctx, t, dsn)
	defer conn.Close(context.Background())

	inserted := make([]int64, cfg.Sources)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	deadline := time.Now().Add(cfg.Duration)
	for time.Now().Before(deadline) {
		for i := range cfg.Sources {
			q := fmt.Sprintf("INSERT INTO hermod_scale.src_%d (customer_id) SELECT 1 + (random() * 999)::int FROM generate_series(1, %d)", i, cfg.RowsPerSec)
			if _, err := conn.Exec(ctx, q); err != nil {
				t.Fatalf("loading src_%d: %v", i, err)
			}
			inserted[i] += int64(cfg.RowsPerSec)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("load interrupted: %v", ctx.Err())
		case <-tick.C:
		}
	}
	return inserted
}

// expectedDeliveries is what a perfect run delivers: every workflow sees every
// row of its own source table once.
func expectedDeliveries(cfg sharedDBConfig, inserted []int64) int64 {
	var total int64
	for i := range cfg.Workflows {
		total += inserted[i%cfg.Sources]
	}
	return total
}

// waitForDeliveries waits until the sink tables hold expected distinct
// deliveries or drain has passed, whichever is first.
func waitForDeliveries(ctx context.Context, t *testing.T, conn *pgx.Conn, expected int64, drain time.Duration) {
	t.Helper()
	deadline := time.Now().Add(drain)
	for time.Now().Before(deadline) {
		var got int64
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM (SELECT DISTINCT wf, src_id FROM hermod_scale.deliveries) d").Scan(&got); err != nil {
			t.Fatalf("counting deliveries: %v", err)
		}
		if got >= expected {
			return
		}
		time.Sleep(time.Second)
	}
}

// readDeliveries counts distinct deliveries and duplicates and reads the
// delay percentiles from the database, so millions of rows never cross into
// the test.
func readDeliveries(ctx context.Context, t *testing.T, conn *pgx.Conn) sharedDBResult {
	t.Helper()
	var res sharedDBResult
	var total int64
	var p50, p95, p99 *float64
	err := conn.QueryRow(ctx, `
		SELECT count(*),
		       count(DISTINCT (wf, src_id)),
		       percentile_cont(0.50) WITHIN GROUP (ORDER BY extract(epoch FROM received_at - created_at) * 1000),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM received_at - created_at) * 1000),
		       percentile_cont(0.99) WITHIN GROUP (ORDER BY extract(epoch FROM received_at - created_at) * 1000)
		FROM hermod_scale.deliveries`).Scan(&total, &res.Delivered, &p50, &p95, &p99)
	if err != nil {
		t.Fatalf("reading deliveries: %v", err)
	}
	res.Duplicates = total - res.Delivered
	for _, p := range []struct {
		dst *float64
		src *float64
	}{{&res.DelayP50Millis, p50}, {&res.DelayP95Millis, p95}, {&res.DelayP99Millis, p99}} {
		if p.src != nil {
			*p.dst = *p.src
		}
	}
	return res
}

type samplePeaks struct {
	connections    int
	lockWaits      int
	maxConnections int
}

type sampler struct {
	cancel context.CancelFunc
	done   chan struct{}
	peaks  samplePeaks
	once   sync.Once
}

// startSampler reads pg_stat_activity every 200ms and keeps the peak number of
// connections Hermod holds on the database, and how many of them were waiting
// on a lock. Through PgBouncer this counts the server connections PgBouncer
// opened for Hermod, which is what the database actually carries.
func startSampler(ctx context.Context, t *testing.T, dsn string) *sampler {
	t.Helper()
	conn := harnessConn(ctx, t, dsn)
	s := &sampler{done: make(chan struct{})}
	if err := conn.QueryRow(ctx, "SELECT current_setting('max_connections')::int").Scan(&s.peaks.maxConnections); err != nil {
		t.Fatalf("reading max_connections: %v", err)
	}
	sctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go func() {
		defer close(s.done)
		defer conn.Close(context.Background())
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			var n, waiting int
			err := conn.QueryRow(sctx, `
				SELECT count(*), count(*) FILTER (WHERE wait_event_type = 'Lock')
				FROM pg_stat_activity
				WHERE datname = current_database() AND backend_type = 'client backend'
				  AND application_name <> $1`, harnessApp).Scan(&n, &waiting)
			if err == nil {
				s.peaks.connections = max(s.peaks.connections, n)
				s.peaks.lockWaits = max(s.peaks.lockWaits, waiting)
			}
			select {
			case <-sctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return s
}

func (s *sampler) stop() samplePeaks {
	s.once.Do(func() {
		s.cancel()
		<-s.done
	})
	return s.peaks
}
