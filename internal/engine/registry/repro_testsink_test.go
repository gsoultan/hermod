package registry

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod/internal/factory"
)

func TestReproTestSinkPostgres(t *testing.T) {
	r := NewRegistry(nil)
	cfg := factory.SinkConfig{
		Type: "postgres",
		Config: map[string]string{
			"host":     "localhost",
			"port":     "5432",
			"user":     "postgres",
			"password": "postgres",
			"dbname":   "postgres",
			"table":    "t",
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	start := time.Now()
	err := r.TestSink(ctx, cfg)
	t.Logf("elapsed=%v err=%v", time.Since(start), err)
}
