package oracle

import (
	"context"
	"testing"
	"time"
)

func TestOracleSource_Ping(t *testing.T) {
	s := NewOracleSource("oracle://user:pass@localhost:1521/xe", []string{"table1"}, "id", 1*time.Second, false)
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := s.Ping(ctx)
	if err == nil {
		t.Log("Unexpectedly connected to Oracle")
	} else {
		t.Logf("Ping failed as expected: %v", err)
	}
}
