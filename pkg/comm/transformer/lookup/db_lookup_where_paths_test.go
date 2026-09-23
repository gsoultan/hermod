package lookup

import (
	"testing"

	_ "modernc.org/sqlite"
)

// queryTemplate learned to resolve through the message; whereClause did not,
// and the two sit in the same node. A `whereClause` of `id = {{.after.user_id}}`
// therefore bound NULL and matched no row, while the identical path in query
// mode found one.
//
// It was left alone on purpose at first: bindingDigest renders this same clause
// to build the cache key, so widening the clause without the key would let two
// messages differing only in `after.user_id` share one cache entry -- the first
// row served to both, for the life of the engine. That is why the tests below
// come in pairs: one for what the query finds, one for what the key
// distinguishes.

func whereConfig(clause string) map[string]any {
	return map[string]any{
		"sourceId":    "src1",
		"mode":        "table",
		"table":       "users",
		"whereClause": clause,
		"valueColumn": "email",
		"targetField": "found",
	}
}

func TestWhereClauseResolvesTheAfterPrefix(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	got := runCDCLookup(t, tr, reg, whereConfig("id = {{.after.user_id}}"),
		map[string]any{"user_id": "u1"})

	if got["found"] != "ada@example.com" {
		t.Errorf("found = %#v, want ada@example.com -- whereClause must resolve the paths queryTemplate does", got["found"])
	}
}

func TestWhereClauseResolvesAVirtualField(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	if _, err := reg.db.ExecContext(t.Context(),
		`INSERT INTO users(id, email, name) VALUES ('insert','op@example.com','Op')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got := runCDCLookup(t, tr, reg, whereConfig("id = {{.operation}}"), map[string]any{"user_id": "u1"})

	if got["found"] != "op@example.com" {
		t.Errorf("found = %#v, want op@example.com", got["found"])
	}
}

// A column holding JSON text has to read the same way here as everywhere else.
func TestWhereClauseDescendsIntoAJSONTextColumn(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	got := runCDCLookup(t, tr, reg, whereConfig("id = {{.payload.user_id}}"),
		map[string]any{"payload": `{"user_id":"u1"}`})

	if got["found"] != "ada@example.com" {
		t.Errorf("found = %#v, want ada@example.com", got["found"])
	}
}

// The other half. If only the clause learns the new paths, the key is built
// from a rendering that still resolves them to nothing -- identical for every
// message -- and the second message is served the first one's row.
func TestCacheKeyVariesWithATemplatedWhereClauseOnAnEnvelopePath(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	seedTwoUsers(t, reg)

	cfg := whereConfig("id = {{.after.user_id}}")

	first := runCDCLookup(t, tr, reg, cfg, map[string]any{"user_id": "u1"})
	second := runCDCLookup(t, tr, reg, cfg, map[string]any{"user_id": "u2"})

	if first["found"] != "ada@example.com" {
		t.Errorf("first = %#v, want ada@example.com", first["found"])
	}
	if second["found"] != "grace@example.com" {
		t.Errorf("second = %#v, want grace@example.com -- the cache key must bind what the clause binds", second["found"])
	}
}

// The spelling that always worked must keep working.
func TestWhereClauseStillResolvesABarePath(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	got := runCDCLookup(t, tr, reg, whereConfig("id = {{.user_id}}"), map[string]any{"user_id": "u1"})

	if got["found"] != "ada@example.com" {
		t.Errorf("found = %#v, want ada@example.com", got["found"])
	}
}
