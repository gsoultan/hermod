// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package hermod

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// TestPackagedServiceLetsDpkgUnpack guards the systemd unit the .deb ships.
// Hermod installs its own updates from inside this unit, and dpkg-deb unpacks
// with tar, which opens each folder with openat2(2). RestrictSUIDSGID=yes adds
// a seccomp filter that answers every openat2 with ENOSYS, so tar failed with
// "Cannot mkdir: Function not implemented" and v1.26.1 could not update.
// A SystemCallFilter would refuse tar, dpkg or their maintainer scripts the
// same way.
func TestPackagedServiceLetsDpkgUnpack(t *testing.T) {
	f, err := os.Open("deploy/package/hermod.service")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch {
		case key == "RestrictSUIDSGID" && value != "no" && value != "false":
			t.Errorf("hermod.service:%d sets %s: its seccomp filter refuses openat2, so dpkg-deb cannot unpack an update", line, text)
		case key == "SystemCallFilter":
			t.Errorf("hermod.service:%d sets %s: a syscall filter can refuse what dpkg and tar need to install an update", line, text)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}
