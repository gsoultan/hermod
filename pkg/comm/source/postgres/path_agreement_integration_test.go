//go:build integration
// +build integration

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/sqlutil"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// The two ways a PostgreSQL row enters a pipeline must produce the same value
// for the same column, and this asserts it directly rather than one column at a
// time.
//
//   - the native path: pgx's rows.Values(), then sqlutil.DecodePGXValue, then
//     message.SanitizeValue. This is what Sample, snapshot and polling do, and
//     it is what the editor builds Available Fields from.
//   - the generic path: database/sql plus sqlutil.ScanRows. This is what
//     db_lookup, batch_sql and the editor's SQL builder do.
//
// They disagreed on six types, and the fixes went three different ways.
//
// json, jsonb and arrays were fixed on the generic side, because a string
// holding a document has no fields to reach into. time, interval and macaddr
// were fixed on the native side, because a pgtype struct and a base64 blob are
// not shapes anyone writes a workflow against.
//
// numeric took a third answer, and it is the most interesting one here: neither
// existing shape was right. The generic side's text could not be compared as a
// number, and the native side's JSON number could not be reached from the
// generic path without going through a float64 -- which corrupts what native
// carries exactly, since numeric(40,20) 1.00000000000000000001 becomes 1. It is
// now a json.Number on the generic side: a type that was in neither path
// before, keeping every digit and serialising as the bare number native already
// produced. See sqlutil.DecodeNumericText.
//
// One is expected to differ and is listed as such, so that the exclusion is a
// decision with a reason rather than a gap:
//
//   - inet: "10.0.0.1/32" against "10.0.0.1". Both are correct and usable and
//     the native form is strictly more informative.
//
// One divergence is NOT covered here and is not a shape problem: pgtype
// marshals both numeric infinities to 0 on the native path, where the generic
// path correctly reports "Infinity". That is silent corruption of a value
// rather than two renderings of the same one, so it wants fixing on the native
// side rather than excluding here. The fixture deliberately holds a finite
// numeric so this test measures shape agreement and nothing else.
//
// Measured against PostgreSQL 18.4. Run with:
//
//	POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/hermod_test_source?sslmode=disable' \
//	  go test -tags=integration -run TestBothReadPathsAgree ./pkg/comm/source/postgres/
func TestBothReadPathsAgreeOnColumnShape(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	ctx := t.Context()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	if _, err := admin.ExecContext(ctx, `DROP TABLE IF EXISTS path_agreement`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE TABLE path_agreement (
		k int PRIMARY KEY,
		c_text text, c_int int, c_bigint bigint, c_bool bool, c_float float8,
		c_uuid uuid, c_bytea bytea, c_date date, c_ts timestamp, c_tstz timestamptz,
		c_json json, c_jsonb jsonb, c_int_arr int[], c_text_arr text[],
		c_time time, c_intv interval, c_mac macaddr,
		c_numeric numeric(12,2), c_inet inet
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP TABLE IF EXISTS path_agreement`)
	})

	if _, err := admin.ExecContext(ctx, `INSERT INTO path_agreement VALUES (
		1, 'hello', 42, 9007199254740993, true, 1.5,
		'11111111-2222-3333-4444-555555555555', 'Hi'::bytea,
		'2026-09-21', '2026-09-21 08:30:00', '2026-09-21 08:30:00+07',
		'{"a":1}', '{"b":2}', '{1,2,3}', ARRAY['x','has,comma'],
		'08:30:00', '1 day 02:00:00', '08:00:2b:01:02:03',
		1200.50, '10.0.0.1')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	const q = `SELECT * FROM path_agreement WHERE k = 1`

	// Native: exactly what the source's record loops now do.
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	defer conn.Close(ctx)

	native := map[string]any{}
	rows, err := conn.Query(ctx, q)
	if err != nil {
		t.Fatalf("pgx query: %v", err)
	}
	fields := rows.FieldDescriptions()
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			t.Fatalf("values: %v", err)
		}
		for i, f := range fields {
			native[f.Name] = message.SanitizeValue(sqlutil.DecodePGXValue(values[i]))
		}
	}
	rows.Close()

	// Generic: exactly what db_lookup and batch_sql do.
	srows, err := admin.QueryContext(ctx, q)
	if err != nil {
		t.Fatalf("sql query: %v", err)
	}
	scanned, err := sqlutil.ScanRows(srows)
	if err != nil {
		t.Fatalf("ScanRows: %v", err)
	}
	if len(scanned) != 1 {
		t.Fatalf("got %d rows, want 1", len(scanned))
	}
	generic := scanned[0]

	// Compared through JSON because that is how both reach a preview panel, a
	// trace row and a sink: a difference invisible here is invisible to an
	// operator too.
	asJSON := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return "<unmarshalable>"
		}
		return string(b)
	}

	// Excluded on purpose, each with its reason. The loop below asserts these
	// still *do* differ rather than merely skipping them: a stale exclusion is
	// how a list like this stops describing the code.
	knownDifferent := map[string]string{
		"c_inet": "native keeps the prefix length; both forms are correct",
	}

	for name, reason := range knownDifferent {
		if _, ok := native[name]; !ok {
			t.Fatalf("%s is excluded from the comparison but is not a column in the "+
				"fixture, so the exclusion proves nothing", name)
		}
		if asJSON(native[name]) == asJSON(generic[name]) {
			t.Errorf("%s now agrees across both read paths (%s) -- remove it from "+
				"knownDifferent; it was excluded because %s",
				name, asJSON(native[name]), reason)
		}
	}

	for name, nv := range native {
		if _, skip := knownDifferent[name]; skip {
			continue
		}
		gv, ok := generic[name]
		if !ok {
			t.Errorf("%s: missing from the generic scan entirely", name)
			continue
		}
		if n, g := asJSON(nv), asJSON(gv); n != g {
			t.Errorf("%s disagrees between the two read paths:\n  native  %s\n  generic %s", name, n, g)
		}
	}

	// Spot-check the three fixed on the native side, so a regression that made
	// both paths equally wrong would still fail here.
	for _, tc := range []struct{ col, want string }{
		{"c_time", `"08:30:00"`},
		{"c_intv", `"1 day 02:00:00"`},
		{"c_mac", `"08:00:2b:01:02:03"`},
	} {
		if got := asJSON(native[tc.col]); got != tc.want {
			t.Errorf("native %s = %s, want %s", tc.col, got, tc.want)
		}
	}
	// And the two fixed on the generic side.
	if got := asJSON(generic["c_jsonb"]); got != `{"b":2}` {
		t.Errorf("generic c_jsonb = %s, want an object", got)
	}
	if got := asJSON(generic["c_int_arr"]); got != `[1,2,3]` {
		t.Errorf("generic c_int_arr = %s, want a list", got)
	}
}
