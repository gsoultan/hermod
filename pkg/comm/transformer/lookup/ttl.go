package lookup

import (
	"fmt"
	"strings"
	"time"
)

// defaultDBLookupTTL bounds how long a looked-up row is reused when the node
// does not say.
//
// It used to be "forever": SetLookupCache reads a ttl of zero as no expiry, and
// the editor's Cache TTL field is empty by default. Since the cache key was
// fixed, such a workflow serves the *right* row -- and then goes on serving it
// after the row has been edited, for as long as the process lives. An
// enrichment lookup that can never observe a change to the table it enriches
// from is not a cache, it is a snapshot nobody asked for.
//
// An hour rather than api_lookup's five minutes, because the two are not alike.
// A remote HTTP response is volatile and the call is somebody else's cost; a
// lookup table is usually slow-moving reference data and the query lands on the
// operator's own database. An hour turns "never correct again" into "correct
// within the hour", which is the change that matters, while leaving the query
// rate low enough not to be felt.
//
// The blast radius is bounded from the other side too: the cache holds at most
// MaxLookupCacheSize (10000) entries. A workflow with more distinct keys than
// that is already re-querying constantly through eviction, so a ttl changes
// little for it; one with fewer has at most 10000 re-queries per hour, which is
// under three a second in the worst case and far less in practice.
//
// A node that genuinely wants the old behaviour can still say so with a long
// duration, e.g. "87600h".
const defaultDBLookupTTL = time.Hour

// lookupTTL is the outcome of reading a node's ttl setting.
//
// The two fields exist because Registry.SetLookupCache cannot express "do not
// cache": it reads a ttl of zero as "no expiry", so the only way to honour an
// explicit zero is to not call it. Keeping that decision here, rather than at
// each call site, is what stops the two lookups drifting apart again.
type lookupTTL struct {
	duration time.Duration
	// cache is false only when the node explicitly asked for no caching.
	cache bool
}

// resolveLookupTTL parses a ttl setting, using unset when the field is empty.
//
// Discarding the parse error is what made this dangerous, and both lookups did
// it: `ttl, _ = time.ParseDuration(ttlStr)`. "5" and "300" are what people type
// into a box labelled Cache TTL, neither is a Go duration, and both left the
// zero value behind -- so the field whose entire purpose is bounding staleness
// silently unbounded it instead. An operator asking for five seconds got
// forever, and nothing said so.
//
// An explicit "0" now means what it says. It used to be indistinguishable from
// leaving the field empty, so there was no way to turn either cache off.
func resolveLookupTTL(ttlStr string, unset time.Duration) (lookupTTL, error) {
	s := strings.TrimSpace(ttlStr)
	if s == "" {
		return lookupTTL{duration: unset, cache: true}, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return lookupTTL{}, fmt.Errorf(
			"ttl %q is not a duration (it needs a unit, e.g. %q, %q or %q; %q disables the cache): %w",
			ttlStr, "30s", "5m", "1h", "0", err)
	}
	if d < 0 {
		return lookupTTL{}, fmt.Errorf("ttl %q is negative", ttlStr)
	}
	if d == 0 {
		return lookupTTL{cache: false}, nil
	}
	return lookupTTL{duration: d, cache: true}, nil
}
