package mcptool

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"sync"
	"time"
)

// cache keeps remote tool descriptions for a while, so a busy workflow does
// not list a server's tools for every message. It is bounded: when full, the
// oldest entry goes.
type cache struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[string]cacheEntry
	now     func() time.Time
}

type cacheEntry struct {
	remote  Remote
	expires time.Time
	added   time.Time
}

func newCache(ttl time.Duration, maxEntries int) *cache {
	return &cache{ttl: ttl, max: maxEntries, entries: map[string]cacheEntry{}, now: time.Now}
}

func (c *cache) get(key string) (Remote, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return Remote{}, false
	}
	if !c.now().Before(e.expires) {
		delete(c.entries, key)
		return Remote{}, false
	}
	return e.remote, true
}

func (c *cache) put(key string, r Remote) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.max {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		for len(c.entries) >= c.max {
			oldest, first := "", true
			var at time.Time
			for k, e := range c.entries {
				if first || e.added.Before(at) {
					oldest, at, first = k, e.added, false
				}
			}
			delete(c.entries, oldest)
		}
	}
	c.entries[key] = cacheEntry{remote: r, expires: now.Add(c.ttl), added: now}
}

func (c *cache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// cacheKey identifies a remote tool as seen with one set of credentials:
// two vhosts with different tokens may be shown different tools. It is a
// hash, so no credential is kept as a map key.
func cacheKey(ep Endpoint, tool string) string {
	h := sha256.New()
	write := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	write(ep.URL)
	write(tool)
	for _, k := range slices.Sorted(maps.Keys(ep.Headers)) {
		write(k)
		write(ep.Headers[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}
