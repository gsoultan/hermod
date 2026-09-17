//go:build integration
// +build integration

package lookup

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/message"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// The reported workflow, reproduced against a real PostgreSQL: a RabbitMQ queue
// feeding a db_lookup node configured exactly the way the editor produces one --
//
//	{"mode":"query","sourceId":...,"targetField":"User",
//	 "queryTemplate":"SELECT * FROM iam.users WHERE id = {{.UserId}}"}
//
// -- with no keyField and no ttl. Every per-message input is inside the template
// token, so the cache key built from the template *text* was identical for all
// of them, and an unset ttl means the entry never expires: message one's row was
// served to every message that followed, however different its UserId.
//
// sqlite is enough to catch the key collision, but not enough to prove the fix
// on the driver this actually runs on: pgx binds $1 rather than ?, and the
// schema-qualified table and uuid column are shapes sqlite does not have.
//
// Run with:
//
//	POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/hermod_test_source?sslmode=disable' \
//	  go test -tags=integration -run TestDBLookupQueryMode ./pkg/comm/transformer/lookup/
func TestDBLookupQueryModeServesEachMessageItsOwnRow(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS lookup_it`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS lookup_it.users`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE lookup_it.users (id uuid PRIMARY KEY, user_name text)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS lookup_it.users`) })

	const (
		idA = "01a0ae4b-7d94-760c-9e15-0266d13b9937"
		idB = "01a0a967-5cf4-7ef6-ac7c-84a77426e31b"
	)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO lookup_it.users(id, user_name) VALUES ($1,$2), ($3,$4)`,
		idA, "andi.finance@gmail.com", idB, "grace@example.com"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	reg := &cachingFakeRegistry{
		db:     db,
		source: storage.Source{ID: "src1", Type: "postgres", Config: hermod.StringMap{"use_cdc": "false"}},
		cache:  map[string]any{},
	}
	tr := &DBLookupTransformer{}

	cfg := map[string]any{
		"mode":          "query",
		"sourceId":      "src1",
		"targetField":   "User",
		"queryTemplate": "SELECT * FROM lookup_it.users WHERE id = {{.UserId}}",
	}

	lookup := func(userID string) any {
		msg := message.AcquireMessage()
		t.Cleanup(msg.Release)
		msg.SetData("UserId", userID)

		lctx := context.WithValue(ctx, hermod.RegistryKey, reg)
		out, err := tr.Transform(lctx, msg, cfg)
		if err != nil {
			t.Fatalf("Transform(%s): %v", userID, err)
		}
		row, ok := out.Data()["User"].(map[string]any)
		if !ok {
			t.Fatalf("Transform(%s): User = %#v, want a row", userID, out.Data()["User"])
		}
		return row["id"]
	}

	if got := lookup(idA); got != idA {
		t.Fatalf("first message: User.id = %#v, want %q", got, idA)
	}
	if got := lookup(idB); got != idB {
		t.Errorf("second message: User.id = %#v, want %q -- it was served the first message's row", got, idB)
	}

	// And the first key still resolves to the first row, i.e. the second lookup
	// added an entry rather than replacing one.
	if got := lookup(idA); got != idA {
		t.Errorf("first message again: User.id = %#v, want %q", got, idA)
	}
	if len(reg.cache) != 2 {
		t.Errorf("cache holds %d entries, want 2 (one per distinct UserId)", len(reg.cache))
	}
	if reg.sets != 2 {
		t.Errorf("the database was queried %d times for 3 lookups over 2 distinct ids; want 2, "+
			"so the cache is still doing its job", reg.sets)
	}
}
