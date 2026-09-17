package registry

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// Editing a source changed what a lookup against it would return, and nothing
// dropped the rows already cached from it. SetLookupCache treats ttl <= 0 as
// "never expires" and the editor's Cache TTL is empty by default, so "until
// something evicts it" means "for the life of the process".
//
// The CDC rule makes that sharper. Switching use_cdc on is exactly the edit that
// must stop a lookup working, and it is the one edit the stale cache would go on
// serving straight past.
type lookupInvalidationStorage struct {
	testutil.BaseMockStorage
	updated int
}

func (m *lookupInvalidationStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	m.updated++
	return nil
}

func seedLookupCache(r *Registry, sourceID string, keys ...string) {
	for _, k := range keys {
		r.SetLookupCache(hermod.LookupCacheKeyPrefix(sourceID)+k, "cached", 0)
	}
}

func cachedUnder(r *Registry, sourceID string, keys ...string) int {
	n := 0
	for _, k := range keys {
		if _, ok := r.GetLookupCache(hermod.LookupCacheKeyPrefix(sourceID) + k); ok {
			n++
		}
	}
	return n
}

func TestUpdateSourceDropsTheRowsCachedFromIt(t *testing.T) {
	r := NewRegistry(&lookupInvalidationStorage{})

	seedLookupCache(r, "customers", "users:id:email:string:u1:::table", "users:id:name:string:u2:::table")
	seedLookupCache(r, "products", "items:sku:price:string:p1:::table")

	if got := cachedUnder(r, "customers", "users:id:email:string:u1:::table", "users:id:name:string:u2:::table"); got != 2 {
		t.Fatalf("fixture did not cache: %d of 2 entries present", got)
	}

	if err := r.UpdateSource(t.Context(), storage.Source{ID: "customers", Type: "postgres"}); err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}

	if got := cachedUnder(r, "customers", "users:id:email:string:u1:::table", "users:id:name:string:u2:::table"); got != 0 {
		t.Errorf("%d rows cached from the edited source are still being served", got)
	}

	// Only that source's rows. Other sources were not edited and their cached
	// rows are still correct; dropping them would be a throughput cliff on
	// every unrelated edit.
	if got := cachedUnder(r, "products", "items:sku:price:string:p1:::table"); got != 1 {
		t.Error("editing one source dropped another source's cached rows")
	}
}

// The source id is not the whole key, so a prefix match has to stop at the
// separator: editing "cust" must not drop what was cached from "customers".
func TestUpdateSourceDoesNotDropASourceWhoseIDIsAPrefix(t *testing.T) {
	r := NewRegistry(&lookupInvalidationStorage{})

	seedLookupCache(r, "cust", "t:k:v:string:1:::table")
	seedLookupCache(r, "customers", "t:k:v:string:1:::table")

	if err := r.UpdateSource(t.Context(), storage.Source{ID: "cust", Type: "postgres"}); err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}

	if got := cachedUnder(r, "customers", "t:k:v:string:1:::table"); got != 1 {
		t.Error("editing 'cust' dropped what was cached from 'customers'")
	}
	if got := cachedUnder(r, "cust", "t:k:v:string:1:::table"); got != 0 {
		t.Error("the edited source's own cached row survived")
	}
}

// A failed write leaves the source as it was, so the cache is still valid --
// and dropping it would turn a storage blip into a cache stampede.
func TestUpdateSourceKeepsTheCacheWhenTheWriteFails(t *testing.T) {
	r := NewRegistry(&failingUpdateStorage{})

	seedLookupCache(r, "customers", "t:k:v:string:1:::table")

	if err := r.UpdateSource(t.Context(), storage.Source{ID: "customers"}); err == nil {
		t.Fatal("expected the storage error to surface")
	}
	if got := cachedUnder(r, "customers", "t:k:v:string:1:::table"); got != 1 {
		t.Error("a failed update dropped the cache anyway")
	}
}

type failingUpdateStorage struct {
	testutil.BaseMockStorage
}

func (m *failingUpdateStorage) UpdateSource(ctx context.Context, src storage.Source) error {
	return context.DeadlineExceeded
}
