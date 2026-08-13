//go:build integration
// +build integration

package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/user/hermod/internal/storage"
	sqlstorage "github.com/user/hermod/internal/storage/sql"
	"github.com/user/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"

	_ "github.com/user/hermod/pkg/comm/transformer/lookup"
)

// ---------------------------------------------------------------------------
// db_lookup, against a real database.
//
// The last of the eleven transformation types the retired browser specs
// exercised to have no Go coverage. It enriches a message from a table in a
// registered source, so unlike api_lookup it needs both a database and a
// Registry to resolve that source — which is why it belongs here and behind the
// integration tag.
//
// Its own comment in db_lookup.go says it best: "Every path below that cannot
// enrich used to return success, so a message that missed its lookup reached
// the sink indistinguishable from one that hit." These pin that it stays fixed.
// ---------------------------------------------------------------------------

func newDBLookupFixture(t *testing.T) (*Registry, string) {
	t.Helper()

	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		// A failure, not a skip: the DSN naming this server is the statement
		// that it should be there.
		t.Fatalf("the configured PostgreSQL is not reachable: %v", err)
	}

	table := "lookup_" + strings.ToLower(t.Name())
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec("DROP TABLE IF EXISTS " + table)
	exec(fmt.Sprintf(`CREATE TABLE %s (code TEXT PRIMARY KEY, tier TEXT, region TEXT)`, table))
	exec(fmt.Sprintf("INSERT INTO %s (code, tier, region) VALUES ($1,$2,$3)", table), "C-1", "gold", "emea")
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table)
	})

	// Its own registry rather than newSimRegistry's, because this fixture needs
	// the store as well: db_lookup resolves its source through the registry, so
	// the source must exist in storage rather than being handed in as config.
	meta, err := sql.Open("sqlite", "file:dblookup_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	t.Cleanup(func() { _ = meta.Close() })

	store := sqlstorage.NewSQLStorage(meta, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init metadata store: %v", err)
	}
	if err := store.CreateSource(t.Context(), storage.Source{
		ID: "lookup-src", Name: "lookup source", Type: "postgres",
		Config: map[string]string{"connection_string": dsn},
	}); err != nil {
		t.Fatalf("create lookup source: %v", err)
	}

	return NewRegistry(store), table
}

// TestDBLookupEnrichesFromATable is the happy path.
func TestDBLookupEnrichesFromATable(t *testing.T) {
	reg, table := newDBLookupFixture(t)

	got := transform(t, reg,
		map[string]any{"customer_code": "C-1"},
		map[string]any{
			"transType":   "db_lookup",
			"sourceId":    "lookup-src",
			"mode":        "table",
			"table":       table,
			"keyColumn":   "code",
			"valueColumn": "tier",
			"keyField":    "customer_code",
			"targetField": "tier",
		})

	if got["tier"] != "gold" {
		t.Errorf("tier = %v, want gold; the row is in the table, so the lookup did not "+
			"reach it. Full message: %v", got["tier"], got)
	}
	if got["customer_code"] != "C-1" {
		t.Errorf("the key field was modified: customer_code = %v", got["customer_code"])
	}
}

// TestDBLookupMissFollowsItsConfiguredPolicy covers what happens when the key
// is not in the lookup table.
//
// A miss is not automatically a bug: onMiss makes the outcome an explicit
// choice, and the default is passthrough with the miss recorded as a metric so
// silent degradation shows up as a number. What matters is that each policy
// does what it says, because a pipeline configured to fail on a miss and
// quietly passing one through is the failure the option exists to prevent.
func TestDBLookupMissFollowsItsConfiguredPolicy(t *testing.T) {
	reg, table := newDBLookupFixture(t)

	base := func() map[string]any {
		return map[string]any{
			"transType":   "db_lookup",
			"sourceId":    "lookup-src",
			"mode":        "table",
			"table":       table,
			"keyColumn":   "code",
			"valueColumn": "tier",
			"keyField":    "customer_code",
			"targetField": "tier",
		}
	}

	run := func(t *testing.T, cfg map[string]any) (map[string]any, error) {
		t.Helper()
		msg := message.AcquireMessage()
		t.Cleanup(msg.Release)
		msg.SetAfter([]byte(`{"customer_code":"NO-SUCH-CODE"}`))
		msg.SetPayload([]byte(`{"customer_code":"NO-SUCH-CODE"}`))

		out, err := reg.applyTransformation(t.Context(), msg, "db_lookup", cfg)
		if err != nil || out == nil {
			return nil, err
		}
		t.Cleanup(func() {
			if out != msg {
				out.Release()
			}
		})
		got := map[string]any{}
		if raw := out.After(); len(raw) > 0 {
			_ = json.Unmarshal(raw, &got)
		}
		for k, v := range out.Data() {
			got[k] = v
		}
		return got, nil
	}

	t.Run("default is passthrough", func(t *testing.T) {
		got, err := run(t, base())
		if err != nil {
			t.Fatalf("the default policy failed the message: %v", err)
		}
		if v, ok := got["tier"]; ok && v != nil && v != "" {
			t.Errorf("tier = %v on a miss; passthrough must not invent a value", v)
		}
		if got["customer_code"] != "NO-SUCH-CODE" {
			t.Errorf("the message was altered on a passthrough miss: %v", got)
		}
	})

	t.Run("fail reports the miss", func(t *testing.T) {
		cfg := base()
		cfg["onMiss"] = "fail"
		if _, err := run(t, cfg); err == nil {
			t.Error("onMiss=fail let a miss through; a pipeline that asked to stop on " +
				"an unresolvable key carried on and wrote an unenriched record")
		}
	})

	t.Run("default value is written", func(t *testing.T) {
		cfg := base()
		cfg["defaultValue"] = "unknown"
		got, err := run(t, cfg)
		if err != nil {
			t.Fatalf("configuring a defaultValue should not fail the message: %v", err)
		}
		if got["tier"] != "unknown" {
			t.Errorf("tier = %v, want the configured default \"unknown\"; a defaultValue "+
				"that is not applied leaves the field absent while reporting success", got["tier"])
		}
	})
}
