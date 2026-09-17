package lookup

import (
	"fmt"
	"strings"
	"time"
)

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
