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

// etcdEndpoints returns the endpoints to test against, or ends the test.
//
// Skipping is right when someone runs the suite locally without a server. It is
// wrong in CI, where an etcd service is configured precisely so that this runs,
// and where a skip is indistinguishable in the log from nine passing clauses —
// which is the state this contract spent its whole life in. So a missing
// endpoint is a failure there, and anyone who removes the service finds out
// from a red build rather than from a green one that covers less than it says.
func etcdEndpoints(t *testing.T) string {
	t.Helper()
	endpoints := os.Getenv("ETCD_ENDPOINTS")
	if endpoints != "" && os.Getenv("HERMOD_INTEGRATION") == "1" {
		return endpoints
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Fatalf("HERMOD_INTEGRATION=%q ETCD_ENDPOINTS=%q in CI, where an etcd service "+
			"is configured for this test; the contract would skip and report success "+
			"for clauses it never ran",
			os.Getenv("HERMOD_INTEGRATION"), endpoints)
	}
	t.Skip("integration: set HERMOD_INTEGRATION=1 and ETCD_ENDPOINTS to run")
	return ""
}

// The etcd store against a real server.
//
// This is the third of three, and the last one whose behaviour was taken on
// trust. The contract's subtlest clause — a missing key returns no value and no
// error — had already caused a bug through a fake that got it wrong, so
// verifying it by reading the code was never going to be enough.
//
// etcd is a CI service now, so this runs on every pull request. It did not
// when the file was written, and the note here said so — which meant the
// contract was three implementations on paper and two in practice, with the
// third reported as nine passing clauses it had never executed. The skip below
// is kept for running the suite locally without a server.
//
//	HERMOD_INTEGRATION=1 ETCD_ENDPOINTS=127.0.0.1:2379 \
//	go test -tags=integration ./pkg/infra/state/
func TestEtcdStateStoreContract(t *testing.T) {
	endpoints := etcdEndpoints(t)

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

// The contract above builds the store directly. This one goes through the
// factory, which is the path an operator's configuration actually takes, and
// they are not the same path: the harness splits ETCD_ENDPOINTS on commas,
// while the factory wrapped a single Address in a one-element slice.
//
// etcd is a quorum. Being able to name only one member of it means the state
// store is unavailable whenever that member is, which removes the reason to
// have chosen etcd in the first place.
func TestEtcdIsConfigurableWithMoreThanOneEndpoint(t *testing.T) {
	endpoints := etcdEndpoints(t)

	// A live member followed by one that is not listening, which is the whole
	// point of naming more than one. A client that parsed the list reaches the
	// first; a client handed the list as a single endpoint reaches nothing.
	live := strings.Split(endpoints, ",")[0]
	address := live + ",127.0.0.1:1"

	prefix := fmt.Sprintf("hermod-test/%d/", time.Now().UnixNano())
	store, err := NewStateStore(Config{Type: "etcd", Address: address, Prefix: prefix})
	if err != nil {
		t.Fatalf("configuring etcd with %q failed at construction: %v", address, err)
	}

	ctx := context.Background()
	if err := store.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatalf("configuring etcd with two endpoints (%q) leaves the store unusable: %v\n"+
			"the factory passes Address as one endpoint, so naming a quorum names "+
			"a host that does not exist", address, err)
	}
	got, err := store.Get(ctx, "k")
	if err != nil {
		t.Fatalf("reading back through a two-endpoint configuration: %v", err)
	}
	if string(got) != "v" {
		t.Errorf("read back %q, want %q", got, "v")
	}
}
