// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

// release serves a release folder: the archive and checksums.txt.
func release(t *testing.T, tag, archive string, body []byte, sum string) *httptest.Server {
	t.Helper()
	if sum == "" {
		d := sha256.Sum256(body)
		sum = hex.EncodeToString(d[:])
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/"+tag+"/"+archive, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	mux.HandleFunc("/"+tag+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sum + "  " + archive + "\n" + sum + "  other.tar.gz\n"))
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func updater(t *testing.T, srv *httptest.Server) (*Updater, string) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "hermod-agent")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Updater{BaseURL: srv.URL, Client: srv.Client(), Exe: exe, Version: "v1.1.0",
		GOOS: "linux", GOARCH: "arm", GOARM: "7"}, exe
}

const newBinary = "#!/bin/sh\necho hermod-agent v1.2.0\n"

func TestArchiveNameFollowsTheReleaseNaming(t *testing.T) {
	cases := map[[3]string]string{
		{"linux", "arm", "7"}:    "hermod-agent_1.2.0_linux_armv7.tar.gz",
		{"linux", "arm64", ""}:   "hermod-agent_1.2.0_linux_arm64.tar.gz",
		{"windows", "amd64", ""}: "hermod-agent_1.2.0_windows_amd64.zip",
		{"darwin", "arm64", "7"}: "hermod-agent_1.2.0_darwin_arm64.tar.gz",
	}
	for in, want := range cases {
		if got := archiveName("v1.2.0", in[0], in[1], in[2]); got != want {
			t.Errorf("archiveName(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestApplyReplacesTheBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binary is a shell script")
	}
	name := "hermod-agent_1.2.0_linux_armv7.tar.gz"
	srv := release(t, "v1.2.0", name, tarGz(t, map[string]string{"hermod-agent": newBinary, "LICENSE": "x"}), "")
	u, exe := updater(t, srv)
	if err := u.Apply(t.Context(), "v1.2.0"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != newBinary {
		t.Fatalf("binary = %q", b)
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "old" {
		t.Fatalf("the old binary was not kept: %q", b)
	}
}

func TestApplyRefusesAChecksumMismatch(t *testing.T) {
	name := "hermod-agent_1.2.0_linux_armv7.tar.gz"
	srv := release(t, "v1.2.0", name, tarGz(t, map[string]string{"hermod-agent": newBinary}), "00"+string(bytes.Repeat([]byte("ab"), 31)))
	u, exe := updater(t, srv)
	if err := u.Apply(t.Context(), "v1.2.0"); !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatal("the binary changed")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "*")); len(left) != 1 {
		t.Fatalf("left behind: %v", left)
	}
}

// A binary that does not run as the version it was offered as is refused.
func TestApplyRefusesABinaryOfAnotherVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binary is a shell script")
	}
	name := "hermod-agent_1.2.0_linux_armv7.tar.gz"
	srv := release(t, "v1.2.0", name, tarGz(t, map[string]string{"hermod-agent": "#!/bin/sh\necho hermod-agent v1.0.0\n"}), "")
	u, exe := updater(t, srv)
	if err := u.Apply(t.Context(), "v1.2.0"); err == nil {
		t.Fatal("a binary reporting v1.0.0 was installed as v1.2.0")
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatal("the binary changed")
	}
}

func TestApplyIgnoresWhatIsNotNewer(t *testing.T) {
	u := &Updater{BaseURL: "https://unused.invalid", Version: "v1.2.0"}
	for _, v := range []string{"v1.2.0", "v1.1.9", "not-a-version", ""} {
		if err := u.Apply(t.Context(), v); !errors.Is(err, ErrNotNewer) {
			t.Errorf("Apply(%q) = %v", v, err)
		}
	}
	dev := &Updater{BaseURL: "https://unused.invalid", Version: "dev"}
	if err := dev.Apply(t.Context(), "v1.2.0"); !errors.Is(err, ErrNotNewer) {
		t.Errorf("a dev build updated itself: %v", err)
	}
}

func TestApplyRefusesPlainHTTP(t *testing.T) {
	u := &Updater{BaseURL: "http://example.com/releases", Version: "v1.0.0", GOOS: "linux", GOARCH: "amd64", Exe: "/x"}
	if err := u.Apply(t.Context(), "v1.2.0"); err == nil {
		t.Fatal("a plain HTTP download was attempted")
	}
}

func TestExtractFromZip(t *testing.T) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create("hermod-agent.exe")
	_, _ = w.Write([]byte("MZ"))
	w2, _ := zw.Create("docs/hermod-agent.exe")
	_, _ = w2.Write([]byte("decoy"))
	_ = zw.Close()
	path := filepath.Join(t.TempDir(), "a.zip")
	_ = os.WriteFile(path, b.Bytes(), 0o600)
	var out bytes.Buffer
	if err := extract(path, "hermod-agent.exe", &out); err != nil || out.String() != "MZ" {
		t.Fatalf("extract = %q, %v", out.String(), err)
	}
}
