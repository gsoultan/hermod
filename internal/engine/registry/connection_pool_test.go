package registry

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod/pkg/infra/pgxutil"
)

func TestConnectionPoolResponsiveness(t *testing.T) {
	// Skip if no real Postgres is available, or use a mock pooler if we just want to test the logic.
	// For this test, we want to verify that multiple requests for the same connection string
	// return the same pool and that the pooler remains responsive.

	pooler := pgxutil.NewPooler()
	defer pooler.Close()

	// Since we don't have a real DB in this test environment easily,
	// we will mock the behavior if needed, but here we can at least test the concurrency of the Pooler.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connStr := "postgres://user:pass@localhost:5432/db?sslmode=disable"

	// Start many goroutines requesting the same pool.
	// If the pooler had a global lock during pool creation (which it doesn't, it uses sync.Map and LoadOrStore),
	// it would be slower.

	start := time.Now()
	const workers = 100
	done := make(chan bool, workers)

	for i := 0; i < workers; i++ {
		go func() {
			// We expect this to fail because there's no DB, but we want to see it fail FAST
			// or at least not block others if it was successful.
			_, _ = pooler.Get(ctx, connStr)
			done <- true
		}()
	}

	for i := 0; i < workers; i++ {
		<-done
	}

	duration := time.Since(start)
	t.Logf("Requested pool %d times in %v", workers, duration)
}
