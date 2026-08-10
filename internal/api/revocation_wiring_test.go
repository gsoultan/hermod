package api

import (
	"testing"
)

// TestNewServerStartsSessionRevocation. The revoker is only half a feature if
// nothing refreshes it: revocations never reach other instances and the list
// never shrinks. This asserts the real construction path starts it, because
// that is the part no unit test of the Revoker itself can catch.
func TestNewServerStartsSessionRevocation(t *testing.T) {
	s := NewServer(nil, nil, nil, "", nil)

	if !s.Handler.RevocationRefreshRunning() {
		t.Fatal("NewServer did not start the session revocation refresher; " +
			"revocations would never replicate and the list would never be pruned")
	}

	s.Stop()
	if s.Handler.RevocationRefreshRunning() {
		t.Error("Stop left the revocation refresher running")
	}
}
