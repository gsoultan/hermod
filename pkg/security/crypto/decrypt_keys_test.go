// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package crypto

import (
	"encoding/base64"
	"testing"
)

// A server installed from the package reads its key from HERMOD_MASTER_KEY,
// but the setup wizard used to save a key of its own to db_config.yaml and
// encrypt with it until the next restart. Everything saved in that window
// stayed under the wizard's key, and after a restart nothing could open it.
// AddDecryptKey lets start-up keep that data readable.

func TestAddDecryptKeyOpensDataWrittenUnderAnotherKey(t *testing.T) {
	withMasterKey(t, "wizard-generated-key-0123456789")
	old, err := Encrypt("s3cret")
	if err != nil {
		t.Fatal(err)
	}

	SetMasterKey("key-from-hermod-env-0123456789")
	if _, err := Decrypt(old); err == nil {
		t.Fatal("precondition: the new key alone must not open the old value")
	}

	AddDecryptKey("wizard-generated-key-0123456789")
	got, err := Decrypt(old)
	if err != nil {
		t.Fatalf("Decrypt after AddDecryptKey: %v", err)
	}
	if got != "s3cret" {
		t.Fatalf("Decrypt = %q, want %q", got, "s3cret")
	}
}

func TestAddDecryptKeyNeverEncrypts(t *testing.T) {
	withMasterKey(t, "key-from-hermod-env-0123456789")
	AddDecryptKey("wizard-generated-key-0123456789")

	ct, err := Encrypt("v")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		t.Fatal(err)
	}
	primary := state.Load().primary
	n := primary.NonceSize()
	if _, err := primary.Open(nil, raw[:n], raw[n:], nil); err != nil {
		t.Fatalf("new values must be written under the primary key: %v", err)
	}
}

func TestAddDecryptKeySurvivesSetMasterKeyOfTheSameKey(t *testing.T) {
	withMasterKey(t, "wizard-generated-key-0123456789")
	old, _ := Encrypt("v")
	SetMasterKey("key-from-hermod-env-0123456789")
	AddDecryptKey("wizard-generated-key-0123456789")

	// Start-up installs the same key again from db_config.yaml handling;
	// that must not drop the fallback.
	SetMasterKey("key-from-hermod-env-0123456789")
	if _, err := Decrypt(old); err != nil {
		t.Fatalf("re-installing the same primary dropped the decrypt-only key: %v", err)
	}
}
