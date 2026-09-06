package registry

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// Storage is swapped while the process runs: the change-database endpoint calls
// SetStorage on a live registry, and so does first-run setup. Everything else
// reads those fields concurrently, from request handlers and from the stats and
// retention tickers.
//
// Run with -race. Before the fields got their own mutex this failed, reporting
// SetStorage writing while GetSourceConfig read.
//
// The reads go through the exported surface rather than the fields, so this
// keeps testing what callers actually do. Iteration counts are small on purpose:
// the detector instruments every access, so a handful of interleavings is enough
// and the test stays fast enough to belong in CI.
func TestStorageSwapIsSafeUnderConcurrentReads(t *testing.T) {
	const (
		swaps   = 200
		readers = 4
		reads   = 200
	)

	reg := NewRegistry(nil)
	t.Cleanup(reg.Close)

	a := &hotswapStore{}
	b := &hotswapStore{}
	ctx := context.Background()

	var wg sync.WaitGroup

	// Writers: swap primary and log storage back and forth, a bounded number of
	// times. Bounded rather than "until the readers finish" because two writers
	// spinning on the mutex starve the readers badly enough to time the test out.
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range swaps {
			if i%2 == 0 {
				reg.SetStorage(a)
			} else {
				reg.SetStorage(b)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := range swaps {
			if i%2 == 0 {
				reg.SetLogStorage(a)
			} else {
				reg.SetLogStorage(b)
			}
		}
	}()

	// Readers: the accessors, plus request-shaped paths that read storage on
	// their way to doing something else.
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range reads {
				_ = reg.GetStorage()
				_ = reg.GetLogStorage()
				_, _ = reg.GetSourceConfig(ctx, "no-such-source")
				_, _ = reg.GetSinkConfig(ctx, "no-such-sink")
				_ = reg.IsResourceInUse(ctx, "no-such-source", "", true)
			}
		}()
	}

	wg.Wait()
}

// hotswapStore is a RegistryStorage whose identity is all that matters — the
// test is about the swap being observed safely, not about what the store
// returns.
//
// The methods the read paths below reach are implemented explicitly rather than
// left to the embedded nil interface. A nil-interface call panics, and
// GetSourceConfig runs its lookup inside a singleflight group: the panic kills
// the flight and every other caller waiting on it blocks forever, which turns a
// race test into a ten-minute timeout with no race reported.
type hotswapStore struct{ storage.Storage }

var errNoStore = errors.New("hotswapStore: not backed by a database")

func (*hotswapStore) GetSource(context.Context, string) (storage.Source, error) {
	return storage.Source{}, errNoStore
}

func (*hotswapStore) GetSink(context.Context, string) (storage.Sink, error) {
	return storage.Sink{}, errNoStore
}

func (*hotswapStore) ListWorkflows(context.Context, storage.CommonFilter) ([]storage.Workflow, int, error) {
	return nil, 0, errNoStore
}
