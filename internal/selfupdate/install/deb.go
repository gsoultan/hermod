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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotHermodPackage is a downloaded .deb that names another package.
var ErrNotHermodPackage = errors.New("the downloaded package is not the hermod package")

// maxDpkgOutput is how much of what dpkg printed an error carries.
const maxDpkgOutput = 2 << 10

// installDeb puts a release's .deb in place with dpkg. The checks run before
// anything changes, as for the archive: the package matches checksums.txt,
// names itself hermod, and the binary inside runs and reports the tag. Only
// then is the rollback copy kept, the boot guard armed and dpkg run. dpkg
// replaces each file by renaming over it, so the binary on disk is always
// whole; dpkg does not restart anything, which is left to the caller.
func (in *Installer) installDeb(ctx context.Context, plan Plan, from string, progress func(Phase)) error {
	work, err := os.MkdirTemp(in.TempDir, tempPrefix+"deb-*")
	if err != nil {
		return fmt.Errorf("cannot make a folder to download the package into: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	progress(PhaseDownloading)
	pkg, err := in.download(ctx, plan, work, func() { progress(PhaseVerifying) })
	if err != nil {
		return err
	}
	out, err := in.run(ctx, "dpkg-deb", "--field", pkg, "Package")
	if err != nil {
		return dpkgError("dpkg-deb could not read "+plan.ArchiveName, err, out)
	}
	if name := strings.TrimSpace(string(out)); name != DebPackage {
		return fmt.Errorf("%w: %s is package %q", ErrNotHermodPackage, plan.ArchiveName, name)
	}
	root := filepath.Join(work, "root")
	if out, err := in.run(ctx, "dpkg-deb", "--extract", pkg, root); err != nil {
		return dpkgError("dpkg-deb could not unpack "+plan.ArchiveName, err, out)
	}
	if err := in.smokeTest(ctx, filepath.Join(root, in.Exe), plan.Tag); err != nil {
		return err
	}

	progress(PhaseInstalling)
	if err := in.arm(plan, from); err != nil {
		return err
	}
	out, err = in.run(ctx, "dpkg", "--force-confdef", "--force-confold", "--install", pkg)
	replaced := !sameFile(in.Exe, PreviousPath(in.Exe))
	if err != nil {
		// When dpkg failed before unpacking, nothing changed and the guard is
		// disarmed. When it failed after (a maintainer script), the new
		// binary is in place and its next start must still be watched.
		if !replaced {
			_ = removeMarker(in.Exe)
		}
		return dpkgError("dpkg could not install "+plan.ArchiveName, err, out)
	}
	if !replaced {
		_ = removeMarker(in.Exe)
		return fmt.Errorf("dpkg installed %s but %s is unchanged", plan.ArchiveName, in.Exe)
	}
	return nil
}

func (in *Installer) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if in.Command != nil {
		return in.Command(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, systemTool(name), args...) //nolint:gosec // G204: a fixed dpkg command on a package whose checksum was verified.
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	return cmd.CombinedOutput()
}

// systemTool is the root-owned copy of a dpkg tool when there is one, so a
// PATH that puts another folder first cannot pick what runs as root.
func systemTool(name string) string {
	for _, dir := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return name
}

// sameFile reports whether a and b are one file, as the hard link keepPrevious
// makes is until dpkg renames a new file over a.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

func dpkgError(what string, err error, out []byte) error {
	msg := strings.TrimSpace(string(out))
	if len(msg) > maxDpkgOutput {
		msg = "…" + msg[len(msg)-maxDpkgOutput:]
	}
	if msg == "" {
		return fmt.Errorf("%s: %w", what, err)
	}
	return fmt.Errorf("%s: %w: %s", what, err, msg)
}
