// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"testing"

	"github.com/gsoultan/hermod/internal/config"
)

// On a server whose master key comes from HERMOD_MASTER_KEY (every package
// install: hermod.env sets it), the setup wizard used to save and use a key it
// generated itself. The next restart brought the environment's key back, and
// everything saved in between failed with "cannot decrypt with the configured
// master key". The key that wins at start-up has to be the one setup uses.
func TestSetupUsesThePinnedMasterKey(t *testing.T) {
	config.SetPinnedMasterKey("")
	t.Setenv("HERMOD_MASTER_KEY", "key-from-hermod-env-0123456789")
	if got := masterKeyForSetup("wizard-generated-key-0123456789"); got != "key-from-hermod-env-0123456789" {
		t.Fatalf("masterKeyForSetup = %q, want the environment's key", got)
	}
}

func TestSetupUsesTheTypedKeyWhenNoneIsPinned(t *testing.T) {
	config.SetPinnedMasterKey("")
	t.Setenv("HERMOD_MASTER_KEY", "")
	if got := masterKeyForSetup("typed-in-the-wizard-0123456789"); got != "typed-in-the-wizard-0123456789" {
		t.Fatalf("masterKeyForSetup = %q, want the typed key", got)
	}
}

// Rotating to a key saved only in db_config.yaml would last until the next
// restart, after which nothing re-encrypted could be read. Refuse instead.
func TestRotationIsRefusedWhenTheKeyIsPinned(t *testing.T) {
	config.SetPinnedMasterKey("")
	t.Setenv("HERMOD_MASTER_KEY", "key-from-hermod-env-0123456789")
	if pinnedKeyRefusal() == "" {
		t.Fatal("rotation was allowed while HERMOD_MASTER_KEY sets the key")
	}
	t.Setenv("HERMOD_MASTER_KEY", "")
	if msg := pinnedKeyRefusal(); msg != "" {
		t.Fatalf("rotation refused with no pinned key: %q", msg)
	}
}
