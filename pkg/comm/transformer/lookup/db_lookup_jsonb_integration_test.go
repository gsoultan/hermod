//go:build integration
// +build integration

package lookup

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// A db_lookup whose row carries a `jsonb` column, against a real PostgreSQL.
//
// The reported symptom was that the looked-up document does not show up: the
// editor's preview panel renders one opaque line, "Flatten Result" produces
// nothing, and a downstream field path into the document resolves to nil --
// with no error anywhere, because nothing failed.
//
// The cause is the driver, not the node. db_lookup reads through database/sql,
// and pgx's database/sql driver hands a jsonb column over as raw []byte
// (stdlib/sql.go routes JSONOID/JSONBOID through a []byte scan plan). The
// generic scan then rendered every []byte as a string. The very same column
// fetched through pgx natively -- PostgresSource.ExecuteSQL, and the source's
// snapshot and polling paths -- arrives as a map, so one document had two
// shapes decided by which path fetched it, and only the string half was
// unreachable.
//
// sqlite cannot catch this: it has no jsonb, and it hands text over as a string
// rather than as []byte, so the carrier under test never appears.
//
// Run with:
//
//	POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/hermod_test_source?sslmode=disable' \
//	  go test -tags=integration -run TestDBLookupJSONB ./pkg/comm/transformer/lookup/
func TestDBLookupJSONBColumnArrivesAsAnObject(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS lookup_it`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS lookup_it.profiles`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE lookup_it.profiles (
		id int PRIMARY KEY,
		email text,
		meta jsonb,
		tags json,
		note text
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS lookup_it.profiles`)
	})

	if _, err := db.ExecContext(ctx,
		`INSERT INTO lookup_it.profiles(id, email, meta, tags, note) VALUES ($1,$2,$3,$4,$5)`,
		7, "ada@example.com",
		`{"vip": true, "addr": {"city": "London"}}`,
		`[1, 2, 3]`,
		`{"still":"text"}`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	newRegistry := func() *cachingFakeRegistry {
		return &cachingFakeRegistry{
			db:     db,
			source: storage.Source{ID: "src1", Type: "postgres", Config: hermod.StringMap{"use_cdc": "false"}},
			cache:  map[string]any{},
		}
	}
	tr := &DBLookupTransformer{}

	run := func(t *testing.T, cfg map[string]any) hermod.Message {
		t.Helper()
		msg := message.AcquireMessage()
		t.Cleanup(msg.Release)
		msg.SetData("UserId", 7)

		lctx := context.WithValue(ctx, hermod.RegistryKey, newRegistry())
		out, err := tr.Transform(lctx, msg, cfg)
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		return out
	}

	// Query mode is what the editor writes when an operator types SQL.
	t.Run("query mode", func(t *testing.T) {
		out := run(t, map[string]any{
			"mode":          "query",
			"sourceId":      "src1",
			"targetField":   "profile",
			"queryTemplate": "SELECT * FROM lookup_it.profiles WHERE id = {{.UserId}}",
		})
		assertProfileRow(t, out.Data()["profile"])
	})

	// Key-column mode is the other half of the node, and reads through a
	// different function -- lookupSQL rather than lookupSQLWithTemplate.
	t.Run("key column mode", func(t *testing.T) {
		out := run(t, map[string]any{
			"sourceId":    "src1",
			"table":       "lookup_it.profiles",
			"keyColumn":   "id",
			"keyField":    "UserId",
			"valueColumn": "*",
			"targetField": "profile",
		})
		assertProfileRow(t, out.Data()["profile"])
	})

	// Asking for the jsonb column alone is the shape an operator uses when the
	// document *is* the thing they want, and it is the one where a string is
	// most obviously wrong: the target field holds the whole answer.
	t.Run("single jsonb column", func(t *testing.T) {
		out := run(t, map[string]any{
			"sourceId":    "src1",
			"table":       "lookup_it.profiles",
			"keyColumn":   "id",
			"keyField":    "UserId",
			"valueColumn": "meta",
			"targetField": "profile_meta",
		})
		meta, ok := out.Data()["profile_meta"].(map[string]any)
		if !ok {
			t.Fatalf("profile_meta = %#v (%T), want a map", out.Data()["profile_meta"], out.Data()["profile_meta"])
		}
		if city := digCity(meta); city != "London" {
			t.Errorf("profile_meta.addr.city = %#v, want %q", city, "London")
		}
	})

	// flattenInto only reaches a map. With the document arriving as a string,
	// "Flatten Result" silently did nothing for exactly the rows an operator
	// most wants flattened.
	t.Run("flatten reaches into the document", func(t *testing.T) {
		out := run(t, map[string]any{
			"sourceId":    "src1",
			"table":       "lookup_it.profiles",
			"keyColumn":   "id",
			"keyField":    "UserId",
			"valueColumn": "meta",
			"targetField": "profile_meta",
			"flattenInto": ".",
		})
		if got := out.Data()["vip"]; got != true {
			t.Errorf("vip = %#v, want true -- flattenInto cannot reach into a string", got)
		}
		if _, ok := out.Data()["addr"].(map[string]any); !ok {
			t.Errorf("addr = %#v, want the nested object flattened out of the document", out.Data()["addr"])
		}
	})

	// The editor's preview panel, the message trace and every JSON sink all
	// serialise the message rather than reading Data() directly, so the shape
	// has to survive that too.
	t.Run("survives serialisation", func(t *testing.T) {
		out := run(t, map[string]any{
			"mode":          "query",
			"sourceId":      "src1",
			"targetField":   "profile",
			"queryTemplate": "SELECT * FROM lookup_it.profiles WHERE id = {{.UserId}}",
		})
		raw, err := json.Marshal(out.ToMap())
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var round map[string]any
		if err := json.Unmarshal(raw, &round); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		assertProfileRow(t, round["profile"])
	})
}

func assertProfileRow(t *testing.T, v any) {
	t.Helper()
	row, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("profile = %#v (%T), want a row", v, v)
	}

	meta, ok := row["meta"].(map[string]any)
	if !ok {
		t.Fatalf("profile.meta = %#v (%T), want a map -- a jsonb column that arrives as a "+
			"string offers no fields to a field picker, a sink mapping or a trace", row["meta"], row["meta"])
	}
	if city := digCity(meta); city != "London" {
		t.Errorf("profile.meta.addr.city = %#v, want %q", city, "London")
	}
	if meta["vip"] != true {
		t.Errorf("profile.meta.vip = %#v (%T), want the boolean true", meta["vip"], meta["vip"])
	}

	tags, ok := row["tags"].([]any)
	if !ok {
		t.Fatalf("profile.tags = %#v (%T), want a slice -- `json` decodes like `jsonb`", row["tags"], row["tags"])
	}
	if len(tags) != 3 {
		t.Errorf("profile.tags = %#v, want 3 elements", tags)
	}

	// The narrowness is the point: only columns PostgreSQL itself calls json or
	// jsonb change. A text column holding a JSON document is still text.
	if got := row["note"]; got != `{"still":"text"}` {
		t.Errorf("profile.note = %#v, want the string unchanged", got)
	}
	if got := row["email"]; got != "ada@example.com" {
		t.Errorf("profile.email = %#v, want the plain string", got)
	}
}

func digCity(meta map[string]any) any {
	addr, ok := meta["addr"].(map[string]any)
	if !ok {
		return nil
	}
	return addr["city"]
}

// A PostgreSQL array through the same node. This is the shape a user reported
// as "real data shows null / no data found": once an array is the string
// "{C-001,gift}", every path into it resolves to nothing, the whereClause binds
// nil, the query matches no row, and the miss policy passes the message
// through unchanged. Nothing errors, because nothing failed.
//
//	POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/hermod_test_source?sslmode=disable' \
//	  go test -tags=integration -run TestDBLookupArray ./pkg/comm/transformer/lookup/
func TestDBLookupArrayColumnArrivesAsAList(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS lookup_it`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS lookup_it.customers`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE lookup_it.customers (
		id int PRIMARY KEY, cust_code text, name text,
		tags text[], scores int[], nasty text[], holes int[]
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS lookup_it.customers`)
	})

	if _, err := db.ExecContext(ctx,
		`INSERT INTO lookup_it.customers VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		1, "C-001", "Ada Lovelace",
		`{C-001,gift}`, `{10,20}`,
		`{"a,b","he said \"hi\"","{brace}","NULL"}`, `{1,NULL,3}`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	newRegistry := func() *cachingFakeRegistry {
		return &cachingFakeRegistry{
			db:     db,
			source: storage.Source{ID: "src1", Type: "postgres", Config: hermod.StringMap{"use_cdc": "false"}},
			cache:  map[string]any{},
		}
	}
	tr := &DBLookupTransformer{}

	out, err := tr.Transform(
		context.WithValue(ctx, hermod.RegistryKey, newRegistry()),
		func() hermod.Message {
			m := message.AcquireMessage()
			t.Cleanup(m.Release)
			m.SetData("Id", 1)
			return m
		}(),
		map[string]any{
			"sourceId": "src1", "table": "lookup_it.customers",
			"keyColumn": "id", "keyField": "Id",
			"valueColumn": "*", "targetField": "customer",
		})
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}

	row, ok := out.Data()["customer"].(map[string]any)
	if !ok {
		t.Fatalf("customer = %#v, want a row", out.Data()["customer"])
	}

	tags, ok := row["tags"].([]any)
	if !ok {
		t.Fatalf("customer.tags = %#v (%T), want a list -- as a string, every path "+
			"into it resolves to nothing and the lookup that uses it silently misses", row["tags"], row["tags"])
	}
	if len(tags) != 2 || tags[0] != "C-001" {
		t.Errorf("customer.tags = %#v, want [C-001 gift]", tags)
	}
	if scores, ok := row["scores"].([]any); !ok || len(scores) != 2 {
		t.Errorf("customer.scores = %#v, want a 2-element list", row["scores"])
	}

	// The two literals that decide whether the parser is right rather than
	// merely plausible: an embedded comma, an escaped quote and a brace inside
	// quoted elements, a *quoted* NULL that is the four-character string, and
	// an unquoted NULL that is a real null.
	nasty, ok := row["nasty"].([]any)
	if !ok || len(nasty) != 4 {
		t.Fatalf("customer.nasty = %#v, want 4 elements", row["nasty"])
	}
	for i, want := range []any{"a,b", `he said "hi"`, "{brace}", "NULL"} {
		if nasty[i] != want {
			t.Errorf("customer.nasty[%d] = %#v, want %#v", i, nasty[i], want)
		}
	}
	holes, ok := row["holes"].([]any)
	if !ok || len(holes) != 3 {
		t.Fatalf("customer.holes = %#v, want 3 elements", row["holes"])
	}
	if holes[1] != nil {
		t.Errorf("customer.holes[1] = %#v, want a real null", holes[1])
	}

	// And the failure the user actually reported: a lookup keyed on an element
	// of the array. With tags as a string this bound nil and produced nothing.
	out2, err := tr.Transform(
		context.WithValue(ctx, hermod.RegistryKey, newRegistry()),
		func() hermod.Message {
			m := message.AcquireMessage()
			t.Cleanup(m.Release)
			m.SetData("tags", []any{"C-001", "gift"})
			return m
		}(),
		map[string]any{
			"sourceId": "src1", "table": "lookup_it.customers",
			"whereClause": "cust_code = {{.tags.0}}",
			"valueColumn": "name", "targetField": "found",
		})
	if err != nil {
		t.Fatalf("Transform(whereClause): %v", err)
	}
	if got := out2.Data()["found"]; got != "Ada Lovelace" {
		t.Errorf("found = %#v, want %q -- this is the miss that renders as "+
			"\"nothing at this path\" in the preview panel", got, "Ada Lovelace")
	}
}
