package idempotency

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// The idempotency store had no tests, and the delivery guarantee rests on it.
//
// README: "at-least-once with sink-side idempotency for duplicate suppression".
// That sentence is what makes every replay, restart and backfill overlap safe,
// so the store deciding "seen this already" is the load-bearing part of the
// whole pipeline.
//
// Claiming is the subtle half. A claim taken before the work and never released
// when the work fails turns at-least-once into at-most-once: the retry is told
// the key was already handled and the message is dropped, reported as a
// suppressed duplicate.
// ---------------------------------------------------------------------------

func newStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStoreWithTable(filepath.Join(t.TempDir(), "idem.db"), "idem_test")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestTheFirstClaimWins(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	claimed, err := s.Claim(ctx, "k1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claimed {
		t.Error("the first claim of a key was refused; nothing would ever be processed")
	}
}

func TestASecondClaimIsRefused(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if _, err := s.Claim(ctx, "k2"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	claimed, err := s.Claim(ctx, "k2")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimed {
		t.Error("the same key was claimed twice; a replay would be delivered again, " +
			"which is the duplicate this exists to suppress")
	}
}

// TestAReleasedClaimCanBeRetaken is the property the delivery guarantee needs.
//
// A claim is taken before the work and only marked sent afterwards. When the
// work fails, the claim has to go — otherwise the retry is told the key was
// handled, and the message is dropped while the pipeline reports a suppressed
// duplicate. That is at-most-once wearing at-least-once's clothes.
func TestAReleasedClaimCanBeRetaken(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if _, err := s.Claim(ctx, "k3"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.Release(ctx, "k3"); err != nil {
		t.Fatalf("release: %v", err)
	}

	claimed, err := s.Claim(ctx, "k3")
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if !claimed {
		t.Error("a released claim could not be retaken; the retry of a failed send is " +
			"refused and the message is lost")
	}
}

// TestReleasingAfterMarkSentKeepsTheSuppression. Release must only undo a claim
// that never completed. Undoing a completed one would let a genuine duplicate
// through.
func TestReleasingAfterMarkSentKeepsTheSuppression(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if _, err := s.Claim(ctx, "k4"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.MarkSent(ctx, "k4"); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if err := s.Release(ctx, "k4"); err != nil {
		t.Fatalf("release: %v", err)
	}

	claimed, err := s.Claim(ctx, "k4")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed {
		t.Error("a key that was already sent became claimable again; the work would be " +
			"repeated, which is the duplicate this exists to prevent")
	}
}

// TestReleasingSomethingUnclaimedIsHarmless: retry paths call it speculatively.
func TestReleasingSomethingUnclaimedIsHarmless(t *testing.T) {
	if err := newStore(t).Release(context.Background(), "never-claimed"); err != nil {
		t.Errorf("releasing an unclaimed key errored: %v", err)
	}
}

// TestMarkSentOnAnUnclaimedKeyIsNotAnError. Ordering under retries is not
// guaranteed, and this must not fail the write.
func TestMarkSentOnAnUnclaimedKeyIsNotAnError(t *testing.T) {
	if err := newStore(t).MarkSent(context.Background(), "ghost"); err != nil {
		t.Errorf("marking an unclaimed key sent errored: %v", err)
	}
}

// TestCleanupRemovesOldEntriesOnly. The table is the dedup window; without
// pruning it grows for the life of the deployment, and pruning too eagerly
// re-admits duplicates.
func TestCleanupRemovesOldEntriesOnly(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if _, err := s.Claim(ctx, "fresh"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// A TTL far longer than the row's age must keep it.
	if err := s.CleanupTTL(ctx, time.Hour); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if claimed, _ := s.Claim(ctx, "fresh"); claimed {
		t.Error("cleanup removed an entry inside the dedup window; a replay would be " +
			"delivered again")
	}
}

// TestCleanupWithNoTTLDoesNothing rather than emptying the table, which would
// silently disable duplicate suppression.
func TestCleanupWithNoTTLDoesNothing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if _, err := s.Claim(ctx, "keep"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.CleanupTTL(ctx, 0); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if claimed, _ := s.Claim(ctx, "keep"); claimed {
		t.Error("a zero TTL emptied the table, which turns duplicate suppression off")
	}
}

// TestTwoTablesDoNotShareKeys. The table is the namespace, so two sinks using
// the same key must not suppress each other's work.
func TestTwoTablesDoNotShareKeys(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "shared.db")

	a, err := NewSQLiteStoreWithTable(dsn, "sink_a")
	if err != nil {
		t.Fatalf("open a: %v", err)
	}
	defer a.Close()
	b, err := NewSQLiteStoreWithTable(dsn, "sink_b")
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	defer b.Close()

	ctx := context.Background()
	if _, err := a.Claim(ctx, "same-key"); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	claimed, err := b.Claim(ctx, "same-key")
	if err != nil {
		t.Fatalf("claim b: %v", err)
	}
	if !claimed {
		t.Error("one sink's claim suppressed another's; two sinks processing the same " +
			"message would silently drop one of them")
	}
}
