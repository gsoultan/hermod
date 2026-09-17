package lookup

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/batcher"
	_ "modernc.org/sqlite"
)

// A db_lookup is a query against a table, not a replication stream. Pointing it
// at a CDC source puts per-message query load on a database that is already
// paying for logical replication, which is why db_lookup has refused CDC
// sources since it was written (db_lookup.go).
//
// The refusal had two holes. It only fired when the source carried an explicit
// use_cdc key, but the factory that builds the source reads the flag as
// opt-out -- `useCDC := cfg.Config["use_cdc"] != "false"`
// (internal/factory/factory.go:160) -- so a source with no key at all runs as a
// CDC source and passed the lookup's check. And it lived inside the `else` of
// the batching branch, so any node with Batch Lookups enabled skipped it
// outright, cached the result, and served every later message from that cache.
//
// SQL Server is the documented exception: its CDC is read back through ordinary
// queries against change tables, so it does not carry the replication cost the
// rule exists to avoid.
func newCDCGuardFixture(t *testing.T, src storage.Source) (*DBLookupTransformer, *cachingFakeRegistry) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users(id, email) VALUES ('u1','ada@example.com')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tr := &DBLookupTransformer{batchers: make(map[string]*batcher.Batcher[any, any])}
	return tr, &cachingFakeRegistry{db: db, source: src, cache: map[string]any{}}
}

func cdcGuardConfig() map[string]any {
	return map[string]any{
		"sourceId":    "src1",
		"mode":        "table",
		"table":       "users",
		"keyColumn":   "id",
		"keyField":    "user_id",
		"valueColumn": "email",
		"targetField": "user_email",
	}
}

// runCDCGuard runs one message through the transformer and reports the error it
// returned, if any.
func runCDCGuard(t *testing.T, src storage.Source, cfg map[string]any) error {
	t.Helper()

	tr, reg := newCDCGuardFixture(t, src)

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("user_id", "u1")

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	ctx = context.WithValue(ctx, hermod.NodeIDKey, "lookup-node")

	_, err := tr.Transform(ctx, msg, cfg)
	return err
}

func isCDCRefusal(err error) bool {
	return err != nil && strings.Contains(err.Error(), "requires a non-CDC source")
}

func TestDBLookupRejectsCDCSources(t *testing.T) {
	batched := func(cfg map[string]any) map[string]any {
		cfg["use_batching"] = "true"
		return cfg
	}

	cases := []struct {
		name   string
		src    storage.Source
		cfg    map[string]any
		reject bool
	}{
		{
			name:   "CDC switched off explicitly is the shape a lookup source is meant to have",
			src:    storage.Source{ID: "src1", Name: "customers", Type: "sqlite", Config: hermod.StringMap{"use_cdc": "false"}},
			cfg:    cdcGuardConfig(),
			reject: false,
		},
		{
			name:   "CDC switched on is refused",
			src:    storage.Source{ID: "src1", Name: "orders", Type: "sqlite", Config: hermod.StringMap{"use_cdc": "true"}},
			cfg:    cdcGuardConfig(),
			reject: true,
		},
		{
			name:   "no use_cdc key means CDC is on, the same reading the factory uses",
			src:    storage.Source{ID: "src1", Name: "orders", Type: "sqlite"},
			cfg:    cdcGuardConfig(),
			reject: true,
		},
		{
			name:   "batching does not buy an exemption",
			src:    storage.Source{ID: "src1", Name: "orders", Type: "sqlite", Config: hermod.StringMap{"use_cdc": "true"}},
			cfg:    batched(cdcGuardConfig()),
			reject: true,
		},
		{
			name:   "batching does not buy an exemption for a source with no key either",
			src:    storage.Source{ID: "src1", Name: "orders", Type: "sqlite"},
			cfg:    batched(cdcGuardConfig()),
			reject: true,
		},
		{
			name:   "batching against a non-CDC source still runs",
			src:    storage.Source{ID: "src1", Name: "customers", Type: "sqlite", Config: hermod.StringMap{"use_cdc": "false"}},
			cfg:    batched(cdcGuardConfig()),
			reject: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runCDCGuard(t, tc.src, tc.cfg)
			switch {
			case tc.reject && !isCDCRefusal(err):
				t.Fatalf("a CDC source was accepted for a lookup; err = %v", err)
			case !tc.reject && err != nil:
				t.Fatalf("a non-CDC source was refused: %v", err)
			}
		})
	}
}

// SQL Server reads its change tables with ordinary queries, so the rule that
// keeps lookups off replication-configured databases does not apply to it.
func TestDBLookupAllowsCDCOnSQLServer(t *testing.T) {
	src := storage.Source{ID: "src1", Name: "erp", Type: "mssql", Config: hermod.StringMap{"use_cdc": "true"}}

	// The query itself runs against sqlite with SQL Server's placeholder style,
	// so it is expected to fail -- what matters is that it got as far as the
	// query instead of being turned away at the guard.
	if err := runCDCGuard(t, src, cdcGuardConfig()); isCDCRefusal(err) {
		t.Fatalf("SQL Server is the documented exception but was refused: %v", err)
	}
}

// The refusal is a configuration error, not a lookup that found no row, so it
// must not be swallowed by onMiss -- a passthrough policy would let every
// message reach the sink unenriched with nothing in the log.
func TestDBLookupCDCRefusalIgnoresMissPolicy(t *testing.T) {
	src := storage.Source{ID: "src1", Name: "orders", Type: "sqlite", Config: hermod.StringMap{"use_cdc": "true"}}

	cfg := cdcGuardConfig()
	cfg["onMiss"] = "passthrough"

	if err := runCDCGuard(t, src, cfg); !isCDCRefusal(err) {
		t.Fatalf("onMiss=passthrough hid the misconfiguration; err = %v", err)
	}
}
