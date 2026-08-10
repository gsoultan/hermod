package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Session revocation.
//
// The session is a stateless JWT: signature valid means accepted, so ending one
// early needs state somewhere. The obvious place is a lookup per request, and
// that is precisely what the auth middleware was built to avoid — it derives
// the user from the claims specifically so an authenticated call costs no I/O.
//
// So the state lives in memory and is *replicated* through the store rather than
// *read* from it. IsRevoked is a map lookup on the hot path; the store is how a
// revocation reaches other instances, on a background refresh.
//
// That buys immediacy on the instance that performed the revocation and a
// bounded delay elsewhere. The delay is the honest cost of not paying for a
// lookup per request, and it is stated rather than hidden.
//
// Two granularities, because they answer different questions:
//
//	Revoke(jti)      one session — "log this browser out".
//	RevokeUser(id)   every session a user holds — "the password changed", or
//	                 "this account is compromised". A per-session list cannot
//	                 express that without enumerating sessions nobody tracked.
// ---------------------------------------------------------------------------

// memStateStore is a hermod.StateStore for these tests.
type memStateStore struct {
	mu     sync.Mutex
	kv     map[string][]byte
	failOp error
}

func newMemStateStore() *memStateStore { return &memStateStore{kv: map[string][]byte{}} }

func (m *memStateStore) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failOp != nil {
		return nil, m.failOp
	}
	v, ok := m.kv[k]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}

func (m *memStateStore) Set(_ context.Context, k string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failOp != nil {
		return m.failOp
	}
	m.kv[k] = v
	return nil
}

func (m *memStateStore) Delete(_ context.Context, k string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kv, k)
	return nil
}

func (m *memStateStore) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.kv)
}

func TestRevokedSessionIsRejected(t *testing.T) {
	r := NewRevoker(nil)
	claims := SessionClaims{UserID: "u1", TokenID: "jti-1"}

	if r.IsRevoked(claims) {
		t.Fatal("a fresh session is already revoked")
	}
	if err := r.Revoke(context.Background(), "jti-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !r.IsRevoked(claims) {
		t.Error("the session was not revoked")
	}
}

// TestRevokingOneSessionLeavesOthers: revoking a browser must not log the user
// out everywhere. That is what RevokeUser is for, and conflating them would
// make logout a far blunter instrument than anyone expects.
func TestRevokingOneSessionLeavesOthers(t *testing.T) {
	r := NewRevoker(nil)

	laptop := SessionClaims{UserID: "u1", TokenID: "jti-laptop"}
	phone := SessionClaims{UserID: "u1", TokenID: "jti-phone"}

	_ = r.Revoke(context.Background(), "jti-laptop", time.Now().Add(time.Hour))

	if !r.IsRevoked(laptop) {
		t.Error("the revoked session still works")
	}
	if r.IsRevoked(phone) {
		t.Error("revoking one session logged the user out of another")
	}
}

// TestRevokeUserEndsEverySession is the case a per-session list cannot express:
// a password change has to invalidate sessions nobody enumerated.
func TestRevokeUserEndsEverySession(t *testing.T) {
	r := NewRevoker(nil)
	started := time.Now().Add(-time.Minute)

	laptop := SessionClaims{UserID: "u1", TokenID: "a", SessionStart: started}
	phone := SessionClaims{UserID: "u1", TokenID: "b", SessionStart: started}
	someoneElse := SessionClaims{UserID: "u2", TokenID: "c", SessionStart: started}

	if err := r.RevokeUser(context.Background(), "u1"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}

	if !r.IsRevoked(laptop) || !r.IsRevoked(phone) {
		t.Error("RevokeUser did not end every session for that user")
	}
	if r.IsRevoked(someoneElse) {
		t.Error("RevokeUser ended another user's session")
	}
}

// TestRevokeUserDoesNotAffectLaterLogins: after a password change the user has
// to be able to log in again, and the new session must survive.
func TestRevokeUserDoesNotAffectLaterLogins(t *testing.T) {
	r := NewRevoker(nil)

	if err := r.RevokeUser(context.Background(), "u1"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}

	// A session that began after the revocation.
	fresh := SessionClaims{UserID: "u1", TokenID: "new", SessionStart: time.Now().Add(time.Second)}
	if r.IsRevoked(fresh) {
		t.Error("a session created after the revocation was rejected; the user could never log back in")
	}
}

// TestRevocationsAreReplicatedThroughTheStore is what makes this work across
// more than one instance.
func TestRevocationsAreReplicatedThroughTheStore(t *testing.T) {
	store := newMemStateStore()
	first := NewRevoker(store)

	if err := first.Revoke(context.Background(), "jti-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if store.len() == 0 {
		t.Fatal("the revocation was not written to the store, so no other instance can see it")
	}

	// A second instance starts cold and must pick it up.
	second := NewRevoker(store)
	if err := second.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !second.IsRevoked(SessionClaims{UserID: "u1", TokenID: "jti-1"}) {
		t.Error("a second instance did not see the revocation after refreshing")
	}
}

// TestIsRevokedDoesNoIO is the constraint the whole design turns on. The
// middleware derives the user from claims precisely so an authenticated request
// costs no I/O; a lookup here would undo that on every call.
func TestIsRevokedDoesNoIO(t *testing.T) {
	store := newMemStateStore()
	r := NewRevoker(store)
	_ = r.Revoke(context.Background(), "jti-1", time.Now().Add(time.Hour))

	// Any store access from here on is a failure, not a fallback.
	store.mu.Lock()
	store.failOp = errors.New("the hot path must not touch the store")
	store.mu.Unlock()

	if !r.IsRevoked(SessionClaims{UserID: "u1", TokenID: "jti-1"}) {
		t.Error("IsRevoked consulted the store instead of memory")
	}
	if r.IsRevoked(SessionClaims{UserID: "u1", TokenID: "other"}) {
		t.Error("IsRevoked reported a live session as revoked")
	}
}

// TestExpiredEntriesArePruned: the list is bounded by the fact that a revoked
// token stops mattering once it would have expired anyway. Without pruning it
// grows for the life of the process.
func TestExpiredEntriesArePruned(t *testing.T) {
	r := NewRevoker(nil)

	_ = r.Revoke(context.Background(), "old", time.Now().Add(-time.Minute))
	_ = r.Revoke(context.Background(), "current", time.Now().Add(time.Hour))

	r.Prune()

	if r.Size() != 1 {
		t.Errorf("after pruning the list holds %d entries, want 1", r.Size())
	}
	if r.IsRevoked(SessionClaims{TokenID: "current"}) != true {
		t.Error("pruning dropped a live revocation")
	}
}

// TestWorksWithoutAStore: revocation must still function on a single instance
// with no distributed state configured, which is the default deployment.
// Refusing to revoke at all would be worse than revoking locally.
func TestWorksWithoutAStore(t *testing.T) {
	r := NewRevoker(nil)

	if err := r.Revoke(context.Background(), "jti-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke with no store: %v", err)
	}
	if !r.IsRevoked(SessionClaims{TokenID: "jti-1"}) {
		t.Error("revocation did not take effect without a store")
	}
}

// TestStoreFailureDoesNotLoseTheLocalRevocation: if the store is unreachable the
// revocation must still hold on this instance. Reporting the error matters —
// the operator needs to know it did not propagate — but dropping it locally
// would leave the session the user just ended still working.
func TestStoreFailureDoesNotLoseTheLocalRevocation(t *testing.T) {
	store := newMemStateStore()
	store.failOp = errors.New("store unavailable")
	r := NewRevoker(store)

	err := r.Revoke(context.Background(), "jti-1", time.Now().Add(time.Hour))
	if err == nil {
		t.Error("a failed replication was reported as success; the operator would not know it did not propagate")
	}
	if !r.IsRevoked(SessionClaims{TokenID: "jti-1"}) {
		t.Error("the revocation was lost locally because the store was unreachable")
	}
}

// TestSessionWithoutATokenIDIsNotRevocable documents the upgrade path: tokens
// issued before this change carry no jti. They cannot be revoked individually —
// there is nothing to name them by — but RevokeUser still reaches them through
// the session-start claim.
func TestSessionWithoutATokenIDIsNotRevocable(t *testing.T) {
	r := NewRevoker(nil)
	old := SessionClaims{UserID: "u1", SessionStart: time.Now().Add(-time.Minute)}

	if r.IsRevoked(old) {
		t.Error("a pre-existing session was treated as revoked, logging everyone out on deploy")
	}

	if err := r.RevokeUser(context.Background(), "u1"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}
	if !r.IsRevoked(old) {
		t.Error("RevokeUser could not reach a session that predates token IDs")
	}
}

// TestStartRefreshingPullsAndPrunes covers the loop rather than its parts. Both
// halves are only correct if something actually runs them on a schedule: without
// the refresh a second instance never learns of a revocation, and without the
// prune the list grows for the life of the process.
func TestStartRefreshingPullsAndPrunes(t *testing.T) {
	store := newMemStateStore()
	first := NewRevoker(store)
	if err := first.Revoke(context.Background(), "jti-remote", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	second := NewRevoker(store)
	// An entry that is already past its expiry: the refresher must drop it.
	second.mu.Lock()
	second.sessions["stale"] = time.Now().Add(-time.Minute)
	second.mu.Unlock()

	stop := second.StartRefreshing(context.Background(), 10*time.Millisecond)
	defer stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if second.IsRevoked(SessionClaims{TokenID: "jti-remote"}) && second.Size() == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("the refresher did not converge: revoked=%v size=%d, want revoked=true size=1",
		second.IsRevoked(SessionClaims{TokenID: "jti-remote"}), second.Size())
}

// TestStopRefreshingIsSynchronous: stop must wait for the goroutine, or a test
// binary reports a leak and a shutdown races with the store closing underneath.
func TestStopRefreshingIsSynchronous(t *testing.T) {
	r := NewRevoker(newMemStateStore())
	stop := r.StartRefreshing(context.Background(), time.Millisecond)

	done := make(chan struct{})
	go func() { stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return; the refresher goroutine outlives shutdown")
	}
}

// TestRefresherHasAnExplicitLifecycle. A revoker that nobody refreshes only ever
// holds what this instance revoked, and never prunes — so the multi-instance
// story silently does not work and the map grows for the life of the process.
// Starting it from the lazy getter would spawn a goroutine per test Handler, so
// the lifecycle is explicit and owned by whoever built the Handler.
func TestRefresherHasAnExplicitLifecycle(t *testing.T) {
	h := &Handler{} // zero value: no registry, no store — the shape tests use
	if h.RevocationRefreshRunning() {
		t.Error("a handler nobody started is already refreshing")
	}

	h.StartSessionRevocation(context.Background())
	if !h.RevocationRefreshRunning() {
		t.Error("StartSessionRevocation did not start the refresher")
	}

	h.StartSessionRevocation(context.Background()) // idempotent
	h.StopSessionRevocation()
	if h.RevocationRefreshRunning() {
		t.Error("StopSessionRevocation left the refresher running")
	}
	h.StopSessionRevocation() // safe twice; shutdown paths double-call
}

// TestRevokeUserToleratesClaimRounding pins the skew that stops the verdict
// turning on floating-point rounding.
//
// The session-start claim is a JSON number, so it cannot carry an exact instant.
// Comparing it against a nanosecond cutoff made a login moments after a password
// change land fractionally before that cutoff and get rejected — the user locked
// out of the account they had just reset. It failed roughly four runs in five,
// which is exactly the kind of test that gets marked flaky and muted.
//
// The rule the skew buys: sessions meaningfully older than the change are ended;
// one that began within a millisecond of it survives.
func TestRevokeUserToleratesClaimRounding(t *testing.T) {
	r := NewRevoker(nil)
	if err := r.RevokeUser(context.Background(), "u1"); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}

	r.mu.RLock()
	cutoff := r.users["u1"]
	r.mu.RUnlock()

	// A login that the claim's precision placed a hair before the cutoff.
	justAfter := SessionClaims{UserID: "u1", TokenID: "new", SessionStart: cutoff.Add(-time.Microsecond)}
	if r.IsRevoked(justAfter) {
		t.Error("a session at the cutoff was revoked; rounding decides the verdict again")
	}

	// A session that genuinely predates the change must still be ended, or the
	// tolerance has quietly become a hole.
	older := SessionClaims{UserID: "u1", TokenID: "old", SessionStart: cutoff.Add(-time.Second)}
	if !r.IsRevoked(older) {
		t.Error("a session from before the password change survived it")
	}
}
