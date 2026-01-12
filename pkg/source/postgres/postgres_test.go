package postgres

import (
	"context"
	"testing"
	"time"
)

func TestPostgresSource_Read(t *testing.T) {
	// Skip test if no postgres is running
	t.Skip("Skipping test that requires a running Postgres instance")
	s := NewPostgresSource("postgres://user:pass@localhost:5432/db", "test_slot", "test_pub", nil)
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read from PostgresSource: %v", err)
	}
}
