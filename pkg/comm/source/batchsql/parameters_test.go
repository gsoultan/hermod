package batchsql

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	_ "modernc.org/sqlite"
)

func paramSource(t *testing.T, cfg Config) *BatchSQLSource {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE t (id TEXT PRIMARY KEY, n INTEGER, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO t VALUES ('u1',1,'active'),('u2',2,'active'),('u3',3,'archived')`); err != nil {
		t.Fatal(err)
	}
	s := NewBatchSQLSource(&mockDBProvider{db: db}, cfg)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPrepareQuery_BindsConfiguredParameters(t *testing.T) {
	cases := []struct {
		name     string
		params   string
		query    string
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "array in an IN list expands",
			params:   `{"ids":["u1","u3"]}`,
			query:    "SELECT * FROM t WHERE id IN ({{.ids}})",
			wantSQL:  "SELECT * FROM t WHERE id IN (?, ?)",
			wantArgs: []any{"u1", "u3"},
		},
		{
			name:     "numeric array in an IN list expands",
			params:   `{"ns":[1,2]}`,
			query:    "SELECT * FROM t WHERE n IN ({{.ns}})",
			wantSQL:  "SELECT * FROM t WHERE n IN (?, ?)",
			wantArgs: []any{float64(1), float64(2)},
		},
		{
			name:     "scalar binds as one parameter",
			params:   `{"status":"active"}`,
			query:    "SELECT * FROM t WHERE status = {{.status}}",
			wantSQL:  "SELECT * FROM t WHERE status = ?",
			wantArgs: []any{"active"},
		},
		{
			name:     "parameters and the watermark together",
			params:   `{"status":"active"}`,
			query:    "SELECT * FROM t WHERE status = {{.status}} AND id > '{{.last_value}}'",
			wantSQL:  "SELECT * FROM t WHERE status = ? AND id > 'u1'",
			wantArgs: []any{"active"},
		},
		{
			name:     "nested parameter path",
			params:   `{"filter":{"status":"active"}}`,
			query:    "SELECT * FROM t WHERE status = {{.filter.status}}",
			wantSQL:  "SELECT * FROM t WHERE status = ?",
			wantArgs: []any{"active"},
		},
		{
			name:     "no tokens is untouched",
			params:   "",
			query:    "SELECT * FROM t",
			wantSQL:  "SELECT * FROM t",
			wantArgs: nil,
		},
		{
			name:     "watermark only keeps the existing splice",
			params:   "",
			query:    "SELECT * FROM t WHERE id > '{{.last_value}}'",
			wantSQL:  "SELECT * FROM t WHERE id > 'u1'",
			wantArgs: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := paramSource(t, Config{Parameters: tc.params})
			gotSQL, gotArgs, err := s.prepareQuery("sqlite", tc.query, "u1")
			if err != nil {
				t.Fatalf("prepareQuery: %v", err)
			}
			if gotSQL != tc.wantSQL {
				t.Errorf("sql:\n got %q\nwant %q", gotSQL, tc.wantSQL)
			}
			if !reflect.DeepEqual(gotArgs, tc.wantArgs) {
				t.Errorf("args:\n got %#v\nwant %#v", gotArgs, tc.wantArgs)
			}
		})
	}
}

// Binding an undefined token as NULL would turn a typo into a query that runs
// and matches nothing -- a green node delivering no rows, with nothing anywhere
// saying why.
func TestPrepareQuery_UndefinedParameterIsAnError(t *testing.T) {
	s := paramSource(t, Config{Parameters: `{"ids":["u1"]}`})
	_, _, err := s.prepareQuery("sqlite", "SELECT * FROM t WHERE id IN ({{.idz}})", "")
	if err == nil {
		t.Fatal("want an error naming the undefined parameter")
	}
	if !strings.Contains(err.Error(), "idz") {
		t.Fatalf("error should name the token, got: %v", err)
	}
}

func TestPrepareQuery_MalformedParametersJSONIsAnError(t *testing.T) {
	s := paramSource(t, Config{Parameters: `{"ids":`})
	_, _, err := s.prepareQuery("sqlite", "SELECT * FROM t WHERE id IN ({{.ids}})", "")
	if err == nil {
		t.Fatal("want an error for malformed parameters JSON")
	}
}

// A malformed parameters blob must not be read as "no parameters": that would
// silently drop every filter the operator configured.
func TestPrepareQuery_MalformedParametersDoesNotDegradeToNoFilter(t *testing.T) {
	s := paramSource(t, Config{Parameters: `not json`})
	if _, _, err := s.prepareQuery("sqlite", "SELECT * FROM t WHERE status = {{.status}}", ""); err == nil {
		t.Fatal("want an error, not an unfiltered query")
	}
}

func TestPrepareQuery_ParametersAcceptAJSONArrayOfStrings(t *testing.T) {
	s := paramSource(t, Config{Parameters: `{"ids":["u1","u2","u3"]}`})
	gotSQL, gotArgs, err := s.prepareQuery("pgx", "SELECT * FROM t WHERE id IN ({{.ids}})", "")
	if err != nil {
		t.Fatal(err)
	}
	if gotSQL != "SELECT * FROM t WHERE id IN ($1, $2, $3)" {
		t.Fatalf("sql = %q", gotSQL)
	}
	if !reflect.DeepEqual(gotArgs, []any{"u1", "u2", "u3"}) {
		t.Fatalf("args = %#v", gotArgs)
	}
}

// The scheduled run and the editor's preview must resolve a query the same way,
// or a query that previews clean fails on the cron and vice versa.
func TestBatchSQL_RunAndSampleAgreeOnParameters(t *testing.T) {
	cfg := Config{
		SourceID:   "s",
		Cron:       "* * * * * *",
		Queries:    `["SELECT id FROM t WHERE id IN ({{.ids}}) ORDER BY id"]`,
		Parameters: `{"ids":["u1","u3"]}`,
	}
	s := paramSource(t, cfg)

	msg, err := s.Sample(t.Context(), "")
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if got := msg.Data()["id"]; got != "u1" {
		t.Fatalf("Sample returned %#v, want u1", got)
	}

	got, err := s.Read(t.Context())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if id := got.Data()["id"]; id != "u1" && id != "u3" {
		t.Fatalf("Read returned %#v, want one of the filtered ids", id)
	}
}

func TestPrepareQuery_OversizedListIsRejected(t *testing.T) {
	ids := make([]string, sqlutil.MaxListExpansion+1)
	for i := range ids {
		ids[i] = "x"
	}
	blob, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	s := paramSource(t, Config{Parameters: string(blob)})
	if _, _, err := s.prepareQuery("sqlite", "SELECT * FROM t WHERE id IN ({{.ids}})", ""); err == nil {
		t.Fatal("want an error for an oversized list")
	}
}
