package lookup

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// The batching path, and the ttl that guards every path.
//
// getOrCreateBatcher builds a closure once per node id and then hands the same
// one back forever, so everything it captured -- the message data it resolves
// templates against, and the source it queries -- is frozen at whichever
// message happened to arrive first.
// ---------------------------------------------------------------------------

// multiSourceRegistry hands out a different database per source id, so a
// lookup that keeps querying a stale source is visible as a wrong row rather
// than having to be inferred.
type multiSourceRegistry struct {
	sources map[string]storage.Source
	dbs     map[string]*sql.DB
	current string
	cache   map[string]any
	sets    int
	lastTTL time.Duration
}

func (f *multiSourceRegistry) GetSourceConfig(ctx context.Context, id string) (storage.Source, error) {
	return f.sources[f.current], nil
}

func (f *multiSourceRegistry) GetOrOpenDB(src storage.Source) (*sql.DB, error) {
	return f.dbs[src.ID], nil
}

func (f *multiSourceRegistry) GetLookupCache(key string) (any, bool) {
	v, ok := f.cache[key]
	return v, ok
}

func (f *multiSourceRegistry) SetLookupCache(key string, value any, ttl time.Duration) {
	f.sets++
	f.lastTTL = ttl
	f.cache[key] = value
}

func newUsersDB(t *testing.T, rows [][3]string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(t.Context(),
		`CREATE TABLE users (id TEXT PRIMARY KEY, tenant TEXT, name TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, r := range rows {
		if _, err := db.ExecContext(t.Context(),
			`INSERT INTO users(id, tenant, name) VALUES (?,?,?)`, r[0], r[1], r[2]); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return db
}

// ---- ttl ------------------------------------------------------------------

// The same discarded parse error api_lookup had. "5" is not a Go duration, the
// error was thrown away, and the zero value left behind means "never expires"
// to SetLookupCache -- so the field whose only purpose is bounding staleness
// silently unbounded it.
func TestDBLookupRejectsAnUnparseableTTL(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	cfg := flattenConfig()
	cfg["ttl"] = "5"

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("user_id", "u1")

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	_, err := tr.Transform(ctx, msg, cfg)
	if err == nil {
		t.Error(`ttl "5" was accepted; the parse failed and left 0, which means "cache forever"`)
	} else if !strings.Contains(err.Error(), "ttl") {
		t.Errorf("the error does not name the field at fault: %v", err)
	}
}

// An explicit zero has to mean "do not cache", which was not sayable: zero and
// unset both landed on SetLookupCache's "never expires".
func TestDBLookupTTLZeroDisablesTheCache(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	cfg := map[string]any{
		"sourceId":    "src1",
		"mode":        "table",
		"table":       "users",
		"keyColumn":   "id",
		"keyField":    "user_id",
		"valueColumn": "email",
		"targetField": "found",
		"ttl":         "0",
	}

	run := func() any {
		msg := message.AcquireMessage()
		t.Cleanup(msg.Release)
		msg.SetData("user_id", "u1")
		ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
		out, err := tr.Transform(ctx, msg, cfg)
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		return out.Data()["found"]
	}

	if got := run(); got != "ada@example.com" {
		t.Fatalf("first lookup: found = %#v", got)
	}

	// Change the row underneath. With caching off the next lookup must see it;
	// this is what distinguishes "not cached" from "cached and we got lucky".
	if _, err := reg.db.ExecContext(t.Context(),
		`UPDATE users SET email = 'ada@new.example.com' WHERE id = 'u1'`); err != nil {
		t.Fatalf("update: %v", err)
	}

	if got := run(); got != "ada@new.example.com" {
		t.Errorf("second lookup: found = %#v, want the updated row -- ttl 0 must mean "+
			"\"do not cache\", not \"cache forever\" (cache=%v)", got, reg.cache)
	}
}

// ---- the batcher's frozen closure ----------------------------------------

func batchingConfig(extra map[string]any) map[string]any {
	cfg := map[string]any{
		"sourceId":     "src1",
		"mode":         "table",
		"table":        "users",
		"keyColumn":    "id",
		"keyField":     "user_id",
		"valueColumn":  "name",
		"targetField":  "found",
		"use_batching": true,
		"batchSize":    1,
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

// batchedNodeID is shared by every message in these tests on purpose: batchers
// are keyed by node id, so two messages reaching the same node is exactly the
// condition under which the second one inherits what the first one captured.
const batchedNodeID = "node-1"

func runBatched(t *testing.T, tr *DBLookupTransformer, reg any, cfg map[string]any, fields map[string]any) (any, error) {
	t.Helper()

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	ctx = context.WithValue(ctx, hermod.NodeIDKey, batchedNodeID)
	out, err := tr.Transform(ctx, msg, cfg)
	if err != nil || out == nil {
		return nil, err
	}
	return out.Data()["found"], err
}

// A templated whereClause is per-message input, and the batcher resolves it
// once -- against whichever message created the batcher. Every later message in
// the workflow is then filtered by the first message's tenant.
//
// Batching one query for many messages and filtering per message are
// fundamentally incompatible, so the right answer is not to batch this at all.
func TestDBLookupBatchingDoesNotReuseTheFirstMessagesWhereClause(t *testing.T) {
	db := newUsersDB(t, [][3]string{
		{"u1", "t1", "Ada"},
		{"u2", "t2", "Grace"},
	})
	reg := &multiSourceRegistry{
		sources: map[string]storage.Source{
			"src1": {ID: "src1", Type: "sqlite", Config: hermod.StringMap{"use_cdc": "false"}},
		},
		dbs:     map[string]*sql.DB{"src1": db},
		current: "src1",
		cache:   map[string]any{},
	}
	tr := &DBLookupTransformer{}

	cfg := batchingConfig(map[string]any{"whereClause": "tenant = {{ .tenant }}"})

	first, err := runBatched(t, tr, reg, cfg, map[string]any{"user_id": "u1", "tenant": "t1"})
	if err != nil {
		t.Fatalf("first message: %v", err)
	}
	if first != "Ada" {
		t.Fatalf("first message: found = %#v, want %q", first, "Ada")
	}

	second, err := runBatched(t, tr, reg, cfg, map[string]any{"user_id": "u2", "tenant": "t2"})
	if err != nil {
		t.Fatalf("second message: %v", err)
	}
	if second != "Grace" {
		t.Errorf("second message: found = %#v, want %q -- the batcher captured the first message's "+
			"data, so every later batch is filtered by the first message's tenant", second, "Grace")
	}
}

// The source is captured in the same closure. Editing a source invalidates the
// lookup cache (Registry.invalidateLookupCacheForSource), but the batcher went
// on querying the database the first message opened -- so the invalidation was
// defeated by the path that needed it most.
func TestDBLookupBatcherFollowsASourceChange(t *testing.T) {
	oldDB := newUsersDB(t, [][3]string{{"u1", "t1", "Ada (old database)"}})
	newDB := newUsersDB(t, [][3]string{{"u1", "t1", "Ada (new database)"}})

	reg := &multiSourceRegistry{
		sources: map[string]storage.Source{
			"src1": {ID: "src1", Type: "sqlite", Config: hermod.StringMap{"use_cdc": "false"}},
			"src2": {ID: "src2", Type: "sqlite", Config: hermod.StringMap{"use_cdc": "false"}},
		},
		dbs:     map[string]*sql.DB{"src1": oldDB, "src2": newDB},
		current: "src1",
		cache:   map[string]any{},
	}
	tr := &DBLookupTransformer{}

	cfg := batchingConfig(nil)

	first, err := runBatched(t, tr, reg, cfg, map[string]any{"user_id": "u1"})
	if err != nil {
		t.Fatalf("first message: %v", err)
	}
	if first != "Ada (old database)" {
		t.Fatalf("first message: found = %#v", first)
	}

	// The operator repoints the node at a different database. Clear the cache
	// the way the registry does on a source edit, so this is about the batcher
	// and not about the cache.
	reg.current = "src2"
	reg.cache = map[string]any{}

	second, err := runBatched(t, tr, reg, cfg, map[string]any{"user_id": "u1"})
	if err != nil {
		t.Fatalf("second message: %v", err)
	}
	if second != "Ada (new database)" {
		t.Errorf("second message: found = %#v, want the new database's row -- the batcher captured "+
			"the source from the first message and never looked again", second)
	}
}

// batchSize arrives as a JSON number from a bundle or the API. It was read with
// a .(int) assertion and a string fallback, neither of which matches float64,
// so a configured size silently fell back to the 100 default.
func TestDBLookupBatchSizeAcceptsANumber(t *testing.T) {
	if got := configInt(map[string]any{"batchSize": float64(25)}, "batchSize"); got != 25 {
		t.Errorf("configInt(float64(25)) = %d, want 25 -- JSON numbers decode to float64", got)
	}
	if got := configInt(map[string]any{"batchSize": 25}, "batchSize"); got != 25 {
		t.Errorf("configInt(int) = %d, want 25", got)
	}
	if got := configInt(map[string]any{"batchSize": "25"}, "batchSize"); got != 25 {
		t.Errorf("configInt(string) = %d, want 25", got)
	}
}

// An unset Cache TTL used to mean "keep this row for the lifetime of the
// process". Correct per key since the cache-key fix, but never refreshed: a row
// edited in the lookup table was invisible to a running workflow forever.
func TestDBLookupDoesNotCacheForeverByDefault(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	cfg := flattenConfig()
	delete(cfg, "ttl")

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetData("user_id", "u1")

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	if _, err := tr.Transform(ctx, msg, cfg); err != nil {
		t.Fatalf("Transform: %v", err)
	}

	if reg.sets != 1 {
		t.Fatalf("the lookup cached %d times, want 1", reg.sets)
	}
	if reg.lastTTL <= 0 {
		t.Errorf("an unset ttl stored the row with ttl=%v, which SetLookupCache treats as never "+
			"expiring, so an edit to the lookup table never reaches a running workflow", reg.lastTTL)
	}
}
