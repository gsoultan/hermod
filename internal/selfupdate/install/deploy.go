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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/mod/semver"
)

// published is every platform the release workflow builds an archive for
// (.goreleaser.yaml: goos linux, goarch amd64 and arm64).
var published = map[string]bool{"linux/amd64": true, "linux/arm64": true}

// Env is what decides whether this process may replace its own binary.
type Env struct {
	GOOS, GOARCH string
	Exe          string
	Version      string
	InContainer  bool
	// Writable reports whether a file can be created in dir.
	Writable func(dir string) error
}

// Capability is whether this process can install an update itself, and in
// plain words why not when it cannot.
type Capability struct {
	CanInstall bool   `json:"can_install"`
	Reason     string `json:"reason,omitempty"`
	Platform   string `json:"platform"`
	Exe        string `json:"-"`
}

// Detect decides whether an in-place update is possible here. Checking for a
// newer release is always possible; this only governs installing one.
func Detect(env Env) Capability {
	c := Capability{Platform: env.GOOS + "/" + env.GOARCH, Exe: env.Exe}
	no := func(format string, a ...any) Capability {
		c.Reason = fmt.Sprintf(format, a...)
		return c
	}
	head := strings.Fields(env.Version + " ")[0]
	if !strings.HasPrefix(head, "v") {
		head = "v" + head
	}
	if !semver.IsValid(head) {
		return no("This is a development build (%s), not a release. Build the new version from source.", strings.TrimSpace(env.Version))
	}
	if env.InContainer {
		return no("Hermod is running in a container, which a new binary inside it would not survive. Update by pulling the new image tag (ghcr.io/gsoultan/hermod) or upgrading the Helm chart.")
	}
	if !published[c.Platform] {
		return no("No release build is published for %s. Build the new version from source.", c.Platform)
	}
	dir := filepath.Dir(env.Exe)
	if dir == "/usr/bin" || dir == "/usr/sbin" {
		return no("Hermod was installed from a package (.deb, .rpm or .apk) into %s. Upgrade it with your package manager, using the package from the release page.", dir)
	}
	if env.Writable != nil {
		if err := env.Writable(dir); err != nil {
			return no("Hermod cannot write to %s, the folder its binary is in (%v). Run it as a user who owns that folder, or replace the binary by hand.", dir, err)
		}
	}
	c.CanInstall = true
	return c
}

// CurrentEnv describes the running process.
func CurrentEnv(version, goos, goarch string) Env {
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
	}
	return Env{
		GOOS: goos, GOARCH: goarch, Exe: exe, Version: version,
		InContainer: inContainer(),
		Writable:    canWrite,
	}
}

func inContainer() bool {
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

func canWrite(dir string) error {
	f, err := os.CreateTemp(dir, tempPrefix+"probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// ExitCodeRestart is what a supervised process exits with after an update, so
// that a supervisor configured to restart only on failure (Windows'
// OnFailure, systemd's Restart=on-failure) still starts the new binary. It is
// EX_TEMPFAIL from sysexits.h.
const ExitCodeRestart = 75

var (
	restartOnce sync.Once
	restartCh   = make(chan struct{})
)

// RequestRestart asks the process to shut down gracefully and come back up as
// the binary now on disk. Safe to call more than once.
func RequestRestart() { restartOnce.Do(func() { close(restartCh) }) }

// RestartRequested is closed once a restart has been asked for.
func RestartRequested() <-chan struct{} { return restartCh }

// Requested reports whether a restart has been asked for.
func Requested() bool {
	select {
	case <-restartCh:
		return true
	default:
		return false
	}
}

// Supervised reports whether a service manager will start the process again
// when it exits. interactive is kardianos/service's Interactive(); systemd
// also sets INVOCATION_ID for every unit it starts.
func Supervised(interactive bool) bool {
	return !interactive || os.Getenv("INVOCATION_ID") != ""
}
