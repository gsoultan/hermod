package core_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	_ "modernc.org/sqlite"
)

// A slice bound to a single placeholder is not a list -- every driver rejects
// it ("unsupported type []interface {}, a slice of interface" on database/sql,
// "cannot find encode plan" on pgx). A token sitting in an IN list has to
// expand to one placeholder per element instead.
func TestParameterizeTemplateExpandsSliceInINList(t *testing.T) {
	cases := []struct {
		name     string
		driver   string
		tpl      string
		data     map[string]any
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "postgres uuid list",
			driver:   "pgx",
			tpl:      "select * from a where id in ({{.id}})",
			data:     map[string]any{"id": []any{"u1", "u2", "u3"}},
			wantSQL:  "select * from a where id in ($1, $2, $3)",
			wantArgs: []any{"u1", "u2", "u3"},
		},
		{
			name:     "mysql int list",
			driver:   "mysql",
			tpl:      "select * from a where id in ({{.id}})",
			data:     map[string]any{"id": []int{1, 2}},
			wantSQL:  "select * from a where id in (?, ?)",
			wantArgs: []any{1, 2},
		},
		{
			name:     "sqlite string list",
			driver:   "sqlite",
			tpl:      "SELECT * FROM a WHERE s IN ({{.s}})",
			data:     map[string]any{"s": []string{"a", "b"}},
			wantSQL:  "SELECT * FROM a WHERE s IN (?, ?)",
			wantArgs: []any{"a", "b"},
		},
		{
			name:     "mssql ordinal placeholders keep counting",
			driver:   "mssql",
			tpl:      "select * from a where n = {{.n}} and id in ({{.id}})",
			data:     map[string]any{"n": 7, "id": []any{"x", "y"}},
			wantSQL:  "select * from a where n = @p1 and id in (@p2, @p3)",
			wantArgs: []any{7, "x", "y"},
		},
		{
			name:     "NOT IN expands too",
			driver:   "pgx",
			tpl:      "select * from a where id not in ({{.id}})",
			data:     map[string]any{"id": []any{1, 2}},
			wantSQL:  "select * from a where id not in ($1, $2)",
			wantArgs: []any{1, 2},
		},
		{
			name:     "IN with no space before paren",
			driver:   "pgx",
			tpl:      "select * from a where id in({{.id}})",
			data:     map[string]any{"id": []any{1, 2}},
			wantSQL:  "select * from a where id in($1, $2)",
			wantArgs: []any{1, 2},
		},
		{
			name:     "scalar in an IN list stays one placeholder",
			driver:   "pgx",
			tpl:      "select * from a where id in ({{.id}})",
			data:     map[string]any{"id": 42},
			wantSQL:  "select * from a where id in ($1)",
			wantArgs: []any{42},
		},
		{
			name:     "two tokens in one IN list",
			driver:   "pgx",
			tpl:      "select * from a where id in ({{.a}}, {{.b}})",
			data:     map[string]any{"a": []any{1, 2}, "b": 3},
			wantSQL:  "select * from a where id in ($1, $2, $3)",
			wantArgs: []any{1, 2, 3},
		},
		{
			name:     "nested subquery IN list",
			driver:   "pgx",
			tpl:      "select * from a where id in (select id from b where k in ({{.k}}))",
			data:     map[string]any{"k": []any{"p", "q"}},
			wantSQL:  "select * from a where id in (select id from b where k in ($1, $2))",
			wantArgs: []any{"p", "q"},
		},
		{
			// = ANY($1) is the native Postgres array form and works today with a
			// single bound array. Expanding it would turn it into a syntax error.
			name:     "ANY keeps the slice as one array argument",
			driver:   "pgx",
			tpl:      "select * from a where id = any({{.id}})",
			data:     map[string]any{"id": []string{"u1", "u2"}},
			wantSQL:  "select * from a where id = any($1)",
			wantArgs: []any{[]string{"u1", "u2"}},
		},
		{
			name:     "ALL keeps the slice as one array argument",
			driver:   "pgx",
			tpl:      "select * from a where n > all({{.n}})",
			data:     map[string]any{"n": []int{1, 2}},
			wantSQL:  "select * from a where n > all($1)",
			wantArgs: []any{[]int{1, 2}},
		},
		{
			name:     "slice outside any list stays one argument",
			driver:   "pgx",
			tpl:      "insert into a (tags) values ({{.tags}})",
			data:     map[string]any{"tags": []string{"a", "b"}},
			wantSQL:  "insert into a (tags) values ($1)",
			wantArgs: []any{[]string{"a", "b"}},
		},
		{
			// IN () is a syntax error everywhere. A bound NULL is valid SQL and
			// matches nothing, which is what an empty set means.
			name:     "empty slice binds a single NULL",
			driver:   "pgx",
			tpl:      "select * from a where id in ({{.id}})",
			data:     map[string]any{"id": []any{}},
			wantSQL:  "select * from a where id in ($1)",
			wantArgs: []any{nil},
		},
		{
			// []byte is a scalar (bytea/blob), not a list of bytes.
			name:     "byte slice is not a list",
			driver:   "pgx",
			tpl:      "select * from a where h in ({{.h}})",
			data:     map[string]any{"h": []byte{1, 2, 3}},
			wantSQL:  "select * from a where h in ($1)",
			wantArgs: []any{[]byte{1, 2, 3}},
		},
		{
			name:     "identifier ending in in is not an IN list",
			driver:   "pgx",
			tpl:      "select * from a where checkin ({{.id}})",
			data:     map[string]any{"id": []any{1, 2}},
			wantSQL:  "select * from a where checkin ($1)",
			wantArgs: []any{[]any{1, 2}},
		},
		{
			name:     "paren inside a string literal does not open a list",
			driver:   "pgx",
			tpl:      "select * from a where s = 'in (' and id in ({{.id}})",
			data:     map[string]any{"id": []any{1, 2}},
			wantSQL:  "select * from a where s = 'in (' and id in ($1, $2)",
			wantArgs: []any{1, 2},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSQL, gotArgs := core.ParameterizeTemplate(tc.driver, tc.tpl, tc.data)
			if gotSQL != tc.wantSQL {
				t.Errorf("sql:\n got %q\nwant %q", gotSQL, tc.wantSQL)
			}
			if !reflect.DeepEqual(gotArgs, tc.wantArgs) {
				t.Errorf("args:\n got %#v\nwant %#v", gotArgs, tc.wantArgs)
			}
		})
	}
}

// ParameterizeTemplate binds an unresolved path as NULL, which is meaningful
// for an optional message field but silent for a misconfigured query. The Ex
// form reports them so a caller that wants to fail loudly can.
func TestParameterizeTemplateExReportsUnresolvedTokens(t *testing.T) {
	b := core.ParameterizeTemplateEx("pgx", "select * from a where id = {{.missing}} and n = {{.n}}", map[string]any{"n": 1})
	if b.SQL != "select * from a where id = $1 and n = $2" {
		t.Fatalf("sql = %q", b.SQL)
	}
	if !reflect.DeepEqual(b.Unresolved, []string{"missing"}) {
		t.Fatalf("unresolved = %#v, want [missing]", b.Unresolved)
	}
	if !reflect.DeepEqual(b.Args, []any{nil, 1}) {
		t.Fatalf("args = %#v", b.Args)
	}
}

func TestParameterizeTemplateExQuotedLiteralIsNotUnresolved(t *testing.T) {
	b := core.ParameterizeTemplateEx("pgx", "select * from a where s = {{'lit'}}", nil)
	if len(b.Unresolved) != 0 {
		t.Fatalf("unresolved = %#v, want none", b.Unresolved)
	}
	if !reflect.DeepEqual(b.Args, []any{"lit"}) {
		t.Fatalf("args = %#v", b.Args)
	}
}

func TestAsSliceCoversAnySliceKindButNotBytesOrStrings(t *testing.T) {
	type custom string
	cases := []struct {
		in   any
		want []any
		ok   bool
	}{
		{[]any{1, "a"}, []any{1, "a"}, true},
		{[]string{"a"}, []any{"a"}, true},
		{[]int{1}, []any{1}, true},
		{[]int32{1}, []any{int32(1)}, true},
		{[]int64{1}, []any{int64(1)}, true},
		{[]float64{1.5}, []any{1.5}, true},
		{[]bool{true}, []any{true}, true},
		{[]custom{"a"}, []any{custom("a")}, true},
		{[2]int{1, 2}, []any{1, 2}, true},
		{[]byte("ab"), nil, false},
		{json.RawMessage(`[1]`), nil, false},
		{"abc", nil, false},
		{42, nil, false},
		{nil, nil, false},
	}
	for _, tc := range cases {
		got, ok := core.AsSlice(tc.in)
		if ok != tc.ok {
			t.Errorf("AsSlice(%#v) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("AsSlice(%#v) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// The end-to-end shape the report was about: an array variable in an IN list
// has to survive a real driver round trip.
func TestParameterizeTemplateINListRunsOnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `create table a (id text, n integer)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `insert into a values ('u1',1),('u2',2),('u3',3)`); err != nil {
		t.Fatal(err)
	}

	sqlText, args := core.ParameterizeTemplate("sqlite",
		"select n from a where id in ({{.ids}}) order by n",
		map[string]any{"ids": []any{"u1", "u3"}})

	rows, err := db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		t.Fatalf("query %q: %v", sqlText, err)
	}
	defer rows.Close()
	var got []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("rows = %v, want [1 3]", got)
	}
}

func TestParameterizeTemplateEmptyINListMatchesNothingOnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `create table a (id text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `insert into a values ('u1')`); err != nil {
		t.Fatal(err)
	}
	sqlText, args := core.ParameterizeTemplate("sqlite",
		"select count(*) from a where id in ({{.ids}})", map[string]any{"ids": []string{}})
	var n int
	if err := db.QueryRowContext(ctx, sqlText, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sqlText, err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

// The element count of an expanded list comes from message data, so nothing in
// the pipeline bounds it. Without a cap, a pathological array builds a
// multi-hundred-megabyte statement in this process before any driver sees it
// and rejects it -- PostgreSQL's wire protocol stops at 65535 parameters, SQL
// Server at 2100.
func TestParameterizeTemplateBoundsListExpansion(t *testing.T) {
	huge := make([]any, sqlutil.MaxListExpansion+1)
	for i := range huge {
		huge[i] = i
	}
	b := core.ParameterizeTemplateEx("pgx", "select * from a where id in ({{.ids}})", map[string]any{"ids": huge})
	if b.Err == nil {
		t.Fatalf("want an error above the cap, got %d args", len(b.Args))
	}
	if !strings.Contains(b.Err.Error(), "ids") {
		t.Fatalf("error should name the token, got: %v", b.Err)
	}
	// Nothing half-built is handed back.
	if len(b.Args) != 0 {
		t.Fatalf("args = %d, want none", len(b.Args))
	}
}

func TestParameterizeTemplateAllowsExactlyTheCap(t *testing.T) {
	atCap := make([]any, sqlutil.MaxListExpansion)
	for i := range atCap {
		atCap[i] = i
	}
	b := core.ParameterizeTemplateEx("pgx", "select * from a where id in ({{.ids}})", map[string]any{"ids": atCap})
	if b.Err != nil {
		t.Fatalf("unexpected error at the cap: %v", b.Err)
	}
	if len(b.Args) != sqlutil.MaxListExpansion {
		t.Fatalf("args = %d, want %d", len(b.Args), sqlutil.MaxListExpansion)
	}
}
