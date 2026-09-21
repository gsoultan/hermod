package lookup

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// A db_lookup pointed at a batch_sql source built its statement in the wrong
// dialect. batch_sql is a wrapper -- queries plus a cron, delegating its
// connection to another source -- so src.Type is "batch_sql" and never the
// name of the database the statement will actually run against. The hand
// written switch had no case for it, so Placeholder fell through to "?" and
// PostgreSQL answered:
//
//	failed to execute lookup query: ERROR: syntax error at or near "LIMIT" (SQLSTATE 42601)
//
// GetOrOpenDB resolved the delegate correctly all along; only the dialect was
// wrong, which is why this reads as the operator's SQL being at fault.
type dialectFakeRegistry struct {
	sources map[string]storage.Source
	asked   []string
}

func (f *dialectFakeRegistry) GetSourceConfig(_ context.Context, id string) (storage.Source, error) {
	f.asked = append(f.asked, id)
	src, ok := f.sources[id]
	if !ok {
		return storage.Source{}, context.Canceled
	}
	return src, nil
}

func TestLookupDialectResolvesABatchSQLDelegate(t *testing.T) {
	reg := &dialectFakeRegistry{sources: map[string]storage.Source{
		"pg1": {ID: "pg1", Type: "postgres"},
		"my1": {ID: "my1", Type: "mysql"},
	}}

	tests := []struct {
		name string
		src  storage.Source
		want string
	}{
		{
			"batch_sql over postgres binds $1, not ?",
			storage.Source{ID: "b1", Type: "batch_sql", Config: hermod.StringMap{"source_id": "pg1"}},
			"pgx",
		},
		{
			"batch_sql over mysql still binds ?",
			storage.Source{ID: "b2", Type: "batch_sql", Config: hermod.StringMap{"source_id": "my1"}},
			"mysql",
		},
		{
			// Absent from the old switch too, but not broken by it: Placeholder
			// and QuoteIdent both know "yugabyte" on their own, so it produced
			// $1 and PostgreSQL quoting either way. Measured, not assumed.
			// Normalising it here removes the latent disagreement rather than
			// fixing a live one.
			"yugabyte binds $1",
			storage.Source{ID: "y1", Type: "yugabyte"},
			"pgx",
		},
		{"postgres", storage.Source{ID: "p", Type: "postgres"}, "pgx"},
		{"mysql", storage.Source{ID: "m", Type: "mysql"}, "mysql"},
		{"mariadb", storage.Source{ID: "d", Type: "mariadb"}, "mysql"},
		{"sqlite", storage.Source{ID: "s", Type: "sqlite"}, "sqlite"},

		// CanonicalDriver calls it "sqlserver"; the old switch said "mssql".
		// Both reach the same placeholder style, and this pins which one the
		// builder now sees so a future reader is not left guessing.
		{"mssql", storage.Source{ID: "x", Type: "mssql"}, "sqlserver"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := lookupDialect(t.Context(), reg, tc.src); got != tc.want {
				t.Errorf("lookupDialect(%s) = %q, want %q", tc.src.Type, got, tc.want)
			}
		})
	}
}

// An unresolvable delegate must not become a worse guess than the one the code
// made before. It keeps the wrapper's own type, which is what it always did.
func TestLookupDialectFallsBackWhenTheDelegateIsUnknown(t *testing.T) {
	reg := &dialectFakeRegistry{sources: map[string]storage.Source{}}
	src := storage.Source{ID: "b", Type: "batch_sql", Config: hermod.StringMap{"source_id": "gone"}}
	if got := lookupDialect(t.Context(), reg, src); got != "batch_sql" {
		t.Errorf("lookupDialect = %q, want %q", got, "batch_sql")
	}
}

// A registry that cannot answer at all is the case every existing fake in this
// package is. It must not panic and must not change their behaviour.
func TestLookupDialectToleratesARegistryThatCannotResolve(t *testing.T) {
	src := storage.Source{ID: "b", Type: "batch_sql", Config: hermod.StringMap{"source_id": "pg1"}}
	if got := lookupDialect(context.Background(), struct{}{}, src); got != "batch_sql" {
		t.Errorf("lookupDialect = %q, want %q", got, "batch_sql")
	}
	if got := lookupDialect(context.Background(), nil, storage.Source{Type: "postgres"}); got != "pgx" {
		t.Errorf("lookupDialect = %q, want %q", got, "pgx")
	}
}
