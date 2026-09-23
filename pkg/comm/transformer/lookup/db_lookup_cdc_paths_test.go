package lookup

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	_ "modernc.org/sqlite"
)

// A SQL template used to resolve its {{ }} tokens with a literal walk of the
// data map, while every other template in Hermod -- conditions, sink mappings,
// this node's own keyField -- resolves through evaluator.GetMsgValByPath, which
// also answers after./before./operation/table/meta.. The data map of a CDC
// message *is* the after-image, so there is no "after" key in it and
// {{.after.x}} bound NULL.
//
// It was silent rather than wrong: an unresolved token is deliberately bound as
// NULL, a query whose join then matches nothing returns zero rows, and
// db_lookup's default onMiss is passthrough. The reported symptom was "the SQL
// builder shows results but the preview does not show the target field" -- the
// builder resolves against the editor's sample, which still has the envelope.

// runCDCLookup runs one message shaped the way the engine shapes a CDC message:
// row columns flat in the data map, envelope on the message itself. reg is any
// value Transform accepts as the registry, so a fixture may add a logger.
func runCDCLookup(t *testing.T, tr *DBLookupTransformer, reg any, cfg map[string]any, fields map[string]any) map[string]any {
	t.Helper()

	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	msg.SetOperation(hermod.Operation("insert"))
	msg.SetTable("registrants")
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

func queryModeConfig(tpl string) map[string]any {
	return map[string]any{
		"sourceId":      "src1",
		"mode":          "query",
		"queryTemplate": tpl,
		"targetField":   "user_details",
	}
}

func wantRow(t *testing.T, data map[string]any, why string) map[string]any {
	t.Helper()
	row, ok := data["user_details"].(map[string]any)
	if !ok {
		t.Fatalf("user_details = %#v, want the row -- %s", data["user_details"], why)
	}
	return row
}

func TestQueryModeResolvesTheAfterPrefix(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	got := runCDCLookup(t, tr, reg,
		queryModeConfig(`SELECT email, name FROM users WHERE id = {{.after.user_id}}`),
		map[string]any{"user_id": "u1"})

	row := wantRow(t, got, "{{.after.x}} must resolve the way every other template in Hermod does")
	if row["email"] != "ada@example.com" {
		t.Errorf("email = %v, want ada@example.com", row["email"])
	}
}

// The bare spelling is what the engine's data map actually holds, and it has
// always worked. It must keep working.
func TestQueryModeStillResolvesABarePath(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	got := runCDCLookup(t, tr, reg,
		queryModeConfig(`SELECT email, name FROM users WHERE id = {{.user_id}}`),
		map[string]any{"user_id": "u1"})

	if row := wantRow(t, got, "a bare path is the spelling that always worked"); row["name"] != "Ada" {
		t.Errorf("name = %v, want Ada", row["name"])
	}
}

// A bare path keeps its Go type on the way to the driver. That is what
// GetMsgRawValByPath exists for: a bigint above 2^53 resolved through JSON
// comes back as float64, the query then matches no row and reports no error.
func TestABarePathKeepsItsGoType(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	const bigID int64 = 9007199254740993 // 2^53 + 1

	if _, err := reg.db.ExecContext(t.Context(),
		`INSERT INTO users(id, email, name) VALUES ('9007199254740993','big@example.com','Big')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got := runCDCLookup(t, tr, reg,
		queryModeConfig(`SELECT email FROM users WHERE id = {{.user_id}}`),
		map[string]any{"user_id": bigID})

	if row := wantRow(t, got, "an int64 key must reach the driver as an int64"); row["email"] != "big@example.com" {
		t.Errorf("email = %v, want big@example.com", row["email"])
	}
}

// The virtual fields come with the evaluator, so a template can read the
// operation or the table without the workflow copying them into data.
func TestQueryModeResolvesTheVirtualFields(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	got := runCDCLookup(t, tr, reg,
		queryModeConfig(`SELECT {{.operation}} AS op, {{.table}} AS tbl, email FROM users WHERE id = {{.user_id}}`),
		map[string]any{"user_id": "u1"})

	row := wantRow(t, got, "operation and table are virtual fields the evaluator answers")
	if row["op"] != "insert" {
		t.Errorf("op = %v, want insert", row["op"])
	}
	if row["tbl"] != "registrants" {
		t.Errorf("tbl = %v, want registrants", row["tbl"])
	}
}

// A real column named "after" still outranks the envelope, the same way the
// data map outranks every other virtual field.
func TestARealColumnNamedAfterWins(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	seedTwoUsers(t, reg)

	got := runCDCLookup(t, tr, reg,
		queryModeConfig(`SELECT email, name FROM users WHERE id = {{.after.user_id}}`),
		map[string]any{
			"user_id": "u2",
			"after":   map[string]any{"user_id": "u1"},
		})

	if row := wantRow(t, got, "a literal after column must not be shadowed"); row["email"] != "ada@example.com" {
		t.Errorf("email = %v, want ada@example.com (the literal after.user_id)", row["email"])
	}
}

// The statement is built by one walk of the template and the cache key by
// another. If only the first learns the new resolution rule the digest is
// identical for every message again, and with no TTL the first row is served
// for the life of the engine -- the defect lookup_cache_fast_path.md records.
func TestCacheKeyVariesWithAnEnvelopePathToo(t *testing.T) {
	tr, reg := newFlattenFixture(t)
	seedTwoUsers(t, reg)

	cfg := queryModeConfig(`SELECT email, name FROM users WHERE id = {{.after.user_id}}`)

	first := runCDCLookup(t, tr, reg, cfg, map[string]any{"user_id": "u1"})
	second := runCDCLookup(t, tr, reg, cfg, map[string]any{"user_id": "u2"})

	if row := wantRow(t, first, "first message"); row["email"] != "ada@example.com" {
		t.Errorf("first email = %v, want ada@example.com", row["email"])
	}
	if row := wantRow(t, second, "second message"); row["email"] != "grace@example.com" {
		t.Errorf("second email = %v, want grace@example.com -- the cache key must bind the same values the statement does", row["email"])
	}
}

// loggingFakeRegistry is cachingFakeRegistry that also offers a logger, the way
// the real Registry does. The other fakes deliberately do not: the warning is
// reached through an optional interface, so a registry without one must still
// run a lookup.
type loggingFakeRegistry struct {
	*cachingFakeRegistry
	logger *captureLogger
}

func (f *loggingFakeRegistry) Logger() hermod.Logger { return f.logger }

type captureLogger struct {
	mu    sync.Mutex
	warns []string
}

func (c *captureLogger) Debug(string, ...any) {}
func (c *captureLogger) Info(string, ...any)  {}
func (c *captureLogger) Error(string, ...any) {}

func (c *captureLogger) Warn(msg string, kv ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warns = append(c.warns, msg+" "+fmt.Sprint(kv...))
}

func (c *captureLogger) warnings() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.warns, "\n")
}

// A path that resolves to nothing is still bound, as NULL -- an optional field
// is a legitimate empty. But a typo looks identical, and the query then matches
// nothing and the default onMiss swallows it. Naming the token is the
// breadcrumb that was missing.
func TestAnUnresolvedTemplatePathIsWarnedAbout(t *testing.T) {
	tr, caching := newFlattenFixture(t)
	reg := &loggingFakeRegistry{cachingFakeRegistry: caching, logger: &captureLogger{}}

	runCDCLookup(t, tr, reg,
		queryModeConfig(`SELECT email, name FROM users WHERE id = {{.user_id}} AND name = {{.typoo}}`),
		map[string]any{"user_id": "u1"})

	got := reg.logger.warnings()
	if got == "" {
		t.Fatal("no warning logged for a template path that resolved to nothing")
	}
	if !strings.Contains(got, "typoo") {
		t.Errorf("warnings = %q, want one naming the unresolved token %q", got, "typoo")
	}
}

// A registry with no logger must not be a crash, and must not stop the lookup.
func TestAnUnresolvedPathWithoutALoggerStillRuns(t *testing.T) {
	tr, reg := newFlattenFixture(t)

	got := runCDCLookup(t, tr, reg,
		queryModeConfig(`SELECT email, name FROM users WHERE id = {{.user_id}} AND {{.typoo}} IS NULL`),
		map[string]any{"user_id": "u1"})

	if row := wantRow(t, got, "an unresolved token binds NULL, it does not abort the query"); row["name"] != "Ada" {
		t.Errorf("name = %v, want Ada", row["name"])
	}
}
