package lookup

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

// cachingFakeRegistry is fullFakeRegistry with a cache that actually remembers,
// which is the whole point: the registry in production caches every lookup, and
// with no TTL configured it caches it forever.
type cachingFakeRegistry struct {
	db     *sql.DB
	source storage.Source
	cache  map[string]any
	sets   int
}

func (f *cachingFakeRegistry) GetSourceConfig(ctx context.Context, id string) (storage.Source, error) {
	return f.source, nil
}

func (f *cachingFakeRegistry) GetOrOpenDB(src storage.Source) (*sql.DB, error) { return f.db, nil }

func (f *cachingFakeRegistry) GetLookupCache(key string) (any, bool) {
	v, ok := f.cache[key]
	return v, ok
}

func (f *cachingFakeRegistry) SetLookupCache(key string, value any, ttl time.Duration) {
	f.sets++
	f.cache[key] = value
}

func newFlattenFixture(t *testing.T) (*DBLookupTransformer, *cachingFakeRegistry) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT, name TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users(id, email, name) VALUES ('u1','ada@example.com','Ada')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	return &DBLookupTransformer{}, &cachingFakeRegistry{
		db:     db,
		source: storage.Source{ID: "src1", Type: "sqlite"},
		cache:  map[string]any{},
	}
}

func flattenConfig() map[string]any {
	return map[string]any{
		"sourceId":    "src1",
		"mode":        "table",
		"table":       "users",
		"keyColumn":   "id",
		"keyField":    "user_id",
		"valueColumn": "email, name",
		"targetField": "user_details",
		"flattenInto": ".",
	}
}

func runLookup(t *testing.T, tr *DBLookupTransformer, reg *cachingFakeRegistry, cfg map[string]any) map[string]any {
	t.Helper()

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("user_id", "u1")

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	out, err := tr.Transform(ctx, msg, cfg)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if out == nil {
		t.Fatal("Transform returned no message")
	}
	return out.Data()
}

// TestDBLookupFlattensOnACacheHit is the bug the editor's live preview surfaces.
//
// The preview re-runs on a debounce, so the first run populates the lookup
// cache and every run after it is a cache hit. The cache-hit path used to write
// targetField and return, skipping flattenInto entirely -- so the flattened
// columns appeared once and then vanished, and the operator watching the panel
// saw "Flatten Result" do nothing.
//
// The same thing happens in a live workflow: message one gets the flattened
// fields, every later message with the same key does not, and the sink receives
// two different shapes from one configuration.
func TestDBLookupFlattensOnACacheHit(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	first := runLookup(t, tr, reg, flattenConfig())
	if first["email"] != "ada@example.com" || first["name"] != "Ada" {
		t.Fatalf("first lookup did not flatten: %#v", first)
	}

	second := runLookup(t, tr, reg, flattenConfig())
	if reg.sets != 1 {
		t.Fatalf("expected the second lookup to be served from cache, but the cache was written %d times", reg.sets)
	}
	if second["email"] != "ada@example.com" {
		t.Errorf("email = %#v after a cache hit; want %q -- flattenInto was skipped on the cached path (%#v)",
			second["email"], "ada@example.com", second)
	}
	if second["name"] != "Ada" {
		t.Errorf("name = %#v after a cache hit; want %q -- flattenInto was skipped on the cached path (%#v)",
			second["name"], "Ada", second)
	}
}

// TestDBLookupCacheKeyDistinguishesKeyTypes: the cache key interpolated the key
// value with %v, so the string "1" and the number 1 produced the same key. A
// pipeline whose messages carry a numeric id and a sibling lookup that carries
// the same id as text served each other's rows.
func TestDBLookupCacheKeyDistinguishesKeyTypes(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	// An INTEGER key column, so SQLite's numeric affinity matches both the
	// string "1" and the number 1 and each lookup really does find a row --
	// otherwise a miss would cache nothing and look just like a collision.
	if _, err := reg.db.ExecContext(t.Context(), `CREATE TABLE accounts (id INTEGER PRIMARY KEY, label TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := reg.db.ExecContext(t.Context(), `INSERT INTO accounts(id, label) VALUES (1,'one')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	cfg := map[string]any{
		"sourceId":    "src1",
		"mode":        "table",
		"table":       "accounts",
		"keyColumn":   "id",
		"keyField":    "account_id",
		"valueColumn": "label",
		"targetField": "found",
	}

	run := func(key any) any {
		msg := message.AcquireMessage()
		t.Cleanup(msg.Release)
		msg.SetData("account_id", key)
		ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
		out, err := tr.Transform(ctx, msg, cfg)
		if err != nil {
			t.Fatalf("Transform(%#v): %v", key, err)
		}
		return out.Data()["found"]
	}

	if got := run("1"); got != "one" {
		t.Fatalf("string key: found = %#v, want %q", got, "one")
	}
	if got := run(1); got != "one" {
		t.Fatalf("numeric key: found = %#v, want %q", got, "one")
	}
	if len(reg.cache) != 2 {
		t.Errorf("cache holds %d entries (%v); want 2 -- the string %q and the number 1 hash to the same cache key, so one lookup serves the other's row",
			len(reg.cache), reg.cache, "1")
	}
}
