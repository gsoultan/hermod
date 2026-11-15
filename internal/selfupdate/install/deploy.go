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
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	// Package names the .deb package that owns path, or fails when dpkg
	// does not know it (an .rpm or .apk install, or no dpkg at all).
	Package func(path string) (string, error)
	// Root is whether the process runs as root, which installing a package
	// needs. User names who it runs as otherwise.
	Root bool
	User string
}

// Method is how an update is put in place.
type Method string

const (
	// MethodBinary renames the release archive's binary over the running one.
	MethodBinary Method = "binary"
	// MethodDeb installs the release's .deb with dpkg, so the package
	// database keeps describing what is installed.
	MethodDeb Method = "deb"
)

// DebPackage is the package name goreleaser's nfpms section publishes.
const DebPackage = "hermod"

// Capability is whether this process can install an update itself, and in
// plain words why not when it cannot.
type Capability struct {
	CanInstall bool   `json:"can_install"`
	Reason     string `json:"reason,omitempty"`
	Platform   string `json:"platform"`
	Method     Method `json:"method,omitempty"`
	Exe        string `json:"-"`
	GOOS       string `json:"-"`
	GOARCH     string `json:"-"`
}

// AssetName is the release file this installation updates from.
func (c Capability) AssetName(tag string) string {
	if c.Method == MethodDeb {
		return PackageName(tag, c.GOOS, c.GOARCH, "deb")
	}
	return ArchiveName(tag, c.GOOS, c.GOARCH)
}

// Detect decides whether an in-place update is possible here. Checking for a
// newer release is always possible; this only governs installing one.
func Detect(env Env) Capability {
	c := Capability{Platform: env.GOOS + "/" + env.GOARCH, Exe: env.Exe, GOOS: env.GOOS, GOARCH: env.GOARCH, Method: MethodBinary}
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
		return packaged(c, env)
	}
	if env.Writable != nil {
		if err := env.Writable(dir); err != nil {
			return no("Hermod cannot write to %s, the folder its binary is in (%v). Run it as a user who owns that folder, or replace the binary by hand.", dir, err)
		}
	}
	c.CanInstall = true
	return c
}

// packaged decides for a binary a package manager put in place. Only the
// .deb is installed by Hermod itself: dpkg can be driven without prompts and
// keeps its own record of the upgrade, and it is the package asked for.
func packaged(c Capability, env Env) Capability {
	dir := filepath.Dir(env.Exe)
	owner := ""
	if env.Package != nil {
		owner, _ = env.Package(env.Exe)
	}
	if owner != DebPackage {
		c.Reason = fmt.Sprintf("Hermod was installed into %s by a package manager other than dpkg (.rpm or .apk). Upgrade it with that package manager, using the package from the release page.", dir)
		return c
	}
	if !env.Root {
		who := env.User
		if who == "" {
			who = "a user other than root"
		}
		c.Reason = fmt.Sprintf("Hermod was installed from the hermod .deb, and installing the newer .deb needs root, but Hermod runs as %s. Run the service as root, or upgrade by hand with sudo apt install ./%s from the release page.", who, PackageName("vX.Y.Z", env.GOOS, env.GOARCH, "deb"))
		return c
	}
	c.CanInstall, c.Method = true, MethodDeb
	return c
}

// dpkgOwner asks dpkg which package installed path.
func dpkgOwner(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, systemTool("dpkg-query"), "--search", path).Output() //nolint:gosec // G204: path is this process's own executable.
	if err != nil {
		return "", err
	}
	return parseDpkgOwner(string(out)), nil
}

// parseDpkgOwner reads `dpkg-query --search` output ("pkg: /usr/bin/hermod").
// A file claimed by several packages, or diverted, has no single owner.
func parseDpkgOwner(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	pkg, _, ok := strings.Cut(line, ": ")
	if !ok || strings.ContainsAny(pkg, ", ") {
		return ""
	}
	pkg, _, _ = strings.Cut(pkg, ":") // hermod:amd64
	return pkg
}

// CurrentEnv describes the running process.
func CurrentEnv(version, goos, goarch string) Env {
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
	}
	env := Env{
		GOOS: goos, GOARCH: goarch, Exe: exe, Version: version,
		InContainer: inContainer(),
		Writable:    canWrite,
		Package:     dpkgOwner,
		Root:        os.Geteuid() == 0,
	}
	if u, err := user.Current(); err == nil {
		env.User = u.Username
	}
	return env
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
