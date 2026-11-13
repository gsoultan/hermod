// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package install

import (
	"errors"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	writable := func(string) error { return nil }
	readOnly := func(string) error { return errors.New("permission denied") }
	base := Env{GOOS: "linux", GOARCH: "amd64", Exe: "/opt/hermod/hermod", Version: "v1.24.9 (Enterprise Edition)", Writable: writable}

	cases := []struct {
		name   string
		mutate func(*Env)
		can    bool
		reason string
	}{
		{"linux binary the process can replace", func(*Env) {}, true, ""},
		{"arm64 is published too", func(e *Env) { e.GOARCH = "arm64" }, true, ""},
		{"development build", func(e *Env) { e.Version = "dev (Enterprise Edition)" }, false, "development build"},
		{"container", func(e *Env) { e.InContainer = true }, false, "container"},
		{"no published build for the platform", func(e *Env) { e.GOOS = "darwin" }, false, "darwin/amd64"},
		{"package-managed", func(e *Env) { e.Exe = "/usr/bin/hermod" }, false, "package"},
		{"folder not writable", func(e *Env) { e.Writable = readOnly }, false, "cannot write"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := base
			c.mutate(&env)
			got := Detect(env)
			if got.CanInstall != c.can {
				t.Fatalf("CanInstall = %v (%q), want %v", got.CanInstall, got.Reason, c.can)
			}
			if c.reason != "" && !strings.Contains(got.Reason, c.reason) {
				t.Fatalf("Reason = %q, want it to mention %q", got.Reason, c.reason)
			}
			if got.Platform != env.GOOS+"/"+env.GOARCH {
				t.Fatalf("Platform = %q", got.Platform)
			}
		})
	}
}

func TestArchiveName(t *testing.T) {
	if got := ArchiveName("v1.25.0-rc.1", "linux", "arm64"); got != "hermod_1.25.0-rc.1_linux_arm64.tar.gz" {
		t.Fatalf("ArchiveName = %q", got)
	}
}
