package api

import (
	"path"
	"regexp"
	"strings"
)

const (
	// cacheImmutable is safe only for content-hashed filenames, where a change
	// in content always produces a change in name.
	cacheImmutable = "public, max-age=31536000, immutable"
	// cacheRevalidate lets the browser keep a copy but forces a freshness check,
	// so a deploy takes effect immediately.
	cacheRevalidate = "no-cache"
)

// hashedAssetPattern matches the content hash Vite appends before the
// extension, e.g. mantine-vendor-BTf5USnm.js or index-CAj72fme.css. The hash is
// base64url-ish and at least 8 characters, which is long enough not to collide
// with ordinary names like "legacy.js" or "vite.svg".
var hashedAssetPattern = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

// cacheControlForPath returns the Cache-Control value to serve for a static
// asset path.
//
// Without this every visit re-downloads the whole bundle (~2.4 MB uncompressed)
// even though nothing changed. Hashed files are immutable by construction, so
// caching them for a year makes a repeat load cost effectively zero bytes of
// JavaScript, while everything unhashed keeps revalidating so a deploy is
// picked up straight away.
func cacheControlForPath(p string) string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return cacheRevalidate
	}

	// Compression siblings inherit the cacheability of what they compress.
	base := path.Base(p)
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".br"), ".gz")

	// A service worker must always be re-checked: caching one indefinitely
	// would leave a user permanently pinned to an old application shell.
	if base == "sw.js" || base == "service-worker.js" || base == "registerSW.js" {
		return cacheRevalidate
	}
	// index.html is the entry point and names no hash of its own.
	if base == "index.html" || strings.HasSuffix(base, ".webmanifest") {
		return cacheRevalidate
	}

	if hashedAssetPattern.MatchString(base) {
		return cacheImmutable
	}
	return cacheRevalidate
}
