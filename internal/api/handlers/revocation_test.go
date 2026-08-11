package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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
	gets   int
}

func newMemStateStore() *memStateStore { return &memStateStore{kv: map[string][]byte{}} }

func (m *memStateStore) Get(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
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

func (m *memStateStore) getCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gets
}

func (m *memStateStore) resetCounts() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets = 0
}

func (m *memStateStore) has(k string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.kv[k]
	return ok
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

// TestRefreshDoesNotRereadWhatItAlreadyHolds. Refresh runs every ten seconds for
// the life of the process, so its cost has to be a function of what changed, not
// of everything ever revoked. Re-reading the whole list each tick makes an idle
// system do more store I/O every day it stays up — the kind of regression that
// looks fine in review and shows up as a store under load a month later.
func TestRefreshDoesNotRereadWhatItAlreadyHolds(t *testing.T) {
	store := newMemStateStore()
	first := NewRevoker(store)
	for _, jti := range []string{"a", "b", "c", "d", "e"} {
		if err := first.Revoke(context.Background(), jti, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
	}

	second := NewRevoker(store)
	if err := second.Refresh(context.Background()); err != nil { // cold: must read everything
		t.Fatalf("Refresh: %v", err)
	}
	if !second.IsRevoked(SessionClaims{TokenID: "e"}) {
		t.Fatal("the cold refresh did not pick up the revocations")
	}

	store.resetCounts()
	for range 3 {
		if err := second.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	}

	// Three ticks that learned nothing new: three index reads, and nothing else.
	if got := store.getCount(); got > 3 {
		t.Errorf("three idle refreshes cost %d store reads, want 3 (one index read each); "+
			"the refresher re-reads every entry it already holds, so an idle system's "+
			"store load grows with every revocation ever made", got)
	}
}

// TestRefreshStillPicksUpNewEntries is the other half: skipping what is already
// held must not mean skipping what is new.
func TestRefreshStillPicksUpNewEntries(t *testing.T) {
	store := newMemStateStore()
	first := NewRevoker(store)
	_ = first.Revoke(context.Background(), "a", time.Now().Add(time.Hour))

	second := NewRevoker(store)
	if err := second.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	_ = first.Revoke(context.Background(), "b", time.Now().Add(time.Hour))
	if err := second.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if !second.IsRevoked(SessionClaims{TokenID: "b"}) {
		t.Error("a revocation made after the first refresh was never picked up")
	}
}

// TestExpiredRevocationsLeaveTheStore. Prune bounds the in-memory maps, but the
// store had no equivalent: every revocation ever made stayed there forever,
// along with its entry in the index. The list is only bounded because a revoked
// token eventually expires — that has to apply to the durable copy too.
func TestExpiredRevocationsLeaveTheStore(t *testing.T) {
	store := newMemStateStore()
	r := NewRevoker(store)

	if err := r.Revoke(context.Background(), "expired", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := r.Revoke(context.Background(), "live", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if err := r.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if store.has(revocationKeyPrefix + "expired") {
		t.Error("an expired revocation is still in the store; nothing ever removes it")
	}
	if !store.has(revocationKeyPrefix + "live") {
		t.Error("a live revocation was removed from the store")
	}

	raw, err := store.Get(context.Background(), revocationIndexKey)
	if err != nil {
		t.Fatalf("reading the index: %v", err)
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("the index is not a list: %v", err)
	}
	if slices.Contains(keys, revocationKeyPrefix+"expired") {
		t.Error("the index still lists an expired revocation, so every refresh keeps looking it up")
	}
	if !slices.Contains(keys, revocationKeyPrefix+"live") {
		t.Error("pruning dropped a live revocation from the index")
	}
}

// TestTheListIsBounded. Every entry expires on its own, so the list is bounded
// in time — but not in rate. A logged-in user can loop login/logout as fast as
// the API answers, and each cycle adds an entry that lives for the token's full
// hour. Time alone is not a bound when the input rate is attacker-controlled.
//
// Eviction is a real cost: a dropped revocation means that session works again.
// So the entries evicted are the ones closest to expiring anyway — the least
// revocation given up per unit of memory reclaimed.
func TestTheListIsBounded(t *testing.T) {
	r := NewRevoker(nil)

	// Expiries increase with i, so the earliest entries are the cheapest to drop.
	for i := range maxTrackedRevocations + 5000 {
		if err := r.Revoke(context.Background(), fmt.Sprintf("jti-%d", i),
			time.Now().Add(time.Hour+time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
	}

	if got := r.Size(); got > maxTrackedRevocations {
		t.Errorf("the list holds %d entries with a cap of %d; an authenticated user can "+
			"grow it without limit by looping login and logout", got, maxTrackedRevocations)
	}

	// The newest revocation is the one most likely to still matter, and must
	// never be the one sacrificed.
	newest := fmt.Sprintf("jti-%d", maxTrackedRevocations+4999)
	if !r.IsRevoked(SessionClaims{TokenID: newest}) {
		t.Error("the most recent revocation was evicted; eviction must give up the " +
			"entries closest to expiring, not the ones most likely to still matter")
	}
}

// TestTheBoundPrefersExpiredEntries: reclaiming space must start with entries
// that have already stopped mattering, before giving up a live revocation.
func TestTheBoundPrefersExpiredEntries(t *testing.T) {
	r := NewRevoker(nil)

	r.mu.Lock()
	for i := range maxTrackedRevocations {
		r.sessions[fmt.Sprintf("dead-%d", i)] = time.Now().Add(-time.Minute)
	}
	r.mu.Unlock()

	if err := r.Revoke(context.Background(), "live", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if !r.IsRevoked(SessionClaims{TokenID: "live"}) {
		t.Error("a new revocation was dropped to make room for entries that had already expired")
	}
	if r.Size() > maxTrackedRevocations {
		t.Errorf("the list holds %d entries, over the %d cap", r.Size(), maxTrackedRevocations)
	}
}

// TestRevocationMetricsArePublished. The metric names are an operational
// contract — dashboards and alerts are written against them — so renaming one
// silently is a broken alert nobody notices until it fails to fire.
func TestRevocationMetricsArePublished(t *testing.T) {
	r := NewRevoker(nil)
	_ = r.Revoke(context.Background(), "jti-1", time.Now().Add(time.Hour))
	_ = r.RevokeUser(context.Background(), "u1")
	r.observe()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	want := map[string]float64{
		"hermod_revoked_sessions": 1,
		"hermod_revoked_users":    1,
	}
	seen := map[string]bool{}
	for _, f := range families {
		if _, ok := want[f.GetName()]; !ok {
			continue
		}
		seen[f.GetName()] = true
		if got := f.GetMetric()[0].GetGauge().GetValue(); got != want[f.GetName()] {
			t.Errorf("%s = %v, want %v", f.GetName(), got, want[f.GetName()])
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s is not published; an operator cannot tell whether pruning still runs", name)
		}
	}
}
