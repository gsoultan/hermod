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
	"testing"
)

func TestMasterKey(t *testing.T) {
	defaultKey := "hermod-default-master-key-32byte"
	newKey := "a-very-secret-key-that-is-32-byt"

	// Test default encryption
	text := "hello world"
	enc1, err := Encrypt(text)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	dec1, err := Decrypt(enc1)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if dec1 != text {
		t.Errorf("Expected %s, got %s", text, dec1)
	}

	// Change master key
	SetMasterKey(newKey)

	enc2, err := Encrypt(text)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if enc1 == enc2 {
		t.Error("Encrypted text should be different after changing master key")
	}

	dec2, err := Decrypt(enc2)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if dec2 != text {
		t.Errorf("Expected %s, got %s", text, dec2)
	}

	// Try decrypting with wrong key
	SetMasterKey(defaultKey)
	_, err = Decrypt(enc2)
	if err == nil {
		t.Error("Decrypt should have failed with wrong master key")
	}
}
