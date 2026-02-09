//go:build integration
// +build integration

package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

func TestPostgresSink_IdempotentUpsert(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to run")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	table := "hermod_idemp_test"
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+table)
	_, err = pool.Exec(ctx, "CREATE TABLE "+table+" (id text PRIMARY KEY, data jsonb NOT NULL)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	defer pool.Exec(ctx, "DROP TABLE IF EXISTS "+table)

	snk := NewPostgresSink(dsn, "", nil, true, "hard_delete", "", "", false, false)

	// First write
	m1 := message.AcquireMessage()
	defer message.ReleaseMessage(m1)
	m1.SetID("id-1")
	m1.SetOperation(hermod.OpCreate)
	m1.SetTable(table)
	m1.SetAfter([]byte(`{"v":1}`))
	if err := snk.Write(ctx, m1); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Duplicate with different payload should upsert to new value
	m2 := message.AcquireMessage()
	defer message.ReleaseMessage(m2)
	m2.SetID("id-1")
	m2.SetOperation(hermod.OpUpdate)
	m2.SetTable(table)
	m2.SetAfter([]byte(`{"v":2}`))
	if err := snk.Write(ctx, m2); err != nil {
		t.Fatalf("second write: %v", err)
	}

	var got string
	if err := pool.QueryRow(ctx, "SELECT data::text FROM "+table+" WHERE id = $1", "id-1").Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != "{\"v\": 2}" && got != "{\"v\":2}" {
		t.Fatalf("unexpected data: %s", got)
	}
}
