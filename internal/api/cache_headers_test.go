// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package api

import "testing"

// Vite emits content-hashed filenames (mantine-vendor-BTf5USnm.js), so those
// files can never change meaning: a new build produces a new name. They are
// therefore safe to cache forever, and doing so means a repeat visit downloads
// no JavaScript at all instead of re-fetching ~2.4 MB.
//
// index.html is the opposite: its name is stable and it points at the hashed
// bundles, so caching it would pin users to a stale app after a deploy.
func TestCacheControlForPath(t *testing.T) {
	const immutable = "public, max-age=31536000, immutable"

	tests := []struct {
		name string
		path string
		want string
	}{
		{"hashed js chunk", "assets/mantine-vendor-BTf5USnm.js", immutable},
		{"hashed css", "assets/index-CAj72fme.css", immutable},
		{"hashed brotli sibling", "assets/index-CAj72fme.js.br", immutable},
		{"hashed chunk in nested dir", "assets/sub/WorkflowEditorPage-DTIHvBvl.js", immutable},

		// No hash in the name: the same URL may serve different bytes later.
		{"index.html must revalidate", "index.html", "no-cache"},
		{"root path must revalidate", "", "no-cache"},
		{"unhashed asset must revalidate", "favicon.svg", "no-cache"},
		{"unhashed js must revalidate", "assets/legacy.js", "no-cache"},
		{"manifest must revalidate", "manifest.webmanifest", "no-cache"},

		// A service worker that got cached forever could never be replaced,
		// permanently freezing the app for that user.
		{"service worker must revalidate", "sw.js", "no-cache"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cacheControlForPath(tc.path); got != tc.want {
				t.Errorf("cacheControlForPath(%q) = %q; want %q", tc.path, got, tc.want)
			}
		})
	}
}
