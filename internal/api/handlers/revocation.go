package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/config"
)

// Session revocation.
//
// The session is a stateless JWT: a valid signature means accepted, so ending
// one early needs state somewhere. The obvious place is a lookup per request —
// and that is exactly what the auth middleware was built to avoid, deriving the
// user from the claims specifically so an authenticated call costs no I/O.
//
// So the state lives in memory and is *replicated* through the store rather
// than *read* from it. IsRevoked is a map lookup; the store is how a revocation
// reaches other instances, on a background refresh.
//
// The cost of that choice, stated plainly: a revocation is immediate on the
// instance that performed it and takes up to RefreshInterval to reach the
// others. Paying for a store lookup on every authenticated request would close
// that window and put I/O back on the hot path. This is the trade, and it is
// the reason the window is short rather than absent.
const (
	// revocationKeyPrefix scopes single-session revocations in the store.
	revocationKeyPrefix = "revoked-session/"

	// userRevocationKeyPrefix scopes whole-user revocations.
	userRevocationKeyPrefix = "revoked-user/"

	// revocationIndexKey lists live revocation keys, because hermod.StateStore
	// cannot enumerate.
	revocationIndexKey = "revoked-index"

	// DefaultRefreshInterval bounds how long a revocation takes to reach other
	// instances. Short enough that a compromised session is not usable for long,
	// long enough that the store is not hammered.
	DefaultRefreshInterval = 10 * time.Second

	// revocationSkew is how much earlier than the cutoff a session must have
	// started before RevokeUser counts it as one of the sessions being ended.
	//
	// The session-start claim is a JWT NumericDate — a JSON number — so it
	// cannot represent an instant exactly, and comparing it against a
	// nanosecond-precision cutoff makes the verdict turn on rounding. Without
	// this, a login moments after a password change can land a few hundred
	// nanoseconds *before* the cutoff the change installed and be rejected,
	// locking the user out of the account they just reset.
	//
	// A millisecond is far above that error and far below the round trip any
	// real login takes, so it removes the ambiguity without opening a window
	// worth attacking: it means a session that began in the millisecond before
	// a password change survives it.
	revocationSkew = time.Millisecond

	// maxTrackedRevocations caps how many single-session revocations are held.
	//
	// Every entry expires on its own, which bounds the list in time but not in
	// rate: a logged-in user can loop login/logout as fast as the API answers,
	// and each cycle adds an entry that lives for the token's full hour. At
	// roughly a hundred bytes an entry this ceiling is a few megabytes, which is
	// the point — a bound that is never reached in normal use and cannot be
	// walked past under abuse.
	maxTrackedRevocations = 100_000

	// revocationLowWater is how far below the cap eviction reclaims, so the
	// sort it costs is amortised over many insertions instead of running on
	// every one once the list is full.
	revocationLowWater = maxTrackedRevocations * 9 / 10
)

// Revoker decides whether a session has been ended before its token expires.
type Revoker struct {
	// store replicates revocations between instances. Nil is supported: a
	// single instance with no distributed state still revokes locally, which is
	// the default deployment and much better than refusing to revoke at all.
	store hermod.StateStore

	mu sync.RWMutex
	// sessions maps token ID to the moment the entry stops mattering, which is
	// when the token would have expired anyway. That is what bounds the list.
	sessions map[string]time.Time
	// users maps user ID to the moment every session of theirs was invalidated.
	// A session that began before it is revoked; one that began after is not,
	// so a user can log back in immediately after a password change.
	users map[string]time.Time
}

// NewRevoker builds a Revoker. store may be nil.
func NewRevoker(store hermod.StateStore) *Revoker {
	return &Revoker{
		store:    store,
		sessions: map[string]time.Time{},
		users:    map[string]time.Time{},
	}
}

// IsRevoked reports whether the session behind these claims has been ended.
//
// This is on the hot path for every authenticated request, so it does no I/O
// and takes only a read lock.
func (r *Revoker) IsRevoked(claims SessionClaims) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Whole-user revocation. Checked first because it also covers sessions
	// issued before token IDs existed, which have nothing to match on.
	if cutoff, ok := r.users[claims.UserID]; ok && claims.UserID != "" {
		// A session that began before the cutoff is gone. One that began after
		// is a fresh login and must survive, or the user could never get back in.
		if !claims.SessionStart.IsZero() && claims.SessionStart.Before(cutoff.Add(-revocationSkew)) {
			return true
		}
		// No session-start claim: it predates that too, so it is older than any
		// cutoff we could have set.
		if claims.SessionStart.IsZero() {
			return true
		}
	}

	if claims.TokenID == "" {
		// Nothing to name this session by. It is not individually revocable;
		// RevokeUser is the instrument that still reaches it.
		return false
	}
	expiry, revoked := r.sessions[claims.TokenID]
	if !revoked {
		return false
	}
	// A past-expiry entry is a leftover: the token is dead on its own terms.
	return time.Now().Before(expiry)
}

// Revoke ends a single session. expiresAt should be the token's own expiry, so
// the entry can be dropped once it stops mattering.
//
// The local revocation takes effect even when replication fails, and the error
// still reports the failure — the operator needs to know it did not propagate,
// but the session the user just ended must not keep working here.
func (r *Revoker) Revoke(ctx context.Context, tokenID string, expiresAt time.Time) error {
	if tokenID == "" {
		return errors.New("revoke: no token ID; this session cannot be revoked individually, use RevokeUser")
	}

	r.mu.Lock()
	r.sessions[tokenID] = expiresAt
	r.evictLocked()
	r.mu.Unlock()

	return r.replicate(ctx, revocationKeyPrefix+tokenID, expiresAt)
}

// RevokeUser ends every session a user currently holds, and no future one.
//
// This is the instrument for a password change or a compromised account: a
// per-session list cannot express it, because nobody enumerated the sessions.
func (r *Revoker) RevokeUser(ctx context.Context, userID string) error {
	if userID == "" {
		return errors.New("revoke user: no user ID")
	}

	cutoff := time.Now()
	r.mu.Lock()
	r.users[userID] = cutoff
	r.mu.Unlock()

	// Kept for a full maximum session age: past that, every session it could
	// have invalidated has expired on its own and the entry is dead weight.
	return r.replicate(ctx, userRevocationKeyPrefix+userID, cutoff.Add(maxSessionAge()))
}

// replicate writes an entry to the store so other instances see it.
func (r *Revoker) replicate(ctx context.Context, key string, expiresAt time.Time) error {
	if r.store == nil {
		// No distributed state configured. The revocation holds on this
		// instance, which is the whole deployment in the common case.
		return nil
	}

	payload, err := json.Marshal(map[string]any{"expires_at": expiresAt.Unix()})
	if err != nil {
		return err
	}
	if err := r.store.Set(ctx, key, payload); err != nil {
		return fmt.Errorf("revocation applied locally but not replicated; other instances will honour it "+
			"within %s only once the store is reachable: %w", DefaultRefreshInterval, err)
	}
	return r.addToIndex(ctx, key)
}

// addToIndex records the key so Refresh can find it. hermod.StateStore has no
// enumeration, so the index is how a cold instance learns what is revoked.
func (r *Revoker) addToIndex(ctx context.Context, key string) error {
	raw, err := r.store.Get(ctx, revocationIndexKey)
	keys := map[string]bool{}
	if err == nil && len(raw) > 0 {
		var list []string
		if err := json.Unmarshal(raw, &list); err == nil {
			for _, k := range list {
				keys[k] = true
			}
		}
	}
	if keys[key] {
		return nil
	}
	keys[key] = true

	list := make([]string, 0, len(keys))
	for k := range keys {
		list = append(list, k)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return r.store.Set(ctx, revocationIndexKey, data)
}

// Refresh pulls revocations recorded by other instances into memory, and
// removes entries the store no longer needs to hold. Call it periodically;
// StartRefreshing does that.
//
// Its cost is a function of what changed, not of everything ever revoked. An
// entry is immutable once written, so an entry already in memory is never read
// again — an idle refresh is a single read of the index no matter how long the
// process has been up.
func (r *Revoker) Refresh(ctx context.Context) error {
	if r.store == nil {
		return nil
	}

	raw, err := r.store.Get(ctx, revocationIndexKey)
	if err != nil {
		// A missing index and an unreachable store look the same through this
		// interface. Neither is dangerous here, unlike the 2PC log: this
		// instance keeps the revocations it already holds and picks up the rest
		// on the next tick, so the only cost is a delay that is already the
		// documented trade.
		return nil //nolint:nilerr // see above
	}
	if len(raw) == 0 {
		return nil
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return fmt.Errorf("revocation index is corrupt: %w", err)
	}

	now := time.Now()
	var expired []string

	for _, key := range keys {
		expiry, held := r.heldExpiry(key)
		if !held {
			data, err := r.store.Get(ctx, key)
			if err != nil || len(data) == 0 {
				continue
			}
			var entry struct {
				ExpiresAt int64 `json:"expires_at"`
			}
			if err := json.Unmarshal(data, &entry); err != nil {
				continue
			}
			expiry = time.Unix(entry.ExpiresAt, 0)
			r.hold(key, expiry)
		}
		if now.After(expiry) {
			expired = append(expired, key)
		}
	}

	if len(expired) > 0 {
		r.dropFromStore(ctx, expired)
	}
	return nil
}

// heldExpiry reports the store-side expiry of a key this instance already holds
// in memory, so Refresh can skip reading it again.
func (r *Revoker) heldExpiry(key string) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	switch {
	case strings.HasPrefix(key, revocationKeyPrefix):
		expiry, ok := r.sessions[strings.TrimPrefix(key, revocationKeyPrefix)]
		return expiry, ok
	case strings.HasPrefix(key, userRevocationKeyPrefix):
		// Memory holds the cutoff; the store holds it plus a max session age.
		cutoff, ok := r.users[strings.TrimPrefix(key, userRevocationKeyPrefix)]
		return cutoff.Add(maxSessionAge()), ok
	}
	return time.Time{}, false
}

// hold records an entry read from the store.
func (r *Revoker) hold(key string, expiry time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch {
	case strings.HasPrefix(key, revocationKeyPrefix):
		r.sessions[strings.TrimPrefix(key, revocationKeyPrefix)] = expiry
		r.evictLocked()
	case strings.HasPrefix(key, userRevocationKeyPrefix):
		// Recover the cutoff so sessions started after it still survive.
		r.users[strings.TrimPrefix(key, userRevocationKeyPrefix)] = expiry.Add(-maxSessionAge())
	}
}

// evictLocked keeps the session list under maxTrackedRevocations. Callers hold
// the write lock.
//
// Evicting a revocation means that session works again, so this gives up as
// little as it can: entries that have already expired first — they had stopped
// mattering anyway — and only then the live entries closest to expiring, which
// are the ones with the least remaining life to surrender.
//
// The users map is not capped. Its keys are account IDs, so it is bounded by the
// number of accounts rather than by request rate.
func (r *Revoker) evictLocked() {
	if len(r.sessions) <= maxTrackedRevocations {
		return
	}

	now := time.Now()
	for id, expiry := range r.sessions {
		if now.After(expiry) {
			delete(r.sessions, id)
		}
	}
	if len(r.sessions) <= maxTrackedRevocations {
		return
	}

	type entry struct {
		id     string
		expiry time.Time
	}
	live := make([]entry, 0, len(r.sessions))
	for id, expiry := range r.sessions {
		live = append(live, entry{id, expiry})
	}
	slices.SortFunc(live, func(a, b entry) int { return a.expiry.Compare(b.expiry) })

	dropped := len(live) - revocationLowWater
	for i := range dropped {
		delete(r.sessions, live[i].id)
	}
	revocationEvictionsTotal.Add(float64(dropped))
}

// dropFromStore removes entries whose token has expired anyway.
//
// Prune bounds what this instance holds in memory; without this the durable copy
// grew for the life of the deployment, and the index kept naming entries every
// cold start would then read.
func (r *Revoker) dropFromStore(ctx context.Context, expired []string) {
	for _, key := range expired {
		if err := r.store.Delete(ctx, key); err != nil {
			// Leave the index naming it and try again next tick. Rewriting the
			// index now would orphan the entry: nothing would ever look at it
			// again, and nothing would ever delete it.
			return
		}
	}

	// Re-read immediately before rewriting, so a revocation recorded since this
	// refresh began is not dropped from the index by the rewrite.
	//
	// hermod.StateStore offers no compare-and-swap, so this narrows the window
	// rather than closing it. What survives it is small and bounded: a
	// revocation written inside the gap loses its *index* entry, never the
	// revocation itself, so every instance that has already seen it still
	// honours it and only a cold-starting one would miss it.
	raw, err := r.store.Get(ctx, revocationIndexKey)
	if err != nil {
		return
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return
	}

	kept := make([]string, 0, len(keys))
	for _, k := range keys {
		if !slices.Contains(expired, k) {
			kept = append(kept, k)
		}
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return
	}
	_ = r.store.Set(ctx, revocationIndexKey, data)
}

// Prune drops entries whose token would have expired anyway. Without it the
// list grows for the life of the process.
func (r *Revoker) Prune() {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	for id, expiry := range r.sessions {
		if now.After(expiry) {
			delete(r.sessions, id)
		}
	}
	for id, cutoff := range r.users {
		if now.After(cutoff.Add(maxSessionAge())) {
			delete(r.users, id)
		}
	}
}

// Size reports how many single-session revocations are held. For metrics and
// tests; a number that only grows means Prune is not running.
func (r *Revoker) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// StartRefreshing keeps this instance in step with the others and prunes as it
// goes, returning a function that stops it and waits.
//
// interval <= 0 uses DefaultRefreshInterval.
func (r *Revoker) StartRefreshing(ctx context.Context, interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.Refresh(ctx)
				r.Prune()
				r.observe()
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// CurrentSessionClaims returns the validated claims of the request's session.
//
// The middleware has already parsed them to authenticate the request, but does
// not put them in the context — only the derived user. Logout needs the token
// ID and expiry, which the user does not carry, so it parses once more rather
// than widening what every request stores.
func (h *Handler) CurrentSessionClaims(r *http.Request) (SessionClaims, error) {
	tokenString, _, ok := extractSessionToken(r)
	if !ok {
		return SessionClaims{}, errors.New("no session token on the request")
	}

	dbCfg, err := config.LoadDBConfig()
	if err != nil {
		return SessionClaims{}, err
	}
	if strings.TrimSpace(dbCfg.JWTSecret) == "" {
		return SessionClaims{}, errors.New("no JWT secret configured")
	}
	return parseSessionClaims(tokenString, []byte(dbCfg.JWTSecret))
}

// Revocation refresher lifecycle.
//
// This is deliberately not started from SessionRevoker: that getter runs on the
// first authenticated request, including in every test that builds a zero-value
// Handler, and would leak a ticker goroutine per test. Whoever constructs the
// Handler for real owns the lifecycle instead.

// StartSessionRevocation begins keeping this instance's revocation list in step
// with the others and bounded. Idempotent.
//
// Without it, a revocation performed here never reaches another instance and
// nothing ever prunes, so the list grows for the life of the process.
func (h *Handler) StartSessionRevocation(ctx context.Context) {
	h.revocationMu.Lock()
	defer h.revocationMu.Unlock()
	if h.stopRevocation != nil {
		return
	}
	h.stopRevocation = h.SessionRevoker().StartRefreshing(ctx, DefaultRefreshInterval)
}

// StopSessionRevocation stops the refresher and waits for it to finish. Safe to
// call when it was never started, and safe to call twice: shutdown paths do.
func (h *Handler) StopSessionRevocation() {
	h.revocationMu.Lock()
	stop := h.stopRevocation
	h.stopRevocation = nil
	h.revocationMu.Unlock()

	if stop != nil {
		stop()
	}
}

// RevocationRefreshRunning reports whether the refresher is live.
func (h *Handler) RevocationRefreshRunning() bool {
	h.revocationMu.Lock()
	defer h.revocationMu.Unlock()
	return h.stopRevocation != nil
}

// Observability.
//
// The list shrinking is what proves pruning still runs, and a size that only
// climbs is the first sign it does not. That failure is silent otherwise: the
// control keeps working right up until the process runs out of memory.
var (
	revokedSessionsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hermod_revoked_sessions",
		Help: "Session revocations currently held in memory. Should rise and fall; " +
			"a value that only rises means expired entries are not being pruned.",
	})
	revokedUsersGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hermod_revoked_users",
		Help: "Whole-user revocations currently held in memory, from password changes, " +
			"role changes and account deletions.",
	})
	revocationEvictionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hermod_revocation_evictions_total",
		Help: "Revocations dropped because the list hit its cap. Any non-zero value " +
			"means sessions that were revoked became usable again; investigate the " +
			"source of the churn.",
	})
)

// observe publishes the current list sizes.
func (r *Revoker) observe() {
	r.mu.RLock()
	sessions, users := len(r.sessions), len(r.users)
	r.mu.RUnlock()

	revokedSessionsGauge.Set(float64(sessions))
	revokedUsersGauge.Set(float64(users))
}
