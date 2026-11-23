// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package config

import "testing"

// A master key set outside db_config.yaml (HERMOD_MASTER_KEY, which the
// package's hermod.env sets, or --master-key) wins at every start-up. Anything
// that saves a different key to the file is saving one the next restart
// throws away, so the handlers ask PinnedMasterKey first, and start-up asks
// FileMasterKey for the key data saved before a restart may still be under.

func TestPinnedMasterKeyReadsTheEnvironment(t *testing.T) {
	SetPinnedMasterKey("")
	t.Setenv("HERMOD_MASTER_KEY", "from-env-0123456789")
	if got := PinnedMasterKey(); got != "from-env-0123456789" {
		t.Fatalf("PinnedMasterKey = %q, want the environment's key", got)
	}
}

func TestPinnedMasterKeyPrefersTheFlag(t *testing.T) {
	t.Cleanup(func() { SetPinnedMasterKey("") })
	t.Setenv("HERMOD_MASTER_KEY", "from-env-0123456789")
	SetPinnedMasterKey("from-flag-0123456789")
	if got := PinnedMasterKey(); got != "from-flag-0123456789" {
		t.Fatalf("PinnedMasterKey = %q, want the flag's key", got)
	}
}

func TestPinnedMasterKeyIsEmptyWhenOnlyTheFileHasOne(t *testing.T) {
	SetPinnedMasterKey("")
	t.Setenv("HERMOD_MASTER_KEY", "")
	configDir(t, "type: sqlite\nconn: x\ncrypto_master_key: in-file-0123456789\n")
	if got := PinnedMasterKey(); got != "" {
		t.Fatalf("PinnedMasterKey = %q, want empty", got)
	}
}

func TestFileMasterKeyIgnoresTheEnvironment(t *testing.T) {
	t.Setenv("HERMOD_MASTER_KEY", "from-env-0123456789")
	configDir(t, "type: sqlite\nconn: x\ncrypto_master_key: in-file-0123456789\n")
	got, err := FileMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	if got != "in-file-0123456789" {
		t.Fatalf("FileMasterKey = %q, want the file's own key", got)
	}
}

func TestFileMasterKeyWithoutAFileIsEmpty(t *testing.T) {
	t.Setenv("HERMOD_CONFIG_DIR", t.TempDir())
	got, err := FileMasterKey()
	if err != nil || got != "" {
		t.Fatalf("FileMasterKey = %q, %v; want empty, nil", got, err)
	}
}
