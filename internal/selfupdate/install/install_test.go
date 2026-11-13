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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarball builds a release-shaped archive holding the given files.
func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fakeRelease serves an archive and its checksums from memory.
type fakeRelease struct {
	files map[string][]byte
}

func (f *fakeRelease) fetch(_ context.Context, rawURL string, limit int64, w io.Writer) (int64, error) {
	body, ok := f.files[rawURL]
	if !ok {
		return 0, fmt.Errorf("404 %s", rawURL)
	}
	if int64(len(body)) > limit {
		return 0, ErrTooLarge
	}
	n, err := w.Write(body)
	return int64(n), err
}

const (
	archiveURL   = "https://github.com/gsoultan/hermod/releases/download/v1.25.0/hermod_1.25.0_linux_amd64.tar.gz"
	checksumsURL = "https://github.com/gsoultan/hermod/releases/download/v1.25.0/checksums.txt"
	archiveName  = "hermod_1.25.0_linux_amd64.tar.gz"
)

func newFixture(t *testing.T, binary string) (*Installer, *fakeRelease, string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "hermod")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := tarball(t, map[string]string{"hermod": binary, "hermodctl": "ctl", "LICENSE": "l"})
	rel := &fakeRelease{files: map[string][]byte{
		archiveURL:   archive,
		checksumsURL: []byte(sum([]byte("other")) + "  hermod_1.25.0_linux_arm64.tar.gz\n" + sum(archive) + "  " + archiveName + "\n"),
	}}
	in := &Installer{
		Exe:   exe,
		Fetch: rel.fetch,
		SmokeTest: func(_ context.Context, path string) (string, error) {
			b, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			// The fake "binary" prints its own contents as its version.
			return "hermod " + string(b), nil
		},
	}
	return in, rel, exe
}

var plan = Plan{Tag: "v1.25.0", ArchiveName: archiveName, ArchiveURL: archiveURL, ChecksumsURL: checksumsURL}

func TestInstallSwapsBinaryKeepsPreviousAndArmsGuard(t *testing.T) {
	in, _, exe := newFixture(t, "v1.25.0 (Enterprise Edition)")
	var phases []Phase
	if err := in.Install(context.Background(), plan, "v1.24.9", func(p Phase) { phases = append(phases, p) }); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "v1.25.0 (Enterprise Edition)" {
		t.Fatalf("binary after install = %q", b)
	}
	if b, _ := os.ReadFile(PreviousPath(exe)); string(b) != "old binary" {
		t.Fatalf("previous binary = %q, want the old one kept for rollback", b)
	}
	if info, _ := os.Stat(exe); info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("new binary mode %v is not executable", info.Mode())
	}
	m, err := readMarker(exe)
	if err != nil || m == nil || m.From != "v1.24.9" || m.To != "v1.25.0" {
		t.Fatalf("pending marker = %+v, %v", m, err)
	}
	if want := []Phase{PhaseDownloading, PhaseVerifying, PhaseInstalling}; fmt.Sprint(phases) != fmt.Sprint(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	assertNoTempFiles(t, filepath.Dir(exe))
}

func TestInstallRefusesChecksumMismatch(t *testing.T) {
	in, rel, exe := newFixture(t, "v1.25.0")
	rel.files[archiveURL] = append(rel.files[archiveURL], 0) // tampered in transit
	err := in.Install(context.Background(), plan, "v1.24.9", nil)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("Install with a tampered archive = %v, want ErrChecksum", err)
	}
	assertUntouched(t, exe)
}

func TestInstallRefusesArchiveMissingFromChecksums(t *testing.T) {
	in, rel, exe := newFixture(t, "v1.25.0")
	rel.files[checksumsURL] = []byte(sum([]byte("x")) + "  something_else.tar.gz\n")
	if err := in.Install(context.Background(), plan, "v1.24.9", nil); !errors.Is(err, ErrChecksum) {
		t.Fatalf("Install with no checksum line = %v, want ErrChecksum", err)
	}
	assertUntouched(t, exe)
}

func TestInstallRefusesBinaryThatDoesNotReportTheRelease(t *testing.T) {
	// Wrong architecture, a truncated file, a mislabelled archive: all show up
	// as a binary that cannot say it is the version we downloaded.
	in, _, exe := newFixture(t, "v1.24.0 (Enterprise Edition)")
	if err := in.Install(context.Background(), plan, "v1.24.9", nil); !errors.Is(err, ErrSmokeTest) {
		t.Fatalf("Install = %v, want ErrSmokeTest", err)
	}
	assertUntouched(t, exe)
}

func TestInstallRefusesArchiveWithoutBinary(t *testing.T) {
	in, rel, exe := newFixture(t, "v1.25.0")
	archive := tarball(t, map[string]string{"hermodctl": "ctl"})
	rel.files[archiveURL] = archive
	rel.files[checksumsURL] = []byte(sum(archive) + "  " + archiveName + "\n")
	if err := in.Install(context.Background(), plan, "v1.24.9", nil); err == nil {
		t.Fatal("Install of an archive with no hermod binary succeeded")
	}
	assertUntouched(t, exe)
}

func TestExtractIgnoresNestedPathsNamedLikeTheBinary(t *testing.T) {
	// Only the top-level entry is the binary; docs/hermod is not.
	archive := tarball(t, map[string]string{"docs/hermod": "doc"})
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExtractBinary(path, "hermod", io.Discard, 1<<20); err == nil {
		t.Fatal("a nested docs/hermod was taken for the binary")
	}
}

func TestExtractBoundsTheBinarySize(t *testing.T) {
	archive := tarball(t, map[string]string{"hermod": strings.Repeat("x", 2048)})
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ExtractBinary(path, "hermod", io.Discard, 1024); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ExtractBinary over the limit = %v, want ErrTooLarge", err)
	}
}

func TestParseChecksums(t *testing.T) {
	got, err := ParseChecksums(strings.NewReader("abc123  a.tar.gz\n\n" + strings.Repeat("f", 64) + " *b.tar.gz\n"))
	if err == nil {
		t.Fatalf("a malformed digest was accepted: %v", got)
	}
	good := strings.Repeat("a", 64)
	got, err = ParseChecksums(strings.NewReader(good + "  a.tar.gz\n" + strings.ToUpper(good) + " *b.tar.gz\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got["a.tar.gz"] != good || got["b.tar.gz"] != good {
		t.Fatalf("ParseChecksums = %v", got)
	}
}

func TestAllowedURL(t *testing.T) {
	ok := []string{
		"https://github.com/gsoultan/hermod/releases/download/v1.25.0/checksums.txt",
		"https://objects.githubusercontent.com/github-production-release-asset/123",
		"https://release-assets.githubusercontent.com/github-production-release-asset/123",
	}
	bad := []string{
		"http://github.com/gsoultan/hermod/releases/download/v1.25.0/checksums.txt",
		"https://github.com/someoneelse/hermod/releases/download/v1.25.0/checksums.txt",
		"https://github.com.evil.example/gsoultan/hermod/releases/download/x",
		"https://evilgithubusercontent.com/x",
		"https://user:pw@github.com/gsoultan/hermod/releases/download/v1/x",
	}
	for _, s := range ok {
		u, _ := url.Parse(s)
		if err := AllowedURL(u); err != nil {
			t.Errorf("AllowedURL(%s) = %v, want allowed", s, err)
		}
	}
	for _, s := range bad {
		u, _ := url.Parse(s)
		if err := AllowedURL(u); err == nil {
			t.Errorf("AllowedURL(%s) allowed", s)
		}
	}
}

func assertUntouched(t *testing.T, exe string) {
	t.Helper()
	if b, _ := os.ReadFile(exe); string(b) != "old binary" {
		t.Fatalf("binary was changed by a failed install: %q", b)
	}
	if m, _ := readMarker(exe); m != nil {
		t.Fatalf("a failed install left a pending marker: %+v", m)
	}
	assertNoTempFiles(t, filepath.Dir(exe))
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".update-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestInstallRunsTheDownloadedBinaryForItsVersion(t *testing.T) {
	// The real smoke test: the extracted file is executed with --version.
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	in, _, exe := newFixture(t, "#!/bin/sh\n[ \"$1\" = --version ] && echo 'hermod v1.25.0 (Enterprise Edition)'\n")
	in.SmokeTest = nil
	if err := in.Install(context.Background(), plan, "v1.24.9", nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if b, _ := os.ReadFile(exe); !strings.HasPrefix(string(b), "#!/bin/sh") {
		t.Fatalf("binary = %q", b)
	}

	in, _, exe = newFixture(t, "#!/bin/sh\nexit 3\n")
	in.SmokeTest = nil
	if err := in.Install(context.Background(), plan, "v1.24.9", nil); !errors.Is(err, ErrSmokeTest) {
		t.Fatalf("Install of a binary that fails to run = %v, want ErrSmokeTest", err)
	}
	assertUntouched(t, exe)
}
