package source

import "testing"

// TestDefaultSourceBuffer guards the lightweight memory budget: the per-source
// channel must stay shallow so connectors apply backpressure instead of
// hoarding messages in RAM. A regression that bumps this back to the old
// 1000/1024 depth would silently inflate the resident set.
func TestDefaultSourceBuffer(t *testing.T) {
	if DefaultSourceBuffer <= 0 {
		t.Fatalf("DefaultSourceBuffer must be positive, got %d", DefaultSourceBuffer)
	}
	if DefaultSourceBuffer > 128 {
		t.Fatalf("DefaultSourceBuffer = %d; expected a small bounded buffer (<=128)", DefaultSourceBuffer)
	}
}
