// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package update replaces the running hermod-agent with a newer release.
//
// The server only names a version. The download address is built here, from
// the project's GitHub releases and this build's platform, and the archive
// must match the release's published checksums.txt, so nothing the server
// says can make the agent run another file. Nothing on disk changes until the
// archive matches and the extracted binary runs and reports the version it
// was offered as; the swap is two renames in the binary's own folder, and the
// old binary is kept beside it as <name>.old.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultBaseURL is where releases are downloaded from:
// <base>/<tag>/<archive> and <base>/<tag>/checksums.txt.
const DefaultBaseURL = "https://github.com/gsoultan/hermod/releases/download"

// Size limits: an agent archive is a few MB.
const (
	maxArchive   = 100 << 20
	maxBinary    = 100 << 20
	maxChecksums = 1 << 20
)

var (
	// ErrNotNewer is an offer of this version, an older one, or not a
	// release version; a development build never updates itself.
	ErrNotNewer = errors.New("the offered version is not newer than this agent")
	// ErrChecksum is an archive that does not match the published digest.
	ErrChecksum = errors.New("the download does not match the release's published SHA-256")
)

// Updater replaces Exe with a release of the same platform.
type Updater struct {
	BaseURL string
	Client  *http.Client
	// Exe is the binary to replace, normally os.Executable.
	Exe string
	// Version is this agent's, for example "v1.8.0".
	Version string
	// The platform the release is for: runtime.GOOS, GOARCH and, for arm,
	// GOARM ("6" or "7").
	GOOS, GOARCH, GOARM string
}

// NewHTTPClient follows redirects over HTTPS only: GitHub sends release
// downloads on to its asset storage.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("refusing a redirect away from https")
			}
			return nil
		},
	}
}

// archiveName is the release archive for a platform, as .goreleaser.yaml
// names it.
func archiveName(tag, goos, goarch, goarm string) string {
	arch := goarch
	if goarch == "arm" && goarm != "" {
		arch += "v" + goarm
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("hermod-agent_%s_%s_%s.%s", strings.TrimPrefix(tag, "v"), goos, arch, ext)
}

// Apply installs release tag over Exe. It returns ErrNotNewer, without any
// download, unless tag is a newer release than Version. On success the new
// binary is in place and the caller restarts into it.
func (u *Updater) Apply(ctx context.Context, tag string) error {
	if !semver.IsValid(tag) || !semver.IsValid(u.Version) || semver.Compare(tag, u.Version) <= 0 {
		return ErrNotNewer
	}
	base, err := url.Parse(u.BaseURL)
	if err != nil || base.Scheme != "https" || base.User != nil {
		return fmt.Errorf("refusing to download releases from %q: https only", u.BaseURL)
	}
	client := u.Client
	if client == nil {
		client = NewHTTPClient()
	}
	name := archiveName(tag, u.GOOS, u.GOARCH, u.GOARM)
	folder := strings.TrimSuffix(u.BaseURL, "/") + "/" + url.PathEscape(tag) + "/"

	var sums bytes.Buffer
	if err := fetch(ctx, client, folder+"checksums.txt", maxChecksums, &sums); err != nil {
		return err
	}
	want, err := digestFor(&sums, name)
	if err != nil {
		return err
	}

	archive, err := download(ctx, client, folder+url.PathEscape(name), filepath.Dir(u.Exe), want)
	if archive != "" {
		defer func() { _ = os.Remove(archive) }()
	}
	if err != nil {
		return err
	}
	return u.install(ctx, archive, tag)
}

// download saves rawURL into a temporary file in dir and checks its SHA-256
// is want. It returns the file's name, to be removed, even on failure.
func download(ctx context.Context, client *http.Client, rawURL, dir, want string) (string, error) {
	archive, err := os.CreateTemp(dir, ".hermod-agent-download-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	err = fetch(ctx, client, rawURL, maxArchive, io.MultiWriter(archive, h))
	if cerr := archive.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = ErrChecksum
	}
	return archive.Name(), err
}

// install extracts the binary from archive next to Exe, checks it runs and
// reports tag, and swaps it in.
func (u *Updater) install(ctx context.Context, archive, tag string) error {
	binName := "hermod-agent"
	if u.GOOS == "windows" {
		binName += ".exe"
	}
	next := u.Exe + ".new"
	out, err := os.OpenFile(next, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //nolint:gosec // G302: an executable
	if err != nil {
		return err
	}
	err = extract(archive, binName, out)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = reportsVersion(ctx, next, tag)
	}
	if err != nil {
		_ = os.Remove(next)
		return err
	}
	return swap(u.Exe, next)
}

func fetch(ctx context.Context, client *http.Client, rawURL string, limit int64, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "hermod-agent-updater")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", rawURL, resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("downloading %s: %w", rawURL, err)
	}
	if n > limit {
		return fmt.Errorf("downloading %s: larger than %d bytes", rawURL, limit)
	}
	return nil
}

// digestFor finds name's SHA-256 in goreleaser's checksums.txt.
func digestFor(r io.Reader, name string) (string, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			d := strings.ToLower(f[0])
			if b, err := hex.DecodeString(d); err != nil || len(b) != sha256.Size {
				return "", fmt.Errorf("checksums.txt has no valid SHA-256 for %s", name)
			}
			return d, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("the release has no %s for this platform", name)
}

// extract copies the top-level file name out of a .tar.gz or .zip archive.
func extract(archive, name string, dst io.Writer) error {
	f, err := os.Open(archive) //nolint:gosec // G304: the archive Apply downloaded and verified
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var magic [2]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return fmt.Errorf("the archive is empty: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if magic == [2]byte{'P', 'K'} {
		return extractZip(f, name, dst)
	}
	return extractTarGz(f, name, dst)
}

func extractZip(f *os.File, name string, dst io.Writer) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return err
	}
	for _, zf := range zr.File {
		if path.Clean(zf.Name) != name || zf.FileInfo().IsDir() {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		return copyLimited(dst, rc, name)
	}
	return fmt.Errorf("the archive has no %s", name)
}

func extractTarGz(f io.Reader, name string, dst io.Writer) error {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("the archive is neither zip nor gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the archive has no %s", name)
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeReg && path.Clean(strings.TrimPrefix(h.Name, "./")) == name {
			return copyLimited(dst, tr, name)
		}
	}
}

func copyLimited(dst io.Writer, src io.Reader, name string) error {
	n, err := io.Copy(dst, io.LimitReader(src, maxBinary+1))
	if err != nil {
		return fmt.Errorf("extracting %s: %w", name, err)
	}
	if n > maxBinary {
		return fmt.Errorf("%s is larger than %d bytes", name, maxBinary)
	}
	return nil
}

// reportsVersion runs bin -version and checks it names tag.
func reportsVersion(ctx context.Context, bin, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-version").Output() //nolint:gosec // G204: the binary just verified against the release checksums
	if err != nil {
		return fmt.Errorf("the new binary does not run here: %w", err)
	}
	if !strings.Contains(string(out), tag) {
		return fmt.Errorf("the new binary reports %q, not %s", strings.TrimSpace(string(out)), tag)
	}
	return nil
}

// swap puts next in exe's place and keeps exe as exe.old. Renaming a running
// binary is allowed on Windows as well as Unix; it is replacing it in place
// that is not.
func swap(exe, next string) error {
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		_ = os.Remove(next)
		return fmt.Errorf("moving the running binary aside: %w", err)
	}
	if err := os.Rename(next, exe); err != nil {
		_ = os.Rename(old, exe)
		_ = os.Remove(next)
		return fmt.Errorf("putting the new binary in place: %w", err)
	}
	return nil
}
