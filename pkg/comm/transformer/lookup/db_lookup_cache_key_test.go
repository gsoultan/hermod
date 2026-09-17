package lookup

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

// runLookupWith runs one message through the transformer and returns its data.
// Unlike runLookup it lets the caller choose the field values, which is the
// whole point here: the bug only shows up when two messages differ.
func runLookupWith(t *testing.T, tr *DBLookupTransformer, reg *cachingFakeRegistry, cfg map[string]any, fields map[string]any) map[string]any {
	t.Helper()

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)
	out, err := tr.Transform(ctx, msg, cfg)
	if err != nil {
		t.Fatalf("Transform(%v): %v", fields, err)
	}
	if out == nil {
		t.Fatalf("Transform(%v) returned no message", fields)
	}
	return out.Data()
}

// seedTwoUsers adds a second row so the two messages below have distinct
// answers to find. Without it a wrong cache hit and a correct lookup are
// indistinguishable.
func seedTwoUsers(t *testing.T, reg *cachingFakeRegistry) {
	t.Helper()
	if _, err := reg.db.ExecContext(t.Context(),
		`INSERT INTO users(id, email, name) VALUES ('u2','grace@example.com','Grace')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

// TestDBLookupCacheKeyVariesWithQueryTemplateValues is the reported bug:
// "every RabbitMQ message gets the same db_lookup result".
//
// In query mode the whole statement is a template, so the per-message input is
// in the {{ ... }} tokens -- not in keyField, which such a config usually leaves
// empty. The cache key was built from the *unresolved* template text plus
// keyVal, so both components are byte-identical for every message in the
// workflow. Message one populates the cache; with no TTL configured that entry
// never expires, and every message after it is served message one's row.
func TestDBLookupCacheKeyVariesWithQueryTemplateValues(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	seedTwoUsers(t, reg)

	cfg := map[string]any{
		"sourceId":      "src1",
		"mode":          "query",
		"queryTemplate": "SELECT email FROM users WHERE id = {{ .user_id }}",
		"valueColumn":   "email",
		"targetField":   "found",
	}

	if got := runLookupWith(t, tr, reg, cfg, map[string]any{"user_id": "u1"})["found"]; got != "ada@example.com" {
		t.Fatalf("first message: found = %#v, want %q", got, "ada@example.com")
	}

	second := runLookupWith(t, tr, reg, cfg, map[string]any{"user_id": "u2"})["found"]
	if second != "grace@example.com" {
		t.Errorf("second message: found = %#v, want %q -- the cache key ignores the resolved template arguments, so every message is served the first message's row (cache=%v)",
			second, "grace@example.com", reg.cache)
	}
}

// TestDBLookupCacheKeyVariesWithWhereClauseValues is the same defect on the
// table-mode path. whereClause is templated per message too, and the key used
// the raw text, so "email = {{ .user_email }}" collapsed every message onto one
// cache entry.
func TestDBLookupCacheKeyVariesWithWhereClauseValues(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	seedTwoUsers(t, reg)

	cfg := map[string]any{
		"sourceId":    "src1",
		"mode":        "table",
		"table":       "users",
		"whereClause": "id = {{ .user_id }}",
		"valueColumn": "name",
		"targetField": "found",
	}

	if got := runLookupWith(t, tr, reg, cfg, map[string]any{"user_id": "u1"})["found"]; got != "Ada" {
		t.Fatalf("first message: found = %#v, want %q", got, "Ada")
	}

	second := runLookupWith(t, tr, reg, cfg, map[string]any{"user_id": "u2"})["found"]
	if second != "Grace" {
		t.Errorf("second message: found = %#v, want %q -- the cache key uses the unresolved whereClause, so every message hits message one's entry (cache=%v)",
			second, "Grace", reg.cache)
	}
}

// TestDBLookupCacheKeyVariesWithMultiTokenTemplate covers a template whose
// tokens are not all in the key: two messages agreeing on one token and
// differing on another must still be two cache entries.
func TestDBLookupCacheKeyVariesWithMultiTokenTemplate(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	seedTwoUsers(t, reg)

	cfg := map[string]any{
		"sourceId":      "src1",
		"mode":          "query",
		"queryTemplate": "SELECT name FROM users WHERE id = {{ .user_id }} AND email = {{ .user_email }}",
		"valueColumn":   "name",
		"targetField":   "found",
	}

	first := runLookupWith(t, tr, reg, cfg, map[string]any{
		"user_id": "u1", "user_email": "ada@example.com",
	})["found"]
	if first != "Ada" {
		t.Fatalf("first message: found = %#v, want %q", first, "Ada")
	}

	// Same id, different email: no row matches, so a correct implementation
	// reports a miss rather than handing back Ada.
	second := runLookupWith(t, tr, reg, cfg, map[string]any{
		"user_id": "u1", "user_email": "grace@example.com",
	})["found"]
	if second == "Ada" {
		t.Errorf("second message: found = %q, but no row has that id/email pair -- the cache key ignores template arguments (cache=%v)",
			second, reg.cache)
	}
}
