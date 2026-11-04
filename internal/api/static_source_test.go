// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package api

import "testing"

// A release binary carries its own UI. An internal/api/static folder left in
// the directory it starts from (an old checkout, an old build) must not take
// its place: that served a pre-upgrade UI from an upgraded .deb install.
func TestTheEmbeddedUIWinsOverAStaticFolderOnDisk(t *testing.T) {
	cases := []struct {
		name             string
		dev              string
		embedded, onDisk bool
		want             bool
	}{
		{"release with an old folder in its working directory", "", true, true, false},
		{"release with no folder", "", true, false, false},
		{"dev run asks for the folder", "true", true, true, true},
		{"source build without a UI falls back to the folder", "", false, true, true},
		{"nothing anywhere", "", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serveUIFromDisk(c.dev, c.embedded, c.onDisk); got != c.want {
				t.Fatalf("serveUIFromDisk(%q, %v, %v) = %v, want %v", c.dev, c.embedded, c.onDisk, got, c.want)
			}
		})
	}
}
