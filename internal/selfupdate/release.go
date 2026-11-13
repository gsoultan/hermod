// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package selfupdate lets Hermod find, install and roll back its own releases.
//
// Releases come from GitHub Releases for gsoultan/hermod, which is where the
// release workflow publishes them. This package decides *what* to offer and
// *when*; the install subpackage does the download, verification, swap, boot
// guard and restart.
package selfupdate

import (
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Release is one published release, reduced to what an update needs.
type Release struct {
	Tag        string    `json:"tag"`
	Name       string    `json:"name,omitempty"`
	Notes      string    `json:"notes,omitempty"`
	URL        string    `json:"url,omitempty"`
	Published  time.Time `json:"published_at"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"-"`
	Assets     []Asset   `json:"-"`
}

// Asset is one file attached to a release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Canonical turns a build string into a comparable semantic version.
//
// The binary's version reads "v1.24.9 (Enterprise Edition)": the release tag
// followed by the edition. Only the first word is the version. A string that
// is not semantic versioning ("dev", "v1.2") comes back empty, and an empty
// version is never offered an update.
func Canonical(v string) string {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return ""
	}
	head := fields[0]
	if !strings.HasPrefix(head, "v") {
		head = "v" + head
	}
	if !semver.IsValid(head) {
		return ""
	}
	// semver accepts the shorthand v1.2 and canonicalises it to v1.2.0; a
	// release tag is always three numbers, so the shorthand is not one.
	core, _, _ := strings.Cut(strings.TrimPrefix(head, "v"), "-")
	core, _, _ = strings.Cut(core, "+")
	if strings.Count(core, ".") != 2 {
		return ""
	}
	return head
}

// IsNewer reports whether candidate is a later version than current. Either
// side being unreadable is "no".
func IsNewer(candidate, current string) bool {
	c, cur := Canonical(candidate), Canonical(current)
	if c == "" || cur == "" {
		return false
	}
	return semver.Compare(c, cur) > 0
}

// SameMajor reports whether a and b share a major version.
func SameMajor(a, b string) bool {
	ca, cb := Canonical(a), Canonical(b)
	return ca != "" && cb != "" && semver.Major(ca) == semver.Major(cb)
}

// Pick returns the newest release later than current, or nil when there is
// none. Drafts are never offered, nor are pre-releases unless asked for.
func Pick(releases []Release, current string, includePrereleases bool) *Release {
	var best *Release
	for i := range releases {
		r := &releases[i]
		if r.Draft || (r.Prerelease && !includePrereleases) {
			continue
		}
		tag := Canonical(r.Tag)
		if tag == "" || semver.Prerelease(tag) != "" && !includePrereleases {
			continue
		}
		if !IsNewer(tag, current) {
			continue
		}
		if best == nil || semver.Compare(tag, Canonical(best.Tag)) > 0 {
			best = r
		}
	}
	return best
}
