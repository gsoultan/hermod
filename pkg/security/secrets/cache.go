package secrets

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// CachedManager remembers what another Manager answered. It is what the
// expression functions secret() and env() read through.
//
// An expression reads its secrets once per message. Against Vault, OpenBao, AWS
// or Azure that is a network call per message, and a manager that stops
// answering would hold every message behind it. So a lookup has a timeout; an
// answer -- a missing secret included -- is kept for ttl; lookups for one key
// that miss at the same moment share a single call; and a failure is not kept,
// so the next message asks again. The keys come from expressions a workflow
// editor writes, so at most maxKeys are held, and the oldest goes first.
type CachedManager struct {
	inner   Manager
	ttl     time.Duration
	maxKeys int
	timeout time.Duration
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]cachedSecret
	group   singleflight.Group
}

type cachedSecret struct {
	value   string
	fetched time.Time
}

// NewCachedManager wraps inner. maxKeys below 1 is taken as 1.
func NewCachedManager(inner Manager, ttl time.Duration, maxKeys int, timeout time.Duration) *CachedManager {
	return &CachedManager{
		inner:   inner,
		ttl:     ttl,
		maxKeys: max(maxKeys, 1),
		timeout: timeout,
		now:     time.Now,
		entries: make(map[string]cachedSecret),
	}
}

// Get answers without a vhost: whatever inner holds globally.
func (c *CachedManager) Get(ctx context.Context, key string) (string, error) {
	return c.GetScoped(ctx, "", key)
}

// GetScoped answers key for vhost. The cache is keyed by both: keyed by name
// alone, the first vhost to read API_KEY would decide what every other vhost
// was given until the entry expired.
func (c *CachedManager) GetScoped(ctx context.Context, vhost, key string) (string, error) {
	entry := cacheKey(vhost, key)
	if v, ok := c.cached(entry); ok {
		return v, nil
	}
	v, err, _ := c.group.Do(entry, func() (any, error) {
		// Another caller may have filled it while this one waited to enter.
		if v, ok := c.cached(entry); ok {
			return v, nil
		}
		// The lookup is shared, so it must not end with whichever caller
		// happened to start it: only the timeout bounds it.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.timeout)
		defer cancel()
		v, err := c.lookup(lctx, vhost, key)
		if err != nil {
			return "", err
		}
		c.store(entry, v)
		return v, nil
	})
	if err != nil {
		return "", err
	}
	s, _ := v.(string)
	return s, nil
}

func (c *CachedManager) lookup(ctx context.Context, vhost, key string) (string, error) {
	if scoped, ok := c.inner.(ScopedManager); ok {
		return scoped.GetScoped(ctx, vhost, key)
	}
	return c.inner.Get(ctx, key)
}

// Invalidate forgets what was cached for one vhost's secret, so a rotation or a
// deletion is seen by the next message rather than when the entry expires.
func (c *CachedManager) Invalidate(vhost, key string) {
	entry := cacheKey(vhost, key)
	c.mu.Lock()
	delete(c.entries, entry)
	c.mu.Unlock()
	c.group.Forget(entry)
}

// cacheKey joins a vhost and a secret name with a byte neither can hold.
func cacheKey(vhost, key string) string {
	return vhost + "\x00" + key
}

func (c *CachedManager) cached(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || c.now().Sub(e.fetched) >= c.ttl {
		return "", false
	}
	return e.value, true
}

func (c *CachedManager) store(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if _, held := c.entries[key]; !held && len(c.entries) >= c.maxKeys {
		c.evictLocked(now)
	}
	c.entries[key] = cachedSecret{value: value, fetched: now}
}

// evictLocked drops every expired entry and, if that freed nothing, the oldest.
func (c *CachedManager) evictLocked(now time.Time) {
	oldestKey, found := "", false
	var oldest time.Time
	for k, e := range c.entries {
		if now.Sub(e.fetched) >= c.ttl {
			delete(c.entries, k)
			continue
		}
		if !found || e.fetched.Before(oldest) {
			oldestKey, oldest, found = k, e.fetched, true
		}
	}
	if len(c.entries) >= c.maxKeys && found {
		delete(c.entries, oldestKey)
	}
}

func (c *CachedManager) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
