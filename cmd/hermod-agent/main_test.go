// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerIsRequired(t *testing.T) {
	if _, err := parseFlags(nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "-server") {
		t.Fatalf("err = %v", err)
	}
}

func TestVersionNeedsNoServer(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-version"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "hermod-agent ") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestTokenComesFromAFileOrTheEnvironment(t *testing.T) {
	t.Setenv(tokenEnv, "")
	if _, err := readToken(""); err == nil {
		t.Fatal("no token accepted")
	}
	t.Setenv(tokenEnv, " from-env \n")
	if tok, _ := readToken(""); tok != "from-env" {
		t.Fatalf("env token = %q", tok)
	}
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, _ := readToken(file); tok != "from-file" {
		t.Fatalf("file token = %q", tok)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	_ = os.WriteFile(empty, nil, 0o600)
	if _, err := readToken(empty); err == nil {
		t.Fatal("empty token file accepted")
	}
}

func TestPlaintextToARemoteServerIsRefused(t *testing.T) {
	t.Setenv(tokenEnv, "x")
	err := run([]string{"-server", "hermod.example.com:9443", "-plaintext", "-data-dir", t.TempDir()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("err = %v", err)
	}
}
