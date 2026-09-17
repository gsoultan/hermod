//go:build integration
// +build integration

package core_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// An IN list over a real uuid column is the shape that had no working form:
// pgx rejects a Go slice bound to a scalar OID with "cannot find encode plan",
// so the failure was a hard error on the first message, per column type.
func TestParameterizeTemplateINListOverRealPostgresTypes(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `create table if not exists hermod_in_list_probe (
		id uuid primary key, n int, s text)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.ExecContext(context.Background(), `drop table if exists hermod_in_list_probe`) })
	if _, err := db.ExecContext(ctx, `truncate hermod_in_list_probe`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `insert into hermod_in_list_probe values
		('6f1a2b3c-0000-0000-0000-000000000001',1,'a'),
		('6f1a2b3c-0000-0000-0000-000000000002',2,'b'),
		('6f1a2b3c-0000-0000-0000-000000000003',3,'c')`); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		tpl  string
		data map[string]any
		want int
	}{
		{"uuid []string", "select count(*) from hermod_in_list_probe where id in ({{.id}})",
			map[string]any{"id": []string{"6f1a2b3c-0000-0000-0000-000000000001", "6f1a2b3c-0000-0000-0000-000000000002"}}, 2},
		{"uuid []any", "select count(*) from hermod_in_list_probe where id in ({{.id}})",
			map[string]any{"id": []any{"6f1a2b3c-0000-0000-0000-000000000001"}}, 1},
		{"int []int", "select count(*) from hermod_in_list_probe where n in ({{.n}})",
			map[string]any{"n": []int{1, 2}}, 2},
		{"int []any float64 from JSON", "select count(*) from hermod_in_list_probe where n in ({{.n}})",
			map[string]any{"n": []any{float64(1), float64(3)}}, 2},
		{"text []string", "select count(*) from hermod_in_list_probe where s in ({{.s}})",
			map[string]any{"s": []string{"a", "c"}}, 2},
		{"empty list matches nothing", "select count(*) from hermod_in_list_probe where s in ({{.s}})",
			map[string]any{"s": []string{}}, 0},
		{"scalar still works", "select count(*) from hermod_in_list_probe where n in ({{.n}})",
			map[string]any{"n": 2}, 1},
		// = ANY binds the slice whole and must keep doing so.
		{"= ANY unchanged", "select count(*) from hermod_in_list_probe where id = any({{.id}})",
			map[string]any{"id": []string{"6f1a2b3c-0000-0000-0000-000000000001", "6f1a2b3c-0000-0000-0000-000000000003"}}, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqlText, args := core.ParameterizeTemplate("pgx", tc.tpl, tc.data)
			var got int
			if err := db.QueryRowContext(ctx, sqlText, args...).Scan(&got); err != nil {
				t.Fatalf("query %q args=%#v: %v", sqlText, args, err)
			}
			if got != tc.want {
				t.Fatalf("query %q args=%#v => %d rows, want %d", sqlText, args, got, tc.want)
			}
		})
	}
}
