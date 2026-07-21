package lookup

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/comm/message"
	"github.com/user/hermod/pkg/comm/transformer/core"
	_ "modernc.org/sqlite"
)

type fakeRegistry struct{ db *sql.DB }

func (f fakeRegistry) GetOrOpenDB(src storage.Source) (*sql.DB, error) { return f.db, nil }

func TestDBLookup_SQL_ByKeyColumn(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE test (id TEXT PRIMARY KEY, value TEXT, status TEXT)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec(`INSERT INTO test(id, value, status) VALUES ('1','one','ACTIVE'), ('2','two','INACTIVE')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	got, err := tr.lookupSQL(t.Context(), reg, src, "test", "id", "1", "", "value", "", nil)
	if err != nil {
		t.Fatalf("lookupSQL: %v", err)
	}
	if got == nil || got.(string) != "one" {
		t.Fatalf("want 'one', got %#v", got)
	}
}

func TestDBLookup_SQL_WithWhereClause(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE test (id TEXT PRIMARY KEY, value TEXT, status TEXT)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec(`INSERT INTO test(id, value, status) VALUES ('1','one','ACTIVE'), ('2','two','INACTIVE')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	data := map[string]any{"id": "2", "st": "INACTIVE"}
	where := "id = {{id}} AND status = '{{st}}'"
	got, err := tr.lookupSQL(t.Context(), reg, src, "test", "", nil, where, "value", "", data)
	if err != nil {
		t.Fatalf("lookupSQL(where): %v", err)
	}
	if got == nil || got.(string) != "two" {
		t.Fatalf("want 'two', got %#v", got)
	}
}

func TestParameterizeTemplate_SQLite(t *testing.T) {
	data := map[string]any{
		"user_id": 42,
		"tenant":  "acme",
	}
	tpl := "SELECT * FROM users WHERE id = {{ source.user_id }} AND status = {{ 'active' }} AND tenant = {{ source.tenant }}"
	sqlText, args := core.ParameterizeTemplate("sqlite", tpl, data)
	expectedSQL := "SELECT * FROM users WHERE id = ? AND status = ? AND tenant = ?"
	if sqlText != expectedSQL {
		t.Fatalf("SQL mismatch. expected %q, got %q", expectedSQL, sqlText)
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(args))
	}
	if args[0] != 42 || args[1] != "active" || args[2] != "acme" {
		t.Fatalf("args mismatch: %#v", args)
	}
}

func TestDBLookup_SQL_BatchIN(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE test (id TEXT PRIMARY KEY, value TEXT)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec(`INSERT INTO test(id, value) VALUES ('1','one'), ('2','two'), ('3','three')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	src := storage.Source{Type: "sqlite"}
	tr := &DBLookupTransformer{}
	reg := fakeRegistry{db: db}

	ids := []string{"1", "3"}
	got, err := tr.lookupSQL(t.Context(), reg, src, "test", "id", ids, "", "value", "", nil)
	if err != nil {
		t.Fatalf("lookupSQL batch: %v", err)
	}
	// Expect a slice with two values in any order
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %#v", got)
	}
	// Order is not guaranteed by SQL IN; sort-like check
	vals := map[any]bool{}
	for _, v := range arr {
		vals[v] = true
	}
	expected := []any{"one", "three"}
	for _, v := range expected {
		if !vals[v] {
			t.Fatalf("missing value %v in result %#v", v, arr)
		}
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 results, got %d (%#v)", len(arr), arr)
	}
}

type fullFakeRegistry struct {
	db     *sql.DB
	source storage.Source
}

func (f *fullFakeRegistry) GetSourceConfig(ctx context.Context, id string) (storage.Source, error) {
	return f.source, nil
}

func (f *fullFakeRegistry) GetOrOpenDB(src storage.Source) (*sql.DB, error) {
	return f.db, nil
}

func (f *fullFakeRegistry) GetLookupCache(key string) (any, bool) {
	return nil, false
}

func (f *fullFakeRegistry) SetLookupCache(key string, value any, ttl time.Duration) {}

func TestDBLookup_ModePrioritization(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE test (id TEXT PRIMARY KEY, value TEXT)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = db.Exec(`INSERT INTO test(id, value) VALUES ('1','table-value'), ('2','query-value')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	tr := &DBLookupTransformer{}
	reg := &fullFakeRegistry{
		db: db,
		source: storage.Source{
			ID:   "src1",
			Type: "sqlite",
		},
	}

	ctx := context.WithValue(t.Context(), hermod.RegistryKey, reg)

	tests := []struct {
		name     string
		config   map[string]any
		expected string
	}{
		{
			name: "mode=table with queryTemplate present",
			config: map[string]any{
				"mode":          "table",
				"sourceId":      "src1",
				"table":         "test",
				"keyColumn":     "id",
				"keyField":      "id",
				"valueColumn":   "value",
				"targetField":   "result",
				"queryTemplate": "SELECT 'query-value' as value",
			},
			expected: "table-value",
		},
		{
			name: "mode=query with table present",
			config: map[string]any{
				"mode":          "query",
				"sourceId":      "src1",
				"table":         "test",
				"keyColumn":     "id",
				"keyField":      "id",
				"valueColumn":   "value",
				"targetField":   "result",
				"queryTemplate": "SELECT 'query-value' as value",
			},
			expected: "query-value",
		},
		{
			name: "no mode with queryTemplate present",
			config: map[string]any{
				"sourceId":      "src1",
				"table":         "test",
				"keyColumn":     "id",
				"keyField":      "id",
				"valueColumn":   "value",
				"targetField":   "result",
				"queryTemplate": "SELECT 'query-value' as value",
			},
			expected: "query-value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := message.AcquireMessage()
			msg.SetData("id", "1")
			res, err := tr.Transform(ctx, msg, tc.config)
			if err != nil {
				t.Fatalf("Transform failed: %v", err)
			}
			got := res.Data()["result"]
			if got != tc.expected {
				t.Errorf("expected %q, got %v", tc.expected, got)
			}
		})
	}
}
