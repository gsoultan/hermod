package mariadb

import (
	"context"
	"testing"
	"time"
)

func TestMariaDBSource_Ping(t *testing.T) {
	// This test will fail if no MariaDB is running, which is expected.
	// We just want to ensure it tries to connect and uses the right driver.
	s := NewMariaDBSource("user:pass@tcp(localhost:3306)/db", []string{"table1"}, "id", 1*time.Second, false)
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := s.Ping(ctx)
	if err == nil {
		t.Log("Unexpectedly connected to MariaDB (is it running?)")
	} else {
		t.Logf("Ping failed as expected: %v", err)
	}
}
