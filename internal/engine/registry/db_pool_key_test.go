package registry

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// seedSQLite creates a standalone SQLite file holding a single table, so a query
// naming that table can only succeed on a connection actually opened against it.
func seedSQLite(t *testing.T, path, table string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE "+table+" (id INTEGER)"); err != nil {
		t.Fatalf("create %s in %s: %v", table, path, err)
	}
}

// The discovery paths (SQL query builder, table sampling, schema discovery) call
// GetDB with an ad-hoc config and no source ID. If the pool is keyed on the ID
// alone, every one of those configs collides on the empty key and the second
// caller silently runs its SQL on the first caller's database — which surfaces to
// the user as "relation ... does not exist" for a table that does exist, in a
// different database than the one they selected.
func TestGetDBDoesNotShareAPoolBetweenDifferentConfigs(t *testing.T) {
	dir := t.TempDir()
	alphaPath := filepath.Join(dir, "alpha.db")
	betaPath := filepath.Join(dir, "beta.db")
	seedSQLite(t, alphaPath, "alpha_only")
	seedSQLite(t, betaPath, "beta_only")

	reg := NewRegistry(&mockStorage{})
	ctx := context.Background()

	alphaDB, err := reg.GetDB(ctx, "sqlite", map[string]string{"path": alphaPath})
	if err != nil {
		t.Fatalf("GetDB(alpha): %v", err)
	}
	if _, err := alphaDB.ExecContext(ctx, "SELECT 1 FROM alpha_only"); err != nil {
		t.Fatalf("alpha connection cannot see its own table: %v", err)
	}

	betaDB, err := reg.GetDB(ctx, "sqlite", map[string]string{"path": betaPath})
	if err != nil {
		t.Fatalf("GetDB(beta): %v", err)
	}
	if _, err := betaDB.ExecContext(ctx, "SELECT 1 FROM beta_only"); err != nil {
		t.Fatalf("second config was served the first config's connection: %v", err)
	}
}

// The same key collision applies to a stored source whose connection details are
// edited: the pool is keyed on the ID, so the old connection keeps being handed
// out as long as it still pings.
func TestGetOrOpenDBReopensAfterTheSourceConfigChanges(t *testing.T) {
	dir := t.TempDir()
	alphaPath := filepath.Join(dir, "alpha.db")
	betaPath := filepath.Join(dir, "beta.db")
	seedSQLite(t, alphaPath, "alpha_only")
	seedSQLite(t, betaPath, "beta_only")

	reg := NewRegistry(&mockStorage{})
	ctx := context.Background()

	src := storage.Source{ID: "lookup-src", Type: "sqlite", Config: map[string]string{"path": alphaPath}}
	if _, err := reg.GetOrOpenDB(src); err != nil {
		t.Fatalf("GetOrOpenDB(alpha): %v", err)
	}

	src.Config = map[string]string{"path": betaPath}
	repointed, err := reg.GetOrOpenDB(src)
	if err != nil {
		t.Fatalf("GetOrOpenDB(beta): %v", err)
	}
	if _, err := repointed.ExecContext(ctx, "SELECT 1 FROM beta_only"); err != nil {
		t.Fatalf("edited config still served the old connection: %v", err)
	}
}
