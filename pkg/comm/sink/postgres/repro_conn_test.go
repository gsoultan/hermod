package postgres

import (
	"context"
	"testing"
	"time"
)

func TestReproPingTimeout(t *testing.T) {
	dsns := []string{
		"postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
		// Unreachable: non-routable IP that silently drops packets (firewall-like).
		"postgres://postgres:postgres@10.255.255.1:5432/postgres?sslmode=disable",
		// Closed port on localhost (connection refused, should fail fast).
		"postgres://postgres:postgres@localhost:1/postgres?sslmode=disable",
	}
	for _, dsn := range dsns {
		t.Run(dsn, func(t *testing.T) {
			s := NewPostgresSink(dsn, "t", nil, false, "", "", "", "auto", false, false)
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			err := s.Ping(ctx)
			t.Logf("dsn=%s elapsed=%v err=%v", dsn, time.Since(start), err)
		})
	}
}
