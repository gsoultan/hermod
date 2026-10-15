// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSaveConfigWritesSecretsWithRestrictedPermissions covers the file
// permissions on config.yaml.
//
// db_config.yaml is already written 0600 with a comment explaining that it
// holds the JWT secret and the crypto master key. config.yaml was written 0644
// — world-readable — even though its schema carries a Vault token, an OpenBao
// token, a database password, an OIDC client secret and AWS credentials. On a
// shared host any local account could read them, and on a container image with
// a baked-in config they leak to anyone who can pull it.
//
// Config that can hold a credential gets credential permissions, whether or not
// the field happens to be populated in a given deployment.
func TestSaveConfigWritesSecretsWithRestrictedPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not meaningful on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")

	cfg := &Config{}
	cfg.Secrets.Vault.Token = "s.super-secret-vault-token"

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("config.yaml written with mode %04o; it holds vault/OIDC/AWS credentials "+
			"and must not be readable by group or other (want 0600)", mode)
	}

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if mode := di.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("config directory created with mode %04o; a readable directory leaks the "+
			"names of secret files even when the files themselves are locked down (want 0700)", mode)
	}
}

// TestSaveConfigRoundTrips is the control: tightening the mode must not break
// reading the file back.
func TestSaveConfigRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := &Config{}
	cfg.Secrets.Vault.Address = "https://vault.example.com"

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after SaveConfig: %v", err)
	}
	if got.Secrets.Vault.Address != "https://vault.example.com" {
		t.Errorf("round-trip lost the vault address: %q", got.Secrets.Vault.Address)
	}
}
