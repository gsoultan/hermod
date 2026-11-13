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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ErrSmokeTest is a downloaded binary that does not run, or runs and reports
// a different version than the release it came from.
var ErrSmokeTest = errors.New("the downloaded binary did not report the expected version")

// Size limits. The archive is ~100 MB and the binary inside it ~150 MB today;
// these leave room to grow while still bounding what a bad response can write.
const (
	DefaultMaxArchive   = 512 << 20
	DefaultMaxBinary    = 768 << 20
	maxChecksums        = 64 << 10
	smokeTestTimeout    = 30 * time.Second
	tempPrefix          = ".hermod.update-"
	binaryInArchiveName = "hermod"
)

// Phase is how far an install has got.
type Phase string

const (
	PhaseDownloading Phase = "downloading"
	PhaseVerifying   Phase = "verifying"
	PhaseInstalling  Phase = "installing"
)

// Plan names the release to install and where its files are.
type Plan struct {
	Tag          string
	ArchiveName  string
	ArchiveURL   string
	ChecksumsURL string
}

// Installer replaces the binary at Exe with a release.
type Installer struct {
	// Exe is the running binary, symlinks resolved.
	Exe string
	// Fetch downloads at most limit bytes of rawURL into w.
	Fetch func(ctx context.Context, rawURL string, limit int64, w io.Writer) (int64, error)
	// SmokeTest runs the binary at path and returns what it prints for
	// --version. Nil runs it for real.
	SmokeTest func(ctx context.Context, path string) (string, error)

	MaxArchive int64
	MaxBinary  int64
	Now        func() time.Time
}

// ArchiveName is the file goreleaser publishes for a tag and platform
// (name_template "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}").
func ArchiveName(tag, goos, goarch string) string {
	return fmt.Sprintf("hermod_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), goos, goarch)
}

// PreviousPath is where the binary that was replaced is kept for rollback.
func PreviousPath(exe string) string { return exe + ".previous" }

// Install downloads, verifies and swaps in plan's release. from is the version
// being replaced, recorded so a rollback can say what it went back to. On any
// error the binary on disk is unchanged.
func (in *Installer) Install(ctx context.Context, plan Plan, from string, progress func(Phase)) error {
	if progress == nil {
		progress = func(Phase) {}
	}
	if in.Fetch == nil {
		return errors.New("installer has no downloader")
	}
	dir := filepath.Dir(in.Exe)

	progress(PhaseDownloading)
	archive, err := in.download(ctx, plan, dir, func() { progress(PhaseVerifying) })
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(archive) }()

	staged, err := in.stage(archive, dir)
	if err != nil {
		return err
	}
	swapped := false
	defer func() {
		if !swapped {
			_ = os.Remove(staged)
		}
	}()
	if err := in.smokeTest(ctx, staged, plan.Tag); err != nil {
		return err
	}

	progress(PhaseInstalling)
	if err := in.swap(staged, plan.Tag, from); err != nil {
		return err
	}
	swapped = true
	return nil
}

// download fetches the archive beside the binary and proves it is the one
// checksums.txt publishes, returning its path. verifying is called between the
// two. On error nothing is left behind.
func (in *Installer) download(ctx context.Context, plan Plan, dir string, verifying func()) (string, error) {
	var sumsBuf bytes.Buffer
	if _, err := in.Fetch(ctx, plan.ChecksumsURL, maxChecksums, &sumsBuf); err != nil {
		return "", fmt.Errorf("fetching checksums.txt: %w", err)
	}
	sums, err := ParseChecksums(&sumsBuf)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrChecksum, err)
	}
	want, ok := sums[plan.ArchiveName]
	if !ok {
		return "", fmt.Errorf("%w: checksums.txt has no entry for %s", ErrChecksum, plan.ArchiveName)
	}

	archive, err := os.CreateTemp(dir, tempPrefix+"*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("cannot write next to the binary: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(archive.Name())
		}
	}()
	hash := sha256.New()
	_, err = in.Fetch(ctx, plan.ArchiveURL, orDefault(in.MaxArchive, DefaultMaxArchive), io.MultiWriter(archive, hash))
	if cerr := archive.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", plan.ArchiveName, err)
	}

	verifying()
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return "", fmt.Errorf("%w: %s is %s, published %s", ErrChecksum, plan.ArchiveName, got, want)
	}
	keep = true
	return archive.Name(), nil
}

// swap keeps the running binary for rollback, arms the boot guard and renames
// staged over the binary.
func (in *Installer) swap(staged, tag, from string) error {
	if err := keepPrevious(in.Exe); err != nil {
		return fmt.Errorf("cannot keep the current binary for rollback: %w", err)
	}
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	// Armed before the swap: the moment the new binary is in place it must be
	// watched, and a marker with no swap behind it is disarmed below.
	if err := writeMarker(in.Exe, &Marker{From: from, To: tag, At: now().UTC()}); err != nil {
		return fmt.Errorf("cannot record the pending update: %w", err)
	}
	if err := os.Rename(staged, in.Exe); err != nil {
		_ = removeMarker(in.Exe)
		return fmt.Errorf("cannot replace %s: %w", in.Exe, err)
	}
	return nil
}

// stage extracts the binary to an executable temporary file beside the
// running one, so the final rename never crosses a filesystem.
func (in *Installer) stage(archivePath, dir string) (string, error) {
	f, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("cannot write next to the binary: %w", err)
	}
	name := f.Name()
	err = ExtractBinary(archivePath, binaryInArchiveName, f, orDefault(in.MaxBinary, DefaultMaxBinary))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o755) //nolint:gosec // G302: the binary has to be executable, like the one it replaces.
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func (in *Installer) smokeTest(ctx context.Context, path, tag string) error {
	run := in.SmokeTest
	if run == nil {
		run = runVersion
	}
	out, err := run(ctx, path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSmokeTest, err)
	}
	if !slices.Contains(strings.Fields(out), tag) {
		return fmt.Errorf("%w: wanted %s, it printed %q", ErrSmokeTest, tag, strings.TrimSpace(out))
	}
	return nil
}

// runVersion runs the binary with --version, which prints and exits before
// touching storage, the network or the update guard.
func runVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, smokeTestTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	return string(out), err
}

// keepPrevious makes PreviousPath a copy of exe: a hard link when the
// filesystem allows one, which is instant and takes no space, else a copy.
func keepPrevious(exe string) error {
	prev := PreviousPath(exe)
	if err := os.Remove(prev); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(exe, prev); err == nil {
		return nil
	}
	src, err := os.Open(exe) //nolint:gosec // G304: exe is the running binary, from os.Executable.
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(prev, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755) //nolint:gosec // G302,G304: an executable copy of the running binary, kept for rollback.
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(prev)
	}
	return err
}

func orDefault(v, d int64) int64 {
	if v > 0 {
		return v
	}
	return d
}
