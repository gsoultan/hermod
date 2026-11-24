// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gsoultan/hermod/cmd/hermod-agent/internal/update"
)

// An agent started with updates allowed installs the release it is offered
// and stops so it can be restarted into it.
func TestAnOfferedReleaseIsInstalledAndTheAgentRestarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in binary is a shell script")
	}
	body := "#!/bin/sh\necho hermod-agent v1.2.0\n"
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "hermod-agent", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(tgz.Bytes())
	name := "hermod-agent_1.2.0_linux_amd64.tar.gz"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.2.0/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tgz.Bytes()) })
	mux.HandleFunc("/v1.2.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + name + "\n"))
	})
	rel := httptest.NewTLSServer(mux)
	defer rel.Close()
	exe := filepath.Join(t.TempDir(), "hermod-agent")
	_ = os.WriteFile(exe, []byte("old"), 0o755)

	srv := &server{update: "v1.2.0"}
	opt := options(t.TempDir(), serve(t, srv))
	opt.Version = "v1.1.0"
	opt.AllowUpdate = true
	opt.Updater = &update.Updater{BaseURL: rel.URL, Client: rel.Client(), Exe: exe, Version: "v1.1.0", GOOS: "linux", GOARCH: "amd64"}
	done := make(chan error, 1)
	go func() { done <- Run(t.Context(), opt) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrRestart) {
			t.Fatalf("Run = %v, want ErrRestart", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not stop to restart")
	}
	if b, _ := os.ReadFile(exe); string(b) != body {
		t.Fatalf("binary = %q", b)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.hello.GetAcceptsUpdates() {
		t.Fatal("the agent did not tell the server it accepts updates")
	}
}

// Without -allow-update an offer changes nothing.
func TestAnOfferIsIgnoredUnlessUpdatesAreAllowed(t *testing.T) {
	srv := &server{update: "v9.0.0"}
	opt := options(t.TempDir(), serve(t, srv))
	opt.Version = "v1.1.0"
	opt.Updater = &update.Updater{BaseURL: "https://unused.invalid", Exe: "/nonexistent", Version: "v1.1.0"}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if err := Run(ctx, opt); err != nil {
		t.Fatalf("Run = %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.hello.GetAcceptsUpdates() {
		t.Fatal("an agent without -allow-update said it accepts updates")
	}
}
