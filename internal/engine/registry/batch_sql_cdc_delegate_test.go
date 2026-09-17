package registry

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// A batch_sql source holds no connection of its own: it names another source in
// `source_id` and borrows that source's database
// (Registry.GetOrOpenDB resolves the delegate at registry.go:801). What it then
// does with it is run whole queries on a cron.
//
// That is the same demand db_lookup makes, for the same reason -- a scheduled
// full-table query does not belong on a database already paying for logical
// replication -- with one extra hazard of its own: if the delegate is also a
// CDC source node in a workflow, every row arrives twice, once streamed and
// once batched.
type batchSQLDelegateStorage struct {
	testutil.BaseMockStorage
	sources map[string]storage.Source
}

func (m *batchSQLDelegateStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	src, ok := m.sources[id]
	if !ok {
		return storage.Source{}, fmt.Errorf("no source %q", id)
	}
	return src, nil
}

func newBatchSQLRegistry(t *testing.T, delegate storage.Source) *Registry {
	t.Helper()
	return NewRegistry(&batchSQLDelegateStorage{
		sources: map[string]storage.Source{"delegate": delegate},
	})
}

func batchSQLConfig() factory.SourceConfig {
	return factory.SourceConfig{
		ID:   "batch-1",
		Type: "batch_sql",
		Config: hermod.StringMap{
			"source_id": "delegate",
			"cron":      "*/5 * * * *",
			"queries":   `["SELECT id FROM orders"]`,
		},
	}
}

func isCDCDelegateRefusal(err error) bool {
	return err != nil && strings.Contains(err.Error(), "non-CDC source")
}

func TestBatchSQLRejectsACDCDelegate(t *testing.T) {
	cases := []struct {
		name     string
		delegate storage.Source
		reject   bool
	}{
		{
			name:     "CDC switched off explicitly is the shape a batch delegate is meant to have",
			delegate: storage.Source{ID: "delegate", Name: "reporting", Type: "postgres", Config: hermod.StringMap{"use_cdc": "false"}},
			reject:   false,
		},
		{
			name:     "CDC switched on is refused",
			delegate: storage.Source{ID: "delegate", Name: "orders", Type: "postgres", Config: hermod.StringMap{"use_cdc": "true"}},
			reject:   true,
		},
		{
			name:     "no use_cdc key means CDC is on, the same reading the factory uses",
			delegate: storage.Source{ID: "delegate", Name: "orders", Type: "postgres"},
			reject:   true,
		},
		{
			name:     "SQL Server is the documented exception",
			delegate: storage.Source{ID: "delegate", Name: "erp", Type: "mssql", Config: hermod.StringMap{"use_cdc": "true"}},
			reject:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newBatchSQLRegistry(t, tc.delegate)

			_, err := r.createSource(t.Context(), batchSQLConfig())
			switch {
			case tc.reject && !isCDCDelegateRefusal(err):
				t.Fatalf("a batch_sql source was built on a CDC delegate; err = %v", err)
			case !tc.reject && err != nil:
				t.Fatalf("a non-CDC delegate was refused: %v", err)
			}
		})
	}
}

// createSourceInternal is the second constructor -- the engine reaches
// batch_sql through both -- so a rule that only one of them applies is a rule
// that half the callers skip.
func TestBatchSQLRejectsACDCDelegateOnBothConstructors(t *testing.T) {
	r := newBatchSQLRegistry(t, storage.Source{
		ID: "delegate", Name: "orders", Type: "postgres",
		Config: hermod.StringMap{"use_cdc": "true"},
	})

	if _, err := r.createSourceInternal(t.Context(), batchSQLConfig()); !isCDCDelegateRefusal(err) {
		t.Fatalf("createSourceInternal built a batch_sql source on a CDC delegate; err = %v", err)
	}
}

// A delegate that cannot be resolved fails the source, but as itself: reporting
// it as "CDC is on" would send the operator to a switch that is not the
// problem. Waving it through instead would be worse — a transient storage error
// during a restart would build exactly the source this check exists to refuse.
func TestBatchSQLMissingDelegateFailsAsItself(t *testing.T) {
	r := newBatchSQLRegistry(t, storage.Source{ID: "delegate", Type: "postgres"})

	cfg := batchSQLConfig()
	cfg.Config["source_id"] = "does-not-exist"

	_, err := r.createSource(t.Context(), cfg)
	if err == nil {
		t.Fatal("a batch_sql source was built on a delegate that does not exist")
	}
	if isCDCDelegateRefusal(err) {
		t.Fatalf("a missing delegate was reported as a CDC delegate: %v", err)
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("the error does not name the delegate it could not resolve: %v", err)
	}
}
