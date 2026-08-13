//go:build integration

package state

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/user/hermod"
)

// The etcd store against a real server.
//
// This is the third of three, and the last one whose behaviour was taken on
// trust. The contract's subtlest clause — a missing key returns no value and no
// error — had already caused a bug through a fake that got it wrong, so
// verifying it by reading the code was never going to be enough.
//
// etcd is not in CI, so this skips unless an endpoint is provided. That is
// worth knowing rather than papering over: the suite covers three
// implementations, and CI runs two of them.
//
//	HERMOD_INTEGRATION=1 ETCD_ENDPOINTS=127.0.0.1:2379 \
//	go test -tags=integration ./pkg/infra/state/
func TestEtcdStateStoreContract(t *testing.T) {
	endpoints := os.Getenv("ETCD_ENDPOINTS")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || endpoints == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and ETCD_ENDPOINTS to run")
	}

	RunStoreContract(t, "etcd", func(t *testing.T) hermod.StateStore {
		t.Helper()
		// A prefix per test keeps runs from colliding on a shared cluster and
		// makes a leftover key traceable to what wrote it.
		prefix := fmt.Sprintf("hermod-test/%d/", time.Now().UnixNano())
		s, err := NewEtcdStateStore(strings.Split(endpoints, ","), prefix, 10*time.Second)
		if err != nil {
			t.Fatalf("connecting to etcd at %s: %v", endpoints, err)
		}
		// Deliberately a failure rather than a skip. Setting ETCD_ENDPOINTS is a
		// statement that a server should be there, and skipping on a connection
		// error is how a suite reports success while testing nothing — the exact
		// failure this whole exercise exists to stop.
		if _, err := s.Get(context.Background(), "reachability-probe"); err != nil {
			t.Fatalf("etcd was named in ETCD_ENDPOINTS (%s) but is not reachable: %v", endpoints, err)
		}
		return s
	})
}
