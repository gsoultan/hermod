package state

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/gsoultan/Hermod"
)

// ---------------------------------------------------------------------------
// The state store contract.
//
// Four things depend on it now: session revocation replicates through it, the
// collect node accumulates groups in it, the stateful node keeps its
// accumulator there, and the two-phase-commit coordinator writes its
// transaction log to it. None of them can see which implementation they got.
//
// The contract had never been written down, and its subtlest clause bit
// already: a missing key returns no value and *no error*. An in-memory fake
// that returned an error instead made the first start of any 2PC group look
// like a corrupt transaction log — the fake was stricter than every real store,
// so the tests exercised a case that cannot happen while missing the one that
// always does.
//
// RunStoreContract is written to be run against every implementation. SQLite
// runs here; Redis and etcd need servers and are covered when those are
// available.
// ---------------------------------------------------------------------------

// RunStoreContract exercises the behaviour every state store must share.
func RunStoreContract(t *testing.T, name string, newStore func(t *testing.T) hermod.StateStore) {
	t.Helper()

	t.Run(name+"/a missing key is empty, not an error", func(t *testing.T) {
		s := newStore(t)
		val, err := s.Get(context.Background(), "never-written")
		if err != nil {
			t.Fatalf("reading a missing key returned an error (%v); callers cannot tell "+
				"'nothing stored yet' from 'the store is broken', and a first start looks "+
				"like corruption", err)
		}
		if len(val) != 0 {
			t.Errorf("a missing key returned %q", val)
		}
	})

	t.Run(name+"/a value round-trips", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("value")); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, err := s.Get(ctx, "k")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if string(got) != "value" {
			t.Errorf("got %q, want %q", got, "value")
		}
	})

	t.Run(name+"/a second write replaces the first", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("first")); err != nil {
			t.Fatalf("set: %v", err)
		}
		if err := s.Set(ctx, "k", []byte("second")); err != nil {
			t.Fatalf("overwrite: %v", err)
		}
		got, _ := s.Get(ctx, "k")
		if string(got) != "second" {
			t.Errorf("got %q after an overwrite, want %q; a revocation list or transaction "+
				"log that appended instead of replacing would grow without bound", got, "second")
		}
	})

	t.Run(name+"/values are binary-safe", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		// The 2PC log and the revocation index store JSON; a store that mangled
		// bytes would corrupt both in ways only visible on recovery.
		payload := []byte{0x00, 0x01, 0xff, 0xfe, '{', '"', 'a', '"', ':', '1', '}', 0x00}
		if err := s.Set(ctx, "bin", payload); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, _ := s.Get(ctx, "bin")
		if !bytes.Equal(got, payload) {
			t.Errorf("got %v, want %v", got, payload)
		}
	})

	t.Run(name+"/delete removes the value", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "k", []byte("v")); err != nil {
			t.Fatalf("set: %v", err)
		}
		if err := s.Delete(ctx, "k"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		got, err := s.Get(ctx, "k")
		if err != nil {
			t.Fatalf("get after delete returned an error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("the value survived a delete: %q", got)
		}
	})

	t.Run(name+"/deleting a missing key is not an error", func(t *testing.T) {
		s := newStore(t)
		// Pruning paths delete speculatively; failing them over a key that was
		// already gone would turn cleanup into an error every time it worked.
		if err := s.Delete(context.Background(), "never-written"); err != nil {
			t.Errorf("deleting a missing key errored: %v", err)
		}
	})

	t.Run(name+"/an empty value is not a missing key", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if err := s.Set(ctx, "empty", []byte{}); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, err := s.Get(ctx, "empty")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		// Callers treat a zero-length read as "nothing there", so this documents
		// which it is rather than leaving it to be discovered.
		if len(got) != 0 {
			t.Errorf("an empty value read back as %q", got)
		}
	})

	t.Run(name+"/keys are independent", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		for i := range 5 {
			if err := s.Set(ctx, fmt.Sprintf("k%d", i), []byte(fmt.Sprintf("v%d", i))); err != nil {
				t.Fatalf("set: %v", err)
			}
		}
		if err := s.Delete(ctx, "k2"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		for i := range 5 {
			got, _ := s.Get(ctx, fmt.Sprintf("k%d", i))
			want := fmt.Sprintf("v%d", i)
			if i == 2 {
				want = ""
			}
			if string(got) != want {
				t.Errorf("k%d = %q, want %q; deleting one key disturbed another", i, got, want)
			}
		}
	})

	t.Run(name+"/concurrent writers do not corrupt it", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		// Revocation replicates from several goroutines, and the 2PC coordinator
		// writes participants concurrently.
		done := make(chan error, 8)
		for i := range 8 {
			go func() {
				key := fmt.Sprintf("c%d", i)
				if err := s.Set(ctx, key, []byte(key)); err != nil {
					done <- err
					return
				}
				got, err := s.Get(ctx, key)
				if err == nil && string(got) != key {
					err = fmt.Errorf("%s read back as %q", key, got)
				}
				done <- err
			}()
		}
		for range 8 {
			if err := <-done; err != nil {
				t.Errorf("concurrent access: %v", err)
			}
		}
	})
}

func TestSQLiteStateStoreContract(t *testing.T) {
	RunStoreContract(t, "sqlite", func(t *testing.T) hermod.StateStore {
		t.Helper()
		s, err := NewSQLiteStateStore(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("open sqlite state store: %v", err)
		}
		t.Cleanup(func() {
			if c, ok := s.(interface{ Close() error }); ok {
				_ = c.Close()
			}
		})
		return s
	})
}
