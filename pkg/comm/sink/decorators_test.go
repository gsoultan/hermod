package sink

import (
	"context"
	"testing"

	"github.com/user/hermod"
)

type mockSink struct {
	hermod.Sink
}

func (m *mockSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	return []string{"db1"}, nil
}

func (m *mockSink) DiscoverTables(ctx context.Context) ([]string, error) {
	return []string{"table1"}, nil
}

func (m *mockSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	return []hermod.ColumnInfo{{Name: "col1"}}, nil
}

func TestDecorators_Discovery(t *testing.T) {
	m := &mockSink{}

	t.Run("TracingSink", func(t *testing.T) {
		ts := NewTracingSink(m, "test")

		d, ok := any(ts).(hermod.Discoverer)
		if !ok {
			t.Fatal("TracingSink does not implement Discoverer")
		}

		dbs, _ := d.DiscoverDatabases(context.Background())
		if len(dbs) == 0 || dbs[0] != "db1" {
			t.Errorf("DiscoverDatabases failed: %v", dbs)
		}

		cd, ok := any(ts).(hermod.ColumnDiscoverer)
		if !ok {
			t.Fatal("TracingSink does not implement ColumnDiscoverer")
		}

		cols, _ := cd.DiscoverColumns(context.Background(), "table1")
		if len(cols) == 0 || cols[0].Name != "col1" {
			t.Errorf("DiscoverColumns failed: %v", cols)
		}
	})

	t.Run("RetrySink", func(t *testing.T) {
		rs := NewRetrySink(m, 3, 0, nil)

		d, ok := any(rs).(hermod.Discoverer)
		if !ok {
			t.Fatal("RetrySink does not implement Discoverer")
		}

		dbs, _ := d.DiscoverDatabases(context.Background())
		if len(dbs) == 0 || dbs[0] != "db1" {
			t.Errorf("DiscoverDatabases failed: %v", dbs)
		}
	})
}
