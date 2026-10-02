package secrets

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingManager answers from a map and counts the lookups that reach it.
type countingManager struct {
	values map[string]string
	calls  atomic.Int64
	delay  time.Duration
	fail   atomic.Int64 // this many lookups fail before any succeeds
}

func (m *countingManager) Get(ctx context.Context, key string) (string, error) {
	m.calls.Add(1)
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if m.fail.Add(-1) >= 0 {
		return "", errors.New("vault is unreachable")
	}
	return m.values[key], nil
}

// An expression reads its secrets once per message. Against Vault or AWS that
// is a network call per message, so the manager behind expressions answers a
// repeat from memory.
func TestCachedManagerAnswersARepeatFromMemory(t *testing.T) {
	inner := &countingManager{values: map[string]string{"K": "v"}}
	c := NewCachedManager(inner, time.Minute, 8, time.Second)

	for range 5 {
		got, err := c.Get(t.Context(), "K")
		if err != nil || got != "v" {
			t.Fatalf("Get = %q, %v; want v", got, err)
		}
	}
	if n := inner.calls.Load(); n != 1 {
		t.Errorf("the manager was asked %d times, want 1", n)
	}
}

// A secret that does not exist is remembered too: otherwise a typo in a
// workflow asks Vault once per message for ever.
func TestCachedManagerRemembersAMissingSecret(t *testing.T) {
	inner := &countingManager{values: map[string]string{}}
	c := NewCachedManager(inner, time.Minute, 8, time.Second)

	for range 3 {
		if got, _ := c.Get(t.Context(), "MISSING"); got != "" {
			t.Fatalf("Get = %q, want empty", got)
		}
	}
	if n := inner.calls.Load(); n != 1 {
		t.Errorf("the manager was asked %d times, want 1", n)
	}
}

func TestCachedManagerRereadsAfterItsTTL(t *testing.T) {
	inner := &countingManager{values: map[string]string{"K": "v"}}
	c := NewCachedManager(inner, time.Minute, 8, time.Second)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }

	_, _ = c.Get(t.Context(), "K")
	now = now.Add(59 * time.Second)
	_, _ = c.Get(t.Context(), "K")
	if n := inner.calls.Load(); n != 1 {
		t.Fatalf("read again inside the TTL: %d lookups", n)
	}
	now = now.Add(2 * time.Second)
	_, _ = c.Get(t.Context(), "K")
	if n := inner.calls.Load(); n != 2 {
		t.Errorf("after the TTL the manager was asked %d times in all, want 2", n)
	}
}

// The keys come from expressions a workflow editor writes, so the cache is
// bounded: past its size the oldest entry goes.
func TestCachedManagerHoldsABoundedNumberOfSecrets(t *testing.T) {
	inner := &countingManager{values: map[string]string{"A": "a", "B": "b", "C": "c"}}
	c := NewCachedManager(inner, time.Minute, 2, time.Second)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { now = now.Add(time.Millisecond); return now }

	for _, k := range []string{"A", "B", "C"} {
		_, _ = c.Get(t.Context(), k)
	}
	if n := c.size(); n > 2 {
		t.Fatalf("the cache holds %d secrets, bound is 2", n)
	}
	_, _ = c.Get(t.Context(), "A")
	if n := inner.calls.Load(); n != 4 {
		t.Errorf("A should have been evicted as the oldest and read again: %d lookups, want 4", n)
	}
}

// A secret manager that does not answer must not hold a message: the lookup
// gives up after its timeout.
func TestCachedManagerGivesUpOnAManagerThatDoesNotAnswer(t *testing.T) {
	inner := &countingManager{values: map[string]string{"K": "v"}, delay: time.Hour}
	c := NewCachedManager(inner, time.Minute, 8, 20*time.Millisecond)

	start := time.Now()
	got, err := c.Get(t.Context(), "K")
	if err == nil || got != "" {
		t.Fatalf("Get = %q, %v; want an error and no value", got, err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Get took %v; the timeout is 20ms", took)
	}
}

// A failed lookup is not remembered: the next message asks again, so a Vault
// blip costs one message its secret, not a whole TTL of them.
func TestCachedManagerDoesNotRememberAFailure(t *testing.T) {
	inner := &countingManager{values: map[string]string{"K": "v"}}
	inner.fail.Store(1)
	c := NewCachedManager(inner, time.Minute, 8, time.Second)

	if _, err := c.Get(t.Context(), "K"); err == nil {
		t.Fatal("the first lookup should fail")
	}
	got, err := c.Get(t.Context(), "K")
	if err != nil || got != "v" {
		t.Errorf("after a failure Get = %q, %v; want v", got, err)
	}
}

// Messages arrive together. When a secret is not cached, the ones asking for
// it at the same moment share one lookup instead of each making their own.
func TestCachedManagerSharesOneLookupBetweenConcurrentMisses(t *testing.T) {
	inner := &countingManager{values: map[string]string{"K": "v"}, delay: 50 * time.Millisecond}
	c := NewCachedManager(inner, time.Minute, 8, time.Second)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if got, err := c.Get(context.Background(), "K"); err != nil || got != "v" {
				t.Errorf("Get = %q, %v; want v", got, err)
			}
		})
	}
	wg.Wait()
	if n := inner.calls.Load(); n != 1 {
		t.Errorf("20 concurrent misses made %d lookups, want 1", n)
	}
}
